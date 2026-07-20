package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type Options struct {
	SecretEnv        map[string]string `json:"secret_env,omitempty"`
	AllowMCP         bool              `json:"allow_mcp,omitempty"`
	AllowExecutables bool              `json:"allow_executables,omitempty"`
}

var (
	ExecutableContract       = spec.Contract{Namespace: "loop.exchange", Name: "executable", Version: "v1"}
	MCPContract              = spec.Contract{Namespace: "loop.exchange", Name: "mcp", Version: "v1"}
	CredentialContract       = spec.Contract{Namespace: "loop.exchange", Name: "credential", Version: "v1"}
	ExecutableImplementation = spec.Contract{Namespace: "loop.exchange", Name: "checker-executable", Version: "0.1.0-alpha.4"}
	MCPImplementation        = spec.Contract{Namespace: "loop.exchange", Name: "checker-mcp", Version: "0.1.0-alpha.4"}
	CredentialImplementation = spec.Contract{Namespace: "loop.exchange", Name: "checker-credential", Version: "0.1.0-alpha.4"}
)

type Observation struct {
	ID             string        `yaml:"id" json:"id"`
	Checker        spec.Contract `yaml:"checker" json:"checker"`
	Status         string        `yaml:"status" json:"status"`
	Implementation string        `yaml:"implementation,omitempty" json:"implementation,omitempty"`
	Version        string        `yaml:"version,omitempty" json:"version,omitempty"`
	ContractDigest string        `yaml:"contract_digest,omitempty" json:"contract_digest,omitempty"`
}

type Checker interface {
	Contract() spec.Contract
	Implementation() spec.Contract
	Check(ctx context.Context, requirement spec.Requirement, opts Options) (Observation, error)
}

type checker struct {
	contract       spec.Contract
	implementation spec.Contract
	check          func(context.Context, spec.Requirement, Options) (Observation, error)
}

func (c checker) Contract() spec.Contract       { return c.contract }
func (c checker) Implementation() spec.Contract { return c.implementation }
func (c checker) Check(ctx context.Context, requirement spec.Requirement, opts Options) (Observation, error) {
	return c.check(ctx, requirement, opts)
}

type Registry struct {
	checkers        map[string]Checker
	registrationErr error
}

func NewRegistry(checkers ...Checker) *Registry {
	r := &Registry{checkers: map[string]Checker{}}
	for _, c := range checkers {
		r.Register(c)
	}
	return r
}

func (r *Registry) Register(c Checker) {
	if c == nil {
		r.registrationErr = fmt.Errorf("cannot register a nil Checker")
		return
	}
	contract := c.Contract()
	implementation := c.Implementation()
	if !contract.Valid() || !implementation.Valid() {
		r.registrationErr = fmt.Errorf("invalid Checker registration %s -> %s", contract.String(), implementation.String())
		return
	}
	key := contract.String()
	if _, exists := r.checkers[key]; exists {
		r.registrationErr = fmt.Errorf("duplicate Checker contract %s", key)
		return
	}
	r.checkers[key] = c
}

func DefaultRegistry() *Registry {
	return NewRegistry(
		checker{contract: CredentialContract, implementation: CredentialImplementation, check: func(_ context.Context, req spec.Requirement, opts Options) (Observation, error) {
			return resolveCredential(req, opts)
		}},
		checker{contract: ExecutableContract, implementation: ExecutableImplementation, check: resolveExecutable},
		checker{contract: MCPContract, implementation: MCPImplementation, check: resolveMCP},
	)
}

func (r *Registry) Get(contract spec.Contract) (Checker, error) {
	if r.registrationErr != nil {
		return nil, r.registrationErr
	}
	c, ok := r.checkers[contract.String()]
	if !ok {
		return nil, fmt.Errorf("unsupported checker contract %s", contract.String())
	}
	return c, nil
}

const (
	externalCommandWaitDelay = 2 * time.Second
	maxProbeOutputBytes      = 64 << 10
	maxMCPMessageBytes       = 1 << 20
)

type limitedOutput struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - len(w.data)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		w.data = append(w.data, p[:remaining]...)
	}
	return len(p), nil
}

func (w *limitedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.data)
}

func Resolve(ctx context.Context, requirements []spec.Requirement, required map[string]bool, opts Options) ([]Observation, error) {
	return DefaultRegistry().Resolve(ctx, requirements, required, opts)
}

func (r *Registry) Resolve(ctx context.Context, requirements []spec.Requirement, required map[string]bool, opts Options) ([]Observation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	observations := make([]Observation, 0, len(requirements))
	for _, req := range requirements {
		c, err := r.Get(req.Check.Checker)
		if err == nil {
			switch req.Check.Checker {
			case ExecutableContract:
				if !opts.AllowExecutables {
					err = fmt.Errorf("executable probes are disabled by import policy")
				}
			case MCPContract:
				if !opts.AllowMCP {
					err = fmt.Errorf("MCP execution is disabled by import policy")
				}
			}
		}
		var observation Observation
		if err == nil {
			observation, err = c.Check(ctx, req, opts)
		}
		if err != nil {
			if required[req.ID] {
				return nil, fmt.Errorf("requirement %q: %w", req.ID, err)
			}
			observations = append(observations, Observation{ID: req.ID, Checker: req.Check.Checker, Status: "unavailable"})
			continue
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

func resolveCredential(req spec.Requirement, opts Options) (Observation, error) {
	accepts, err := configStrings(req.Check.Config, "accepts")
	if err != nil || len(accepts) == 0 {
		return Observation{}, fmt.Errorf("credential checker requires non-empty config.accepts")
	}
	for _, scheme := range accepts {
		switch scheme {
		case "ssh-agent":
			if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
				if _, err := os.Stat(sock); err == nil {
					return Observation{ID: req.ID, Checker: req.Check.Checker, Status: "ready", Implementation: "ssh-agent"}, nil
				}
			}
		case "environment", "bearer-token":
			if envName := opts.SecretEnv[req.ID]; envName != "" && os.Getenv(envName) != "" {
				return Observation{ID: req.ID, Checker: req.Check.Checker, Status: "ready", Implementation: scheme}, nil
			}
		default:
			return Observation{}, fmt.Errorf("unsupported credential scheme %q", scheme)
		}
	}
	return Observation{}, fmt.Errorf("no accepted binding scheme is available")
}

func resolveExecutable(ctx context.Context, req spec.Requirement, _ Options) (Observation, error) {
	command, _ := configString(req.Check.Config, "command")
	if command == "" || strings.ContainsAny(command, `/\\`) {
		return Observation{}, fmt.Errorf("command must be a bare executable name")
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return Observation{}, err
	}
	args, err := configStrings(req.Check.Config, "args")
	if err != nil {
		return Observation{}, err
	}
	if len(args) == 0 {
		args = []string{"--version"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, args...)
	cmd.Env = MinimalEnv()
	cmd.WaitDelay = externalCommandWaitDelay
	out := &limitedOutput{limit: maxProbeOutputBytes}
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()
	if err != nil {
		if probeCtx.Err() != nil {
			return Observation{}, fmt.Errorf("probe failed: %w", probeCtx.Err())
		}
		return Observation{}, fmt.Errorf("probe failed: %w", err)
	}
	version := strings.TrimSpace(out.String())
	if len(version) > 256 {
		version = version[:256]
	}
	return Observation{ID: req.ID, Checker: req.Check.Checker, Status: "ready", Implementation: path, Version: version}, nil
}

func resolveMCP(ctx context.Context, req spec.Requirement, opts Options) (Observation, error) {
	command, _ := configString(req.Check.Config, "command")
	if command == "" || strings.ContainsAny(command, `/\\`) {
		return Observation{}, fmt.Errorf("MCP command must be a bare executable name")
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return Observation{}, err
	}
	args, err := configStrings(req.Check.Config, "args")
	if err != nil {
		return Observation{}, err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, path, args...)
	cmd.WaitDelay = externalCommandWaitDelay
	env := MinimalEnv()
	if inject, _ := configStringMap(req.Check.Config, "secret_env"); inject != nil {
		for slot, target := range inject {
			source := opts.SecretEnv[slot]
			if target != "" && source != "" {
				env = append(env, target+"="+os.Getenv(source))
			}
		}
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Observation{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Observation{}, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return Observation{}, err
	}
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	enc := json.NewEncoder(stdin)
	dec := bufio.NewScanner(stdout)
	dec.Buffer(make([]byte, 64<<10), maxMCPMessageBytes)
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "lxp", "version": "0.1.0-alpha.4"}}}); err != nil {
		return Observation{}, err
	}
	if _, err := scanResponse(cmdCtx, dec, stdout, 1); err != nil {
		return Observation{}, err
	}
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return Observation{}, err
	}
	result, err := scanResponse(cmdCtx, dec, stdout, 2)
	if err != nil {
		return Observation{}, err
	}
	canonical, _ := json.Marshal(result)
	sum := sha256.Sum256(canonical)
	available := map[string]bool{}
	if obj, ok := result.(map[string]any); ok {
		if tools, ok := obj["tools"].([]any); ok {
			for _, raw := range tools {
				if tool, ok := raw.(map[string]any); ok {
					if n, ok := tool["name"].(string); ok {
						available[n] = true
					}
				}
			}
		}
	}
	requiredTools, err := configStrings(req.Check.Config, "required_tools")
	if err != nil {
		return Observation{}, err
	}
	for _, name := range requiredTools {
		if !available[name] {
			return Observation{}, fmt.Errorf("required MCP tool %q is missing", name)
		}
	}
	return Observation{ID: req.ID, Checker: req.Check.Checker, Status: "ready", Implementation: path, ContractDigest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

func configString(config map[string]any, key string) (string, error) {
	value, ok := config[key]
	if !ok {
		return "", nil
	}
	out, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("config.%s must be a string", key)
	}
	return out, nil
}

func configStrings(config map[string]any, key string) ([]string, error) {
	value, ok := config[key]
	if !ok {
		return nil, nil
	}
	if stringsValue, ok := value.([]string); ok {
		return stringsValue, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("config.%s must be a string array", key)
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("config.%s must be a string array", key)
		}
		out = append(out, text)
	}
	return out, nil
}

func configStringMap(config map[string]any, key string) (map[string]string, error) {
	value, ok := config[key]
	if !ok {
		return nil, nil
	}
	if stringsValue, ok := value.(map[string]string); ok {
		return stringsValue, nil
	}
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config.%s must be a string map", key)
	}
	out := make(map[string]string, len(mapping))
	for name, item := range mapping {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("config.%s must be a string map", key)
		}
		out[name] = text
	}
	return out, nil
}

type scanResult struct {
	value any
	err   error
}

func scanResponse(ctx context.Context, scanner *bufio.Scanner, output io.Closer, id int) (any, error) {
	result := make(chan scanResult, 1)
	go func() {
		value, err := scanResponseBlocking(scanner, id)
		result <- scanResult{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = output.Close()
		return nil, ctx.Err()
	case value := <-result:
		return value.value, value.err
	}
}

func scanResponseBlocking(scanner *bufio.Scanner, id int) (any, error) {
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		intID, _ := strconv.Atoi(fmt.Sprint(msg["id"]))
		if intID != id {
			continue
		}
		if rpcErr := msg["error"]; rpcErr != nil {
			return nil, fmt.Errorf("MCP error: %v", rpcErr)
		}
		return msg["result"], nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read MCP response: %w", err)
	}
	return nil, fmt.Errorf("MCP server closed before response %d", id)
}

func stringSlice(v any) []string {
	if in, ok := v.([]string); ok {
		return in
	}
	var out []string
	if in, ok := v.([]any); ok {
		for _, item := range in {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func MinimalEnv() []string {
	out := []string{"PATH=" + os.Getenv("PATH")}
	if home := os.Getenv("HOME"); home != "" {
		out = append(out, "HOME="+home)
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		out = append(out, "SystemRoot="+root)
	}
	return out
}
