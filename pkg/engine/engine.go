package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loop-exchange-protocol/go-sdk/pkg/protocol"
	"github.com/loop-exchange-protocol/go-sdk/pkg/provider"
	lxpruntime "github.com/loop-exchange-protocol/go-sdk/pkg/runtime"
	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

type Engine struct {
	Root      string
	Providers *provider.Registry
}

// New constructs an Engine from explicitly supplied providers. The SDK never
// installs concrete providers implicitly; applications are the composition root.
func New(root string, providers ...provider.Provider) *Engine {
	return &Engine{Root: root, Providers: provider.NewRegistry(providers...)}
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
	absWorkdir, err := filepath.Abs(workdir)
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

func environmentRequirements(requirements []spec.Requirement) []spec.Requirement {
	out := make([]spec.Requirement, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.ProvidedBy == "" || requirement.ProvidedBy == "environment" {
			out = append(out, requirement)
		}
	}
	return out
}

func (e *Engine) activateComponents(ctx context.Context, components []protocol.ResolvedRef, requirements []spec.Requirement, existing []spec.RuntimeLock, opts lxpruntime.Options) ([]spec.RuntimeLock, error) {
	consumed := map[string]bool{}
	for _, component := range components {
		for _, requirement := range component.Requires {
			consumed[requirement] = true
		}
	}
	ready := map[string]bool{}
	for _, lock := range existing {
		ready[lock.ID] = lock.Status == "ready"
	}
	provided := map[string][]spec.Requirement{}
	for _, requirement := range requirements {
		if strings.HasPrefix(requirement.ProvidedBy, "component:") {
			id := strings.TrimPrefix(requirement.ProvidedBy, "component:")
			provided[id] = append(provided[id], requirement)
		}
	}
	activated := map[string]bool{}
	var locks []spec.RuntimeLock
	for len(activated) < len(components) {
		progress := false
		for _, component := range components {
			if activated[component.ID] {
				continue
			}
			canActivate := true
			for _, requirement := range component.Requires {
				if !ready[requirement] {
					canActivate = false
					break
				}
			}
			if !canActivate {
				continue
			}
			p, err := e.Providers.Get(component.Provider)
			if err != nil {
				return nil, err
			}
			if err := p.Activate(ctx, component); err != nil {
				return nil, fmt.Errorf("activate component %q: %w", component.ID, err)
			}
			activated[component.ID] = true
			progress = true
			if checks := provided[component.ID]; len(checks) > 0 {
				required := map[string]bool{}
				for _, check := range checks {
					required[check.ID] = consumed[check.ID]
				}
				checked, err := lxpruntime.Resolve(ctx, checks, required, opts)
				if err != nil {
					return nil, err
				}
				locks = append(locks, checked...)
				for _, lock := range checked {
					ready[lock.ID] = lock.Status == "ready"
				}
			}
		}
		if !progress {
			return nil, fmt.Errorf("component activation is blocked by unsatisfied requirements")
		}
	}
	return locks, nil
}
