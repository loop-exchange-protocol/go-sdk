# LXP Official Reference Implementation (Go)

**English** | [中文主版本](README.md)

[![CI](https://github.com/loop-exchange-protocol/lxp/actions/workflows/ci.yml/badge.svg)](https://github.com/loop-exchange-protocol/lxp/actions/workflows/ci.yml)

This repository is the official reference implementation of the Loop Exchange Protocol. It is currently written in Go and includes a reusable library (protocol types, Artifact/CAS support, Provider APIs, and Requirements), the Engine, and the `lxp` CLI. Concrete Providers live in separate repositories and are injected explicitly by applications.

```go
e := engine.New(stateRoot, providers...)
```

Provider authors primarily implement `pkg/provider.Provider`. Every Provider declares both a globally unique, language-independent contract coordinate and an exact implementation-package coordinate; the EngineConfig binding and Helper `initialize` handshake must match the implementation exactly. The official Production MVP binds only `loop.exchange:git:v1`. `Validate` must check a Component without writing its content, while `Apply` must be idempotent and retryable. Authors may additionally implement `Tracker` for native change selection and `Adopter` for taking ownership of existing roots; `NestedDiscoverer` returns Provider-native direct child roots, and `BoundaryTracker` synchronizes parent metadata such as a gitlink. Engine derives nested topology from lexical paths, routes ordinary operations to the deepest root, imports parent to child, and exports child to parent; Artifacts contain no mount-capability DSL. The normative specification, schemas, and canonical examples live in [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol).

The generic Engine API supports `reference`, `embedded`, and `mirrored` as declared by each Provider. A Component supplies its actual distribution, locator, revision, and payload to the corresponding contract for validation and application. Mirrored reference and embedded revisions must match; the corresponding contract defines safe locators, selected state, and fallback behavior. Import records the `importing` state and pins local extension resolution before calling `Apply`, then persists progress after each successful Component. A failure does not roll back or clean completed content: retrying the same Artifact with the same implementations continues reconciliation, while retrying a `ready` Session is a no-op.

## Install the CLI

The CLI is an independent Go module under `cmd/lxp`, so release tags use the `cmd/lxp/vX.Y.Z` prefix:

```bash
go install github.com/loop-exchange-protocol/lxp/cmd/lxp@latest
lxp help
```

The first public-alpha version is `v0.1.0-alpha.1`; with no stable release, `@latest` selects the highest pre-release. Published modules contain no `replace` directives. The active local layout has three repositories: the `loop-exchange-protocol` specification source, this `lxp` reference implementation, and `provider-git`; the parent `go.work` composes the modules from the two Go implementation repositories.

The CLI gives external operations a 15-minute deadline by default; override it with a positive Go duration such as `LXP_TIMEOUT=30m lxp import ...`. The Git Provider also disables interactive credential prompts, so authentication must already be available from a non-interactive credential helper or SSH agent. Session storage and discovery use physical paths with symlinked prefixes resolved, preventing macOS `/var` and `/private/var` from being treated as different Workdirs.

## Verification

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` is a nested module and the official Production MVP composition root. It combines this repository's library and Engine with `provider-git` only. Its public commands are `init/add/status/export/import/inspect/requirements`; `lxp export --distribution` supports reference/embedded/mirrored `.lxpz` (default: embedded), and Import reads the Artifact declaration automatically.

EngineConfig `source: helper` executes local argv directly. `source: repository` pulls a platform-specific executable by manifest digest only from an OCI repository with local explicit `auto_install` and a namespace allowlist. A Helper performs an exact NDJSON handshake over stdin/stdout and exits at command end; it uses neither a Go plugin ABI nor a shell. Artifacts declare contracts only and cannot choose a repository, command, digest, or execution authority. The default CLI keeps its builtin Git Provider, so the ordinary quickstart does not require network access. The `provider-git` repository also builds `lxp-provider-git` as the standard Helper example.

The Harness in `provider-git`, as part of the real three-repository layout, directly verifies online reference Import, offline reference failure with retryable state, and offline mirrored fallback through this public CLI.

CLI integration tests use parent, child, and grandchild remotes to cover automatic recursive Git-submodule registration, all three distributions, offline child restore, consistent parent native submodule config/recursive status after Import, a child commit, and staged parent-gitlink selection.

The protocol is `v1alpha1`, while Go modules use `v0.x` alpha tags. Neither carries a compatibility promise, and only trusted Artifacts are supported.
