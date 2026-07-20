package extension

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
	"gopkg.in/yaml.v3"
)

const (
	KindProvider = "provider"
	KindChecker  = "checker"
)

type Config struct {
	APIVersion   string       `yaml:"api_version" json:"api_version"`
	Kind         string       `yaml:"kind" json:"kind"`
	Repositories []Repository `yaml:"repositories,omitempty" json:"repositories,omitempty"`
	Bindings     []Binding    `yaml:"bindings,omitempty" json:"bindings,omitempty"`
}

type Repository struct {
	ID                string   `yaml:"id" json:"id"`
	URL               string   `yaml:"url" json:"url"`
	Credential        string   `yaml:"credential,omitempty" json:"credential,omitempty"`
	AutoInstall       bool     `yaml:"auto_install,omitempty" json:"auto_install,omitempty"`
	TrustedNamespaces []string `yaml:"trusted_namespaces,omitempty" json:"trusted_namespaces,omitempty"`
}

type Binding struct {
	Kind           string         `yaml:"kind" json:"kind"`
	Contract       spec.Contract  `yaml:"contract" json:"contract"`
	Implementation Implementation `yaml:"implementation" json:"implementation"`
}

type Implementation struct {
	Source  string        `yaml:"source" json:"source"`
	Package spec.Contract `yaml:"package" json:"package"`
	Digest  string        `yaml:"digest,omitempty" json:"digest,omitempty"`
	Command []string      `yaml:"command,omitempty" json:"command,omitempty"`
}

func Default() Config {
	return Config{
		APIVersion: spec.APIVersion,
		Kind:       "EngineConfig",
		Repositories: []Repository{{
			ID: "official", URL: "oci://ghcr.io/loop-exchange-protocol", AutoInstall: true, TrustedNamespaces: []string{"loop.exchange"},
		}},
		Bindings: []Binding{
			builtin(KindProvider, spec.Contract{Namespace: "loop.exchange", Name: "git", Version: "v1"}, "provider-git"),
			builtinImplementation(KindChecker, runtime.ExecutableContract, runtime.ExecutableImplementation),
			builtinImplementation(KindChecker, runtime.MCPContract, runtime.MCPImplementation),
			builtinImplementation(KindChecker, runtime.CredentialContract, runtime.CredentialImplementation),
		},
	}
}

func builtin(kind string, contract spec.Contract, packageName string) Binding {
	return builtinImplementation(kind, contract, spec.Contract{Namespace: "loop.exchange", Name: packageName, Version: "0.1.0-alpha.4"})
}

func builtinImplementation(kind string, contract, implementation spec.Contract) Binding {
	return Binding{Kind: kind, Contract: contract, Implementation: Implementation{Source: "builtin", Package: implementation}}
}

func Read(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("EngineConfig must contain exactly one YAML document")
		}
		return Config{}, fmt.Errorf("read trailing EngineConfig content: %w", err)
	}
	return config, config.Validate()
}

func (c Config) Validate() error {
	if c.APIVersion != spec.APIVersion || c.Kind != "EngineConfig" {
		return fmt.Errorf("unsupported engine config %s %s", c.APIVersion, c.Kind)
	}
	repositories := map[string]bool{}
	for _, repository := range c.Repositories {
		if !spec.ValidIdentifier(repository.ID) || repositories[repository.ID] {
			return fmt.Errorf("invalid or duplicate extension repository %q", repository.ID)
		}
		parsed, err := url.Parse(repository.URL)
		if err != nil || (parsed.Scheme != "oci" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("extension repository %q must use an absolute URL without inline credentials", repository.ID)
		}
		if repository.Credential != "" && !spec.ValidIdentifier(repository.Credential) {
			return fmt.Errorf("extension repository %q has an invalid credential slot", repository.ID)
		}
		if repository.AutoInstall && len(repository.TrustedNamespaces) == 0 {
			return fmt.Errorf("auto-install repository %q requires trusted_namespaces", repository.ID)
		}
		trusted := map[string]bool{}
		for _, namespace := range repository.TrustedNamespaces {
			if !validNamespace(namespace) || trusted[namespace] {
				return fmt.Errorf("extension repository %q has an invalid or duplicate trusted namespace %q", repository.ID, namespace)
			}
			trusted[namespace] = true
		}
		repositories[repository.ID] = true
	}
	bindings := map[string]bool{}
	for _, binding := range c.Bindings {
		if binding.Kind != KindProvider && binding.Kind != KindChecker {
			return fmt.Errorf("invalid extension kind %q", binding.Kind)
		}
		if !binding.Contract.Valid() || !binding.Implementation.Package.Valid() {
			return fmt.Errorf("invalid extension binding %s", binding.Contract.String())
		}
		key := binding.Contract.String()
		if bindings[key] {
			return fmt.Errorf("duplicate global extension contract %s", key)
		}
		bindings[key] = true
		switch binding.Implementation.Source {
		case "builtin":
			if binding.Implementation.Digest != "" || len(binding.Implementation.Command) != 0 {
				return fmt.Errorf("builtin extension %s must not declare digest or command", binding.Contract.String())
			}
		case "helper":
			if binding.Implementation.Digest != "" || !validCommand(binding.Implementation.Command) {
				return fmt.Errorf("helper extension %s requires a command and must not declare a digest", binding.Contract.String())
			}
		case "repository":
			if len(c.Repositories) == 0 || !validDigest(binding.Implementation.Digest) || len(binding.Implementation.Command) != 0 {
				return fmt.Errorf("repository extension %s requires an ordered repository list and SHA-256 digest, and must not declare a command", binding.Contract.String())
			}
			if !c.authorizes(binding.Implementation.Package.Namespace) {
				return fmt.Errorf("repository extension %s has no auto-install repository trusted for namespace %q", binding.Contract.String(), binding.Implementation.Package.Namespace)
			}
		default:
			return fmt.Errorf("extension %s has unsupported source %q", binding.Contract.String(), binding.Implementation.Source)
		}
	}
	return nil
}

func (c Config) authorizes(namespace string) bool {
	for _, repository := range c.Repositories {
		if !repository.AutoInstall {
			continue
		}
		for _, trusted := range repository.TrustedNamespaces {
			if trusted == namespace {
				return true
			}
		}
	}
	return false
}

func validNamespace(namespace string) bool {
	return (spec.Contract{Namespace: namespace, Name: "extension", Version: "v1"}).Valid()
}

func validCommand(command []string) bool {
	if len(command) == 0 || command[0] == "" {
		return false
	}
	for _, value := range command {
		if value == "" || bytes.IndexByte([]byte(value), 0) >= 0 {
			return false
		}
	}
	return true
}

func (c Config) Resolve(kind string, contract spec.Contract) (Implementation, error) {
	implementation, ok, err := c.Lookup(kind, contract)
	if err != nil {
		return Implementation{}, err
	}
	if ok {
		return implementation, nil
	}
	return Implementation{}, fmt.Errorf("no %s implementation binding for %s", kind, contract.String())
}

// Lookup returns the exact configured implementation without treating an
// absent binding as a configuration error.
func (c Config) Lookup(kind string, contract spec.Contract) (Implementation, bool, error) {
	if err := c.Validate(); err != nil {
		return Implementation{}, false, err
	}
	for _, binding := range c.Bindings {
		if binding.Kind == kind && binding.Contract == contract {
			return binding.Implementation, true, nil
		}
	}
	return Implementation{}, false, nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
