package spec

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var namespace = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (c Contract) String() string {
	return c.Namespace + ":" + c.Name + ":" + c.Version
}

func (c Contract) Valid() bool {
	return namespace.MatchString(c.Namespace) && identifier.MatchString(c.Name) && identifier.MatchString(c.Version)
}

func ParseContract(value string) (Contract, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return Contract{}, fmt.Errorf("invalid contract %q; expected NAMESPACE:NAME:VERSION", value)
	}
	contract := Contract{Namespace: parts[0], Name: parts[1], Version: parts[2]}
	if !contract.Valid() {
		return Contract{}, fmt.Errorf("invalid contract %q; expected DNS namespace and safe name/version", value)
	}
	return contract, nil
}

func ReadArtifact(path string) (Artifact, error) {
	var out Artifact
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return out, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return out, fmt.Errorf("manifest must contain exactly one YAML document")
		}
		return out, fmt.Errorf("read trailing manifest content: %w", err)
	}
	return out, Validate(out)
}

func Write(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lxp-spec-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
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

func Validate(a Artifact) error {
	if a.APIVersion != APIVersion || a.Kind != Kind {
		return fmt.Errorf("unsupported artifact %s %s", a.APIVersion, a.Kind)
	}
	for field, value := range map[string]string{"namespace": a.Coordinates.Namespace, "name": a.Coordinates.Name, "version": a.Coordinates.Version} {
		if !identifier.MatchString(value) {
			return fmt.Errorf("invalid coordinate %s %q", field, value)
		}
	}
	seen := map[string]bool{}
	paths := map[string]string{}
	requirements := map[string]bool{}
	for _, req := range a.Requirements {
		if !identifier.MatchString(req.ID) || requirements[req.ID] {
			return fmt.Errorf("invalid or duplicate requirement id %q", req.ID)
		}
		requirements[req.ID] = true
		if err := validateRequirement(req); err != nil {
			return fmt.Errorf("requirement %q: %w", req.ID, err)
		}
	}
	for _, ref := range a.Components {
		if !identifier.MatchString(ref.ID) {
			return fmt.Errorf("invalid ref identity %q", ref.ID)
		}
		if seen[ref.ID] {
			return fmt.Errorf("duplicate ref id %q", ref.ID)
		}
		seen[ref.ID] = true
		resolvedPath, err := protocolPath(ref.Path)
		if err != nil {
			return fmt.Errorf("ref %q: %w", ref.ID, err)
		}
		if otherID, exists := paths[resolvedPath]; exists {
			return fmt.Errorf("refs %q and %q use duplicate path %q", otherID, ref.ID, ref.Path)
		}
		paths[resolvedPath] = ref.ID
		if !ref.Provider.Valid() {
			return fmt.Errorf("component %q has invalid provider contract %q", ref.ID, ref.Provider.String())
		}
		if err := rejectInlineSecrets(ref.Config, "component."+ref.ID+".config"); err != nil {
			return err
		}
		if err := rejectInlineSecrets(ref.Metadata, "component."+ref.ID+".metadata"); err != nil {
			return err
		}
		if err := validateDistribution(ref); err != nil {
			return fmt.Errorf("component %q: %w", ref.ID, err)
		}
		for _, requirementID := range ref.Requires {
			if !requirements[requirementID] {
				return fmt.Errorf("component %q requires unknown requirement %q", ref.ID, requirementID)
			}
		}
		if utf8.RuneCountInString(ref.Description) > 2048 {
			return fmt.Errorf("component %q description exceeds 2048 characters", ref.ID)
		}
	}
	return nil
}

func validateDistribution(component Component) error {
	switch component.Distribution {
	case "reference":
		if component.Reference == nil || component.Embedded != nil {
			return fmt.Errorf("reference distribution requires only reference")
		}
	case "embedded":
		if component.Embedded == nil || len(component.Embedded.Payloads) == 0 || component.Reference != nil {
			return fmt.Errorf("embedded distribution requires only embedded payload")
		}
	case "mirrored":
		if component.Reference == nil || component.Embedded == nil || len(component.Embedded.Payloads) == 0 {
			return fmt.Errorf("mirrored distribution requires reference and embedded payload")
		}
		if component.Reference.Revision != component.Embedded.Revision {
			return fmt.Errorf("mirrored reference and embedded revisions must match")
		}
	default:
		return fmt.Errorf("unsupported distribution %q", component.Distribution)
	}
	if component.Reference != nil && component.Reference.Revision == "" {
		return fmt.Errorf("reference is not locked to a revision")
	}
	if component.Reference != nil && component.Reference.Locator == "" {
		return fmt.Errorf("reference locator is empty")
	}
	if component.Embedded != nil {
		for role, payload := range component.Embedded.Payloads {
			if !identifier.MatchString(role) {
				return fmt.Errorf("invalid embedded payload role %q", role)
			}
			if payload.MediaType == "" || !sha256Digest.MatchString(payload.Digest) || payload.Size < 0 {
				return fmt.Errorf("invalid embedded payload %q", role)
			}
		}
	}
	return nil
}

func validateRequirement(req Requirement) error {
	if utf8.RuneCountInString(req.Description) > 2048 || utf8.RuneCountInString(req.Prompt) > 8192 {
		return fmt.Errorf("description or prompt exceeds limit")
	}
	if !req.Check.Checker.Valid() {
		return fmt.Errorf("invalid checker contract %q", req.Check.Checker.String())
	}
	if err := rejectInlineSecrets(req.Check.Config, "check.config"); err != nil {
		return err
	}
	return nil
}

func ValidateRequirement(req Requirement) error { return validateRequirement(req) }

func ValidateOrchestration(components []Component, requirements []Requirement) error {
	return validateOrchestration(components, requirements)
}

func validateOrchestration(components []Component, requirements []Requirement) error {
	known := map[string]bool{}
	for _, requirement := range requirements {
		known[requirement.ID] = true
	}
	for _, component := range components {
		for _, requirement := range component.Requires {
			if !known[requirement] {
				return fmt.Errorf("component %q requires unknown requirement %q", component.ID, requirement)
			}
		}
	}
	return nil
}

func protocolPath(value string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "" || value != clean || strings.Contains(value, "\\") || filepath.IsAbs(filepath.FromSlash(value)) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".loop" || strings.HasPrefix(clean, ".loop/") || clean == ".lxp" || strings.HasPrefix(clean, ".lxp/") {
		return "", fmt.Errorf("unsafe ref path %q", value)
	}
	return clean, nil
}

func ValidIdentifier(value string) bool { return identifier.MatchString(value) }

func rejectInlineSecrets(value any, path string) error {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			next := key
			if path != "" {
				next = path + "." + key
			}
			lower := strings.ToLower(key)
			if key != "secret_env" && (strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "authorization") || strings.Contains(lower, "private_key")) {
				return fmt.Errorf("inline secret-like field %q is forbidden; use a credential Requirement", next)
			}
			if key != "secret_env" {
				if err := rejectInlineSecrets(item, next); err != nil {
					return err
				}
			}
		}
	case map[string]string:
		for key := range v {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "authorization") || strings.Contains(lower, "private_key") {
				return fmt.Errorf("inline secret-like field %q is forbidden; use a credential Requirement", path+"."+key)
			}
		}
	case []any:
		for i, item := range v {
			if err := rejectInlineSecrets(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
