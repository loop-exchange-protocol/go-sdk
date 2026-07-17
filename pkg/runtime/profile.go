package runtime

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	AllowExecutables bool              `yaml:"allow_executables,omitempty"`
	AllowMCP         bool              `yaml:"allow_mcp,omitempty"`
	SecretEnv        map[string]string `yaml:"secret_env,omitempty"`
}

func ReadProfile(path string) (Profile, error) {
	var out Profile
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = yaml.Unmarshal(data, &out)
	return out, err
}

func WriteProfile(path string, p Profile) error {
	persisted := map[string]string{}
	for slot, name := range p.SecretEnv {
		if !strings.HasPrefix(name, "LXP_TUI_SECRET_") {
			persisted[slot] = name
		}
	}
	p.SecretEnv = persisted
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lxp-profile-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (p Profile) Options() Options {
	return Options{SecretEnv: p.SecretEnv, AllowMCP: p.AllowMCP, AllowExecutables: p.AllowExecutables}
}
func ProfileFromOptions(opts Options) Profile {
	return Profile{AllowExecutables: opts.AllowExecutables, AllowMCP: opts.AllowMCP, SecretEnv: opts.SecretEnv}
}
