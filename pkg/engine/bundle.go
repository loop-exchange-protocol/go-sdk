package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/bundle"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	lxpruntime "github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
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

// ReadyBundleImport returns a completed same-Artifact Session without resolving
// extensions or running Checks. A non-ready target is reported with ready=false.
func (e *Engine) ReadyBundleImport(opts BundleImportOptions) (protocol.InstanceManifest, bool, error) {
	if !spec.ValidIdentifier(opts.SessionID) {
		return protocol.InstanceManifest{}, false, fmt.Errorf("invalid session id %q", opts.SessionID)
	}
	stage, artifact, manifestDigest, err := e.stageBundle(opts.Bundle)
	if err != nil {
		return protocol.InstanceManifest{}, false, err
	}
	defer os.RemoveAll(stage)
	workdir := opts.Workdir
	if workdir == "" {
		workdir = filepath.Join(e.Root, "sessions", opts.SessionID, "workdir")
	}
	workdir, err = protocol.CanonicalPath(workdir)
	if err != nil {
		return protocol.InstanceManifest{}, false, err
	}
	manifestPath := filepath.Join(e.Root, "sessions", opts.SessionID, "manifest.yaml")
	existing, _, err := inspectImportTarget(workdir, manifestPath, opts.SessionID, manifestDigest, artifact.Components)
	if err != nil {
		return protocol.InstanceManifest{}, false, err
	}
	return existing, existing.Metadata["import_state"] == "ready", nil
}

func (e *Engine) ValidateBundle(ctx context.Context, path string) (spec.Artifact, error) {
	stage, artifact, _, err := e.stageBundle(path)
	if err != nil {
		return spec.Artifact{}, err
	}
	defer os.RemoveAll(stage)
	if err := e.validateExtensions(artifact); err != nil {
		return spec.Artifact{}, err
	}
	store := bundle.Store{Root: stage}
	for _, component := range sortedArtifact(artifact.Components, true) {
		p, err := e.providerFor(component.Provider)
		if err != nil {
			return spec.Artifact{}, err
		}
		target := provider.ApplyTarget{Workdir: filepath.Join(stage, "validation-target"), Path: filepath.Join(stage, "validation-target", filepath.FromSlash(component.Path)), Children: directArtifactChildren(component, artifact.Components)}
		if err := p.Validate(ctx, component, store, target); err != nil {
			return spec.Artifact{}, fmt.Errorf("validate component %q: %w", component.ID, err)
		}
	}
	return artifact, nil
}

func (e *Engine) stageBundle(path string) (string, spec.Artifact, string, error) {
	stage, err := os.MkdirTemp("", "lxp-import-")
	if err != nil {
		return "", spec.Artifact{}, "", err
	}
	fail := func(err error) (string, spec.Artifact, string, error) {
		_ = os.RemoveAll(stage)
		return "", spec.Artifact{}, "", err
	}
	if err := bundle.Unpack(path, stage); err != nil {
		return fail(err)
	}
	artifact, err := spec.ReadArtifact(filepath.Join(stage, "manifest.yaml"))
	if err != nil {
		return fail(err)
	}
	if err := validateBundleEnvelope(stage, artifact); err != nil {
		return fail(err)
	}
	if err := verifyPayloads(artifact, bundle.Store{Root: stage}); err != nil {
		return fail(err)
	}
	digest, err := fileDigest(filepath.Join(stage, "manifest.yaml"))
	if err != nil {
		return fail(err)
	}
	return stage, artifact, digest, nil
}

func (e *Engine) validateExtensions(artifact spec.Artifact) error {
	_, err := e.resolveExtensions(artifact)
	return err
}

func (e *Engine) resolveExtensions(artifact spec.Artifact) ([]protocol.ExtensionResolution, error) {
	resolved := map[string]protocol.ExtensionResolution{}
	for _, component := range artifact.Components {
		p, err := e.providerFor(component.Provider)
		if err != nil {
			return nil, fmt.Errorf("component %q: %w", component.ID, err)
		}
		implementation, _ := e.Extensions.Resolve("provider", component.Provider)
		resolved[component.Provider.String()] = protocol.ExtensionResolution{Kind: "provider", Contract: component.Provider, Source: implementation.Source, Implementation: p.Implementation(), Digest: implementation.Digest}
	}
	for _, requirement := range artifact.Requirements {
		checker, err := e.checkerFor(requirement.Check.Checker)
		if err != nil {
			return nil, fmt.Errorf("requirement %q: %w", requirement.ID, err)
		}
		implementation, _ := e.Extensions.Resolve("checker", requirement.Check.Checker)
		resolved[requirement.Check.Checker.String()] = protocol.ExtensionResolution{Kind: "checker", Contract: requirement.Check.Checker, Source: implementation.Source, Implementation: checker.Implementation(), Digest: implementation.Digest}
	}
	out := make([]protocol.ExtensionResolution, 0, len(resolved))
	for _, resolution := range resolved {
		out = append(out, resolution)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Contract.String() < out[j].Contract.String() })
	return out, nil
}

func sameExtensionResolutions(a, b []protocol.ExtensionResolution) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	portableByID := make(map[string]spec.Component, len(instance.Components))
	revisions := make(map[string]string, len(instance.Components))
	for _, ref := range sortedResolved(instance.Components, false) {
		p, err := e.providerFor(ref.Provider)
		if err != nil {
			return "", err
		}
		mode, err := distributionFor(p, ref.ID, opts)
		if err != nil {
			return "", err
		}
		ref.Children = directResolvedChildren(ref, instance.Components, revisions)
		portable, err := p.ExportComponent(ctx, ref, mode, store)
		if err != nil {
			return "", fmt.Errorf("export component %q: %w", ref.ID, err)
		}
		portableByID[portable.ID] = portable
		revisions[portable.ID] = componentRevision(portable)
	}
	for _, ref := range sortedResolved(instance.Components, true) {
		portable, ok := portableByID[ref.ID]
		if !ok {
			return "", fmt.Errorf("component %q was not exported", ref.ID)
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
	stage, artifact, manifestDigest, err := e.stageBundle(opts.Bundle)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	defer os.RemoveAll(stage)
	workdir := opts.Workdir
	if workdir == "" {
		workdir = filepath.Join(e.Root, "sessions", opts.SessionID, "workdir")
	}
	workdir, err = protocol.CanonicalPath(workdir)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	sessionDir := filepath.Join(e.Root, "sessions", opts.SessionID)
	manifestPath := filepath.Join(sessionDir, "manifest.yaml")
	existing, retry, err := inspectImportTarget(workdir, manifestPath, opts.SessionID, manifestDigest, artifact.Components)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	if existing.Metadata["import_state"] == "ready" {
		return existing, nil
	}
	resolutions, err := e.resolveExtensions(artifact)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	if retry && !sameExtensionResolutions(existing.Extensions, resolutions) {
		return protocol.InstanceManifest{}, fmt.Errorf("Import extension resolution changed; retry requires the implementations pinned by the importing Session")
	}
	store := bundle.Store{Root: stage}
	runtimeOptions := lxpruntime.Options{SecretEnv: opts.SecretEnv, AllowMCP: opts.AllowMCP, AllowExecutables: opts.AllowExecutables}
	observations, err := e.Checkers.Resolve(ctx, artifact.Requirements, requiredArtifactRequirements(artifact.Components), runtimeOptions)
	if err != nil {
		return protocol.InstanceManifest{}, err
	}
	for _, component := range sortedArtifact(artifact.Components, true) {
		target, err := bundle.SafeJoin(workdir, component.Path)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		p, err := e.providerFor(component.Provider)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		applyTarget := provider.ApplyTarget{Workdir: workdir, Path: target, Children: directArtifactChildren(component, artifact.Components)}
		if hasArtifactAncestor(component, artifact.Components) {
			if err := validateNestedTarget(workdir, target); err != nil {
				return protocol.InstanceManifest{}, fmt.Errorf("validate component %q target: %w", component.ID, err)
			}
		}
		if err := p.Validate(ctx, component, store, applyTarget); err != nil {
			return protocol.InstanceManifest{}, fmt.Errorf("validate component %q: %w", component.ID, err)
		}
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return protocol.InstanceManifest{}, err
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return protocol.InstanceManifest{}, err
	}
	instance := existing
	if !retry {
		instance = protocol.InstanceManifest{Kind: "LoopSessionInstance", APIVersion: spec.APIVersion, ID: opts.SessionID, Requirements: artifact.Requirements, Extensions: resolutions, Paths: protocol.InstancePaths{SessionDir: sessionDir, Workdir: workdir, Manifest: manifestPath}, Metadata: map[string]string{"artifact": artifact.Coordinates.Namespace + ":" + artifact.Coordinates.Name + ":" + artifact.Coordinates.Version, "artifact_digest": manifestDigest, "parent_artifact": manifestDigest, "import_state": "importing"}}
		if err := protocol.WriteYAML(manifestPath, instance); err != nil {
			return protocol.InstanceManifest{}, err
		}
	}
	if err := spec.Write(filepath.Join(sessionDir, "requirements.state.yaml"), map[string]any{"api_version": spec.APIVersion, "kind": "RequirementState", "observations": observations}); err != nil {
		return protocol.InstanceManifest{}, err
	}
	for _, component := range sortedArtifact(artifact.Components, true) {
		if observedComponentComplete(instance.Components, component) {
			continue
		}
		target, _ := bundle.SafeJoin(workdir, component.Path)
		p, err := e.providerFor(component.Provider)
		if err != nil {
			return protocol.InstanceManifest{}, err
		}
		observed, err := p.Apply(ctx, component, store, provider.ApplyTarget{Workdir: workdir, Path: target, Children: directArtifactChildren(component, artifact.Components)})
		if err != nil {
			return protocol.InstanceManifest{}, fmt.Errorf("apply component %q: %w; retry the same import to continue", component.ID, err)
		}
		instance.Components = replaceObserved(instance.Components, observed)
		if err := protocol.WriteYAML(manifestPath, instance); err != nil {
			return protocol.InstanceManifest{}, err
		}
	}
	instance.Metadata["import_state"] = "ready"
	if err := protocol.WriteYAML(manifestPath, instance); err != nil {
		return protocol.InstanceManifest{}, err
	}
	return instance, nil
}

func observedComponentComplete(observed []protocol.ResolvedRef, desired spec.Component) bool {
	wantRevision := componentRevision(desired)
	for _, ref := range observed {
		if ref.ID == desired.ID && ref.Path == desired.Path && ref.Provider == desired.Provider && ref.Revision == wantRevision {
			return true
		}
	}
	return false
}

func inspectImportTarget(workdir, manifestPath, sessionID, artifactDigest string, components []spec.Component) (protocol.InstanceManifest, bool, error) {
	_, manifestErr := os.Lstat(manifestPath)
	hasState := manifestErr == nil
	if manifestErr != nil && !os.IsNotExist(manifestErr) {
		return protocol.InstanceManifest{}, false, manifestErr
	}
	info, err := os.Lstat(workdir)
	if os.IsNotExist(err) {
		if hasState {
			return protocol.InstanceManifest{}, false, fmt.Errorf("Session state exists but import target %q is missing", workdir)
		}
		return protocol.InstanceManifest{}, false, nil
	}
	if err != nil {
		return protocol.InstanceManifest{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return protocol.InstanceManifest{}, false, fmt.Errorf("import target %q must be a directory, not a symlink", workdir)
	}
	entries, err := os.ReadDir(workdir)
	if err != nil {
		return protocol.InstanceManifest{}, false, err
	}
	if !hasState {
		if len(entries) == 0 {
			return protocol.InstanceManifest{}, false, nil
		}
		return protocol.InstanceManifest{}, false, fmt.Errorf("import target %q is not empty and has no retryable LXP state", workdir)
	}
	instance, err := protocol.ReadYAML[protocol.InstanceManifest](manifestPath)
	if err != nil {
		return protocol.InstanceManifest{}, false, fmt.Errorf("import target %q is not empty and has no retryable LXP state", workdir)
	}
	if instance.ID != sessionID || instance.Metadata["artifact_digest"] != artifactDigest {
		return protocol.InstanceManifest{}, false, fmt.Errorf("import target belongs to a different Session or Artifact")
	}
	if filepath.Clean(instance.Paths.Workdir) != filepath.Clean(workdir) || filepath.Clean(instance.Paths.Manifest) != filepath.Clean(manifestPath) {
		return protocol.InstanceManifest{}, false, fmt.Errorf("import target Session paths do not match this target")
	}
	state := instance.Metadata["import_state"]
	if state != "importing" && state != "ready" {
		return protocol.InstanceManifest{}, false, fmt.Errorf("import target has invalid state %q", state)
	}
	if state == "importing" {
		if err := validateRetryOwnership(workdir, components); err != nil {
			return protocol.InstanceManifest{}, false, err
		}
	}
	return instance, true, nil
}

func validateRetryOwnership(workdir string, components []spec.Component) error {
	return filepath.Walk(workdir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == workdir {
			return nil
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".lxp" {
			return filepath.SkipDir
		}
		for _, component := range components {
			if rel == component.Path || strings.HasPrefix(rel, component.Path+"/") || strings.HasPrefix(component.Path, rel+"/") {
				return nil
			}
		}
		return fmt.Errorf("import target contains unowned retry path %q", rel)
	})
}

func replaceObserved(components []protocol.ResolvedRef, observed protocol.ResolvedRef) []protocol.ResolvedRef {
	for i := range components {
		if components[i].ID == observed.ID {
			components[i] = observed
			return components
		}
	}
	return append(components, observed)
}

func hasArtifactAncestor(component spec.Component, components []spec.Component) bool {
	for _, candidate := range components {
		if pathContains(candidate.Path, component.Path) {
			return true
		}
	}
	return false
}

func validateNestedTarget(workdir, target string) error {
	rel, err := filepath.Rel(workdir, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("nested Component target is outside the workdir")
	}
	current := workdir
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("nested Component path crosses symlink %q", filepath.ToSlash(strings.Join(parts[:i+1], string(filepath.Separator))))
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("nested Component ancestor %q is not a directory", filepath.ToSlash(strings.Join(parts[:i+1], string(filepath.Separator))))
		}
		if i == len(parts)-1 {
			if !info.IsDir() {
				return fmt.Errorf("nested Component target is not a directory")
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

func validateBundleEnvelope(root string, artifact spec.Artifact) error {
	allowedFiles := map[string]bool{"manifest.yaml": true}
	allowedDirectories := map[string]bool{".": true}
	for _, component := range artifact.Components {
		if component.Embedded == nil {
			continue
		}
		for _, payload := range component.Embedded.Payloads {
			hexDigest := strings.TrimPrefix(payload.Digest, "sha256:")
			allowedFiles[filepath.Join("objects", "sha256", hexDigest)] = true
			allowedDirectories["objects"] = true
			allowedDirectories[filepath.Join("objects", "sha256")] = true
		}
	}
	return filepath.Walk(root, func(entryPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, entryPath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if !allowedDirectories[rel] {
				return fmt.Errorf("bundle contains unexpected directory %q", filepath.ToSlash(rel))
			}
			return nil
		}
		if !allowedFiles[rel] {
			return fmt.Errorf("bundle contains unexpected file %q", filepath.ToSlash(rel))
		}
		return nil
	})
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
	return "", fmt.Errorf("component %q provider %s does not support %s distribution", id, componentProvider.Contract().String(), mode)
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
