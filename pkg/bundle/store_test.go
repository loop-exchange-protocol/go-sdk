package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreVerifiesDigestAndSize(t *testing.T) {
	store := Store{Root: t.TempDir()}
	digest, size, err := store.Put(strings.NewReader("content"))
	if err != nil {
		t.Fatal(err)
	}
	if size != 7 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("unexpected object identity %s %d", digest, size)
	}
	f, err := store.Open(digest)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path, err := store.Path(digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(digest); err == nil {
		t.Fatal("tampered object unexpectedly verified")
	}
}

func TestStoreRejectsInvalidDigestPaths(t *testing.T) {
	store := Store{Root: t.TempDir()}
	for _, digest := range []string{"md5:abc", "sha256:../escape", "sha256:" + strings.Repeat("z", 64)} {
		if _, err := store.Path(digest); err == nil {
			t.Fatalf("invalid digest %q accepted", digest)
		}
	}
	if _, err := os.Stat(filepath.Join(store.Root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("digest validation touched filesystem: %v", err)
	}
}
