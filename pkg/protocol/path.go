package protocol

import (
	"fmt"
	"os"
	"path/filepath"
)

// CanonicalPath returns an absolute physical path. Existing symlinked prefixes
// are resolved even when the final path does not exist yet.
func CanonicalPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(absolute)
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("resolve path %q: %w", value, err)
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect path %q: %w", value, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot resolve existing prefix for %q", value)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
