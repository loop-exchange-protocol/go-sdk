package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loop-exchange-protocol/go-sdk/pkg/bundle"
	"github.com/loop-exchange-protocol/go-sdk/pkg/protocol"
	"github.com/loop-exchange-protocol/go-sdk/pkg/provider"
	lxpruntime "github.com/loop-exchange-protocol/go-sdk/pkg/runtime"
	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

type ExportOptions struct {
	SessionID             string
	Out                   string
	Namespace             string
	Name                  string
	Version               string
	Distribution          string
	ComponentDistribution map[string]string
}

type BundleImportOptions struct {
	Bundle           string
	SessionID        string
	Workdir          string
	AllowMCP         bool
	AllowExecutables bool
	SecretEnv        map[string]string
}

type ComponentPlan struct {
	Component    string   `json:"component"`
	Provider     string   `json:"provider"`
	Contract     string   `json:"contract"`
	Actions      []string `json:"actions"`
	Requirements []string `json:"requirements,omitempty"`
}

func (e *Engine) PlanBundle(ctx context.Context, path string) ([]ComponentPlan, error) {
	stage, err := os.MkdirTemp("", "lxp-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := bundle.Unpack(path, stage); err != nil {
		return nil, err
	}
	a, err := spec.ReadArtifact(filepath.Join(stage, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	if err := verifyPayloads(a, bundle.Store{Root: stage}); err != nil {
		return nil, err
	}
	lock, err := protocol.ReadYAML[spec.Lock](filepath.Join(stage, "lock.yaml"))
	if err != nil {
		return nil, err
	}
	digest, err := fileDigest(filepath.Join(stage, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	if err := validateArtifactLock(a, lock, digest); err != nil {
		return nil, err
	}
	return e.planArtifact(ctx, a)
}

func (e *Engine) planArtifact(ctx context.Context, a spec.Artifact) ([]ComponentPlan, error) {
	plans := make([]ComponentPlan, 0, len(a.Components))
	for _, component := range a.Components {
		p, err := e.Providers.Get(component.Provider)
		if err != nil {
			return nil, err
		}
		if component.Contract != p.Contract() {
			return nil, fmt.Errorf("component %q requires provider %s@%s; installed contract is %s", component.ID, component.Provider, component.Contract, p.Contract())
		}
		ref := protocol.ResolvedRef{ID: component.ID, Description: component.Description, Path: component.Path, Provider: component.Provider, Contract: component.Contract, Distribution: component.Distribution, Config: component.Config, Requires: component.Requires, Metadata: component.Metadata}
		if component.Reference != nil {
			ref.Source = component.Reference.Locator
			ref.Revision = component.Reference.Revision
		} else if component.Embedded != nil {
			ref.Revision = component.Embedded.Revision
		}
		plan, err := p.Plan(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("plan component %q: %w", component.ID, err)
		}
		if len(plan.Actions) == 0 {
			return nil, fmt.Errorf("provider %s@%s returned an empty plan for component %q", component.Provider, component.Contract, component.ID)
		}
		plans = append(plans, ComponentPlan{Component: component.ID, Provider: component.Provider, Contract: component.Contract, Actions: plan.Actions, Requirements: plan.Requirements})
	}
	return plans, nil
}

func (e *Engine) Export(ctx context.Context, opts ExportOptions) (string, error) {
	if !spec.ValidIdentifier(opts.Namespace) || !spec.ValidIdentifier(opts.Name) || !spec.ValidIdentifier(opts.Version) {
		return "", fmt.Errorf("export requires safe --namespace, --name, and --version coordinates")
	}
	instance, err := e.readInstance(opts.SessionID)
	if err != nil {
		return "", err
	}
	status, err := statusFor(instance)
	if err != nil {
		return "", err
	}
	if len(status.Untracked) > 0 {
		return "", fmt.Errorf("export blocked by unregistered paths: %s; run lxp add or update .lxpignore", strings.Join(status.Untracked, ", "))
	}
	if opts.Out == "" {
		opts.Out = filepath.Join(e.Root, "exports", opts.Namespace+"-"+opts.Name+"-"+opts.Version+".lxp")
	}
	isArchive := strings.HasSuffix(opts.Out, ".lxpz")
	if !isArchive && filepath.Ext(opts.Out) != ".lxp" {
		return "", fmt.Errorf("artifact directory must end in .lxp or archive in .lxpz")
	}
	if _, err := os.Stat(opts.Out); err == nil {
		return "", fmt.Errorf("artifact output %q already exists", opts.Out)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(opts.Out), 0o755); err != nil {
		return "", err
	}
	stageParent := ""
	if !isArchive {
		stageParent = filepath.Dir(opts.Out)
	}
	stage, err := os.MkdirTemp(stageParent, ".lxp-export-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	store := bundle.Store{Root: stage}

	a := spec.Artifact{
		APIVersion:   spec.APIVersion,
		Kind:         spec.Kind,
		Coordinates:  spec.Coordinates{Namespace: opts.Namespace, Name: opts.Name, Version: opts.Version, Variant: "checkpoint"},
		Requirements: instance.Requirements,
		Provenance:   spec.Provenance{CreatedAt: time.Now().UTC().Format(time.RFC3339), Engine: "lxp/0.1.0", Parent: instance.Metadata["parent_artifact"]},
	}
	for _, ref := range instance.Components {
		p, err := e.Providers.Get(ref.Provider)
		if err != nil {
			return "", err
		}
		mode, err := distributionFor(p, ref.ID, opts)
		if err != nil {
			return "", err
		}
		portable, err := p.ExportComponent(ctx, ref, mode, store)
		if err != nil {
			return "", fmt.Errorf("export component %q: %w", ref.ID, err)
		}
		a.Components = append(a.Components, portable)
	}
	if err := spec.Validate(a); err != nil {
		return "", err
	}

	manifestPath := filepath.Join(stage, "manifest.yaml")
	if err := spec.Write(manifestPath, a); err != nil {
		return "", err
	}
	manifestDigest, err := fileDigest(manifestPath)
	if err != nil {
		return "", err
	}
	lock := spec.Lock{APIVersion: spec.APIVersion, Artifact: manifestDigest}
	for _, ref := range a.Components {
		embedded := map[string]string{}
		if ref.Embedded != nil {
			for role, payload := range ref.Embedded.Payloads {
				embedded[role] = payload.Digest
			}
		}
		revision := ""
		if ref.Reference != nil {
			revision = ref.Reference.Revision
		} else if ref.Embedded != nil {
			revision = ref.Embedded.Revision
		}
		lock.Components = append(lock.Components, spec.RefLock{ID: ref.ID, Path: ref.Path, Provider: ref.Provider, Contract: ref.Contract, Distribution: ref.Distribution, Revision: revision, Embedded: embedded})
	}
	if err := spec.Write(filepath.Join(stage, "lock.yaml"), lock); err != nil {
		return "", err
	}
	if isArchive {
		if err := bundle.Pack(stage, opts.Out); err != nil {
			return "", err
		}
	} else {
		if err := os.Rename(stage, opts.Out); err != nil {
			return "", err
		}
	}
	if instance.Metadata == nil {
		instance.Metadata = map[string]string{}
	}
	instance.Metadata["parent_artifact"] = manifestDigest
	if err := protocol.WriteYAML(instance.Paths.Manifest, instance); err != nil {
		return "", fmt.Errorf("artifact written to %q but session parent update failed: %w", opts.Out, err)
	}
	return opts.Out, nil
}

func (e *Engine) ImportBundle(ctx context.Context, opts BundleImportOptions) (protocol.InstanceManifest, error) {
	if !spec.ValidIdentifier(opts.SessionID) {
		return protocol.InstanceManifest{}, fmt.Errorf("invalid session id %q", opts.SessionID)
	}
	cleanupTarget := ""
	completed := false
	if opts.Workdir != "" {
		absWorkdir, err := filepath.Abs(opts.Workdir)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		opts.Workdir = absWorkdir
		if _, err := os.Stat(absWorkdir); os.IsNotExist(err) {
			cleanupTarget = absWorkdir
		} else if err != nil {
			return protocol.InstanceManifest{}, err
		}
	}
	defer func() {
		if !completed && cleanupTarget != "" {
			_ = os.RemoveAll(cleanupTarget)
		}
	}()
	stage, err := os.MkdirTemp("", "lxp-import-")
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	defer os.RemoveAll(stage)
	if err := bundle.Unpack(opts.Bundle, stage); err != nil {
		return protocol.InstanceManifest{}, err
	}
	a, err := spec.ReadArtifact(filepath.Join(stage, "manifest.yaml"))
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	lock, err := protocol.ReadYAML[spec.Lock](filepath.Join(stage, "lock.yaml"))
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	manifestDigest, err := fileDigest(filepath.Join(stage, "manifest.yaml"))
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	if err := validateArtifactLock(a, lock, manifestDigest); err != nil {
		return protocol.InstanceManifest{}, err
	}
	store := bundle.Store{Root: stage}
	if err := verifyPayloads(a, store); err != nil {
		return protocol.InstanceManifest{}, err
	}
	if _, err := e.planArtifact(ctx, a); err != nil {
		return protocol.InstanceManifest{}, err
	}
	runtimeOptions := lxpruntime.Options{SecretEnv: opts.SecretEnv, AllowMCP: opts.AllowMCP, AllowExecutables: opts.AllowExecutables}
	runtimeLocks, err := lxpruntime.Resolve(ctx, environmentRequirements(a.Requirements), requiredArtifactRequirements(a.Components), runtimeOptions)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}

	sessionDir := filepath.Join(e.Root, "sessions", opts.SessionID)
	if _, err := os.Stat(sessionDir); err == nil {
		return protocol.InstanceManifest{}, fmt.Errorf("session %q already exists", opts.SessionID)
	} else if !os.IsNotExist(err) {
		return protocol.InstanceManifest{}, err
	}
	sessionsRoot := filepath.Join(e.Root, "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		return protocol.InstanceManifest{}, err
	}
	tempSession, err := os.MkdirTemp(sessionsRoot, ".import-"+opts.SessionID+"-")
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	defer os.RemoveAll(tempSession)
	workdir := filepath.Join(tempSession, "workdir")
	var refs []protocol.ResolvedRef
	for _, ref := range a.Components {
		target, err := bundle.SafeJoin(workdir, ref.Path)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		var materialized protocol.ResolvedRef
		p, providerErr := e.Providers.Get(ref.Provider)
		if providerErr != nil {
			return protocol.InstanceManifest{}, providerErr
		}
		if ref.Contract != p.Contract() {
			return protocol.InstanceManifest{}, fmt.Errorf("component %q requires provider %s@%s; installed contract is %s", ref.ID, ref.Provider, ref.Contract, p.Contract())
		}
		materialized, err = p.Restore(ctx, ref, store, provider.MaterializeTarget{Workdir: workdir, Path: target})
		if err != nil {
			return protocol.InstanceManifest{}, fmt.Errorf("import ref %q: %w", ref.ID, err)
		}
		refs = append(refs, materialized)
	}
	activationLocks, err := e.activateComponents(ctx, refs, a.Requirements, runtimeLocks, runtimeOptions)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	runtimeLocks = append(runtimeLocks, activationLocks...)
	if err := os.MkdirAll(filepath.Join(workdir, ".loop"), 0o755); err != nil {
		return protocol.InstanceManifest{}, err
	}
	if err := spec.Write(filepath.Join(workdir, ".loop", "context.yaml"), a); err != nil {
		return protocol.InstanceManifest{}, err
	}
	if err := spec.Write(filepath.Join(workdir, ".loop", "requirements.lock.yaml"), spec.Lock{APIVersion: spec.APIVersion, Requirements: runtimeLocks}); err != nil {
		return protocol.InstanceManifest{}, err
	}
	oldWorkdir := workdir
	if opts.Workdir == "" {
		if err := os.Rename(tempSession, sessionDir); err != nil {
			return protocol.InstanceManifest{}, err
		}
		workdir = filepath.Join(sessionDir, "workdir")
	} else {
		workdir, err = filepath.Abs(opts.Workdir)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		if err := os.MkdirAll(workdir, 0o755); err != nil {
			return protocol.InstanceManifest{}, err
		}
		entries, err := os.ReadDir(workdir)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		for _, entry := range entries {
			if entry.Name() != ".lxp" {
				return protocol.InstanceManifest{}, fmt.Errorf("import target %q is not empty", workdir)
			}
		}
		materializedEntries, err := os.ReadDir(oldWorkdir)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		for _, entry := range materializedEntries {
			if err := os.Rename(filepath.Join(oldWorkdir, entry.Name()), filepath.Join(workdir, entry.Name())); err != nil {
				return protocol.InstanceManifest{}, err
			}
		}
		if err := os.Remove(oldWorkdir); err != nil {
			return protocol.InstanceManifest{}, err
		}
		if err := os.Rename(tempSession, sessionDir); err != nil {
			return protocol.InstanceManifest{}, err
		}
	}
	for i := range refs {
		refs[i].PoolPath = strings.Replace(refs[i].PoolPath, oldWorkdir, workdir, 1)
		refs[i].Materialized = strings.Replace(refs[i].Materialized, oldWorkdir, workdir, 1)
	}
	instance := protocol.InstanceManifest{Kind: "LoopSessionInstance", APIVersion: spec.APIVersion, ID: opts.SessionID, Components: refs, Requirements: a.Requirements, Paths: protocol.InstancePaths{SessionDir: sessionDir, Workdir: workdir, Manifest: filepath.Join(sessionDir, "manifest.yaml")}, Metadata: map[string]string{"artifact": a.Coordinates.Namespace + ":" + a.Coordinates.Name + ":" + a.Coordinates.Version, "parent_artifact": manifestDigest}}
	if err := protocol.WriteYAML(instance.Paths.Manifest, instance); err != nil {
		return protocol.InstanceManifest{}, err
	}
	completed = true
	return instance, nil
}

func validateArtifactLock(a spec.Artifact, lock spec.Lock, manifestDigest string) error {
	if lock.APIVersion != spec.APIVersion || lock.Artifact != manifestDigest {
		return fmt.Errorf("manifest does not match lock digest")
	}
	if len(lock.Components) != len(a.Components) {
		return fmt.Errorf("component lock count does not match manifest")
	}
	locked := make(map[string]spec.RefLock, len(lock.Components))
	for _, item := range lock.Components {
		if _, exists := locked[item.ID]; exists {
			return fmt.Errorf("duplicate component lock %q", item.ID)
		}
		locked[item.ID] = item
	}
	for _, component := range a.Components {
		item, ok := locked[component.ID]
		if !ok || item.Path != component.Path || item.Provider != component.Provider || item.Contract != component.Contract || item.Distribution != component.Distribution {
			return fmt.Errorf("component %q does not match lock", component.ID)
		}
		revision := ""
		if component.Reference != nil {
			revision = component.Reference.Revision
		} else if component.Embedded != nil {
			revision = component.Embedded.Revision
		}
		if item.Revision != revision {
			return fmt.Errorf("component %q revision does not match lock", component.ID)
		}
		embedded := map[string]string{}
		if component.Embedded != nil {
			for role, payload := range component.Embedded.Payloads {
				embedded[role] = payload.Digest
			}
		}
		if len(item.Embedded) != len(embedded) {
			return fmt.Errorf("component %q payload lock does not match manifest", component.ID)
		}
		for role, digest := range embedded {
			if item.Embedded[role] != digest {
				return fmt.Errorf("component %q payload %q does not match lock", component.ID, role)
			}
		}
	}
	return nil
}

func validateWorkspaceLink(path, target string) error {
	if target == "" || filepath.IsAbs(filepath.FromSlash(target)) {
		return fmt.Errorf("workspace symlink %q has unsafe target %q", path, target)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(path)), filepath.FromSlash(target)))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace symlink %q escapes workdir", path)
	}
	return nil
}

func verifyPayloads(a spec.Artifact, store bundle.Store) error {
	var payloads []spec.Payload
	for _, ref := range a.Components {
		if ref.Embedded != nil {
			for _, payload := range ref.Embedded.Payloads {
				payloads = append(payloads, payload)
			}
		}
	}
	for _, payload := range payloads {
		f, err := store.Open(payload.Digest)
		if err != nil {
			return err
		}
		info, _ := f.Stat()
		f.Close()
		if info != nil && info.Size() != payload.Size {
			return fmt.Errorf("payload %s size mismatch", payload.Digest)
		}
	}
	return nil
}

func distributionFor(componentProvider provider.Provider, id string, opts ExportOptions) (string, error) {
	mode := opts.Distribution
	if override := opts.ComponentDistribution[id]; override != "" {
		mode = override
	}
	if mode == "" || mode == "auto" {
		for _, supported := range componentProvider.Distributions() {
			if supported == "reference" {
				return "reference", nil
			}
		}
		return "embedded", nil
	}
	if mode != "reference" && mode != "embedded" && mode != "mirrored" {
		return "", fmt.Errorf("unsupported distribution %q", mode)
	}
	for _, supported := range componentProvider.Distributions() {
		if mode == supported {
			return mode, nil
		}
	}
	return "", fmt.Errorf("component %q provider %q does not support %s distribution", id, componentProvider.Name(), mode)
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
