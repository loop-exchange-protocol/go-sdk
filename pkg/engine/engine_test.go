package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/bundle"
	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	lxpruntime "github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

var fakeContract = spec.Contract{Namespace: "loop.exchange", Name: "git", Version: "v1"}

type fakeProvider struct{}

func (fakeProvider) Contract() spec.Contract { return fakeContract }
func (fakeProvider) Implementation() spec.Contract {
	return spec.Contract{Namespace: "loop.exchange", Name: "provider-git", Version: "0.1.0-alpha.4"}
}
func (fakeProvider) Match(context.Context, string) (int, error) { return 1, nil }
func (fakeProvider) Distributions() []string                    { return []string{"reference", "embedded"} }
func (fakeProvider) Validate(context.Context, spec.Component, bundle.Store, provider.ApplyTarget) error {
	return nil
}
func (fakeProvider) Apply(_ context.Context, component spec.Component, _ bundle.Store, target provider.ApplyTarget) (protocol.ResolvedRef, error) {
	if err := os.MkdirAll(target.Path, 0o755); err != nil {
		return protocol.ResolvedRef{}, err
	}
	if err := os.WriteFile(filepath.Join(target.Path, "fixture.txt"), []byte("fake\n"), 0o644); err != nil {
		return protocol.ResolvedRef{}, err
	}
	return protocol.ResolvedRef{ID: component.ID, Path: component.Path, Provider: fakeContract, Revision: component.Reference.Revision, PoolPath: target.Path, Materialized: target.Path}, nil
}
func (fakeProvider) ExportComponent(context.Context, protocol.ResolvedRef, string, bundle.Store) (spec.Component, error) {
	return spec.Component{}, nil
}

type retryProvider struct {
	fakeProvider
	calls          int
	validations    int
	implementation spec.Contract
}

func (p *retryProvider) Implementation() spec.Contract {
	if p.implementation.Valid() {
		return p.implementation
	}
	return p.fakeProvider.Implementation()
}

func (p *retryProvider) Validate(context.Context, spec.Component, bundle.Store, provider.ApplyTarget) error {
	p.validations++
	return nil
}

func (p *retryProvider) Apply(ctx context.Context, component spec.Component, store bundle.Store, target provider.ApplyTarget) (protocol.ResolvedRef, error) {
	p.calls++
	if p.calls == 1 {
		if err := os.MkdirAll(target.Path, 0o755); err != nil {
			return protocol.ResolvedRef{}, err
		}
		if err := os.WriteFile(filepath.Join(target.Path, "partial"), []byte("retry"), 0o644); err != nil {
			return protocol.ResolvedRef{}, err
		}
		return protocol.ResolvedRef{}, fmt.Errorf("transient apply failure")
	}
	return p.fakeProvider.Apply(ctx, component, store, target)
}

type countingChecker struct{ calls int }

func (c *countingChecker) Contract() spec.Contract       { return lxpruntime.CredentialContract }
func (c *countingChecker) Implementation() spec.Contract { return lxpruntime.CredentialImplementation }
func (c *countingChecker) Check(context.Context, spec.Requirement, lxpruntime.Options) (lxpruntime.Observation, error) {
	c.calls++
	return lxpruntime.Observation{ID: "credential", Checker: c.Contract(), Status: "ready"}, nil
}

type childFailureProvider struct {
	fakeProvider
	calls  map[string]int
	failed bool
}

type noWriteRetryProvider struct {
	fakeProvider
	calls          int
	implementation spec.Contract
}

func (p *noWriteRetryProvider) Implementation() spec.Contract {
	if p.implementation.Valid() {
		return p.implementation
	}
	return p.fakeProvider.Implementation()
}

func (p *noWriteRetryProvider) Apply(ctx context.Context, component spec.Component, store bundle.Store, target provider.ApplyTarget) (protocol.ResolvedRef, error) {
	p.calls++
	if p.calls == 1 {
		return protocol.ResolvedRef{}, fmt.Errorf("transient failure before target write")
	}
	return p.fakeProvider.Apply(ctx, component, store, target)
}

func (p *childFailureProvider) Apply(ctx context.Context, component spec.Component, store bundle.Store, target provider.ApplyTarget) (protocol.ResolvedRef, error) {
	p.calls[component.ID]++
	if component.ID == "child" && !p.failed {
		p.failed = true
		if err := os.MkdirAll(target.Path, 0o755); err != nil {
			return protocol.ResolvedRef{}, err
		}
		return protocol.ResolvedRef{}, fmt.Errorf("transient child failure")
	}
	return p.fakeProvider.Apply(ctx, component, store, target)
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

type countingMatchProvider struct {
	fakeProvider
	contract       spec.Contract
	implementation spec.Contract
	score          int
	calls          int
}

func (p *countingMatchProvider) Contract() spec.Contract { return p.contract }
func (p *countingMatchProvider) Implementation() spec.Contract {
	return p.implementation
}
func (p *countingMatchProvider) Match(context.Context, string) (int, error) {
	p.calls++
	return p.score, nil
}

func TestAddDiscoveryNeverInvokesUnboundOrMismatchedProvider(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "work")
	bound := &countingMatchProvider{fakeProvider: fakeProvider{}, contract: fakeContract, implementation: fakeProvider{}.Implementation(), score: 10}
	unbound := &countingMatchProvider{
		fakeProvider:   fakeProvider{},
		contract:       spec.Contract{Namespace: "example.test", Name: "extra", Version: "v1"},
		implementation: spec.Contract{Namespace: "example.test", Name: "provider-extra", Version: "1.0.0"},
		score:          1,
	}
	e := New(filepath.Join(workdir, ".lxp"), bound, unbound)
	if _, err := e.InitAt(context.Background(), "work", workdir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddWithOptionsContext(context.Background(), "work", []string{"source"}, AddOptions{}); err != nil {
		t.Fatalf("discover bound Provider: %v", err)
	}
	if bound.calls == 0 || unbound.calls != 0 {
		t.Fatalf("Match calls: bound=%d unbound=%d", bound.calls, unbound.calls)
	}

	mismatchRoot := filepath.Join(root, "mismatch")
	mismatch := &countingMatchProvider{fakeProvider: fakeProvider{}, contract: fakeContract, implementation: spec.Contract{Namespace: "loop.exchange", Name: "provider-git", Version: "9.9.9"}, score: 10}
	mismatchEngine := New(filepath.Join(mismatchRoot, ".lxp"), mismatch)
	if _, err := mismatchEngine.InitAt(context.Background(), "work", mismatchRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mismatchRoot, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := mismatchEngine.AddWithOptionsContext(context.Background(), "work", []string{"source"}, AddOptions{}); err == nil {
		t.Fatal("mismatched implementation unexpectedly discovered")
	}
	if mismatch.calls != 0 {
		t.Fatalf("mismatched Provider Match called %d times", mismatch.calls)
	}
}

func TestValidateBundleUsesOnlyInjectedProvider(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, fakeContract)
	if _, err := New(filepath.Join(tmp, "state"), fakeProvider{}).ValidateBundle(context.Background(), artifactDir); err != nil {
		t.Fatalf("validate with injected Provider: %v", err)
	}
	if _, err := New(filepath.Join(tmp, "missing-state")).ValidateBundle(context.Background(), artifactDir); err == nil {
		t.Fatal("missing Provider unexpectedly validated")
	}
	mismatch := spec.Contract{Namespace: "example.test", Name: "fake", Version: "v2"}
	mismatchDir := filepath.Join(tmp, "contract-mismatch.lxp")
	writeReferenceArtifact(t, mismatchDir, mismatch)
	if _, err := New(filepath.Join(tmp, "mismatch-state"), fakeProvider{}).ValidateBundle(context.Background(), mismatchDir); err == nil {
		t.Fatal("unbound Provider contract unexpectedly validated")
	}
}

func TestEngineConfigBindingMustMatchInstalledImplementation(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, fakeContract)
	config := extension.Default()
	config.Bindings[0].Implementation.Package.Version = "9.9.9"
	e := NewWithConfig(filepath.Join(tmp, "state"), config, lxpruntime.DefaultRegistry(), fakeProvider{})
	if _, err := e.ValidateBundle(context.Background(), artifactDir); err == nil {
		t.Fatal("mismatched builtin implementation unexpectedly validated")
	}
	e = New(filepath.Join(tmp, "duplicate-state"), fakeProvider{}, fakeProvider{})
	if _, err := e.ValidateBundle(context.Background(), artifactDir); err == nil {
		t.Fatal("duplicate Provider contract unexpectedly validated")
	}
}

func TestEngineConfigConstrainsAddStatusAndExport(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "work")
	config := extension.Default()
	config.Bindings[0].Implementation.Package.Version = "9.9.9"
	e := NewWithConfig(filepath.Join(workdir, ".lxp"), config, lxpruntime.DefaultRegistry(), fakeProvider{})
	instance, err := e.InitAt(context.Background(), "work", workdir)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workdir, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	instance.Components = []protocol.ResolvedRef{{ID: "source", Path: "source", Provider: fakeContract, PoolPath: source, Materialized: source}}
	if err := protocol.WriteYAML(instance.Paths.Manifest, instance); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddWithOptionsContext(context.Background(), "work", []string{"source/file.txt"}, AddOptions{}); err == nil {
		t.Fatal("Add ignored implementation binding mismatch")
	}
	if _, err := e.StatusContext(context.Background(), "work"); err == nil {
		t.Fatal("Status ignored implementation binding mismatch")
	}
	if _, err := e.Export(context.Background(), ExportOptions{SessionID: "work", Out: filepath.Join(root, "artifact.lxp"), Namespace: "test", Name: "binding", Version: "1", Distribution: "reference"}); err == nil {
		t.Fatal("Export ignored implementation binding mismatch")
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
	_, err := e.AddWithOptionsContext(ctx, "work", []string{"owned.txt"}, AddOptions{Provider: fakeContract})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Add cancellation error = %v", err)
	}
}

func TestImportPreservesFailureForRetryAndReadyImportIsNoOp(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, fakeContract)
	artifact, err := spec.ReadArtifact(filepath.Join(artifactDir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	artifact.Requirements = []spec.Requirement{{ID: "credential", Check: spec.Check{Checker: lxpruntime.CredentialContract}}}
	artifact.Components[0].Requires = []string{"credential"}
	if err := spec.Write(filepath.Join(artifactDir, "manifest.yaml"), artifact); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "target")
	p := &retryProvider{}
	e := New(filepath.Join(target, ".lxp"), p)
	checker := &countingChecker{}
	e.Checkers = lxpruntime.NewRegistry(checker)
	opts := BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("transient failure unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(target, "fixture", "partial")); err != nil {
		t.Fatalf("failed Import did not preserve retry state: %v", err)
	}
	instance, err := e.ImportBundle(context.Background(), opts)
	if err != nil {
		t.Fatalf("retry Import: %v", err)
	}
	if instance.Metadata["import_state"] != "ready" || p.calls != 2 || p.validations != 2 || checker.calls != 2 {
		t.Fatalf("retry result = %#v, apply calls=%d, validations=%d, checker calls=%d", instance.Metadata, p.calls, p.validations, checker.calls)
	}
	e.Providers = provider.NewRegistry()
	e.Checkers = lxpruntime.NewRegistry()
	if _, err := e.ImportBundle(context.Background(), opts); err != nil {
		t.Fatalf("ready no-op Import: %v", err)
	}
	if p.calls != 2 || p.validations != 2 || checker.calls != 2 {
		t.Fatalf("ready no-op invoked extensions: apply calls=%d, validations=%d, checker calls=%d", p.calls, p.validations, checker.calls)
	}
}

func TestImportRetryPinsExtensionsAndRejectsUnownedPaths(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, fakeContract)
	target := filepath.Join(tmp, "target")
	p := &retryProvider{}
	e := New(filepath.Join(target, ".lxp"), p)
	opts := BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("transient failure unexpectedly succeeded")
	}
	if err := os.WriteFile(filepath.Join(target, "unowned"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("retry with unowned path unexpectedly succeeded")
	}
	if err := os.Remove(filepath.Join(target, "unowned")); err != nil {
		t.Fatal(err)
	}
	original := p.Implementation()
	changed := spec.Contract{Namespace: "loop.exchange", Name: "provider-git", Version: "9.9.9"}
	p.implementation = changed
	e.Extensions.Bindings[0].Implementation.Package = changed
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("retry with changed implementation unexpectedly succeeded")
	}
	if p.calls != 1 {
		t.Fatalf("changed implementation reached Apply: %d calls", p.calls)
	}
	p.implementation = original
	e.Extensions.Bindings[0].Implementation.Package = original
	if _, err := e.ImportBundle(context.Background(), opts); err != nil {
		t.Fatalf("retry with pinned implementation: %v", err)
	}
}

func TestImportRetrySkipsCompletedComponents(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := spec.Artifact{
		APIVersion:  spec.APIVersion,
		Kind:        spec.Kind,
		Coordinates: spec.Coordinates{Namespace: "test", Name: "multi", Version: "1"},
		Components: []spec.Component{
			{ID: "parent", Path: "parent", Provider: fakeContract, Distribution: "reference", Reference: &spec.Reference{Locator: "https://example.invalid/parent", Revision: "parent-revision"}},
			{ID: "child", Path: "parent/child", Provider: fakeContract, Distribution: "reference", Reference: &spec.Reference{Locator: "https://example.invalid/child", Revision: "child-revision"}},
		},
		Provenance: spec.Provenance{CreatedAt: "2026-07-20T00:00:00Z", Engine: "test"},
	}
	if err := spec.Write(filepath.Join(artifactDir, "manifest.yaml"), artifact); err != nil {
		t.Fatal(err)
	}
	p := &childFailureProvider{calls: map[string]int{}}
	target := filepath.Join(tmp, "target")
	e := New(filepath.Join(target, ".lxp"), p)
	opts := BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("child failure unexpectedly succeeded")
	}
	if p.calls["parent"] != 1 || p.calls["child"] != 1 {
		t.Fatalf("first Import calls = %#v", p.calls)
	}
	if _, err := e.ImportBundle(context.Background(), opts); err != nil {
		t.Fatalf("retry Import: %v", err)
	}
	if p.calls["parent"] != 1 || p.calls["child"] != 2 {
		t.Fatalf("retry reapplied completed parent: %#v", p.calls)
	}
}

func TestImportFindsExternalRetryStateWhenWorkdirIsEmpty(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, fakeContract)
	p := &noWriteRetryProvider{}
	e := New(filepath.Join(tmp, "state"), p)
	target := filepath.Join(tmp, "target")
	opts := BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("first no-write failure unexpectedly succeeded")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed Apply unexpectedly wrote target: entries=%v, err=%v", entries, err)
	}
	original := p.Implementation()
	changed := spec.Contract{Namespace: "loop.exchange", Name: "provider-git", Version: "9.9.9"}
	p.implementation = changed
	e.Extensions.Bindings[0].Implementation.Package = changed
	if _, err := e.ImportBundle(context.Background(), opts); err == nil {
		t.Fatal("empty-workdir retry lost its pinned implementation")
	}
	if p.calls != 1 {
		t.Fatalf("changed implementation reached Apply: %d calls", p.calls)
	}
	p.implementation = original
	e.Extensions.Bindings[0].Implementation.Package = original
	if _, err := e.ImportBundle(context.Background(), opts); err != nil {
		t.Fatalf("empty-workdir retry: %v", err)
	}
}

func TestReadyEmptyArtifactIsNoOpWithExternalState(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "empty.lxp")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := spec.Artifact{APIVersion: spec.APIVersion, Kind: spec.Kind, Coordinates: spec.Coordinates{Namespace: "test", Name: "empty", Version: "1"}, Provenance: spec.Provenance{CreatedAt: "2026-07-20T00:00:00Z", Engine: "test"}}
	if err := spec.Write(filepath.Join(artifactDir, "manifest.yaml"), artifact); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "target")
	e := New(filepath.Join(tmp, "state"))
	opts := BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}
	if _, err := e.ImportBundle(context.Background(), opts); err != nil {
		t.Fatalf("first empty Import: %v", err)
	}
	if _, err := e.ImportBundle(context.Background(), opts); err != nil {
		t.Fatalf("ready empty Import no-op: %v", err)
	}
	if _, err := e.ImportBundle(context.Background(), BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: filepath.Join(tmp, "other-target")}); err == nil {
		t.Fatal("existing Session was unexpectedly rebound to another workdir")
	}
}

func TestStageBundleRejectsFilesOutsideEnvelope(t *testing.T) {
	for _, archived := range []bool{false, true} {
		for _, extra := range []string{"lock.yaml", filepath.Join("objects", "sha256", fmt.Sprintf("%064x", 1))} {
			name := "directory/" + filepath.Base(extra)
			if archived {
				name = "archive/" + filepath.Base(extra)
			}
			t.Run(name, func(t *testing.T) {
				artifactDir := filepath.Join(t.TempDir(), "artifact.lxp")
				writeReferenceArtifact(t, artifactDir, fakeContract)
				if extra != "lock.yaml" {
					artifact, err := spec.ReadArtifact(filepath.Join(artifactDir, "manifest.yaml"))
					if err != nil {
						t.Fatal(err)
					}
					digest, size, err := (bundle.Store{Root: artifactDir}).PutBytes([]byte("declared payload\n"))
					if err != nil {
						t.Fatal(err)
					}
					artifact.Components[0].Distribution = "embedded"
					artifact.Components[0].Reference = nil
					artifact.Components[0].Embedded = &spec.Embedded{Revision: "revision", Payloads: map[string]spec.Payload{"content": {MediaType: "application/octet-stream", Digest: digest, Size: size}}}
					if err := spec.Write(filepath.Join(artifactDir, "manifest.yaml"), artifact); err != nil {
						t.Fatal(err)
					}
				}
				extraPath := filepath.Join(artifactDir, extra)
				if err := os.MkdirAll(filepath.Dir(extraPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(extraPath, []byte("unexpected\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				input := artifactDir
				if archived {
					input = filepath.Join(t.TempDir(), "artifact.lxpz")
					if err := bundle.Pack(artifactDir, input); err != nil {
						t.Fatal(err)
					}
				}
				if _, _, _, err := New(t.TempDir()).stageBundle(input); err == nil {
					t.Fatal("bundle containing an undeclared file unexpectedly staged")
				}
			})
		}
	}
}

func TestImportRejectsUnownedExistingTarget(t *testing.T) {
	tmp := t.TempDir()
	artifactDir := filepath.Join(tmp, "artifact.lxp")
	writeReferenceArtifact(t, artifactDir, fakeContract)
	target := filepath.Join(tmp, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "unowned"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(filepath.Join(target, ".lxp"), fakeProvider{})
	if _, err := e.ImportBundle(context.Background(), BundleImportOptions{Bundle: artifactDir, SessionID: "work", Workdir: target}); err == nil {
		t.Fatal("unowned existing target unexpectedly imported")
	}
	if data, err := os.ReadFile(filepath.Join(target, "unowned")); err != nil || string(data) != "keep" {
		t.Fatalf("unowned content changed: %q, %v", data, err)
	}
}

func writeReferenceArtifact(t *testing.T, artifactDir string, contract spec.Contract) {
	t.Helper()
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := spec.Artifact{
		APIVersion:  spec.APIVersion,
		Kind:        spec.Kind,
		Coordinates: spec.Coordinates{Namespace: "test", Name: "fixture", Version: "1"},
		Components: []spec.Component{{
			ID: "fixture", Path: "fixture", Provider: contract, Distribution: "reference",
			Reference: &spec.Reference{Locator: "https://example.invalid/fixture", Revision: "revision"},
		}},
		Provenance: spec.Provenance{CreatedAt: "2026-07-16T00:00:00Z", Engine: "test"},
	}
	if err := spec.Write(filepath.Join(artifactDir, "manifest.yaml"), artifact); err != nil {
		t.Fatal(err)
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

func TestValidateNestedTargetAllowsRetryContentAndRejectsSymlink(t *testing.T) {
	workdir := t.TempDir()
	nonEmpty := filepath.Join(workdir, "parent", "retry")
	if err := os.MkdirAll(nonEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "partial"), []byte("provider-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateNestedTarget(workdir, nonEmpty); err != nil {
		t.Fatalf("retryable nested target rejected: %v", err)
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
