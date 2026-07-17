package bundle

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestPackUnpackRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "value.txt"), []byte("portable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "context.lxpz")
	if err := Pack(root, artifact); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := Unpack(artifact, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "nested", "value.txt"))
	if err != nil || string(data) != "portable\n" {
		t.Fatalf("round trip: %q, %v", data, err)
	}
}

func TestPackFailureDoesNotPublishPartialArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bad.lxpz")
	if err := Pack(root, out); err == nil {
		t.Fatal("symlink unexpectedly packed")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("partial artifact was published: %v", err)
	}
}

func TestPackNeverOverwritesExistingArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.yaml"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "existing.lxpz")
	if err := os.WriteFile(out, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Pack(root, out); err == nil {
		t.Fatal("existing Artifact was unexpectedly replaced")
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != "existing\n" {
		t.Fatalf("existing Artifact changed: %q, %v", data, err)
	}
}

func TestPackRejectsArtifactItCannotUnpack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "oversized")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxBundleEntry + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "oversized.lxpz")
	if err := Pack(root, out); err == nil {
		t.Fatal("oversized Artifact unexpectedly packed")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("oversized Artifact was published: %v", err)
	}
}

func TestUnpackRejectsUnsafeEntries(t *testing.T) {
	for _, entry := range []testEntry{
		{header: &tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Size: 1}, body: []byte("x")},
		{header: &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"}},
	} {
		t.Run(entry.header.Name, func(t *testing.T) {
			artifact := writeTestArchive(t, []testEntry{entry})
			if err := Unpack(artifact, t.TempDir()); err == nil {
				t.Fatal("unsafe archive unexpectedly unpacked")
			}
		})
	}
}

func TestUnpackRejectsDuplicateEntries(t *testing.T) {
	header := func() *tar.Header { return &tar.Header{Name: "same", Typeflag: tar.TypeReg, Size: 1} }
	artifact := writeTestArchive(t, []testEntry{{header: header(), body: []byte("a")}, {header: header(), body: []byte("b")}})
	if err := Unpack(artifact, t.TempDir()); err == nil {
		t.Fatal("duplicate archive entry unexpectedly unpacked")
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "a/../../escape", "/absolute", `..\escape`, `C:/absolute`, "a//b", "a/./b", "a/"} {
		if _, err := SafeJoin(t.TempDir(), name); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

type testEntry struct {
	header *tar.Header
	body   []byte
}

func writeTestArchive(t *testing.T, entries []testEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.lxpz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(entry.header); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 {
			if _, err := tw.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
