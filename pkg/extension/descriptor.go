package extension

import "github.com/loop-exchange-protocol/lxp/pkg/spec"

const (
	HelperProtocol           = "loop.exchange/helper-v1"
	PackageArtifactType      = "application/vnd.loop.exchange.extension.v1"
	PackageConfigMediaType   = "application/vnd.loop.exchange.extension.config.v1+json"
	PackageBinaryMediaType   = "application/vnd.loop.exchange.extension.binary.v1"
	MaxPackageBinaryBytes    = 128 << 20
	MaxHelperMessageBytes    = 8 << 20
	MaxHelperDiagnosticBytes = 64 << 10
)

// PackageDescriptor is the OCI config object for one platform-specific Helper.
// The OCI manifest contains exactly one raw executable layer.
type PackageDescriptor struct {
	APIVersion     string        `json:"api_version" yaml:"api_version"`
	Kind           string        `json:"kind" yaml:"kind"`
	ExtensionKind  string        `json:"extension_kind" yaml:"extension_kind"`
	Contract       spec.Contract `json:"contract" yaml:"contract"`
	Implementation spec.Contract `json:"implementation" yaml:"implementation"`
	Protocol       string        `json:"protocol" yaml:"protocol"`
	OS             string        `json:"os" yaml:"os"`
	Architecture   string        `json:"architecture" yaml:"architecture"`
	Entrypoint     string        `json:"entrypoint" yaml:"entrypoint"`
	Arguments      []string      `json:"arguments,omitempty" yaml:"arguments,omitempty"`
}
