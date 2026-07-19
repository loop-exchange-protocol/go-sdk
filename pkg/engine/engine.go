package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	lxpruntime "github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type Engine struct {
	Root       string
	Providers  *provider.Registry
	Checkers   *lxpruntime.Registry
	Extensions extension.Config
}

// New constructs an Engine from explicitly supplied providers. The SDK never
// installs concrete providers implicitly; applications are the composition root.
func New(root string, providers ...provider.Provider) *Engine {
	return &Engine{Root: root, Providers: provider.NewRegistry(providers...), Checkers: lxpruntime.DefaultRegistry(), Extensions: extension.Default()}
}

func NewWithConfig(root string, config extension.Config, checkers *lxpruntime.Registry, providers ...provider.Provider) *Engine {
	if checkers == nil {
		checkers = lxpruntime.NewRegistry()
	}
	return &Engine{Root: root, Providers: provider.NewRegistry(providers...), Checkers: checkers, Extensions: config}
}

func (e *Engine) providerFor(contract spec.Contract) (provider.Provider, error) {
	implementation, err := e.Extensions.Resolve(extension.KindProvider, contract)
	if err != nil {
		return nil, err
	}
	if implementation.Source != "builtin" {
		return nil, fmt.Errorf("implementation %s for %s is not installed; this Engine does not auto-install repository extensions", implementation.Package.String(), contract.String())
	}
	p, err := e.Providers.Get(contract)
	if err != nil {
		return nil, err
	}
	if p.Implementation() != implementation.Package {
		return nil, fmt.Errorf("Provider %s is bound to %s but installed implementation is %s", contract.String(), implementation.Package.String(), p.Implementation().String())
	}
	return p, nil
}

func (e *Engine) checkerFor(contract spec.Contract) (lxpruntime.Checker, error) {
	implementation, err := e.Extensions.Resolve(extension.KindChecker, contract)
	if err != nil {
		return nil, err
	}
	if implementation.Source != "builtin" {
		return nil, fmt.Errorf("implementation %s for %s is not installed; this Engine does not auto-install repository extensions", implementation.Package.String(), contract.String())
	}
	checker, err := e.Checkers.Get(contract)
	if err != nil {
		return nil, err
	}
	if checker.Implementation() != implementation.Package {
		return nil, fmt.Errorf("Checker %s is bound to %s but installed implementation is %s", contract.String(), implementation.Package.String(), checker.Implementation().String())
	}
	return checker, nil
}

func (e *Engine) ensureProvider(p provider.Provider) error {
	if p == nil {
		return fmt.Errorf("no Provider selected")
	}
	_, err := e.providerFor(p.Contract())
	return err
}

// discoveryProviders validates all configured candidates before invoking any
// Provider Match hook. Registered but unbound Providers remain inert.
func (e *Engine) discoveryProviders() ([]provider.Provider, error) {
	installed, err := e.Providers.Providers()
	if err != nil {
		return nil, err
	}
	eligible := make([]provider.Provider, 0, len(installed))
	for _, candidate := range installed {
		implementation, bound, err := e.Extensions.Lookup(extension.KindProvider, candidate.Contract())
		if err != nil {
			return nil, err
		}
		if !bound {
			continue
		}
		if implementation.Source != "builtin" {
			return nil, fmt.Errorf("implementation %s for %s is not installed; this Engine does not auto-install repository extensions", implementation.Package.String(), candidate.Contract().String())
		}
		if candidate.Implementation() != implementation.Package {
			return nil, fmt.Errorf("Provider %s is bound to %s but installed implementation is %s", candidate.Contract().String(), implementation.Package.String(), candidate.Implementation().String())
		}
		eligible = append(eligible, candidate)
	}
	return eligible, nil
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
