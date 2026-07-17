package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store struct{ Root string }

func (s Store) Put(r io.Reader) (string, int64, error) {
	if err := os.MkdirAll(filepath.Join(s.Root, "objects", "sha256"), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.Root, "objects", "sha256"), ".incoming-")
	if err != nil {
		return "", 0, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, err
	}
	hexDigest := hex.EncodeToString(h.Sum(nil))
	digest := "sha256:" + hexDigest
	dst := filepath.Join(s.Root, "objects", "sha256", hexDigest)
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.Rename(name, dst); err != nil {
			return "", 0, err
		}
	}
	return digest, n, nil
}

func (s Store) PutBytes(data []byte) (string, int64, error) {
	return s.Put(strings.NewReader(string(data)))
}

func (s Store) PutFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	return s.Put(f)
}

func (s Store) Open(digest string) (*os.File, error) {
	path, err := s.Path(digest)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return nil, err
	}
	if "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest {
		f.Close()
		return nil, fmt.Errorf("bundle object %s failed digest verification", digest)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s Store) Path(digest string) (string, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) {
		return "", fmt.Errorf("unsupported digest %q", digest)
	}
	hexDigest := strings.TrimPrefix(digest, prefix)
	if len(hexDigest) != 64 {
		return "", fmt.Errorf("invalid sha256 digest %q", digest)
	}
	if _, err := hex.DecodeString(hexDigest); err != nil {
		return "", fmt.Errorf("invalid sha256 digest %q", digest)
	}
	return filepath.Join(s.Root, "objects", "sha256", hexDigest), nil
}
