# Agent Contract

**English** | [中文](AGENTS.md)

This repository is the official Go reference implementation of LXP and contains a reusable library, the Engine, and the `cmd/lxp` composition root. Do not add concrete Git, filesystem, OSS, or other Provider implementations to the core module.

## Required verification

```bash
go test -race ./...
go vet ./...
(cd cmd/lxp && go test -race ./... && go vet ./...)
git diff --check
```

The Engine obtains Providers through explicit injection. Providers and Checkers use globally unique, language-independent `namespace:name:version` contracts and declare exact implementation-package coordinates. Unknown contracts, duplicate registrations, or mismatched local bindings fail without fallback. Artifacts do not carry Provider executables or local working paths. `v1alpha1` makes no compatibility promise and supports trusted Artifacts only.

A Provider's Import contract consists only of non-writing `Validate` and idempotent, retryable `Apply`. The Engine validates every Component before recording the `importing` state, pinning extension resolution, and calling `Apply` parent to child; each successful Component is persisted immediately. A failure must not roll back or clean completed content. Retrying the same Artifact must use the pinned implementations to continue reconciliation, and retrying a `ready` Session must be a no-op.

The CLI passes a deadline-bearing Context to every Provider operation, and external processes cannot wait for interactive credentials. Local Session/Workdir comparisons use physical absolute paths with existing symlink prefixes resolved; `filepath.Abs` is not a realpath substitute.

Component roots are unique and may nest. Engine routes to the deepest root, supplies direct-child context to parent Providers, imports parent to child while rejecting symlink/non-empty collisions, and exports child to parent. Do not add a mount-capability matrix to the wire model.

The official CLI Production Profile injects `loop.exchange:git:v1` only, accepts reference/embedded/mirrored `.lxpz` Artifacts, and freezes the public surface to `init/add/status/export/import/inspect/requirements`. Export selects the form with `--distribution` and defaults to embedded; Import reads the Artifact declaration automatically.
