package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/loop-exchange-protocol/lxp/pkg/bundle"
	"github.com/loop-exchange-protocol/lxp/pkg/extension"
	"github.com/loop-exchange-protocol/lxp/pkg/protocol"
	"github.com/loop-exchange-protocol/lxp/pkg/provider"
	"github.com/loop-exchange-protocol/lxp/pkg/runtime"
	"github.com/loop-exchange-protocol/lxp/pkg/spec"
)

type server struct {
	scanner *bufio.Scanner
	writer  io.Writer
	handle  func(context.Context, request) (any, error)
}

func runServer(handle func(context.Context, request) (any, error)) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), extension.MaxHelperMessageBytes)
	s := server{scanner: scanner, writer: os.Stdout, handle: handle}
	for scanner.Scan() {
		var req request
		if err := decodeStrictJSON(scanner.Bytes(), &req); err != nil {
			return fmt.Errorf("decode Helper request: %w", err)
		}
		resp := response{Protocol: protocolName(), ID: req.ID}
		if req.Protocol != protocolName() || req.ID == 0 || req.Method == "" {
			resp.Error = &responseError{Code: "invalid-request", Message: "invalid Helper request envelope"}
		} else {
			ctx, cancel, err := requestContext(req.Deadline)
			if err != nil {
				resp.Error = &responseError{Code: "invalid-request", Message: err.Error()}
			} else {
				result, callErr := s.handle(ctx, req)
				cancel()
				if callErr != nil {
					resp.Error = &responseError{Code: "operation-failed", Message: boundedMessage(callErr.Error())}
				} else {
					resp.Result, err = json.Marshal(result)
					if err != nil {
						resp.Result = nil
						resp.Error = &responseError{Code: "internal", Message: "encode Helper result"}
					}
				}
			}
		}
		encoded, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if len(encoded) > extension.MaxHelperMessageBytes {
			return fmt.Errorf("Helper response exceeds protocol limit")
		}
		if _, err := s.writer.Write(append(encoded, '\n')); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func requestContext(deadline string) (context.Context, context.CancelFunc, error) {
	if deadline == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		return ctx, cancel, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid request deadline")
	}
	ctx, cancel := context.WithDeadline(context.Background(), parsed)
	return ctx, cancel, nil
}

func boundedMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 4096 {
		message = message[:4096]
	}
	return message
}

func decodeParams(req request, target any) error {
	if len(req.Params) == 0 {
		return fmt.Errorf("%s requires params", req.Method)
	}
	if err := decodeStrictJSON(req.Params, target); err != nil {
		return fmt.Errorf("decode %s params: %w", req.Method, err)
	}
	return nil
}

// ServeProvider runs a single sequential Helper connection over stdin/stdout.
// factory is called only after the Engine's exact binding is received.
func ServeProvider(factory func(root string) provider.Provider) error {
	var implementation provider.Provider
	return runServer(func(ctx context.Context, req request) (any, error) {
		if req.Method == "initialize" {
			if implementation != nil {
				return nil, fmt.Errorf("Helper is already initialized")
			}
			var params initializeParams
			if err := decodeParams(req, &params); err != nil {
				return nil, err
			}
			if params.ExtensionKind != extension.KindProvider || !params.Contract.Valid() || !params.Implementation.Valid() {
				return nil, fmt.Errorf("invalid Provider binding")
			}
			implementation = factory(params.Root)
			if implementation == nil || implementation.Contract() != params.Contract || implementation.Implementation() != params.Implementation {
				return nil, fmt.Errorf("Provider implementation does not match binding")
			}
			capabilities := providerCapabilities(implementation)
			return initializeResult{Protocol: protocolName(), ExtensionKind: extension.KindProvider, Contract: implementation.Contract(), Implementation: implementation.Implementation(), Distributions: implementation.Distributions(), Capabilities: capabilities}, nil
		}
		if implementation == nil {
			return nil, fmt.Errorf("initialize must be the first request")
		}
		return handleProvider(ctx, implementation, req)
	})
}

func providerCapabilities(p provider.Provider) []string {
	var capabilities []string
	if _, ok := p.(provider.Adopter); ok {
		capabilities = append(capabilities, "adopt")
	}
	if _, ok := p.(provider.Tracker); ok {
		capabilities = append(capabilities, "track")
	}
	if _, ok := p.(provider.NestedDiscoverer); ok {
		capabilities = append(capabilities, "discover-children")
	}
	if _, ok := p.(provider.BoundaryTracker); ok {
		capabilities = append(capabilities, "track-child")
	}
	sort.Strings(capabilities)
	return capabilities
}

func handleProvider(ctx context.Context, p provider.Provider, req request) (any, error) {
	switch req.Method {
	case "provider.match":
		var params struct {
			Path string `json:"path"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		return p.Match(ctx, params.Path)
	case "provider.validate", "provider.apply":
		var params componentParams
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		store := bundle.Store{Root: params.StoreRoot}
		if req.Method == "provider.validate" {
			return nil, p.Validate(ctx, params.Component, store, params.Target)
		}
		return p.Apply(ctx, params.Component, store, params.Target)
	case "provider.export":
		var params struct {
			Ref       protocol.ResolvedRef `json:"ref"`
			Mode      string               `json:"mode"`
			StoreRoot string               `json:"store_root"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		return p.ExportComponent(ctx, params.Ref, params.Mode, bundle.Store{Root: params.StoreRoot})
	case "provider.adopt":
		adopter, ok := p.(provider.Adopter)
		if !ok {
			return nil, fmt.Errorf("Provider does not support adopt")
		}
		var params struct {
			ID           string `json:"id"`
			Path         string `json:"path"`
			Materialized string `json:"materialized"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		return adopter.Adopt(ctx, params.ID, params.Path, params.Materialized)
	case "provider.add":
		tracker, ok := p.(provider.Tracker)
		if !ok {
			return nil, fmt.Errorf("Provider does not support track")
		}
		var params struct {
			Ref   protocol.ResolvedRef `json:"ref"`
			Paths []string             `json:"paths"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		return nil, tracker.Add(ctx, params.Ref, params.Paths)
	case "provider.status", "provider.discover-children":
		var params struct {
			Ref protocol.ResolvedRef `json:"ref"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		if req.Method == "provider.status" {
			tracker, ok := p.(provider.Tracker)
			if !ok {
				return nil, fmt.Errorf("Provider does not support track")
			}
			return tracker.Status(ctx, params.Ref)
		}
		discoverer, ok := p.(provider.NestedDiscoverer)
		if !ok {
			return nil, fmt.Errorf("Provider does not support discover-children")
		}
		return discoverer.DiscoverChildren(ctx, params.Ref)
	case "provider.track-child":
		tracker, ok := p.(provider.BoundaryTracker)
		if !ok {
			return nil, fmt.Errorf("Provider does not support track-child")
		}
		var params struct {
			Parent protocol.ResolvedRef `json:"parent"`
			Child  protocol.ResolvedRef `json:"child"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		return nil, tracker.TrackChild(ctx, params.Parent, params.Child)
	default:
		return nil, fmt.Errorf("unsupported Provider method %q", req.Method)
	}
}

// ServeChecker runs a Checker Helper connection over stdin/stdout.
func ServeChecker(factory func(root string) runtime.Checker) error {
	var implementation runtime.Checker
	return runServer(func(ctx context.Context, req request) (any, error) {
		if req.Method == "initialize" {
			if implementation != nil {
				return nil, fmt.Errorf("Helper is already initialized")
			}
			var params initializeParams
			if err := decodeParams(req, &params); err != nil {
				return nil, err
			}
			if params.ExtensionKind != extension.KindChecker || !params.Contract.Valid() || !params.Implementation.Valid() {
				return nil, fmt.Errorf("invalid Checker binding")
			}
			implementation = factory(params.Root)
			if implementation == nil || implementation.Contract() != params.Contract || implementation.Implementation() != params.Implementation {
				return nil, fmt.Errorf("Checker implementation does not match binding")
			}
			return initializeResult{Protocol: protocolName(), ExtensionKind: extension.KindChecker, Contract: implementation.Contract(), Implementation: implementation.Implementation()}, nil
		}
		if implementation == nil {
			return nil, fmt.Errorf("initialize must be the first request")
		}
		if req.Method != "checker.check" {
			return nil, fmt.Errorf("unsupported Checker method %q", req.Method)
		}
		var params struct {
			Requirement spec.Requirement `json:"requirement"`
			Options     runtime.Options  `json:"options"`
		}
		if err := decodeParams(req, &params); err != nil {
			return nil, err
		}
		return implementation.Check(ctx, params.Requirement, params.Options)
	})
}
