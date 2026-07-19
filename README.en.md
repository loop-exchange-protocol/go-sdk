# LXP Go SDK

**English** | [中文主版本](README.md)

This repository contains the Go SDK for the Loop Exchange Protocol: protocol types, Artifact/CAS support, Provider APIs, Engine, and Requirements. Concrete Providers are not part of the SDK; applications inject them explicitly.

```go
e := engine.New(stateRoot, providers...)
```

Provider authors implement `pkg/provider.Provider`, optionally `Tracker` for native change selection and `Adopter` for taking ownership of existing roots. `NestedDiscoverer` can perform contract-defined preparation and return Provider-native direct child roots, while `BoundaryTracker` can synchronize parent metadata such as a gitlink. Engine derives nested topology from lexical paths, routes ordinary operations to the deepest root, imports parent to child, and exports child to parent; Artifacts contain no mount-capability DSL. The normative specification, schemas, and canonical examples live in [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol).

The generic Engine API supports `reference`, `embedded`, and `mirrored` as declared by each Provider, and passes the actual distribution, locator, and revision into Plan. Mirrored reference and embedded revisions must match. The exact contract defines safe locators, selected state, and fallback behavior.

## Verification

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` is a nested module and the official Production MVP composition root. It combines this SDK with `go-provider-git` only. Its public commands are `init/add/status/export/import/inspect/requirements`; `lxp export --distribution` supports reference/embedded/mirrored `.lxpz` (default: embedded), and Import reads the Artifact declaration automatically. Local `replace` directives support adjacent-repository development and must become released versions for distribution.

The real four-repository Harness in `go-provider-git` directly verifies online reference Import, offline reference failure/cleanup, and offline mirrored fallback through this public CLI.

CLI integration tests use parent, child, and grandchild remotes to cover automatic recursive Git-submodule registration, all three distributions, offline child restore, a child commit, and staged parent-gitlink selection.

The current `v1alpha1` release makes no compatibility promise and supports trusted Artifacts only.
