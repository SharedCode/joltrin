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

- Every push and pull request runs `govulncheck` against the Go module graph in CI (`.github/workflows/go.yml`, `.github/workflows/security.yml`).
- GitHub's Dependabot security alerts are enabled on this repository for supported ecosystems (Go, npm, Maven, Cargo).
- Recent releases have included dependency bumps (`go-git`, `cel-go`, `golang.org/x/crypto`, `golang.org/x/net`, `jackson-databind`) specifically to close open advisories; see `CHANGELOG.md` for the history.

## Pipeline Security Controls

Every push and pull request against `master` runs:

- **SAST** - CodeQL (`.github/workflows/codeql.yml`), Go and JavaScript/TypeScript, `security-extended` query pack. Results appear in the repository's Security tab.
- **Secret scanning** - Gitleaks (`.github/workflows/security.yml`), config in `.gitleaks.toml`.
- **Dependency scanning (SCA)** - `govulncheck` plus Trivy filesystem scan. Critical/high severity findings fail the build; medium severity is reported but non-blocking.
- **Container and config scanning** - Trivy config scan against the three Dockerfiles, and a Trivy image scan of the built runtime image, with the same critical/high-blocks, medium-warns policy.

Run the same checks locally before pushing with `make security-scan` (or the individual `make lint-sec`, `make sca`, `make secrets-scan`, `make iac-scan` targets). `secrets-scan` and `iac-scan` require `gitleaks` and `trivy` on your PATH respectively.

### Claude Code review and remediation

- `.github/workflows/claude-review.yml` runs an automated security-focused review on every PR open/update and posts findings as a comment. It has read-only tool access and never modifies files.
- `.github/workflows/claude-remediation.yml` runs only when a maintainer comments `/remediate` or `/claude fix` on a PR. It diagnoses the failing checks and posts a suggested patch as a comment for a human to apply; it does not commit anything itself.
- Both require the `ANTHROPIC_API_KEY` repository secret. Without it, the review/remediation jobs fail but no other CI is affected.

## Scope

This policy covers the Go core engine, the Python, C#, Java, and Rust bindings, the WebAssembly browser demo, and the standalone `tools/httpserver` Data Manager. It does not cover third-party services you choose to run alongside Joltrin (Redis, cloud storage, etc.), which have their own security policies.
