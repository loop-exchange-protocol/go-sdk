# Security Policy

**English** | [中文](SECURITY.md)

## Project status

LXP is a public-alpha project. The `v1alpha1` format and Go MVP are under active development and have not received a comprehensive independent security audit. Do not treat the current implementation as a hardened sandbox or as suitable for processing hostile artifacts in a high-trust environment without additional isolation and review.

Security fixes are made on a best-effort basis for the latest revision. There are currently no separately supported release branches or guaranteed response times.

## Trusted-artifact boundary

LXP verifies archive structure, declared sizes, and SHA-256 payload digests. These checks establish byte integrity relative to a manifest; they do not authenticate a publisher, establish provenance, scan content for malware, or make imported content safe.

An artifact may contain source code, scripts, configuration, conversation content, and other data that becomes available in a consumer session. Treat artifacts, their referenced sources, and the providers that interpret them as trusted inputs. Inspect provenance and requirements, obtain artifacts through a trusted channel, and apply OS-level isolation appropriate to the content.

LXP does not implicitly execute prompt text or package-local hooks. Executable and MCP checks and provider activation are local code-execution boundaries and require explicit consumer import policy. Approval means the consumer trusts that specific operation in its local environment; digest verification alone is not approval.

Credentials and external services are consumer-managed requirements, not portable artifact state. Secret values must never appear in manifests, locks, payloads, command-line arguments, conversations, provenance, or logs. Report any path that permits secret disclosure, policy bypass, unsafe archive extraction, digest confusion, publisher-local restoration, or unintended command execution.

## Reporting a vulnerability

Please do not disclose a suspected vulnerability in a public issue.

Use the repository hosting service's private security-advisory or private maintainer-contact feature. If neither is available, contact a maintainer privately through the profile information associated with the repository before sharing exploit details. Include:

- the affected revision and environment;
- the vulnerability class and expected impact;
- minimal reproduction steps or a proof of concept;
- whether you believe credentials or published artifacts are exposed; and
- any suggested mitigation, if known.

Maintainers will acknowledge the report when practical, investigate it, coordinate a fix and disclosure, and credit reporters who wish to be named. Please allow reasonable time for remediation before public disclosure.

## Out of scope

The protocol does not promise deterministic model output, execution replay, supervision of external services, protection from malicious content after a user or platform authorizes its execution, or secrecy for values that a caller places into artifact content contrary to the specification. Reports showing a protocol or implementation bypass of the documented boundary remain in scope.
