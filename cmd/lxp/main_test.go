package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loop-exchange-protocol/go-sdk/pkg/bundle"
	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

func TestDirectoryAwareInitAddExportImport(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	if err := run(ctx, []string{"init", work}); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(work, "source")
	mustGit(t, "", "init", "-b", "main", source)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, source, "add", "README.md")
	mustGit(t, source, "-c", "user.name=LXP Test", "-c", "user.email=lxp@example.test", "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "draft.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"add", "source/README.md"}); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(tmp, "first.lxpz")
	if err := run(ctx, []string{"export", artifact}); err != nil {
		t.Fatal(err)
	}
	imported := filepath.Join(tmp, "imported")
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"import", artifact, imported}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(imported, "source", "README.md"))
	if err != nil || string(data) != "selected\n" {
		t.Fatalf("imported content: %q, %v", data, err)
	}
	if err := os.Chdir(imported); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(imported, "source", "draft.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked file restored: %v", err)
	}
	if staged := mustGitText(t, filepath.Join(imported, "source"), "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "README.md" {
		t.Fatalf("restored selection = %q", staged)
	}
	if err := os.WriteFile(filepath.Join(imported, "source", "README.md"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"add", "source/README.md"}); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(tmp, "second.lxpz")
	if err := run(ctx, []string{"export", second}); err != nil {
		t.Fatal(err)
	}
	unpacked := filepath.Join(tmp, "unpacked")
	if err := bundle.Unpack(second, unpacked); err != nil {
		t.Fatal(err)
	}
	manifest, err := spec.ReadArtifact(filepath.Join(unpacked, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Provenance.Parent == "" {
		t.Fatal("continued export did not record its parent artifact digest")
	}
	secondImported := filepath.Join(tmp, "second-imported")
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"import", second, secondImported}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(secondImported, "source", "README.md"))
	if err != nil || string(data) != "second\n" {
		t.Fatalf("second-generation content: %q, %v", data, err)
	}
}

func TestProductionProfileRejectsNonEmbeddedArtifact(t *testing.T) {
	artifact := spec.Artifact{Components: []spec.Component{{
		ID:           "source",
		Provider:     "git",
		Contract:     "v1",
		Distribution: "reference",
		Reference:    &spec.Reference{Locator: "https://example.invalid/repo.git", Revision: strings.Repeat("a", 40)},
	}}}
	if err := validateProductionArtifact(artifact); err == nil {
		t.Fatal("reference Artifact unexpectedly accepted by Production Profile")
	}
}

func TestProductionProfileRejectsUnknownGitPayloadRole(t *testing.T) {
	artifact := spec.Artifact{Components: []spec.Component{{
		ID:           "source",
		Provider:     "git",
		Contract:     "v1",
		Distribution: "embedded",
		Embedded: &spec.Embedded{
			Revision: strings.Repeat("a", 40),
			Payloads: map[string]spec.Payload{
				"base":  {MediaType: "application/vnd.git.bundle"},
				"extra": {MediaType: "application/octet-stream"},
			},
		},
	}}}
	if err := validateProductionArtifact(artifact); err == nil {
		t.Fatal("unknown Git payload role unexpectedly accepted")
	}
}

func TestArtifactVersionsAreSafeAndUnique(t *testing.T) {
	first, err := newArtifactVersion()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newArtifactVersion()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !spec.ValidIdentifier(first) || !spec.ValidIdentifier(second) {
		t.Fatalf("generated versions are not safe and unique: %q %q", first, second)
	}
}

func TestProductionCommandSurfaceRejectsLegacyEntrypoints(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{"clone"}, {"plan"}, {"artifact"}, {"import", "--template", "template.yaml"}, {"export", "--distribution", "reference"}} {
		if err := run(ctx, args); err == nil {
			t.Fatalf("legacy command unexpectedly accepted: %v", args)
		}
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustGitText(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
