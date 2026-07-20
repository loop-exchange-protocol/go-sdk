package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/loop-exchange-protocol/lxp/pkg/spec"
	"golang.org/x/term"
)

func RunChecklist(ctx context.Context, requirements []spec.Requirement, required map[string]bool, opts Options, in io.Reader, out io.Writer) (Options, error) {
	return RunChecklistWithRegistry(ctx, DefaultRegistry(), requirements, required, opts, in, out)
}

func RunChecklistWithRegistry(ctx context.Context, registry *Registry, requirements []spec.Requirement, required map[string]bool, opts Options, in io.Reader, out io.Writer) (Options, error) {
	reader := bufio.NewReader(in)
	for {
		items := CheckWithRegistry(ctx, registry, requirements, required, opts)
		renderChecklist(out, items)
		if AllRequiredReady(items) {
			fmt.Fprintln(out, "\nAll required items are ready.")
			return opts, nil
		}
		fmt.Fprint(out, "\n[r] refresh  [e] approve executables  [m] approve MCP  [b] bind env  [s] one-time secret  [q] quit\n> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return opts, err
		}
		command := strings.TrimSpace(line)
		switch command {
		case "", "r":
		case "e":
			opts.AllowExecutables = true
		case "m":
			opts.AllowMCP = true
		case "b":
			fmt.Fprint(out, "slot=ENV_NAME: ")
			value, err := reader.ReadString('\n')
			if err != nil {
				return opts, err
			}
			parts := strings.SplitN(strings.TrimSpace(value), "=", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				fmt.Fprintln(out, "Invalid binding.")
				continue
			}
			if opts.SecretEnv == nil {
				opts.SecretEnv = map[string]string{}
			}
			opts.SecretEnv[parts[0]] = parts[1]
		case "s":
			fmt.Fprint(out, "local credential source: ")
			slot, err := reader.ReadString('\n')
			if err != nil {
				return opts, err
			}
			slot = strings.TrimSpace(slot)
			if slot == "" {
				continue
			}
			fmt.Fprint(out, "secret value: ")
			value, err := readSecret(in, reader, out)
			if err != nil {
				return opts, err
			}
			sum := sha256.Sum256([]byte(slot))
			name := "LXP_TUI_SECRET_" + strings.ToUpper(hex.EncodeToString(sum[:6]))
			if err := os.Setenv(name, value); err != nil {
				return opts, err
			}
			if opts.SecretEnv == nil {
				opts.SecretEnv = map[string]string{}
			}
			opts.SecretEnv[slot] = name
		case "q":
			return opts, fmt.Errorf("requirements are not complete")
		default:
			fmt.Fprintln(out, "Unknown command.")
		}
	}
}

func renderChecklist(out io.Writer, items []CheckItem) {
	fmt.Fprint(out, "\x1b[2J\x1b[HLoop Exchange requirements\n\n")
	for _, item := range items {
		mark := "[ ]"
		if item.Status == "ready" {
			mark = "[x]"
		} else if item.Status == "optional" {
			mark = "[-]"
		}
		required := "optional"
		if item.Required {
			required = "required"
		}
		fmt.Fprintf(out, "%s %-24s %-20s %s\n", mark, item.ID, item.Kind, required)
		if item.Description != "" {
			fmt.Fprintf(out, "    %s\n", item.Description)
		}
		fmt.Fprintf(out, "    %s\n    -> %s\n", item.Detail, item.Prompt)
	}
}

func readSecret(in io.Reader, reader *bufio.Reader, out io.Writer) (string, error) {
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		data, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(out)
		return string(data), err
	}
	value, err := reader.ReadString('\n')
	return strings.TrimSpace(value), err
}
