# LXP Go SDK

**English** | [中文主版本](README.md)

[![CI](https://github.com/loop-exchange-protocol/go-sdk/actions/workflows/ci.yml/badge.svg)](https://github.com/loop-exchange-protocol/go-sdk/actions/workflows/ci.yml)

This repository contains the Go SDK for the Loop Exchange Protocol: protocol types, Artifact/CAS support, Provider APIs, Engine, and Requirements. Concrete Providers are not part of the SDK; applications inject them explicitly.

```go
e := engine.New(stateRoot, providers...)
```

Provider authors implement `pkg/provider.Provider`, optionally `Tracker` for native change selection and `Adopter` for taking ownership of existing roots. `NestedDiscoverer` can perform contract-defined preparation and return Provider-native direct child roots, while `BoundaryTracker` can synchronize parent metadata such as a gitlink. Engine derives nested topology from lexical paths, routes ordinary operations to the deepest root, imports parent to child, and exports child to parent; Artifacts contain no mount-capability DSL. The normative specification, schemas, and canonical examples live in [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol).

The generic Engine API supports `reference`, `embedded`, and `mirrored` as declared by each Provider, and passes the actual distribution, locator, and revision into Plan. Mirrored reference and embedded revisions must match. The exact contract defines safe locators, selected state, and fallback behavior.

## Install the CLI

The CLI is an independent Go module under `cmd/lxp`, so release tags use the `cmd/lxp/vX.Y.Z` prefix:

```bash
go install github.com/loop-exchange-protocol/go-sdk/cmd/lxp@latest
lxp help
```

The first public-alpha version is `v0.1.0-alpha.1`; with no stable release, `@latest` selects the highest pre-release. Published modules contain no `replace` directives; the parent `go.work` composes all four repositories for local development.

The CLI gives external operations a 15-minute deadline by default; override it with a positive Go duration such as `LXP_TIMEOUT=30m lxp import ...`. The Git Provider also disables interactive credential prompts, so authentication must already be available from a non-interactive credential helper or SSH agent. Session storage and discovery use physical paths with symlinked prefixes resolved, preventing macOS `/var` and `/private/var` from being treated as different Workdirs.

## Verification

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` is a nested module and the official Production MVP composition root. It combines this SDK with `go-provider-git` only. Its public commands are `init/add/status/export/import/inspect/requirements`; `lxp export --distribution` supports reference/embedded/mirrored `.lxpz` (default: embedded), and Import reads the Artifact declaration automatically.

The real four-repository Harness in `go-provider-git` directly verifies online reference Import, offline reference failure/cleanup, and offline mirrored fallback through this public CLI.

CLI integration tests use parent, child, and grandchild remotes to cover automatic recursive Git-submodule registration, all three distributions, offline child restore, consistent parent native submodule config/recursive status after Import, a child commit, and staged parent-gitlink selection.

The protocol is `v1alpha1`, while Go modules use `v0.x` alpha tags. Neither carries a compatibility promise, and only trusted Artifacts are supported.
