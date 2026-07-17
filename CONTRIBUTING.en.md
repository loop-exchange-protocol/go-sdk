# Contributing to LXP

**English** | [中文](CONTRIBUTING.md)

Thank you for helping improve the Loop Exchange Protocol (LXP). LXP is a public-alpha project: interfaces and schemas may change, and production adoption should be evaluated carefully.

## Before you start

- Read `README.en.md`, `docs/spec-v1alpha1.en.md`, and `AGENTS.en.md`.
- Search existing issues and proposals before opening a new one.
- For substantial protocol, schema, security, or compatibility changes, open an issue first so the design and migration impact can be discussed.
- Use the security reporting process in `SECURITY.en.md` for suspected vulnerabilities.

The specification and schemas define portable behavior. Engine-local session files under `.lxp/sessions` are implementation state and must not become a source of protocol requirements.

## Development workflow

1. Fork the repository and create a focused branch.
2. Make the smallest coherent change, including tests and documentation where behavior changes.
3. Preserve the protocol invariants in `AGENTS.md`. In particular, portable artifacts must not depend on publisher-local paths, original source state, secret material, implicit execution, or Agent-authored migration steps.
4. Run the required validation:

   ```bash
   go test ./...
   go vet ./...
   scripts/verify-local.sh
   ```

5. Use clear commits, preferably following Conventional Commits, such as `fix: reject unsafe archive paths` or `docs: clarify requirement policy`.
6. Open a pull request that explains the problem, the chosen approach, compatibility or security effects, and validation performed.

The destructive loop in `scripts/verify-local.sh` must continue to delete the publisher root and original Git source before consumer import. A restore that reaches publisher-local state is not portable.

## Pull request expectations

Contributions should be reviewable and scoped. Avoid unrelated formatting or dependency changes. Protocol changes should update the normative specification and schemas together and include tests that demonstrate portable behavior and relevant failure cases.

By submitting a contribution, you agree that it is licensed under the Apache License 2.0 according to the repository's `LICENSE` file. No Contributor License Agreement is currently required.

Participation in this project is governed by `CODE_OF_CONDUCT.en.md`.
