package protocol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadInstanceUsesStrictRequirementWireModel(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.yaml")
	if err := os.WriteFile(valid, []byte(`kind: LoopSessionInstance
api_version: loop.exchange/v1alpha1
id: strict
paths: {session_dir: /tmp/session, workdir: /tmp/work, manifest: /tmp/session/manifest.yaml}
requirements:
  - id: git-cli
    check:
      checker: {namespace: loop.exchange, name: executable, version: v1}
      config: {command: git}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	instance, err := ReadYAML[InstanceManifest](valid)
	if err != nil || len(instance.Requirements) != 1 {
		t.Fatalf("read session instance: %+v %v", instance, err)
	}
	legacy := filepath.Join(dir, "legacy.yaml")
	if err := os.WriteFile(legacy, []byte(`kind: LoopSessionInstance
api_version: loop.exchange/v1alpha1
id: legacy
paths: {session_dir: /tmp/session, workdir: /tmp/work, manifest: /tmp/session/manifest.yaml}
runtime: {dependencies: []}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadYAML[InstanceManifest](legacy); err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("expected strict legacy field rejection, got %v", err)
	}
}

func TestWriteYAMLReplacesFileWithoutTemporaryResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := WriteYAML(path, map[string]string{"value": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteYAML(path, map[string]string{"value": "second"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "second") {
		t.Fatalf("state = %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.yaml" {
		t.Fatalf("unexpected state directory: %v", entries)
	}
}
