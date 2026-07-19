package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

func TestResolveMCPContract(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "fake-mcp")
	script := `#!/bin/sh
read initialize
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}}'
read initialized
read tools
printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"context.get","description":"fixture","inputSchema":{"type":"object"}}]}}'
`
	if err := os.WriteFile(server, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	requirements := []spec.Requirement{{ID: "context-tools", Check: spec.Check{Checker: MCPContract, Config: map[string]any{"command": "fake-mcp", "required_tools": []string{"context.get"}}}}}
	observations, err := Resolve(context.Background(), requirements, map[string]bool{"context-tools": true}, Options{AllowMCP: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].Status != "ready" || observations[0].ContractDigest == "" {
		t.Fatalf("unexpected observation: %+v", observations)
	}
}

func TestRequiredSecretUsesReferenceNotValue(t *testing.T) {
	t.Setenv("LXP_TEST_TOKEN", "secret-value")
	_, err := Resolve(context.Background(), []spec.Requirement{{ID: "token", Check: spec.Check{Checker: CredentialContract, Config: map[string]any{"accepts": []string{"bearer-token"}}}}}, map[string]bool{"token": true}, Options{SecretEnv: map[string]string{"token": "LXP_TEST_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveCredentialBinding(t *testing.T) {
	t.Setenv("LXP_CREDENTIAL", "token-value")
	observations, err := Resolve(context.Background(), []spec.Requirement{{ID: "git_auth", Check: spec.Check{Checker: CredentialContract, Config: map[string]any{"accepts": []string{"bearer-token"}}}}}, map[string]bool{"git_auth": true}, Options{SecretEnv: map[string]string{"git_auth": "LXP_CREDENTIAL"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].Implementation != "bearer-token" {
		t.Fatalf("unexpected observation: %+v", observations)
	}
}

func TestExecutableProbeRequiresPolicy(t *testing.T) {
	_, err := Resolve(context.Background(), []spec.Requirement{{ID: "git", Check: spec.Check{Checker: ExecutableContract, Config: map[string]any{"command": "git"}}}}, map[string]bool{"git": true}, Options{})
	if err == nil {
		t.Fatal("expected executable policy rejection")
	}
}

func TestRegistryRejectsDuplicateCheckerContract(t *testing.T) {
	first := checker{contract: CredentialContract, implementation: CredentialImplementation, check: func(context.Context, spec.Requirement, Options) (Observation, error) { return Observation{}, nil }}
	registry := NewRegistry(first, first)
	if _, err := registry.Get(CredentialContract); err == nil {
		t.Fatal("duplicate Checker contract unexpectedly resolved")
	}
}

func TestMCPProbeHonorsCallerDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	dir := t.TempDir()
	server := filepath.Join(dir, "blocking-mcp")
	if err := os.WriteFile(server, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := Resolve(ctx, []spec.Requirement{{ID: "blocking", Check: spec.Check{Checker: MCPContract, Config: map[string]any{"command": "blocking-mcp"}}}}, map[string]bool{"blocking": true}, Options{AllowMCP: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resolve error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("MCP cancellation took %s", elapsed)
	}
}
