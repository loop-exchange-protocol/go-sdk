package provider

import (
	"testing"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

func TestRegistryDoesNotInstallConcreteProviders(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get(spec.Contract{Namespace: "loop.exchange", Name: "git", Version: "v1"}); err == nil {
		t.Fatal("empty SDK registry unexpectedly installed git provider")
	}
}
