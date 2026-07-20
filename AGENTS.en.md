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

The Engine obtains builtin Providers from composition-root injection or activates independent Helpers through local EngineConfig. Providers and Checkers use globally unique, language-independent `namespace:name:version` contracts and declare exact implementation-package coordinates. Unknown contracts, duplicate registrations, and mismatched bindings or handshakes fail without fallback. A repository Helper installs by digest only from an OCI repository with explicit `auto_install` and a namespace allowlist. Artifacts carry no Provider executable, installation policy, or local working path. `v1alpha1` makes no compatibility promise and supports trusted Artifacts only.

A Provider's Import contract consists only of non-writing `Validate` and idempotent, retryable `Apply`. The Engine validates every Component before recording the `importing` state, pinning extension resolution, and calling `Apply` parent to child; each successful Component is persisted immediately. A failure must not roll back or clean completed content. Retrying the same Artifact must use the pinned implementations to continue reconciliation, and retrying a `ready` Session must be a no-op.

The CLI passes a deadline-bearing Context to every Provider operation and Helper request, and external processes cannot wait for interactive credentials. Helper argv never passes through a shell, stdout carries NDJSON only, stderr diagnostics are bounded, and the process exits at command completion or cancellation. Local Session/Workdir comparisons use physical absolute paths with existing symlink prefixes resolved; `filepath.Abs` is not a realpath substitute.

Component roots are unique and may nest. Engine routes to the deepest root, supplies direct-child context to parent Providers, imports parent to child while rejecting symlink/non-empty collisions, and exports child to parent. Do not add a mount-capability matrix to the wire model.

The official CLI Production Profile injects builtin `loop.exchange:git:v1` by default while supporting a local `lxp-provider-git` Helper and explicitly authorized OCI Helpers. It accepts reference/embedded/mirrored `.lxpz` Artifacts and freezes the public surface to `init/add/status/export/import/inspect/requirements`. Export selects the form with `--distribution` and defaults to embedded; Import reads the Artifact declaration automatically. Do not add a central Registry, global search, or public install/activate command.
