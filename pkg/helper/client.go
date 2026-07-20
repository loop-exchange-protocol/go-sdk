package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

const (
	helperWaitDelay          = 2 * time.Second
	defaultHelperCallTimeout = 15 * time.Minute
)

type limitedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := extension.MaxHelperDiagnosticBytes - len(b.data)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		b.data = append(b.data, p[:remaining]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}

type client struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	stderr  *limitedBuffer
	nextID  uint64
	closed  bool
}

func startClient(_ context.Context, command []string) (*client, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("empty Helper command")
	}
	path := command[0]
	if !strings.ContainsAny(path, `/\\`) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return nil, fmt.Errorf("find Helper %q: %w", path, err)
		}
		path = resolved
	}
	cmd := exec.Command(path, command[1:]...)
	cmd.Env = helperEnvironment()
	cmd.WaitDelay = helperWaitDelay
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	diagnostics := &limitedBuffer{}
	cmd.Stderr = diagnostics
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start Helper: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), extension.MaxHelperMessageBytes)
	return &client{cmd: cmd, stdin: stdin, scanner: scanner, stderr: diagnostics}, nil
}

func helperEnvironment() []string {
	env := runtime.MinimalEnv()
	for _, key := range []string{"SSH_AUTH_SOCK", "TMPDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func (c *client) call(ctx context.Context, method string, params, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultHelperCallTimeout)
		defer cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("Helper is closed")
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.nextID++
	req := request{Protocol: protocolName(), ID: c.nextID, Method: method, Params: payload}
	if deadline, ok := ctx.Deadline(); ok {
		req.Deadline = deadline.UTC().Format(time.RFC3339Nano)
	}
	if err := json.NewEncoder(c.stdin).Encode(req); err != nil {
		return c.processError("write request", err)
	}
	type scanResult struct {
		line []byte
		err  error
	}
	done := make(chan scanResult, 1)
	go func() {
		if !c.scanner.Scan() {
			err := c.scanner.Err()
			if err == nil {
				err = io.EOF
			}
			done <- scanResult{err: err}
			return
		}
		done <- scanResult{line: append([]byte(nil), c.scanner.Bytes()...)}
	}()
	var scanned scanResult
	select {
	case <-ctx.Done():
		_ = c.cmd.Process.Kill()
		return fmt.Errorf("Helper %s: %w", method, ctx.Err())
	case scanned = <-done:
	}
	if scanned.err != nil {
		return c.processError("read response", scanned.err)
	}
	var resp response
	if err := decodeStrictJSON(scanned.line, &resp); err != nil {
		return c.processError("decode response", err)
	}
	if resp.Protocol != protocolName() || resp.ID != req.ID {
		return fmt.Errorf("Helper returned an invalid response envelope")
	}
	if (resp.Error == nil) == (len(resp.Result) == 0) {
		return fmt.Errorf("Helper returned both result and error or neither")
	}
	if resp.Error != nil {
		return fmt.Errorf("Helper %s failed (%s): %s", method, boundedMessage(resp.Error.Code), boundedMessage(resp.Error.Message))
	}
	if result != nil {
		if len(resp.Result) == 0 {
			return fmt.Errorf("Helper %s returned no result", method)
		}
		if err := decodeStrictJSON(resp.Result, result); err != nil {
			return fmt.Errorf("decode Helper %s result: %w", method, err)
		}
	}
	return nil
}

func (c *client) processError(action string, err error) error {
	diagnostic := c.stderr.String()
	if diagnostic != "" {
		return fmt.Errorf("%s: %w; Helper diagnostic: %s", action, err, diagnostic)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func (c *client) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	if c.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(helperWaitDelay):
		_ = c.cmd.Process.Kill()
		err = <-done
	}
	if err != nil {
		if c.cmd.ProcessState != nil && c.cmd.ProcessState.Exited() {
			return c.processError("wait for Helper", err)
		}
		return c.processError("stop Helper", err)
	}
	return nil
}

func initialize(ctx context.Context, c *client, root, kind string, contract, implementation spec.Contract) (initializeResult, error) {
	var result initializeResult
	err := c.call(ctx, "initialize", initializeParams{Root: root, ExtensionKind: kind, Contract: contract, Implementation: implementation}, &result)
	if err != nil {
		return initializeResult{}, err
	}
	if result.Protocol != protocolName() || result.ExtensionKind != kind || result.Contract != contract || result.Implementation != implementation {
		return initializeResult{}, fmt.Errorf("Helper handshake does not match configured binding %s -> %s", contract.String(), implementation.String())
	}
	return result, nil
}
