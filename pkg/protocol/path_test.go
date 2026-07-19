package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalPathResolvesExistingSymlinkPrefixForMissingTarget(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "private", "var")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(root, "var")
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	got, err := CanonicalPath(filepath.Join(logical, "session", "work"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := CanonicalPath(filepath.Join(physical, "session", "work"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonical path = %q, want %q", got, want)
	}
}

func TestCanonicalPathRejectsDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalPath(link); err == nil {
		t.Fatal("dangling symlink unexpectedly resolved")
	}
}
