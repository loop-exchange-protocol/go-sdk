package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

func TestChecklistGuidesAndApprovesExecutable(t *testing.T) {
	requirements := []spec.Requirement{{ID: "git-cli", Check: spec.Check{Checker: ExecutableContract, Config: map[string]any{"command": "git", "args": []string{"--version"}}}}}
	required := map[string]bool{"git-cli": true}
	items := Check(context.Background(), requirements, required, Options{})
	if len(items) != 1 || items[0].Action != "approve-executable" || !strings.Contains(items[0].Prompt, "Install") {
		t.Fatalf("unexpected guidance: %+v", items)
	}
	var out bytes.Buffer
	opts, err := RunChecklist(context.Background(), requirements, required, Options{}, strings.NewReader("e\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AllowExecutables || !strings.Contains(out.String(), "[x]") {
		t.Fatalf("checklist did not complete:\n%s", out.String())
	}
}

func TestChecklistPreservesArtifactPromptSource(t *testing.T) {
	requirements := []spec.Requirement{{ID: "token", Description: "Identity used by the project API", Prompt: "Obtain the reviewed project token, bind it, then refresh.", Check: spec.Check{Checker: CredentialContract, Config: map[string]any{"accepts": []string{"bearer-token"}}}}}
	items := Check(context.Background(), requirements, map[string]bool{"token": true}, Options{})
	if len(items) != 1 || items[0].Description != requirements[0].Description || items[0].Prompt != requirements[0].Prompt || items[0].PromptSource != "artifact" {
		t.Fatalf("artifact prompt was not preserved: %+v", items)
	}
}

func TestOneTimeSecretIsNotPersisted(t *testing.T) {
	requirements := []spec.Requirement{{ID: "token", Check: spec.Check{Checker: CredentialContract, Config: map[string]any{"accepts": []string{"bearer-token"}}}}}
	var out bytes.Buffer
	opts, err := RunChecklist(context.Background(), requirements, map[string]bool{"token": true}, Options{}, strings.NewReader("s\ntoken\nvalue\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(opts.SecretEnv["token"]) != "value" {
		t.Fatal("one-time secret was not bound")
	}
	path := filepath.Join(t.TempDir(), "profile.yaml")
	if err := WriteProfile(path, ProfileFromOptions(opts)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("value")) || bytes.Contains(data, []byte("LXP_TUI_SECRET")) {
		t.Fatalf("ephemeral secret leaked to profile: %s", data)
	}
}
