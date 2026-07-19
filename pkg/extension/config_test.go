package extension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

func TestDefaultResolvesBuiltinGitProvider(t *testing.T) {
	contract := spec.Contract{Namespace: "loop.exchange", Name: "git", Version: "v1"}
	implementation, err := Default().Resolve(KindProvider, contract)
	if err != nil {
		t.Fatal(err)
	}
	if implementation.Source != "builtin" || implementation.Package.Name != "provider-git" {
		t.Fatalf("unexpected implementation: %#v", implementation)
	}
}

func TestReadRejectsInlineRepositoryCredential(t *testing.T) {
	for _, repositoryURL := range []string{"https://user:secret@example.com/lxp", "https://example.com/lxp?token=secret"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		data := []byte("api_version: loop.exchange/v1alpha1\nkind: EngineConfig\nrepositories:\n  - id: private\n    url: " + repositoryURL + "\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); err == nil {
			t.Fatalf("inline repository credential in %q unexpectedly accepted", repositoryURL)
		}
	}
}

func TestReadRejectsMultipleYAMLDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("api_version: loop.exchange/v1alpha1\nkind: EngineConfig\n---\nkind: HiddenDocument\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("multiple-document EngineConfig unexpectedly accepted")
	}
}

func TestValidateRejectsDuplicateContractAcrossKinds(t *testing.T) {
	contract := spec.Contract{Namespace: "example.test", Name: "shared", Version: "v1"}
	implementation := Implementation{Source: "builtin", Package: spec.Contract{Namespace: "example.test", Name: "implementation", Version: "v1"}}
	config := Config{APIVersion: spec.APIVersion, Kind: "EngineConfig", Bindings: []Binding{
		{Kind: KindProvider, Contract: contract, Implementation: implementation},
		{Kind: KindChecker, Contract: contract, Implementation: implementation},
	}}
	if err := config.Validate(); err == nil {
		t.Fatal("cross-kind duplicate contract unexpectedly accepted")
	}
}

func TestRepositoryBindingUsesOrderedRepositories(t *testing.T) {
	config := Config{
		APIVersion:   spec.APIVersion,
		Kind:         "EngineConfig",
		Repositories: []Repository{{ID: "central", URL: "https://packages.example.test/lxp"}},
		Bindings: []Binding{{
			Kind:           KindProvider,
			Contract:       spec.Contract{Namespace: "example.test", Name: "source", Version: "v1"},
			Implementation: Implementation{Source: "repository", Package: spec.Contract{Namespace: "example.test", Name: "provider-source", Version: "1.0.0"}, Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		}},
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("ordered repository binding rejected: %v", err)
	}
}
