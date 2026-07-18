package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

func TestProductionProfileAcceptsAllGitDistributions(t *testing.T) {
	revision := strings.Repeat("a", 40)
	base := spec.Payload{MediaType: "application/vnd.git.bundle"}
	for _, component := range []spec.Component{
		{ID: "reference", Provider: "git", Contract: "v1", Distribution: "reference", Reference: &spec.Reference{Locator: "https://example.invalid/repo.git", Revision: revision}},
		{ID: "embedded", Provider: "git", Contract: "v1", Distribution: "embedded", Embedded: &spec.Embedded{Revision: revision, Payloads: map[string]spec.Payload{"base": base}}},
		{ID: "mirrored", Provider: "git", Contract: "v1", Distribution: "mirrored", Reference: &spec.Reference{Locator: "https://example.invalid/repo.git", Revision: revision}, Embedded: &spec.Embedded{Revision: revision, Payloads: map[string]spec.Payload{"base": base}}},
	} {
		if err := validateProductionArtifact(spec.Artifact{Components: []spec.Component{component}}); err != nil {
			t.Fatalf("%s distribution rejected: %v", component.Distribution, err)
		}
	}
}

func TestCLIReferenceAndMirroredJourney(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	upstream := filepath.Join(tmp, "upstream")
	mustGit(t, "", "init", "-b", "main", upstream)
	if err := os.WriteFile(filepath.Join(upstream, "README.md"), []byte("portable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, upstream, "add", "README.md")
	mustGit(t, upstream, "-c", "user.name=LXP Test", "-c", "user.email=lxp@example.test", "commit", "-m", "base")
	remote, stop := serveGitRepository(t, upstream)

	work := filepath.Join(tmp, "work")
	if err := run(ctx, []string{"init", work}); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "", "clone", remote, filepath.Join(work, "source"))
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"add", "source"}); err != nil {
		t.Fatal(err)
	}

	referenceArtifact := filepath.Join(tmp, "reference.lxpz")
	if err := run(ctx, []string{"export", "--distribution", "reference", referenceArtifact}); err != nil {
		t.Fatal(err)
	}
	mirroredArtifact := filepath.Join(tmp, "mirrored.lxpz")
	if err := run(ctx, []string{"export", "--distribution", "mirrored", mirroredArtifact}); err != nil {
		t.Fatal(err)
	}
	for path, distribution := range map[string]string{referenceArtifact: "reference", mirroredArtifact: "mirrored"} {
		artifact, err := readBundleArtifact(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(artifact.Components) != 1 || artifact.Components[0].Distribution != distribution {
			t.Fatalf("%s Artifact components = %#v", distribution, artifact.Components)
		}
	}

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	referenceOnline := filepath.Join(tmp, "reference-online")
	if err := run(ctx, []string{"import", referenceArtifact, referenceOnline}); err != nil {
		t.Fatal(err)
	}
	stop()
	referenceOffline := filepath.Join(tmp, "reference-offline")
	if err := run(ctx, []string{"import", referenceArtifact, referenceOffline}); err == nil {
		t.Fatal("offline reference import unexpectedly succeeded")
	}
	if _, err := os.Stat(referenceOffline); !os.IsNotExist(err) {
		t.Fatalf("failed reference import target was not cleaned: %v", err)
	}
	mirroredOffline := filepath.Join(tmp, "mirrored-offline")
	if err := run(ctx, []string{"import", mirroredArtifact, mirroredOffline}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{referenceOnline, mirroredOffline} {
		content, err := os.ReadFile(filepath.Join(root, "source", "README.md"))
		if err != nil || string(content) != "portable\n" {
			t.Fatalf("restored %s content = %q, %v", root, content, err)
		}
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
	for _, args := range [][]string{{"clone"}, {"plan"}, {"artifact"}, {"import", "--template", "template.yaml"}, {"export", "--distribution", "unknown", "artifact.lxpz"}} {
		if err := run(ctx, args); err == nil {
			t.Fatalf("legacy command unexpectedly accepted: %v", args)
		}
	}
}

func serveGitRepository(t *testing.T, source string) (string, func()) {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "source.git")
	mustGit(t, "", "clone", "--bare", source, repository)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "daemon", "--reuseaddr", "--export-all", "--base-path="+root, "--listen=127.0.0.1", fmt.Sprintf("--port=%d", port), root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	remote := fmt.Sprintf("git://127.0.0.1:%d/source.git", port)
	ready := false
	for attempt := 0; attempt < 100; attempt++ {
		if err := exec.Command("git", "ls-remote", remote).Run(); err == nil {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatal("git daemon did not become ready")
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		})
	}
	t.Cleanup(stop)
	return remote, stop
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
