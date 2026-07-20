package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	extensionoci "github.com/loop-exchange-protocol/lxp/pkg/extension/oci"
	"github.com/loop-exchange-protocol/lxp/pkg/helper"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	lxpruntime "github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type Engine struct {
	Root            string
	Providers       *provider.Registry
	Checkers        *lxpruntime.Registry
	Extensions      extension.Config
	helperMu        sync.Mutex
	helperProviders map[string]provider.Provider
	helperCheckers  map[string]lxpruntime.Checker
	helperClosers   []io.Closer
}

// New constructs an Engine from explicitly supplied builtin Providers. Local
// EngineConfig may additionally activate exact Helper bindings.
func New(root string, providers ...provider.Provider) *Engine {
	return NewWithConfig(root, extension.Default(), lxpruntime.DefaultRegistry(), providers...)
}

func NewWithConfig(root string, config extension.Config, checkers *lxpruntime.Registry, providers ...provider.Provider) *Engine {
	if checkers == nil {
		checkers = lxpruntime.NewRegistry()
	}
	return &Engine{Root: root, Providers: provider.NewRegistry(providers...), Checkers: checkers, Extensions: config, helperProviders: map[string]provider.Provider{}, helperCheckers: map[string]lxpruntime.Checker{}}
}

func (e *Engine) providerFor(ctx context.Context, contract spec.Contract) (provider.Provider, error) {
	implementation, err := e.Extensions.Resolve(extension.KindProvider, contract)
	if err != nil {
		return nil, err
	}
	if implementation.Source == "builtin" {
		p, err := e.Providers.Get(contract)
		if err != nil {
			return nil, err
		}
		if p.Implementation() != implementation.Package {
			return nil, fmt.Errorf("Provider %s is bound to %s but installed implementation is %s", contract.String(), implementation.Package.String(), p.Implementation().String())
		}
		return p, nil
	}
	e.helperMu.Lock()
	defer e.helperMu.Unlock()
	if cached := e.helperProviders[contract.String()]; cached != nil {
		return cached, nil
	}
	command := implementation.Command
	if implementation.Source == "repository" {
		command, err = extensionoci.Install(ctx, extension.KindProvider, contract, implementation, e.Extensions.Repositories)
		if err != nil {
			return nil, fmt.Errorf("install Provider %s: %w", contract.String(), err)
		}
	}
	p, err := helper.NewProvider(ctx, e.Root, contract, implementation, command)
	if err != nil {
		return nil, fmt.Errorf("activate Provider %s: %w", contract.String(), err)
	}
	e.helperProviders[contract.String()] = p
	e.helperClosers = append(e.helperClosers, p)
	return p, nil
}

func (e *Engine) checkerFor(ctx context.Context, contract spec.Contract) (lxpruntime.Checker, error) {
	implementation, err := e.Extensions.Resolve(extension.KindChecker, contract)
	if err != nil {
		return nil, err
	}
	if implementation.Source == "builtin" {
		checker, err := e.Checkers.Get(contract)
		if err != nil {
			return nil, err
		}
		if checker.Implementation() != implementation.Package {
			return nil, fmt.Errorf("Checker %s is bound to %s but installed implementation is %s", contract.String(), implementation.Package.String(), checker.Implementation().String())
		}
		return checker, nil
	}
	e.helperMu.Lock()
	defer e.helperMu.Unlock()
	if cached := e.helperCheckers[contract.String()]; cached != nil {
		return cached, nil
	}
	command := implementation.Command
	if implementation.Source == "repository" {
		command, err = extensionoci.Install(ctx, extension.KindChecker, contract, implementation, e.Extensions.Repositories)
		if err != nil {
			return nil, fmt.Errorf("install Checker %s: %w", contract.String(), err)
		}
	}
	checker, err := helper.NewChecker(ctx, e.Root, contract, implementation, command)
	if err != nil {
		return nil, fmt.Errorf("activate Checker %s: %w", contract.String(), err)
	}
	e.helperCheckers[contract.String()] = checker
	e.helperClosers = append(e.helperClosers, checker)
	return checker, nil
}

// discoveryProviders validates all configured candidates before invoking any
// Provider Match hook. Registered but unbound Providers remain inert.
func (e *Engine) discoveryProviders(ctx context.Context) ([]provider.Provider, error) {
	if err := e.Extensions.Validate(); err != nil {
		return nil, err
	}
	var eligible []provider.Provider
	for _, binding := range e.Extensions.Bindings {
		if binding.Kind != extension.KindProvider {
			continue
		}
		candidate, err := e.providerFor(ctx, binding.Contract)
		if err != nil {
			return nil, err
		}
		eligible = append(eligible, candidate)
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Contract().String() < eligible[j].Contract().String() })
	return eligible, nil
}

// Close ends every Helper process activated during this Engine command.
func (e *Engine) Close() error {
	e.helperMu.Lock()
	defer e.helperMu.Unlock()
	var first error
	for i := len(e.helperClosers) - 1; i >= 0; i-- {
		if err := e.helperClosers[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	e.helperClosers = nil
	return first
}

func (e *Engine) Init(ctx context.Context, sessionID string) (protocol.InstanceManifest, error) {
	return e.InitAt(ctx, sessionID, filepath.Join(e.Root, "sessions", sessionID, "workdir"))
}

// InitAt creates an empty session whose workdir is the user-visible directory,
// while keeping Engine state under workdir/.lxp.
func (e *Engine) InitAt(_ context.Context, sessionID, workdir string) (protocol.InstanceManifest, error) {
	if !spec.ValidIdentifier(sessionID) {
		return protocol.InstanceManifest{}, fmt.Errorf("invalid session id %q", sessionID)
	}
	absWorkdir, err := protocol.CanonicalPath(workdir)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	if err := os.MkdirAll(absWorkdir, 0o755); err != nil {
		return protocol.InstanceManifest{}, err
	}
	sessionDir := filepath.Join(e.Root, "sessions", sessionID)
	manifestPath := filepath.Join(sessionDir, "manifest.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		return protocol.InstanceManifest{}, fmt.Errorf("session %q already exists", sessionID)
	} else if !os.IsNotExist(err) {
		return protocol.InstanceManifest{}, err
	}
	instance := protocol.InstanceManifest{
		Kind:         "LoopSessionInstance",
		APIVersion:   spec.APIVersion,
		ID:           sessionID,
		Requirements: []spec.Requirement{},
		Paths:        protocol.InstancePaths{SessionDir: sessionDir, Workdir: absWorkdir, Manifest: manifestPath},
		Metadata:     map[string]string{"created_at": time.Now().UTC().Format(time.RFC3339)},
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return protocol.InstanceManifest{}, err
	}
	if err := protocol.WriteYAML(manifestPath, instance); err != nil {
		return protocol.InstanceManifest{}, err
	}
	return instance, nil
}

func requiredArtifactRequirements(components []spec.Component) map[string]bool {
	out := map[string]bool{}
	for _, component := range components {
		for _, id := range component.Requires {
			out[id] = true
		}
	}
	return out
}

func (e *Engine) readInstance(sessionID string) (protocol.InstanceManifest, error) {
	if sessionID == "" {
		return protocol.InstanceManifest{}, fmt.Errorf("--session-id is required")
	}
	return protocol.ReadYAML[protocol.InstanceManifest](filepath.Join(e.Root, "sessions", sessionID, "manifest.yaml"))
}
