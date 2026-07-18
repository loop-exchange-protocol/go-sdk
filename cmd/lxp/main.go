package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitprovider "github.com/loop-exchange-protocol/go-provider-git"

	"github.com/loop-exchange-protocol/go-sdk/pkg/bundle"
	"github.com/loop-exchange-protocol/go-sdk/pkg/engine"
	"github.com/loop-exchange-protocol/go-sdk/pkg/protocol"
	lxpruntime "github.com/loop-exchange-protocol/go-sdk/pkg/runtime"
	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "lxp: %v\n", err)
		os.Exit(1)
	}
}

func newEngine(root string) *engine.Engine {
	return engine.New(root, gitprovider.New(filepath.Join(root, "provider-store", "git")))
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "init":
		return runInit(ctx, args[1:])
	case "import":
		return runImport(ctx, args[1:])
	case "export":
		return runExport(ctx, args[1:])
	case "inspect":
		return runInspect(ctx, args[1:])
	case "requirements":
		return runRequirements(ctx, args[1:])
	case "status":
		return runStatus(args[1:])
	case "add":
		return runAdd(args[1:])
	case "help", "-h", "--help":
		return usage()
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	root := fs.String("root", "", "protocol state root (default: WORKDIR/.lxp)")
	sessionID := fs.String("session-id", "work", "session id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("init accepts at most one workdir")
	}
	workdir := "."
	if fs.NArg() == 1 {
		workdir = fs.Arg(0)
	}
	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		return err
	}
	if *root == "" {
		*root = filepath.Join(absWorkdir, ".lxp")
	}
	instance, err := newEngine(*root).InitAt(ctx, *sessionID, absWorkdir)
	if err != nil {
		return err
	}
	fmt.Printf("Initialized empty LXP session %s\nworkdir: %s\n", instance.ID, instance.Paths.Workdir)
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	root := fs.String("root", "", "protocol state root")
	sessionID := fs.String("session-id", "", "session id")
	format := fs.String("format", "text", "text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedRoot, resolvedSession, err := resolveSession(*root, *sessionID)
	if err != nil {
		return err
	}
	status, err := newEngine(resolvedRoot).Status(resolvedSession)
	if err != nil {
		return err
	}
	if *format == "json" {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	if *format != "text" {
		return fmt.Errorf("unsupported format %q", *format)
	}
	for _, path := range status.Components {
		fmt.Printf("C  %s\n", path)
	}
	for _, change := range status.Changes {
		fmt.Printf("%s %s\n", change.Code, change.Path)
	}
	for _, path := range status.Untracked {
		fmt.Printf("?? %s\n", path)
	}
	for _, path := range status.Ignored {
		fmt.Printf("!! %s\n", path)
	}
	if !status.Ready {
		fmt.Printf("\nPrompt: %s\n", status.Prompt)
	}
	return nil
}

func runAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	root := fs.String("root", "", "protocol state root")
	sessionID := fs.String("session-id", "", "session id")
	providerContract := fs.String("provider", "", "explicit provider contract ID@VERSION")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedRoot, resolvedSession, err := resolveSession(*root, *sessionID)
	if err != nil {
		return err
	}
	providerID, contract, err := parseProviderContract(*providerContract)
	if err != nil {
		return err
	}
	paths, err := normalizeWorkspaceArgs(resolvedRoot, resolvedSession, fs.Args())
	if err != nil {
		return err
	}
	instance, err := newEngine(resolvedRoot).AddWithOptions(resolvedSession, paths, engine.AddOptions{Provider: providerID, Contract: contract})
	if err != nil {
		return err
	}
	fmt.Printf("tracked_components: %d\n", len(instance.Components))
	return nil
}

func runImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	sessionID := fs.String("session-id", "work", "session id")
	interactive := fs.Bool("interactive", false, "review and persist requirement policy before import")
	profilePath := fs.String("profile", "", "local non-secret requirement profile")
	allowMCP := fs.Bool("allow-mcp", false, "allow declared MCP checks")
	allowExecutables := fs.Bool("allow-executables", false, "allow declared executable checks")
	var secretBindings stringList
	fs.Var(&secretBindings, "secret-env", "bind a secret slot to an environment variable: SLOT=ENV_NAME")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("import requires ARTIFACT [WORKDIR]")
	}
	bundlePath, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := requireArchivePath(bundlePath); err != nil {
		return err
	}
	workdir := ""
	if fs.NArg() == 2 {
		workdir = fs.Arg(1)
	} else {
		base := filepath.Base(bundlePath)
		workdir = strings.TrimSuffix(strings.TrimSuffix(base, ".lxpz"), ".lxp")
	}
	workdir, err = filepath.Abs(workdir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(workdir); err == nil {
		return fmt.Errorf("import target %q already exists", workdir)
	} else if !os.IsNotExist(err) {
		return err
	}
	root := filepath.Join(workdir, ".lxp")
	e := newEngine(root)
	artifact, plans, err := validateProductionBundle(ctx, bundlePath)
	if err != nil {
		return err
	}
	printPlans(os.Stderr, plans)
	resolvedProfile := *profilePath
	if resolvedProfile == "" {
		resolvedProfile = defaultProfilePath(root, artifact)
	}
	profile, err := lxpruntime.ReadProfile(resolvedProfile)
	if err != nil {
		return err
	}
	opts := profile.Options()
	opts.AllowMCP = opts.AllowMCP || *allowMCP
	opts.AllowExecutables = opts.AllowExecutables || *allowExecutables
	if opts.SecretEnv == nil {
		opts.SecretEnv = map[string]string{}
	}
	bindings, err := parseBindings(secretBindings)
	if err != nil {
		return err
	}
	for slot, name := range bindings {
		opts.SecretEnv[slot] = name
	}
	if *interactive {
		opts, err = lxpruntime.RunChecklist(ctx, artifact.Requirements, requiredArtifactRequirements(artifact.Components), opts, os.Stdin, os.Stdout)
		if err != nil {
			return err
		}
		if err := lxpruntime.WriteProfile(resolvedProfile, lxpruntime.ProfileFromOptions(opts)); err != nil {
			return err
		}
	}
	instance, err := e.ImportBundle(ctx, engine.BundleImportOptions{Bundle: bundlePath, SessionID: *sessionID, Workdir: workdir, AllowMCP: opts.AllowMCP, AllowExecutables: opts.AllowExecutables, SecretEnv: opts.SecretEnv})
	if err != nil {
		return err
	}
	fmt.Printf("Imported into %s\n", instance.Paths.Workdir)
	return nil
}

func printPlans(out *os.File, plans []engine.ComponentPlan) {
	for _, plan := range plans {
		fmt.Fprintf(out, "Plan %s (%s@%s):\n", plan.Component, plan.Provider, plan.Contract)
		for _, action := range plan.Actions {
			fmt.Fprintf(out, "  - %s\n", action)
		}
		if len(plan.Requirements) > 0 {
			fmt.Fprintf(out, "  requirements: %s\n", strings.Join(plan.Requirements, ", "))
		}
	}
}

func runRequirements(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("requirements", flag.ContinueOnError)
	format := fs.String("format", "tui", "tui or json")
	profilePath := fs.String("profile", "", "local non-secret requirement profile")
	allowMCP := fs.Bool("allow-mcp", false, "allow MCP checks")
	allowExecutables := fs.Bool("allow-executables", false, "allow executable probes")
	var secretBindings stringList
	fs.Var(&secretBindings, "secret-env", "SLOT=ENV_NAME")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("requirements requires one Artifact")
	}
	bundlePath := fs.Arg(0)
	artifact, _, err := validateProductionBundle(ctx, bundlePath)
	if err != nil {
		return err
	}
	resolvedProfile := *profilePath
	if resolvedProfile == "" {
		resolvedProfile = defaultProfilePath("", artifact)
	}
	profile, err := lxpruntime.ReadProfile(resolvedProfile)
	if err != nil {
		return err
	}
	opts := profile.Options()
	opts.AllowMCP = opts.AllowMCP || *allowMCP
	opts.AllowExecutables = opts.AllowExecutables || *allowExecutables
	bindings, err := parseBindings(secretBindings)
	if err != nil {
		return err
	}
	if opts.SecretEnv == nil {
		opts.SecretEnv = map[string]string{}
	}
	for slot, name := range bindings {
		opts.SecretEnv[slot] = name
	}
	if *format == "json" {
		items := lxpruntime.Check(ctx, artifact.Requirements, requiredArtifactRequirements(artifact.Components), opts)
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": lxpruntime.AllRequiredReady(items), "items": items})
	}
	if *format != "tui" {
		return fmt.Errorf("unsupported format %q", *format)
	}
	opts, err = lxpruntime.RunChecklist(ctx, artifact.Requirements, requiredArtifactRequirements(artifact.Components), opts, os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	return lxpruntime.WriteProfile(resolvedProfile, lxpruntime.ProfileFromOptions(opts))
}

func readBundleArtifact(path string) (spec.Artifact, error) {
	tmp, err := os.MkdirTemp("", "lxp-requirements-")
	if err != nil {
		return spec.Artifact{}, err
	}
	defer os.RemoveAll(tmp)
	if err := bundle.Unpack(path, tmp); err != nil {
		return spec.Artifact{}, err
	}
	return spec.ReadArtifact(filepath.Join(tmp, "manifest.yaml"))
}

func validateProductionBundle(ctx context.Context, path string) (spec.Artifact, []engine.ComponentPlan, error) {
	if err := requireArchivePath(path); err != nil {
		return spec.Artifact{}, nil, err
	}
	artifact, err := readBundleArtifact(path)
	if err != nil {
		return spec.Artifact{}, nil, err
	}
	if err := validateProductionArtifact(artifact); err != nil {
		return spec.Artifact{}, nil, err
	}
	plans, err := newEngine("").PlanBundle(ctx, path)
	if err != nil {
		return spec.Artifact{}, nil, err
	}
	return artifact, plans, nil
}

func validateProductionArtifact(artifact spec.Artifact) error {
	for _, component := range artifact.Components {
		if component.Provider != "git" || component.Contract != "v1" {
			return fmt.Errorf("production profile rejects component %q: only git@v1 is supported", component.ID)
		}
		switch component.Distribution {
		case "reference":
			if component.Reference == nil || component.Embedded != nil || component.Reference.Subdir != "" || !validGitObjectID(component.Reference.Revision) {
				return fmt.Errorf("production profile rejects component %q: reference Git revision must be a full object ID", component.ID)
			}
		case "embedded":
			if component.Embedded == nil || component.Reference != nil || !validGitObjectID(component.Embedded.Revision) {
				return fmt.Errorf("production profile rejects component %q: embedded Git revision must be a full object ID", component.ID)
			}
			if err := validateGitPayloads(component.ID, component.Embedded.Payloads, true); err != nil {
				return err
			}
		case "mirrored":
			if component.Reference == nil || component.Embedded == nil || component.Reference.Subdir != "" || !validGitObjectID(component.Reference.Revision) || component.Reference.Revision != component.Embedded.Revision {
				return fmt.Errorf("production profile rejects component %q: mirrored Git revisions must be identical full object IDs", component.ID)
			}
			if err := validateGitPayloads(component.ID, component.Embedded.Payloads, false); err != nil {
				return err
			}
		default:
			return fmt.Errorf("production profile rejects component %q: unsupported distribution %q", component.ID, component.Distribution)
		}
	}
	return nil
}

func validateGitPayloads(componentID string, payloads map[string]spec.Payload, allowState bool) error {
	limit := 1
	if allowState {
		limit = 2
	}
	if len(payloads) < 1 || len(payloads) > limit {
		return fmt.Errorf("production profile rejects component %q: unsupported Git payload roles", componentID)
	}
	base, ok := payloads["base"]
	if !ok || base.MediaType != "application/vnd.git.bundle" {
		return fmt.Errorf("production profile rejects component %q: Git base bundle is required", componentID)
	}
	for role, payload := range payloads {
		if role == "base" {
			continue
		}
		if !allowState || role != "state" || payload.MediaType != "application/vnd.loop.git-worktree-state.v1+tar" {
			return fmt.Errorf("production profile rejects component %q: unsupported Git payload role %q", componentID, role)
		}
	}
	return nil
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func requireArchivePath(path string) error {
	if !strings.HasSuffix(strings.ToLower(path), ".lxpz") {
		return fmt.Errorf("production profile requires a .lxpz Artifact")
	}
	return nil
}

func defaultProfilePath(_ string, a spec.Artifact) string {
	root, err := os.UserConfigDir()
	if err != nil || root == "" {
		root = "."
	}
	return filepath.Join(root, "lxp", "profiles", a.Coordinates.Namespace, a.Coordinates.Name, a.Coordinates.Version+".yaml")
}

func runExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	root := fs.String("root", "", "protocol state root")
	sessionID := fs.String("session-id", "", "session id")
	distribution := fs.String("distribution", "embedded", "embedded, reference, or mirrored")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("export requires one output Artifact path")
	}
	if err := requireArchivePath(fs.Arg(0)); err != nil {
		return err
	}
	if *distribution != "embedded" && *distribution != "reference" && *distribution != "mirrored" {
		return fmt.Errorf("unsupported distribution %q", *distribution)
	}
	resolvedRoot, resolvedSession, err := resolveSession(*root, *sessionID)
	if err != nil {
		return err
	}
	version, err := newArtifactVersion()
	if err != nil {
		return err
	}
	path, err := newEngine(resolvedRoot).Export(ctx, engine.ExportOptions{
		SessionID:    resolvedSession,
		Namespace:    "local",
		Name:         resolvedSession,
		Version:      version,
		Out:          fs.Arg(0),
		Distribution: *distribution,
	})
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func newArtifactVersion() (string, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate Artifact version: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(nonce), nil
}

func runInspect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	format := fs.String("format", "yaml", "yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "yaml" {
		return fmt.Errorf("unsupported format %q", *format)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("inspect requires one Artifact")
	}
	if _, _, err := validateProductionBundle(ctx, fs.Arg(0)); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "lxp-inspect-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := bundle.Unpack(fs.Arg(0), tmp); err != nil {
		return err
	}
	manifest := filepath.Join(tmp, "manifest.yaml")
	if _, err := spec.ReadArtifact(manifest); err != nil {
		return err
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func usage() error {
	fmt.Fprintln(os.Stderr, `lxp is the LXP context artifact engine.

Usage:
  lxp init [--session-id work] [WORKDIR]
  lxp import [--session-id work] ARTIFACT.lxpz [WORKDIR]
  lxp export [--distribution embedded|reference|mirrored] ARTIFACT.lxpz
  lxp status [--format text|json]
  lxp add [--provider ID@CONTRACT] PATH...
  lxp inspect ARTIFACT.lxpz
  lxp requirements [--format tui|json] ARTIFACT.lxpz`)
	return nil
}

func resolveSession(root, sessionID string) (string, string, error) {
	if root != "" && sessionID != "" {
		return root, sessionID, nil
	}
	if root != "" {
		matches, err := filepath.Glob(filepath.Join(root, "sessions", "*", "manifest.yaml"))
		if err != nil {
			return "", "", err
		}
		if len(matches) != 1 {
			return "", "", fmt.Errorf("cannot infer session under %q: found %d manifests", root, len(matches))
		}
		instance, err := protocol.ReadYAML[protocol.InstanceManifest](matches[0])
		if err != nil {
			return "", "", err
		}
		return root, instance.ID, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, ".lxp")
		matches, globErr := filepath.Glob(filepath.Join(candidate, "sessions", "*", "manifest.yaml"))
		if globErr != nil {
			return "", "", globErr
		}
		for _, manifest := range matches {
			instance, readErr := protocol.ReadYAML[protocol.InstanceManifest](manifest)
			if readErr != nil {
				return "", "", readErr
			}
			if sessionID != "" && instance.ID != sessionID {
				continue
			}
			rel, relErr := filepath.Rel(instance.Paths.Workdir, cwd)
			if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return candidate, instance.ID, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", "", fmt.Errorf("not inside an LXP worktree; run lxp init or pass --root and --session-id")
}

func normalizeWorkspaceArgs(root, sessionID string, paths []string) ([]string, error) {
	instance, err := protocol.ReadYAML[protocol.InstanceManifest](filepath.Join(root, "sessions", sessionID, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cwdRel, err := filepath.Rel(instance.Paths.Workdir, cwd)
	if err != nil || cwdRel == ".." || strings.HasPrefix(cwdRel, ".."+string(filepath.Separator)) {
		return paths, nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		absolute := path
		if !filepath.IsAbs(path) {
			absolute = filepath.Join(cwd, path)
		}
		rel, err := filepath.Rel(instance.Paths.Workdir, absolute)
		if err != nil {
			return nil, err
		}
		if rel == "." {
			entries, err := os.ReadDir(instance.Paths.Workdir)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				if entry.Name() != ".lxp" && entry.Name() != ".loop" && entry.Name() != ".lxpignore" {
					out = append(out, entry.Name())
				}
			}
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}

type stringList []string

func (s *stringList) String() string         { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }

func parseBindings(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid secret binding %q; expected SLOT=ENV_NAME", value)
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}

func parseProviderContract(value string) (string, string, error) {
	if value == "" {
		return "", "", nil
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid provider %q; expected ID@CONTRACT", value)
	}
	return parts[0], parts[1], nil
}

func requiredArtifactRequirements(components []spec.Component) map[string]bool {
	out := map[string]bool{}
	for _, component := range components {
		for _, id := range component.Requires {
			out[id] = true
		}
	}
	return out
}
