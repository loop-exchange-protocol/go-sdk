package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validArtifact() Artifact {
	return Artifact{
		APIVersion: APIVersion,
		Kind:       Kind,
		Coordinates: Coordinates{
			Namespace: "test", Name: "artifact", Version: "1.0.0",
		},
		Components: []Component{{
			ID: "source", Path: "source", Provider: "git", Contract: "v1", Distribution: "reference",
			Reference: &Reference{Locator: "https://example.invalid/repo.git", Revision: strings.Repeat("a", 40)},
			Requires:  []string{"git-cli"},
		}},
		Requirements: []Requirement{{ID: "git-cli", Check: Check{Type: "executable", Command: "git", Args: []string{"--version"}}}},
		Provenance:   Provenance{CreatedAt: "2026-07-11T00:00:00Z", Engine: "lxp/test"},
	}
}

func TestValidateAcceptsReferenceDistributionAndRequirements(t *testing.T) {
	if err := Validate(validArtifact()); err != nil {
		t.Fatalf("validate revised artifact: %v", err)
	}
}

func TestValidateDistributionFieldCombinations(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Component)
	}{
		{"reference requires reference", func(c *Component) { c.Reference = nil }},
		{"reference rejects embedded", func(c *Component) {
			c.Embedded = &Embedded{Payloads: map[string]Payload{"content": {MediaType: "application/octet-stream", Digest: "sha256:" + strings.Repeat("b", 64), Size: 1}}}
		}},
		{"embedded requires payload", func(c *Component) { c.Distribution, c.Reference = "embedded", nil }},
		{"embedded rejects reference", func(c *Component) {
			c.Distribution = "embedded"
			c.Embedded = &Embedded{Payloads: map[string]Payload{"content": {MediaType: "application/octet-stream", Digest: "sha256:" + strings.Repeat("b", 64), Size: 1}}}
		}},
		{"mirrored requires both", func(c *Component) { c.Distribution = "mirrored" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validArtifact()
			tt.edit(&a.Components[0])
			if err := Validate(a); err == nil {
				t.Fatal("expected distribution validation error")
			}
		})
	}
}

func TestValidateRejectsMirroredRevisionMismatch(t *testing.T) {
	artifact := validArtifact()
	artifact.Components[0].Distribution = "mirrored"
	artifact.Components[0].Embedded = &Embedded{
		Revision: strings.Repeat("b", 40),
		Payloads: map[string]Payload{"base": {MediaType: "application/vnd.git.bundle", Digest: "sha256:" + strings.Repeat("c", 64), Size: 1}},
	}
	if err := Validate(artifact); err == nil {
		t.Fatal("mirrored component with mismatched revisions unexpectedly validated")
	}
}

func TestReadArtifactRejectsLegacyRuntimeField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	data := `api_version: loop.exchange/v1alpha1
kind: ContextArtifact
coordinates: {namespace: test, name: old, version: 1.0.0}
runtime: {dependencies: []}
provenance: {created_at: 2026-07-11T00:00:00Z, engine: test}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArtifact(path); err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("expected unknown runtime field error, got %v", err)
	}
}

func TestValidateRejectsMissingRequirementReference(t *testing.T) {
	a := validArtifact()
	a.Components[0].Requires = []string{"missing"}
	if err := Validate(a); err == nil {
		t.Fatal("expected missing requirement reference error")
	}
}

func TestValidateRejectsInlineCredentialMaterial(t *testing.T) {
	a := validArtifact()
	a.Requirements[0] = Requirement{ID: "token", Check: Check{Type: "credential", Accepts: []string{"environment"}, Extensions: map[string]any{"api_token": "secret"}}}
	if err := Validate(a); err == nil {
		t.Fatal("expected inline credential material rejection")
	}
}

func TestValidateRejectsSecretLikeProviderConfig(t *testing.T) {
	a := validArtifact()
	a.Components[0].Config = map[string]any{"api_token": "must-not-travel"}
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "credential Requirement") {
		t.Fatalf("expected provider config secret rejection, got %v", err)
	}
}

func TestValidateRejectsSecretLikeProviderMetadata(t *testing.T) {
	a := validArtifact()
	a.Components[0].Metadata = map[string]string{"api_token": "must-not-travel"}
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "credential Requirement") {
		t.Fatalf("expected provider metadata secret rejection, got %v", err)
	}
}

func TestValidateRejectsLocalLXPStatePath(t *testing.T) {
	a := validArtifact()
	a.Components[0].Path = ".lxp/provider-state"
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "unsafe ref path") {
		t.Fatalf("expected local .lxp path rejection, got %v", err)
	}
}

func TestValidateRejectsNonNormalizedPortablePaths(t *testing.T) {
	for _, path := range []string{".", "source/./code", "source//code", "source/", `source\code`} {
		t.Run(path, func(t *testing.T) {
			a := validArtifact()
			a.Components[0].Path = path
			if err := Validate(a); err == nil || !strings.Contains(err.Error(), "unsafe ref path") {
				t.Fatalf("expected portable path rejection for %q, got %v", path, err)
			}
		})
	}
}

func TestValidateAcceptsNestedComponentsAndRejectsDuplicateRoots(t *testing.T) {
	a := validArtifact()
	child := a.Components[0]
	child.ID = "dependency"
	child.Path = "source/deps/dependency"
	a.Components = append(a.Components, child)
	if err := Validate(a); err != nil {
		t.Fatalf("nested Component rejected: %v", err)
	}
	a.Components[1].Path = a.Components[0].Path
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "duplicate path") {
		t.Fatalf("duplicate Component root accepted: %v", err)
	}
}

func TestValidateRejectsRequirementOrchestrationCycle(t *testing.T) {
	a := validArtifact()
	a.Requirements[0].ProvidedBy = "component:source"
	if err := Validate(a); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected orchestration cycle rejection, got %v", err)
	}
}
