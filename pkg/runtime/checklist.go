package runtime

import (
	"context"
	"fmt"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
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
	return CheckWithRegistry(ctx, DefaultRegistry(), requirements, required, opts)
}

func CheckWithRegistry(ctx context.Context, registry *Registry, requirements []spec.Requirement, required map[string]bool, opts Options) []CheckItem {
	items := make([]CheckItem, 0, len(requirements))
	for _, req := range requirements {
		contract := req.Check.Checker
		item := CheckItem{ID: req.ID, Description: req.Description, Kind: contract.String(), Required: required[req.ID], Status: "missing", Action: "satisfy-requirement"}
		checker, err := registry.Get(contract)
		var observation Observation
		switch contract {
		case CredentialContract:
			item.Action = "provide-binding"
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Provide credential %q using an accepted scheme, then refresh.", req.ID))
		case ExecutableContract:
			command, _ := configString(req.Check.Config, "command")
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Install %q on PATH, approve executable probes, then refresh.", command))
			if !opts.AllowExecutables {
				item.Status, item.Detail, item.Action = "approval", "executable probe requires approval", "approve-executable"
			}
		case MCPContract:
			command, _ := configString(req.Check.Config, "command")
			item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Make MCP command %q available with the declared tools, approve MCP checks, then refresh.", command))
			if !opts.AllowMCP {
				item.Status, item.Detail, item.Action = "approval", "MCP check requires approval", "approve-mcp"
			}
		default:
			if err == nil {
				item.Prompt, item.PromptSource = selectPrompt(req.Prompt, fmt.Sprintf("Satisfy checker contract %s, then refresh.", contract.String()))
			}
		}
		if item.Status != "approval" {
			if err == nil {
				observation, err = checker.Check(ctx, req, opts)
			}
			if err != nil {
				item.Detail = err.Error()
			} else {
				item.Status, item.Action, item.Prompt, item.PromptSource = "ready", "none", "No action required.", "engine"
				item.Detail = observation.Implementation
				if item.Detail == "" {
					item.Detail = observation.ContractDigest
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
