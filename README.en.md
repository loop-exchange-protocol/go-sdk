# LXP Go SDK

**English** | [中文主版本](README.md)

This repository contains the Go SDK for the Loop Exchange Protocol: protocol types, Artifact/CAS support, Provider APIs, Engine, and Requirements. Concrete Providers are not part of the SDK; applications inject them explicitly.

```go
e := engine.New(stateRoot, providers...)
```

Provider authors implement `pkg/provider.Provider`, optionally `Tracker` for native change selection and `Adopter` for taking ownership of existing roots. The normative specification, schemas, and canonical examples live in [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol).

## Verification

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` is a nested module and the official Production MVP composition root. It combines this SDK with `go-provider-git` only. Its public commands are `init/add/status/export/import/inspect/requirements`, and it accepts embedded `.lxpz` Artifacts only. Local `replace` directives support adjacent-repository development and must become released versions for distribution.

The current `v1alpha1` release makes no compatibility promise and supports trusted Artifacts only.
