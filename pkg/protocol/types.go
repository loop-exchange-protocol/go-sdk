package protocol

import "github.com/loop-exchange-protocol/go-sdk/pkg/spec"

type InstanceManifest struct {
	Kind         string             `yaml:"kind" json:"kind"`
	APIVersion   string             `yaml:"api_version" json:"api_version"`
	ID           string             `yaml:"id" json:"id"`
	Components   []ResolvedRef      `yaml:"components,omitempty" json:"components,omitempty"`
	Requirements []spec.Requirement `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	Paths        InstancePaths      `yaml:"paths" json:"paths"`
	Metadata     map[string]string  `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

type RefSpec struct {
	ID          string            `yaml:"id" json:"id"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Path        string            `yaml:"path" json:"path"`
	Provider    string            `yaml:"provider" json:"provider"`
	Contract    string            `yaml:"contract,omitempty" json:"contract,omitempty"`
	Config      map[string]any    `yaml:"config,omitempty" json:"config,omitempty"`
	Mode        string            `yaml:"mode,omitempty" json:"mode,omitempty"`
	Selector    RefSelector       `yaml:"selector" json:"selector"`
	Metadata    map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Requires    []string          `yaml:"requires,omitempty" json:"requires,omitempty"`
}

type RefSelector struct {
	URL    string   `yaml:"url,omitempty" json:"url,omitempty"`
	Ref    string   `yaml:"ref,omitempty" json:"ref,omitempty"`
	Path   string   `yaml:"path,omitempty" json:"path,omitempty"`
	Tags   []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	Depth  int      `yaml:"depth,omitempty" json:"depth,omitempty"`
	Subdir string   `yaml:"subdir,omitempty" json:"subdir,omitempty"`
}

type ResolvedRef struct {
	ID           string            `yaml:"id" json:"id"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	Path         string            `yaml:"path" json:"path"`
	Provider     string            `yaml:"provider" json:"provider"`
	Contract     string            `yaml:"contract" json:"contract"`
	Distribution string            `yaml:"distribution,omitempty" json:"distribution,omitempty"`
	Config       map[string]any    `yaml:"config,omitempty" json:"config,omitempty"`
	Source       string            `yaml:"source" json:"source"`
	Revision     string            `yaml:"revision,omitempty" json:"revision,omitempty"`
	PoolPath     string            `yaml:"pool_path" json:"pool_path"`
	Materialized string            `yaml:"materialized,omitempty" json:"materialized,omitempty"`
	Metadata     map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Requires     []string          `yaml:"requires,omitempty" json:"requires,omitempty"`
	Children     []ChildComponent  `yaml:"-" json:"-"`
}

// ChildComponent describes a direct nested Component to its parent Provider.
// Path is relative to the parent root. The field is runtime orchestration
// context and is never serialized into portable or local manifests.
type ChildComponent struct {
	ID       string
	Path     string
	Provider string
	Contract string
	Revision string
}

type InstancePaths struct {
	SessionDir string `yaml:"session_dir" json:"session_dir"`
	Workdir    string `yaml:"workdir" json:"workdir"`
	Manifest   string `yaml:"manifest" json:"manifest"`
}
