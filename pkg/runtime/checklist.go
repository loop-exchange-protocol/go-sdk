package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/loop-exchange-protocol/go-sdk/pkg/spec"
)

type CheckItem struct {
	ID           string `json:"id"`
	Description  string `json:"description,omitempty"`
	Kind         string `json:"kind"`
	Required     bool   `json:"required"`
	Status       string `json:"status"`
	Detail       string `json:"detail"`
	Action       string `json:"action"`
	Prompt       string `json:"prompt"`
	PromptSource string `json:"prompt_source"`
}

func Check(ctx context.Context, requirements []spec.Requirement, required map[string]bool, opts Options) []CheckItem {
	items := make([]CheckItem, 0, len(requirements))
	for _, req := range requirements {
		item := CheckItem{ID: req.ID, Description: req.Description, Kind: req.Check.Type, Required: required[req.ID], Status: "missing", Action: "satisfy-requirement"}
		if strings.HasPrefix(req.ProvidedBy, "component:") {
			producer := strings.TrimPrefix(req.ProvidedBy, "component:")
			item.Status, item.Detail, item.Action = "deferred", "checked after component "+producer+" activates", "activate-component"
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Activate component %q, then refresh this Requirement.", producer))
			items = append(items, item)
			continue
		}
		var lock spec.RuntimeLock
		var err error
		switch req.Check.Type {
		case "credential":
			item.Action = "provide-binding"
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Provide credential %q using an accepted scheme, then refresh.", req.ID))
			lock, err = resolveCredential(req, opts)
		case "executable":
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Install %q on PATH, approve executable probes, then refresh.", req.Check.Command))
			if !opts.AllowExecutables {
				item.Status, item.Detail, item.Action = "approval", "executable probe requires approval", "approve-executable"
			} else {
				lock, err = resolveExecutable(ctx, req)
			}
		case "mcp":
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Make MCP command %q available with the declared tools, approve MCP checks, then refresh.", req.Check.Command))
			if !opts.AllowMCP {
				item.Status, item.Detail, item.Action = "approval", "MCP check requires approval", "approve-mcp"
			} else {
				lock, err = resolveMCP(ctx, req, opts)
			}
		default:
			err = fmt.Errorf("unsupported check type %q", req.Check.Type)
		}
		if item.Status != "approval" {
			if err != nil {
				item.Detail = err.Error()
			} else {
				item.Status, item.Action, item.Prompt, item.PromptSource = "ready", "none", "No action required.", "engine"
				item.Detail = lock.Implementation
				if item.Detail == "" {
					item.Detail = lock.ContractDigest
				}
			}
		}
		if !item.Required && item.Status != "ready" {
			item.Status = "optional"
		}
		items = append(items, item)
	}
	return items
}

func selectPrompt(artifact, fallback string) (string, string) {
	if artifact != "" {
		return artifact, "artifact"
	}
	return fallback, "provider"
}

func AllRequiredReady(items []CheckItem) bool {
	for _, item := range items {
		if item.Required && item.Status != "ready" && item.Status != "deferred" {
			return false
		}
	}
	return true
}
