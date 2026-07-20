package protocol

import "github.com/loop-exchange-protocol/lxp/pkg/spec"

type InstanceManifest struct {
	Kind         string                `yaml:"kind" json:"kind"`
	APIVersion   string                `yaml:"api_version" json:"api_version"`
	ID           string                `yaml:"id" json:"id"`
	Components   []ResolvedRef         `yaml:"components,omitempty" json:"components,omitempty"`
	Requirements []spec.Requirement    `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	Extensions   []ExtensionResolution `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	Paths        InstancePaths         `yaml:"paths" json:"paths"`
	Metadata     map[string]string     `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// ExtensionResolution pins consumer-local implementation identity for a
// retryable Import. It is Session state and never portable Artifact content.
type ExtensionResolution struct {
	Kind           string        `yaml:"kind" json:"kind"`
	Contract       spec.Contract `yaml:"contract" json:"contract"`
	Source         string        `yaml:"source" json:"source"`
	Implementation spec.Contract `yaml:"implementation" json:"implementation"`
	Digest         string        `yaml:"digest,omitempty" json:"digest,omitempty"`
}

type ResolvedRef struct {
	ID           string            `yaml:"id" json:"id"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	Path         string            `yaml:"path" json:"path"`
	Provider     spec.Contract     `yaml:"provider" json:"provider"`
	Distribution string            `yaml:"distribution,omitempty" json:"distribution,omitempty"`
	Config       map[string]any    `yaml:"config,omitempty" json:"config,omitempty"`
	Source       string            `yaml:"source" json:"source"`
	Revision     string            `yaml:"revision,omitempty" json:"revision,omitempty"`
	PoolPath     string            `yaml:"pool_path" json:"pool_path"`
	Materialized string            `yaml:"materialized,omitempty" json:"materialized,omitempty"`
	Metadata     map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Requires     []string          `yaml:"requires,omitempty" json:"requires,omitempty"`
	Children     []ChildComponent  `yaml:"-" json:"children,omitempty"`
}

// ChildComponent describes a direct nested Component to its parent Provider.
// Path is relative to the parent root. The field is runtime orchestration
// context and is never serialized into portable or local manifests.
type ChildComponent struct {
	ID       string        `json:"id"`
	Path     string        `json:"path"`
	Provider spec.Contract `json:"provider"`
	Revision string        `json:"revision"`
}

type InstancePaths struct {
	SessionDir string `yaml:"session_dir" json:"session_dir"`
	Workdir    string `yaml:"workdir" json:"workdir"`
	Manifest   string `yaml:"manifest" json:"manifest"`
}
