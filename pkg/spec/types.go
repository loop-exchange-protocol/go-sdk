package spec

const (
	APIVersion = "loop.exchange/v1alpha1"
	Kind       = "ContextArtifact"
)

type Artifact struct {
	APIVersion   string        `yaml:"api_version" json:"api_version"`
	Kind         string        `yaml:"kind" json:"kind"`
	Coordinates  Coordinates   `yaml:"coordinates" json:"coordinates"`
	Components   []Component   `yaml:"components,omitempty" json:"components,omitempty"`
	Requirements []Requirement `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	Provenance   Provenance    `yaml:"provenance" json:"provenance"`
}

type Coordinates struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	Name      string `yaml:"name" json:"name"`
	Version   string `yaml:"version" json:"version"`
	Variant   string `yaml:"variant,omitempty" json:"variant,omitempty"`
}

type Payload struct {
	MediaType string `yaml:"media_type" json:"media_type"`
	Digest    string `yaml:"digest" json:"digest"`
	Size      int64  `yaml:"size" json:"size"`
}

// Contract is a globally unique, language-independent extension contract.
// Namespace is a DNS name controlled by the contract maintainer.
type Contract struct {
	Namespace string `yaml:"namespace" json:"namespace"`
	Name      string `yaml:"name" json:"name"`
	Version   string `yaml:"version" json:"version"`
}

type Component struct {
	ID           string            `yaml:"id" json:"id"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	Path         string            `yaml:"path" json:"path"`
	Provider     Contract          `yaml:"provider" json:"provider"`
	Config       map[string]any    `yaml:"config,omitempty" json:"config,omitempty"`
	Distribution string            `yaml:"distribution" json:"distribution"`
	Reference    *Reference        `yaml:"reference,omitempty" json:"reference,omitempty"`
	Embedded     *Embedded         `yaml:"embedded,omitempty" json:"embedded,omitempty"`
	Requires     []string          `yaml:"requires,omitempty" json:"requires,omitempty"`
	Metadata     map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

type Reference struct {
	Locator  string `yaml:"locator" json:"locator"`
	Revision string `yaml:"revision" json:"revision"`
	Subdir   string `yaml:"subdir,omitempty" json:"subdir,omitempty"`
}

type Embedded struct {
	Revision string             `yaml:"revision,omitempty" json:"revision,omitempty"`
	Payloads map[string]Payload `yaml:"payloads" json:"payloads"`
}

type Requirement struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Prompt      string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Check       Check  `yaml:"check" json:"check"`
}

type Check struct {
	Checker Contract       `yaml:"checker" json:"checker"`
	Config  map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
}

type Provenance struct {
	CreatedAt string `yaml:"created_at" json:"created_at"`
	Engine    string `yaml:"engine" json:"engine"`
	Parent    string `yaml:"parent,omitempty" json:"parent,omitempty"`
}
