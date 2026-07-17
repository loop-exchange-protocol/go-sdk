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

type Component struct {
	ID           string            `yaml:"id" json:"id"`
	Description  string            `yaml:"description,omitempty" json:"description,omitempty"`
	Path         string            `yaml:"path" json:"path"`
	Provider     string            `yaml:"provider" json:"provider"`
	Contract     string            `yaml:"contract" json:"contract"`
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
	ProvidedBy  string `yaml:"provided_by,omitempty" json:"provided_by,omitempty"`
	Check       Check  `yaml:"check" json:"check"`
}

type Check struct {
	Type          string            `yaml:"type" json:"type"`
	Command       string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args          []string          `yaml:"args,omitempty" json:"args,omitempty"`
	RequiredTools []string          `yaml:"required_tools,omitempty" json:"required_tools,omitempty"`
	Accepts       []string          `yaml:"accepts,omitempty" json:"accepts,omitempty"`
	SecretEnv     map[string]string `yaml:"secret_env,omitempty" json:"secret_env,omitempty"`
	Extensions    map[string]any    `yaml:"-" json:"-"`
}

type Provenance struct {
	CreatedAt string `yaml:"created_at" json:"created_at"`
	Engine    string `yaml:"engine" json:"engine"`
	Parent    string `yaml:"parent,omitempty" json:"parent,omitempty"`
}

type Lock struct {
	APIVersion   string            `yaml:"api_version" json:"api_version"`
	Artifact     string            `yaml:"artifact" json:"artifact"`
	Components   []RefLock         `yaml:"components,omitempty" json:"components,omitempty"`
	Requirements []RequirementLock `yaml:"requirements,omitempty" json:"requirements,omitempty"`
}

type RefLock struct {
	ID           string            `yaml:"id" json:"id"`
	Path         string            `yaml:"path" json:"path"`
	Provider     string            `yaml:"provider" json:"provider"`
	Contract     string            `yaml:"contract" json:"contract"`
	Distribution string            `yaml:"distribution" json:"distribution"`
	Revision     string            `yaml:"revision,omitempty" json:"revision,omitempty"`
	Embedded     map[string]string `yaml:"embedded,omitempty" json:"embedded,omitempty"`
}

type RequirementLock struct {
	ID             string `yaml:"id" json:"id"`
	Provider       string `yaml:"provider" json:"provider"`
	Status         string `yaml:"status" json:"status"`
	Implementation string `yaml:"implementation,omitempty" json:"implementation,omitempty"`
	Version        string `yaml:"version,omitempty" json:"version,omitempty"`
	ContractDigest string `yaml:"contract_digest,omitempty" json:"contract_digest,omitempty"`
}

type RuntimeLock = RequirementLock
