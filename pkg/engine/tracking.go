package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"

	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type WorktreeStatus struct {
	Ready      bool              `json:"ready"`
	Components []string          `json:"components"`
	Untracked  []string          `json:"untracked"`
	Ignored    []string          `json:"ignored"`
	Changes    []provider.Change `json:"changes"`
	Prompt     string            `json:"prompt"`
}

func (e *Engine) Status(sessionID string) (WorktreeStatus, error) {
	return e.StatusContext(context.Background(), sessionID)
}

// StatusContext reports Session state and bounds Provider work with ctx.
func (e *Engine) StatusContext(ctx context.Context, sessionID string) (WorktreeStatus, error) {
	instance, err := e.readInstance(sessionID)
	if err != nil {
		return WorktreeStatus{}, err
	}
	status, err := statusFor(instance)
	if err != nil {
		return status, err
	}
	for _, component := range sortedResolved(instance.Components, true) {
		component.Children = directResolvedChildren(component, instance.Components, nil)
		p, err := e.providerFor(component.Provider)
		if err != nil {
			return status, err
		}
		tracker, ok := p.(provider.Tracker)
		if !ok {
			continue
		}
		changes, err := tracker.Status(ctx, component)
		if err != nil {
			return status, fmt.Errorf("status component %q: %w", component.ID, err)
		}
		for _, change := range changes {
			change.Path = component.Path + "/" + change.Path
			status.Changes = append(status.Changes, change)
		}
	}
	return status, nil
}

type AddOptions struct {
	Provider spec.Contract
}

func (e *Engine) Add(sessionID string, paths []string) (protocol.InstanceManifest, error) {
	return e.AddWithOptionsContext(context.Background(), sessionID, paths, AddOptions{})
}

func (e *Engine) AddWithOptions(sessionID string, paths []string, opts AddOptions) (protocol.InstanceManifest, error) {
	return e.AddWithOptionsContext(context.Background(), sessionID, paths, opts)
}

// AddWithOptionsContext selects paths while bounding Provider work with ctx.
func (e *Engine) AddWithOptionsContext(ctx context.Context, sessionID string, paths []string, opts AddOptions) (protocol.InstanceManifest, error) {
	if len(paths) == 0 {
		return protocol.InstanceManifest{}, fmt.Errorf("add requires at least one workspace path")
	}
	instance, err := e.readInstance(sessionID)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	for _, raw := range paths {
		rel, err := cleanTrackedPath(raw)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		target := filepath.Join(instance.Paths.Workdir, filepath.FromSlash(rel))
		if _, err := os.Lstat(target); err != nil {
			return protocol.InstanceManifest{}, fmt.Errorf("add %q: %w", rel, err)
		}
		owner := deepestOwner(instance.Components, rel)
		if owner >= 0 {
			component := instance.Components[owner]
			if opts.Provider.Valid() && component.Provider != opts.Provider {
				return protocol.InstanceManifest{}, fmt.Errorf("path %q is owned by %s, not requested %s", rel, component.Provider.String(), opts.Provider.String())
			}
			if !opts.Provider.Valid() {
				p, err := e.providerFor(component.Provider)
				if err != nil {
					return protocol.InstanceManifest{}, err
				}
				if err := e.adoptDiscoveredChildren(ctx, &instance, p, component); err != nil {
					return protocol.InstanceManifest{}, err
				}
				owner = deepestOwner(instance.Components, rel)
				if owner < 0 {
					return protocol.InstanceManifest{}, fmt.Errorf("path %q lost its owning Component", rel)
				}
				component = instance.Components[owner]
			}
			if err := e.addThroughProvider(ctx, instance, component, rel); err != nil {
				return protocol.InstanceManifest{}, err
			}
			if err := e.trackParentBoundary(ctx, instance, component); err != nil {
				return protocol.InstanceManifest{}, err
			}
			continue
		}
		var p provider.Provider
		componentPath := rel
		componentTarget := target
		if opts.Provider.Valid() {
			p, err = e.providerFor(opts.Provider)
			if err != nil {
				return protocol.InstanceManifest{}, err
			}
		} else {
			p, componentPath, componentTarget, err = e.discoverProviderRoot(ctx, instance.Paths.Workdir, rel, target)
			if err != nil {
				return protocol.InstanceManifest{}, err
			}
		}
		if componentAtPath(instance.Components, componentPath) >= 0 {
			return protocol.InstanceManifest{}, fmt.Errorf("component root %q is already registered", componentPath)
		}
		ref, err := e.adoptComponentTree(ctx, &instance, p, componentPath, componentTarget)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		if err := e.addThroughProvider(ctx, instance, ref, rel); err != nil {
			return protocol.InstanceManifest{}, err
		}
	}
	if err := protocol.WriteYAML(instance.Paths.Manifest, instance); err != nil {
		return protocol.InstanceManifest{}, err
	}
	return instance, nil
}

func deepestOwner(components []protocol.ResolvedRef, path string) int {
	selected := -1
	for i, component := range components {
		if path != component.Path && !strings.HasPrefix(path, component.Path+"/") {
			continue
		}
		if selected < 0 || len(component.Path) > len(components[selected].Path) {
			selected = i
		}
	}
	return selected
}

func componentAtPath(components []protocol.ResolvedRef, path string) int {
	for i, component := range components {
		if component.Path == path {
			return i
		}
	}
	return -1
}

func (e *Engine) adoptComponentTree(ctx context.Context, instance *protocol.InstanceManifest, p provider.Provider, componentPath, componentTarget string) (protocol.ResolvedRef, error) {
	ref := protocol.ResolvedRef{ID: componentID(componentPath), Path: componentPath, Provider: p.Contract(), Source: "session", PoolPath: componentTarget, Materialized: componentTarget}
	if adopter, ok := p.(provider.Adopter); ok {
		adopted, err := adopter.Adopt(ctx, ref.ID, componentPath, componentTarget)
		if err != nil {
			return protocol.ResolvedRef{}, fmt.Errorf("adopt through provider %s: %w", p.Contract().String(), err)
		}
		ref = adopted
	}
	for _, component := range instance.Components {
		if component.ID == ref.ID {
			return protocol.ResolvedRef{}, fmt.Errorf("component id %q is already registered", ref.ID)
		}
	}
	instance.Components = append(instance.Components, ref)
	if err := e.adoptDiscoveredChildren(ctx, instance, p, ref); err != nil {
		return protocol.ResolvedRef{}, err
	}
	return ref, nil
}

func (e *Engine) adoptDiscoveredChildren(ctx context.Context, instance *protocol.InstanceManifest, p provider.Provider, ref protocol.ResolvedRef) error {
	discoverer, ok := p.(provider.NestedDiscoverer)
	if !ok {
		return nil
	}
	children, err := discoverer.DiscoverChildren(ctx, ref)
	if err != nil {
		return fmt.Errorf("discover nested components through provider %s: %w", p.Contract().String(), err)
	}
	for _, relative := range children {
		cleanRelative := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
		if relative == "" || cleanRelative != relative || cleanRelative == "." || cleanRelative == ".." || strings.HasPrefix(cleanRelative, "../") || filepath.IsAbs(filepath.FromSlash(relative)) {
			return fmt.Errorf("provider %s returned unsafe nested root %q", p.Contract().String(), relative)
		}
		childPath, err := cleanTrackedPath(ref.Path + "/" + cleanRelative)
		if err != nil || !pathContains(ref.Path, childPath) {
			return fmt.Errorf("provider %s returned unsafe nested root %q", p.Contract().String(), relative)
		}
		if existing := componentAtPath(instance.Components, childPath); existing >= 0 {
			child := instance.Components[existing]
			childProvider, err := e.providerFor(child.Provider)
			if err != nil {
				return err
			}
			if err := e.adoptDiscoveredChildren(ctx, instance, childProvider, child); err != nil {
				return err
			}
			continue
		}
		childTarget := filepath.Join(ref.Materialized, filepath.FromSlash(cleanRelative))
		candidates, err := e.discoveryProviders()
		if err != nil {
			return fmt.Errorf("nested component %q: %w", childPath, err)
		}
		childProvider, _, err := matchProvider(ctx, childTarget, candidates)
		if err != nil {
			return fmt.Errorf("nested component %q: %w", childPath, err)
		}
		if childProvider == nil {
			return fmt.Errorf("nested component %q: no provider matches %q", childPath, childTarget)
		}
		if _, err := e.adoptComponentTree(ctx, instance, childProvider, childPath, childTarget); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) addThroughProvider(ctx context.Context, instance protocol.InstanceManifest, component protocol.ResolvedRef, workspacePath string) error {
	p, err := e.providerFor(component.Provider)
	if err != nil {
		return err
	}
	tracker, ok := p.(provider.Tracker)
	if !ok {
		return nil
	}
	component.Children = directResolvedChildren(component, instance.Components, nil)
	providerPath := strings.TrimPrefix(strings.TrimPrefix(workspacePath, component.Path), "/")
	if providerPath == "" {
		providerPath = "."
	}
	if err := tracker.Add(ctx, component, []string{providerPath}); err != nil {
		return fmt.Errorf("add through provider %s: %w", component.Provider.String(), err)
	}
	return nil
}

func (e *Engine) trackParentBoundary(ctx context.Context, instance protocol.InstanceManifest, child protocol.ResolvedRef) error {
	parentIndex := -1
	for i, candidate := range instance.Components {
		if !pathContains(candidate.Path, child.Path) {
			continue
		}
		if parentIndex < 0 || len(candidate.Path) > len(instance.Components[parentIndex].Path) {
			parentIndex = i
		}
	}
	if parentIndex < 0 {
		return nil
	}
	parent := instance.Components[parentIndex]
	p, err := e.providerFor(parent.Provider)
	if err != nil {
		return err
	}
	tracker, ok := p.(provider.BoundaryTracker)
	if !ok {
		return nil
	}
	parent.Children = directResolvedChildren(parent, instance.Components, nil)
	if err := tracker.TrackChild(ctx, parent, child); err != nil {
		return fmt.Errorf("track child boundary through provider %s: %w", parent.Provider.String(), err)
	}
	return nil
}

func (e *Engine) discoverProviderRoot(ctx context.Context, workdir, rel, target string) (provider.Provider, string, string, error) {
	candidates, err := e.discoveryProviders()
	if err != nil {
		return nil, "", "", err
	}
	current := target
	bestPath, bestTarget := rel, target
	var best provider.Provider
	bestScore := 0
	for {
		p, score, err := matchProvider(ctx, current, candidates)
		if err != nil {
			return nil, "", "", err
		}
		if score > bestScore {
			best, bestScore, bestTarget = p, score, current
			bestPath, err = filepath.Rel(workdir, current)
			if err != nil {
				return nil, "", "", err
			}
			bestPath = filepath.ToSlash(bestPath)
		}
		parent := filepath.Dir(current)
		if parent == current || parent == workdir {
			break
		}
		current = parent
	}
	if best == nil {
		return nil, "", "", fmt.Errorf("no provider matches %q", rel)
	}
	return best, bestPath, bestTarget, nil
}

func matchProvider(ctx context.Context, path string, candidates []provider.Provider) (provider.Provider, int, error) {
	var selected provider.Provider
	best := 0
	matches := 0
	for _, candidate := range candidates {
		score, err := candidate.Match(ctx, path)
		if err != nil {
			return nil, 0, fmt.Errorf("match provider %s: %w", candidate.Contract().String(), err)
		}
		if score > best {
			selected, best, matches = candidate, score, 1
		} else if score > 0 && score == best {
			matches++
		}
	}
	if matches > 1 {
		return nil, 0, fmt.Errorf("path %q matches multiple providers at priority %d", path, best)
	}
	return selected, best, nil
}

func statusFor(instance protocol.InstanceManifest) (WorktreeStatus, error) {
	status := WorktreeStatus{Components: make([]string, 0, len(instance.Components)), Untracked: []string{}, Ignored: []string{}}
	for _, component := range instance.Components {
		status.Components = append(status.Components, component.Path)
	}
	matcher, err := loadIgnore(instance.Paths.Workdir)
	if err != nil {
		return status, err
	}
	err = filepath.Walk(instance.Paths.Workdir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == instance.Paths.Workdir {
			return nil
		}
		rel, err := filepath.Rel(instance.Paths.Workdir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".loop" || rel == ".lxp" {
			return filepath.SkipDir
		}
		if rel == ".lxpignore" {
			return nil
		}
		owned, ancestor := false, false
		for _, component := range instance.Components {
			if rel == component.Path || strings.HasPrefix(rel, component.Path+"/") {
				owned = true
				break
			}
			if strings.HasPrefix(component.Path, rel+"/") {
				ancestor = true
			}
		}
		if owned {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ancestor {
			return nil
		}
		ignoredPath := rel
		if info.IsDir() {
			ignoredPath += "/"
		}
		if matcher != nil && matcher.MatchesPath(ignoredPath) {
			status.Ignored = append(status.Ignored, rel)
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		status.Untracked = append(status.Untracked, rel)
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(status.Components)
	sort.Strings(status.Untracked)
	sort.Strings(status.Ignored)
	status.Ready = len(status.Untracked) == 0
	if status.Ready {
		status.Prompt = "No action required."
	} else {
		status.Prompt = "Track every untracked path with lxp add or add a matching rule to .lxpignore, then refresh status."
	}
	return status, err
}

func loadIgnore(workdir string) (*ignore.GitIgnore, error) {
	path := filepath.Join(workdir, ".lxpignore")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ignore.CompileIgnoreLines(strings.Split(string(data), "\n")...), nil
}

func cleanTrackedPath(raw string) (string, error) {
	rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if rel == "." || filepath.IsAbs(filepath.FromSlash(raw)) || rel == ".." || strings.HasPrefix(rel, "../") || rel == ".loop" || strings.HasPrefix(rel, ".loop/") || rel == ".lxp" || strings.HasPrefix(rel, ".lxp/") || rel == ".lxpignore" {
		return "", fmt.Errorf("unsafe or reserved tracked path %q", raw)
	}
	return rel, nil
}

func componentID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "component-" + hex.EncodeToString(sum[:6])
}
