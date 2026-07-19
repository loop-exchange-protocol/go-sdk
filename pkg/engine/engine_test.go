package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loop-exchange-protocol/go-sdk/pkg/bundle"
	"github.com/loop-exchange-protocol/go-sdk/pkg/protocol"
	"github.com/loop-exchange-protocol/go-sdk/pkg/provider"
	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

type fakeProvider struct{}

type failingProvider struct{ fakeProvider }

type planningProvider struct {
	fakeProvider
	seen *protocol.ResolvedRef
}

type blockingTracker struct{ fakeProvider }

func (blockingTracker) Add(ctx context.Context, _ protocol.ResolvedRef, _ []string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingTracker) Status(ctx context.Context, _ protocol.ResolvedRef) ([]provider.Change, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (failingProvider) Restore(context.Context, spec.Component, bundle.Store, provider.MaterializeTarget) (protocol.ResolvedRef, error) {
	return protocol.ResolvedRef{}, fmt.Errorf("restore failed")
}

func (p planningProvider) Plan(_ context.Context, ref protocol.ResolvedRef) (provider.Plan, error) {
	*p.seen = ref
	return provider.Plan{Actions: []string{"inspect distribution-aware plan"}}, nil
}

func (fakeProvider) Name() string                               { return "fake" }
func (fakeProvider) Contract() string                           { return "v1" }
func (fakeProvider) Match(context.Context, string) (int, error) { return 1, nil }
func (fakeProvider) Plan(context.Context, protocol.ResolvedRef) (provider.Plan, error) {
	return provider.Plan{Actions: []string{"materialize deterministic fixture"}}, nil
}
func (fakeProvider) Distributions() []string { return []string{"embedded"} }
func (fakeProvider) Resolve(_ context.Context, ref protocol.RefSpec) (protocol.ResolvedRef, error) {
	return protocol.ResolvedRef{ID: ref.ID, Path: ref.Path, Provider: "fake", Contract: "v1", Revision: "sha256:fixture"}, nil
}
func (fakeProvider) Materialize(_ context.Context, ref protocol.ResolvedRef, target provider.MaterializeTarget) (protocol.ResolvedRef, error) {
	if err := os.MkdirAll(target.Path, 0o755); err != nil {
		return protocol.ResolvedRef{}, err
	}
	if err := os.WriteFile(filepath.Join(target.Path, "fixture.txt"), []byte("fake\n"), 0o644); err != nil {
		return protocol.ResolvedRef{}, err
	}
	ref.Materialized = target.Path
	return ref, nil
}
func (fakeProvider) ExportComponent(context.Context, protocol.ResolvedRef, string, bundle.Store) (spec.Component, error) {
	return spec.Component{}, nil
}
func (fakeProvider) Restore(context.Context, spec.Component, bundle.Store, provider.MaterializeTarget) (protocol.ResolvedRef, error) {
	return protocol.ResolvedRef{}, nil
}
func (fakeProvider) Activate(context.Context, protocol.ResolvedRef) error { return nil }

func TestPlanBundleUsesOnlyInjectedProvider(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, "v1")
	plans, err := New(filepath.Join(tmp, "state"), fakeProvider{}).PlanBundle(context.Background(), artifactDir)
	if err != nil || len(plans) != 1 || plans[0].Provider != "fake" {
		t.Fatalf("injected provider plan = %#v, %v", plans, err)
	}
	if _, err := New(filepath.Join(tmp, "missing-state")).PlanBundle(context.Background(), artifactDir); err == nil {
		t.Fatal("missing provider unexpectedly planned")
	}
	mismatchDir := filepath.Join(tmp, "contract-mismatch.lxp")
	writeReferenceArtifact(t, mismatchDir, "v2")
	if _, err := New(filepath.Join(tmp, "mismatch-state"), fakeProvider{}).PlanBundle(context.Background(), mismatchDir); err == nil {
		t.Fatal("mismatched Provider contract unexpectedly planned")
	}
}

func TestAddWithOptionsContextPropagatesCancellationToProvider(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "work")
	e := New(filepath.Join(workdir, ".lxp"), blockingTracker{})
	if _, err := e.InitAt(context.Background(), "work", workdir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "owned.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := e.AddWithOptionsContext(ctx, "work", []string{"owned.txt"}, AddOptions{Provider: "fake", Contract: "v1"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Add cancellation error = %v", err)
	}
}

func TestPlanBundlePassesDistributionDescriptorToProvider(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, "v1")
	var seen protocol.ResolvedRef
	if _, err := New(filepath.Join(tmp, "state"), planningProvider{seen: &seen}).PlanBundle(context.Background(), artifactDir); err != nil {
		t.Fatal(err)
	}
	if seen.Distribution != "reference" || seen.Source != "https://example.invalid/fixture" || seen.Revision != "revision" {
		t.Fatalf("plan descriptor = %#v", seen)
	}
}

func writeReferenceArtifact(t *testing.T, artifactDir, contract string) {
	t.Helper()
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := spec.Artifact{
		APIVersion:  spec.APIVersion,
		Kind:        spec.Kind,
		Coordinates: spec.Coordinates{Namespace: "test", Name: "fixture", Version: "1"},
		Components: []spec.Component{{
			ID: "fixture", Path: "fixture", Provider: "fake", Contract: contract, Distribution: "reference",
			Reference: &spec.Reference{Locator: "https://example.invalid/fixture", Revision: "revision"},
		}},
		Provenance: spec.Provenance{CreatedAt: "2026-07-16T00:00:00Z", Engine: "test"},
	}
	manifestPath := filepath.Join(artifactDir, "manifest.yaml")
	if err := spec.Write(manifestPath, artifact); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lock := spec.Lock{APIVersion: spec.APIVersion, Artifact: digest, Components: []spec.RefLock{{ID: "fixture", Path: "fixture", Provider: "fake", Contract: contract, Distribution: "reference", Revision: "revision"}}}
	if err := spec.Write(filepath.Join(artifactDir, "lock.yaml"), lock); err != nil {
		t.Fatal(err)
	}
}

func TestFailedExternalImportRemovesNewTarget(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := spec.Artifact{
		APIVersion:  spec.APIVersion,
		Kind:        spec.Kind,
		Coordinates: spec.Coordinates{Namespace: "test", Name: "failure", Version: "1"},
		Components: []spec.Component{{
			ID: "fixture", Path: "fixture", Provider: "fake", Contract: "v1", Distribution: "reference",
			Reference: &spec.Reference{Locator: "https://example.invalid/fixture", Revision: "revision"},
		}},
		Provenance: spec.Provenance{CreatedAt: "2026-07-16T00:00:00Z", Engine: "test"},
	}
	manifestPath := filepath.Join(artifactDir, "manifest.yaml")
	if err := spec.Write(manifestPath, artifact); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lock := spec.Lock{APIVersion: spec.APIVersion, Artifact: digest, Components: []spec.RefLock{{ID: "fixture", Path: "fixture", Provider: "fake", Contract: "v1", Distribution: "reference", Revision: "revision"}}}
	if err := spec.Write(filepath.Join(artifactDir, "lock.yaml"), lock); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "target")
	e := New(filepath.Join(target, ".lxp"), failingProvider{})
	if _, err := e.ImportBundle(context.Background(), BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}); err == nil {
		t.Fatal("failing provider unexpectedly imported")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("failed import left target behind: %v", err)
	}
}

func TestVerifyPayloadsRejectsDeclaredSizeMismatch(t *testing.T) {
	store := bundle.Store{Root: t.TempDir()}
	digest, size, err := store.PutBytes([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	artifact := spec.Artifact{Components: []spec.Component{{
		ID: "fixture", Embedded: &spec.Embedded{Payloads: map[string]spec.Payload{
			"content": {MediaType: "application/octet-stream", Digest: digest, Size: size + 1},
		}},
	}}}
	if err := verifyPayloads(artifact, store); err == nil {
		t.Fatal("declared payload size mismatch unexpectedly verified")
	}
}

func TestValidateNestedTargetRejectsPhysicalCollisions(t *testing.T) {
	workdir := t.TempDir()
	nonEmpty := filepath.Join(workdir, "parent", "non-empty")
	if err := os.MkdirAll(nonEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "owned.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateNestedTarget(workdir, nonEmpty); err == nil {
		t.Fatal("non-empty nested target unexpectedly accepted")
	}
	empty := filepath.Join(workdir, "parent", "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateNestedTarget(workdir, empty); err != nil {
		t.Fatalf("empty nested target rejected: %v", err)
	}
	outside := t.TempDir()
	link := filepath.Join(workdir, "parent", "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := validateNestedTarget(workdir, filepath.Join(link, "child")); err == nil {
		t.Fatal("cross-symlink nested target unexpectedly accepted")
	}
}
