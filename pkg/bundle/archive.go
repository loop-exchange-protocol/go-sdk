package bundle

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxBundleEntries = 100000
	maxBundleEntry   = int64(4 << 30)
	maxBundleTotal   = int64(20 << 30)
)

func Pack(root, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(out), ".lxp-pack-*")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	entries := 0
	var total int64
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle cannot contain symlink %s", rel)
		}
		entries++
		if entries > maxBundleEntries {
			return fmt.Errorf("bundle contains too many entries")
		}
		if info.Size() < 0 || info.Size() > maxBundleEntry {
			return fmt.Errorf("bundle entry %q exceeds size limit", rel)
		}
		total += info.Size()
		if total > maxBundleTotal {
			return fmt.Errorf("bundle exceeds uncompressed size limit")
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpPath, out); err != nil {
		return fmt.Errorf("publish bundle without overwrite: %w", err)
	}
	return nil
}

func Unpack(path, root string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDirectory(path, root)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	entries := 0
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return fmt.Errorf("unsupported bundle entry %q", h.Name)
		}
		entries++
		if entries > maxBundleEntries {
			return fmt.Errorf("bundle contains too many entries")
		}
		if h.Size < 0 || h.Size > maxBundleEntry {
			return fmt.Errorf("bundle entry %q exceeds size limit", h.Name)
		}
		total += h.Size
		if total > maxBundleTotal {
			return fmt.Errorf("bundle exceeds uncompressed size limit")
		}
		dst, err := SafeJoin(root, h.Name)
		if err != nil {
			return err
		}
		clean := filepath.Clean(dst)
		if seen[clean] {
			return fmt.Errorf("duplicate bundle entry %q", h.Name)
		}
		seen[clean] = true
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(dst, os.FileMode(h.Mode)&0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func copyDirectory(source, target string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		dst, err := SafeJoin(target, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("artifact directory contains unsupported entry %q", rel)
		}
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func SafeJoin(root, name string) (string, error) {
	cleanSlash := path.Clean(name)
	windowsVolume := len(name) >= 2 && name[1] == ':' && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z'))
	if name == "" || name != cleanSlash || path.IsAbs(name) || filepath.IsAbs(name) || windowsVolume || strings.Contains(name, "\\") || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(cleanSlash))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	dst := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	return dst, nil
}
