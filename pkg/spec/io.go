package spec

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var sha256Digest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

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
		if !identifier.MatchString(ref.Provider) {
			return fmt.Errorf("component %q has invalid provider id %q", ref.ID, ref.Provider)
		}
		if !identifier.MatchString(ref.Contract) {
			return fmt.Errorf("component %q has invalid provider contract %q", ref.ID, ref.Contract)
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
	if err := validateOrchestration(a.Components, a.Requirements); err != nil {
		return err
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
	if req.ProvidedBy != "" && req.ProvidedBy != "environment" && !strings.HasPrefix(req.ProvidedBy, "component:") {
		return fmt.Errorf("invalid provided_by %q", req.ProvidedBy)
	}
	if err := rejectInlineSecrets(req.Check.Extensions, "check"); err != nil {
		return err
	}
	switch req.Check.Type {
	case "executable", "mcp":
		if req.Check.Command == "" || strings.ContainsAny(req.Check.Command, `/\\`) {
			return fmt.Errorf("%s check requires a bare command", req.Check.Type)
		}
	case "credential":
		if len(req.Check.Accepts) == 0 {
			return fmt.Errorf("credential check requires accepted schemes")
		}
		for _, scheme := range req.Check.Accepts {
			if scheme != "ssh-agent" && scheme != "environment" && scheme != "bearer-token" {
				return fmt.Errorf("unsupported credential scheme %q", scheme)
			}
		}
	default:
		return fmt.Errorf("unsupported check type %q", req.Check.Type)
	}
	return nil
}

func ValidateRequirement(req Requirement) error { return validateRequirement(req) }

func ValidateOrchestration(components []Component, requirements []Requirement) error {
	return validateOrchestration(components, requirements)
}

func validateOrchestration(components []Component, requirements []Requirement) error {
	componentIDs := map[string]bool{}
	for _, component := range components {
		componentIDs[component.ID] = true
	}
	graph := map[string][]string{}
	for _, requirement := range requirements {
		if strings.HasPrefix(requirement.ProvidedBy, "component:") {
			producer := strings.TrimPrefix(requirement.ProvidedBy, "component:")
			if !componentIDs[producer] {
				return fmt.Errorf("requirement %q references unknown producer component %q", requirement.ID, producer)
			}
			graph["c:"+producer] = append(graph["c:"+producer], "r:"+requirement.ID)
		}
	}
	for _, component := range components {
		for _, requirement := range component.Requires {
			graph["r:"+requirement] = append(graph["r:"+requirement], "c:"+component.ID)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(node string) error {
		if visiting[node] {
			return fmt.Errorf("requirement orchestration contains cycle at %q", strings.TrimPrefix(strings.TrimPrefix(node, "c:"), "r:"))
		}
		if visited[node] {
			return nil
		}
		visiting[node] = true
		for _, next := range graph[node] {
			if err := visit(next); err != nil {
				return err
			}
		}
		visiting[node] = false
		visited[node] = true
		return nil
	}
	for node := range graph {
		if err := visit(node); err != nil {
			return err
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
