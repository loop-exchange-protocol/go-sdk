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

	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

type Options struct {
	SecretEnv        map[string]string
	AllowMCP         bool
	AllowExecutables bool
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

func Resolve(ctx context.Context, requirements []spec.Requirement, required map[string]bool, opts Options) ([]spec.RuntimeLock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	locks := make([]spec.RuntimeLock, 0, len(requirements))
	for _, req := range requirements {
		var lock spec.RuntimeLock
		var err error
		switch req.Check.Type {
		case "credential":
			lock, err = resolveCredential(req, opts)
		case "executable":
			if !opts.AllowExecutables {
				err = fmt.Errorf("executable probes are disabled by import policy")
			} else {
				lock, err = resolveExecutable(ctx, req)
			}
		case "mcp":
			if !opts.AllowMCP {
				err = fmt.Errorf("MCP execution is disabled by import policy")
			} else {
				lock, err = resolveMCP(ctx, req, opts)
			}
		default:
			err = fmt.Errorf("unsupported check type %q", req.Check.Type)
		}
		if err != nil {
			if required[req.ID] {
				return nil, fmt.Errorf("requirement %q: %w", req.ID, err)
			}
			locks = append(locks, spec.RuntimeLock{ID: req.ID, Provider: req.Check.Type, Status: "unavailable"})
			continue
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

func resolveCredential(req spec.Requirement, opts Options) (spec.RuntimeLock, error) {
	for _, scheme := range req.Check.Accepts {
		switch scheme {
		case "ssh-agent":
			if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
				if _, err := os.Stat(sock); err == nil {
					return spec.RuntimeLock{ID: req.ID, Provider: "credential", Status: "ready", Implementation: "ssh-agent"}, nil
				}
			}
		case "environment", "bearer-token":
			if envName := opts.SecretEnv[req.ID]; envName != "" && os.Getenv(envName) != "" {
				return spec.RuntimeLock{ID: req.ID, Provider: "credential", Status: "ready", Implementation: scheme}, nil
			}
		}
	}
	return spec.RuntimeLock{}, fmt.Errorf("no accepted binding scheme is available")
}

func resolveExecutable(ctx context.Context, req spec.Requirement) (spec.RuntimeLock, error) {
	command := req.Check.Command
	if command == "" || strings.ContainsAny(command, `/\\`) {
		return spec.RuntimeLock{}, fmt.Errorf("command must be a bare executable name")
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return spec.RuntimeLock{}, err
	}
	args := req.Check.Args
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
			return spec.RuntimeLock{}, fmt.Errorf("probe failed: %w", probeCtx.Err())
		}
		return spec.RuntimeLock{}, fmt.Errorf("probe failed: %w", err)
	}
	version := strings.TrimSpace(out.String())
	if len(version) > 256 {
		version = version[:256]
	}
	return spec.RuntimeLock{ID: req.ID, Provider: req.Check.Type, Status: "ready", Implementation: path, Version: version}, nil
}

func resolveMCP(ctx context.Context, req spec.Requirement, opts Options) (spec.RuntimeLock, error) {
	command := req.Check.Command
	if command == "" || strings.ContainsAny(command, `/\\`) {
		return spec.RuntimeLock{}, fmt.Errorf("MCP command must be a bare executable name")
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return spec.RuntimeLock{}, err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, path, req.Check.Args...)
	cmd.WaitDelay = externalCommandWaitDelay
	env := MinimalEnv()
	if inject := req.Check.SecretEnv; inject != nil {
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
		return spec.RuntimeLock{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return spec.RuntimeLock{}, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return spec.RuntimeLock{}, err
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
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "lxp", "version": "0.1.0"}}}); err != nil {
		return spec.RuntimeLock{}, err
	}
	if _, err := scanResponse(cmdCtx, dec, stdout, 1); err != nil {
		return spec.RuntimeLock{}, err
	}
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return spec.RuntimeLock{}, err
	}
	result, err := scanResponse(cmdCtx, dec, stdout, 2)
	if err != nil {
		return spec.RuntimeLock{}, err
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
	for _, name := range req.Check.RequiredTools {
		if !available[name] {
			return spec.RuntimeLock{}, fmt.Errorf("required MCP tool %q is missing", name)
		}
	}
	return spec.RuntimeLock{ID: req.ID, Provider: req.Check.Type, Status: "ready", Implementation: path, ContractDigest: "sha256:" + hex.EncodeToString(sum[:])}, nil
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
