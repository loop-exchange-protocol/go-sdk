package provider

import "testing"

func TestRegistryDoesNotInstallConcreteProviders(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("git"); err == nil {
		t.Fatal("empty SDK registry unexpectedly installed git provider")
	}
}
