# Security Policy

## Supported Versions

Security fixes are applied to the latest released version of the core Go module and its language bindings (Python `sop4py`, C# `Sop`). Older tagged releases do not receive backports.

## Reporting a Vulnerability

Please do not open a public GitHub issue for security vulnerabilities.

Instead, report it privately using [GitHub Security Advisories](https://github.com/SharedCode/joltrin/security/advisories/new) for this repository. If that is not available to you, open a [GitHub Discussion](https://github.com/SharedCode/joltrin/discussions) marked private or contact a maintainer directly.

When reporting, please include:

- A description of the vulnerability and its potential impact.
- Steps to reproduce, or a minimal proof of concept.
- The affected package (Go core, Python, C#, Java, Rust, WebAssembly demo, or `tools/httpserver`) and version.

We aim to acknowledge reports within a few business days. Timelines for a fix depend on severity and complexity.

## How Dependencies Are Monitored

- Every push and pull request runs `govulncheck` against the Go module graph in CI (`.github/workflows/go.yml`).
- GitHub's Dependabot security alerts are enabled on this repository for supported ecosystems (Go, npm, Maven, Cargo).
- Recent releases have included dependency bumps (`go-git`, `cel-go`, `golang.org/x/crypto`, `golang.org/x/net`, `jackson-databind`) specifically to close open advisories; see `CHANGELOG.md` for the history.

## Scope

This policy covers the Go core engine, the Python, C#, Java, and Rust bindings, the WebAssembly browser demo, and the standalone `tools/httpserver` Data Manager. It does not cover third-party services you choose to run alongside Joltrin (Redis, cloud storage, etc.), which have their own security policies.
