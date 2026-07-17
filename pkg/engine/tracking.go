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

	"github.com/loop-exchange-protocol/go-sdk/pkg/protocol"
	"github.com/loop-exchange-protocol/go-sdk/pkg/provider"
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
	instance, err := e.readInstance(sessionID)
	if err != nil {
		return WorktreeStatus{}, err
	}
	status, err := statusFor(instance)
	if err != nil {
		return status, err
	}
	for _, component := range instance.Components {
		p, err := e.Providers.Get(component.Provider)
		if err != nil {
			return status, err
		}
		tracker, ok := p.(provider.Tracker)
		if !ok {
			continue
		}
		changes, err := tracker.Status(context.Background(), component)
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
	Provider string
	Contract string
}

func (e *Engine) Add(sessionID string, paths []string) (protocol.InstanceManifest, error) {
	return e.AddWithOptions(sessionID, paths, AddOptions{})
}

func (e *Engine) AddWithOptions(sessionID string, paths []string, opts AddOptions) (protocol.InstanceManifest, error) {
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
		owned := false
		for _, component := range instance.Components {
			if rel == component.Path || strings.HasPrefix(rel, component.Path+"/") {
				if opts.Provider != "" && (component.Provider != opts.Provider || component.Contract != opts.Contract) {
					return protocol.InstanceManifest{}, fmt.Errorf("path %q is owned by %s@%s, not requested %s@%s", rel, component.Provider, component.Contract, opts.Provider, opts.Contract)
				}
				p, err := e.Providers.Get(component.Provider)
				if err != nil {
					return protocol.InstanceManifest{}, err
				}
				tracker, ok := p.(provider.Tracker)
				if !ok {
					owned = true
					break
				}
				providerPath := strings.TrimPrefix(strings.TrimPrefix(rel, component.Path), "/")
				if providerPath == "" {
					providerPath = "."
				}
				if err := tracker.Add(context.Background(), component, []string{providerPath}); err != nil {
					return protocol.InstanceManifest{}, fmt.Errorf("add through provider %q: %w", component.Provider, err)
				}
				owned = true
				break
			}
			if protocol.PathsOverlap(component.Path, rel) {
				return protocol.InstanceManifest{}, fmt.Errorf("path %q overlaps component %q", rel, component.ID)
			}
		}
		if owned {
			continue
		}
		var p provider.Provider
		componentPath := rel
		componentTarget := target
		if opts.Provider != "" {
			p, err = e.Providers.Get(opts.Provider)
			if err != nil {
				return protocol.InstanceManifest{}, err
			}
			if opts.Contract != p.Contract() {
				return protocol.InstanceManifest{}, fmt.Errorf("requested provider %s@%s; installed contract is %s", opts.Provider, opts.Contract, p.Contract())
			}
		} else {
			p, componentPath, componentTarget, err = e.discoverProviderRoot(context.Background(), instance.Paths.Workdir, rel, target)
			if err != nil {
				return protocol.InstanceManifest{}, err
			}
		}
		for _, component := range instance.Components {
			if protocol.PathsOverlap(component.Path, componentPath) {
				return protocol.InstanceManifest{}, fmt.Errorf("discovered root %q overlaps component %q", componentPath, component.ID)
			}
		}
		ref := protocol.ResolvedRef{ID: componentID(componentPath), Path: componentPath, Provider: p.Name(), Contract: p.Contract(), Source: "session", PoolPath: componentTarget, Materialized: componentTarget}
		if adopter, ok := p.(provider.Adopter); ok {
			ref, err = adopter.Adopt(context.Background(), ref.ID, componentPath, componentTarget)
			if err != nil {
				return protocol.InstanceManifest{}, fmt.Errorf("adopt through provider %q: %w", p.Name(), err)
			}
		}
		instance.Components = append(instance.Components, ref)
		if tracker, ok := p.(provider.Tracker); ok {
			selection := strings.TrimPrefix(strings.TrimPrefix(rel, componentPath), "/")
			if selection == "" {
				selection = "."
			}
			if err := tracker.Add(context.Background(), ref, []string{selection}); err != nil {
				return protocol.InstanceManifest{}, fmt.Errorf("add through provider %q: %w", p.Name(), err)
			}
		}
	}
	if err := protocol.WriteYAML(instance.Paths.Manifest, instance); err != nil {
		return protocol.InstanceManifest{}, err
	}
	return instance, nil
}

func (e *Engine) discoverProviderRoot(ctx context.Context, workdir, rel, target string) (provider.Provider, string, string, error) {
	current := target
	bestPath, bestTarget := rel, target
	var best provider.Provider
	bestScore := 0
	for {
		p, score, err := e.Providers.MatchScore(ctx, current)
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
