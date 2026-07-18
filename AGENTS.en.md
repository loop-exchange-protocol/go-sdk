# Agent Contract

**English** | [中文](AGENTS.md)

This repository contains only the language-level Go SDK, Engine, and the `cmd/lxp` composition root. Do not add concrete Git, filesystem, OSS, or other Provider implementations to the core module.

## Required verification

```bash
go test -race ./...
go vet ./...
(cd cmd/lxp && go test -race ./... && go vet ./...)
git diff --check
```

The Engine obtains Providers through explicit injection. Unknown `provider + contract` pairs fail without fallback. Artifacts do not carry Provider executables or local materialization paths. `v1alpha1` makes no compatibility promise and supports trusted Artifacts only.

Component roots are unique and may nest. Engine routes to the deepest root, supplies direct-child context to parent Providers, imports parent to child while rejecting symlink/non-empty collisions, and exports child to parent. Do not add a mount-capability matrix to the wire model.

The official CLI Production Profile injects `git@v1` only, accepts reference/embedded/mirrored `.lxpz` Artifacts, and freezes the public surface to `init/add/status/export/import/inspect/requirements`. Export selects the form with `--distribution` and defaults to embedded; Import reads the Artifact declaration automatically. Provider Plan is internal Import preflight, not a public command.
