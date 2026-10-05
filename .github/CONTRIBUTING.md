# Contributing to SOP

Thank you for your interest in contributing to Joltrin (formerly SOP, Scalable Objects Persistence)! We welcome contributions from the community.

## Getting Started

1.  **Read the Documentation**:
    *   **[README.md](../README.md)**: High-level overview and quick start.
    *   **[ARCHITECTURE.md](../docs/ARCHITECTURE.md)**: Understanding the codebase structure, specifically the Public vs. Internal package split.

2.  **Explore the Code**:
    *   SOP V2 is written in Go. We prioritize simplicity and readability.
    *   Check out the unit tests to understand the interfaces.

## Development Workflow

1.  **Communication**:
    *   Before starting a major feature, please open an issue or discussion to coordinate with the authors. This helps avoid duplication of effort.

2.  **Branching Strategy**:
    *   Fork the repository.
    *   Create a feature branch for your changes.
    *   Submit a Pull Request (PR) to the `master` branch.

3.  **Testing**:
    *   **Unit Tests**: Run `run_all_tests.sh` bash shell script for quick feedback.
    *   **Stress & Integration Tests**: Critical for verifying backend interactions.
        *   For `infs` (In File System), you must run:
            ```bash
            go test -v -tags=stress -count=1 ./infs/stresstests/...
            ```
        *   For `incfs` (Hybrid Cassandra & File System), you must run:
            ```bash
            export SOP_RUN_INCFS_IT=1
            go test -v -tags=integration -count=1 ./incfs/integrationtests/...
            ```
    *   **Race detector and benchmarks**: `go test -race ./verify/... ./tools/... ./cmd/...` and `go test -run xxx -bench . -benchmem ./verify ./tools/mcpserver ./tools/blocklog`. Compare benchmark numbers before and after a change that touches a hot path, and say so in the pull request.
    *   **Site tests**: `npm ci`, then `scripts/build-site.sh` and `npx playwright test --project=chromium`. The site is the files under `demo/`, `demo-agents/`, `docs/` and `sop-arena/`.
    *   **The homepage demo video** is built from the real `sop-mcp-server` by `scripts/promo/build.sh`, which needs Go, Node with Playwright's Chromium, and ffmpeg. Rebuild it when the demo command's output changes.
    *   Ensure all tests pass before submitting your PR.

## Code Structure & Guidelines

*   **Public vs. Internal**:
    *   **Primary Public Packages**: `infs` (In File System), `jsondb` (JSON Document Store), `database` (Helpers) & `tools/httpserver` Data Manager RESTful web service & SPA WebUI. These represent the core user-facing APIs & Tools.
    *   **Secondary**: `incfs` (In Cassandra & File System) is now considered secondary.
    *   **Internal Packages**: Packages under `internal/` (e.g., `internal/inredck`) contain implementation details that should not be exposed.
    *   See [ARCHITECTURE.md](../docs/ARCHITECTURE.md) for more details on the architecture.

*   **Style**: Follow standard Go idioms and formatting (`gofmt`).

## Gemini review gate

Every pull request to `master` needs a passing `Gemini Review` status on its latest commit.

- The `Gemini PR Review` workflow reviews the diff when a PR is opened, updated, or marked ready, and posts the findings as a PR comment. The comment records the reviewed commit and the verdict.
- The status is per commit. Pushing a new commit clears it, so an earlier pass never covers newer code.
- Any of these leave the status failing and the PR blocked: actionable findings, no `GEMINI_API_KEY`, a Gemini error or timeout, a response without a `VERDICT: PASS` or `VERDICT: FAIL` final line, an empty diff, or a diff larger than the review limit.
- Draft PRs get no status and cannot merge until they are marked ready.
- To get a fresh review after pushing a fix, an owner, member, or collaborator comments `/gemini review`. Outside contributors ask a maintainer to do this.
- Dependabot PRs need `GEMINI_API_KEY` added under Dependabot secrets as well as Actions secrets, or the status fails.

### Disputing a finding

If a finding is wrong or not applicable, do not merge around it. Comment `/gemini dispute` on the PR with:

1. The exact finding, quoted.
2. The file, symbol, and line or behavior it refers to.
3. Whether the claim is Observed, Inferred, or unsupported.
4. Repository evidence showing why it is wrong or does not apply.
5. Links to official docs, release notes, standards, or advisories.
6. Reachability and exploitability analysis.
7. Tests, reproduction steps, or code references.
8. Any compensating control.
9. The resolution you are asking for.

Then comment `/gemini review`. Disputes from owners, members, and collaborators are passed to Gemini as claims to check against the diff, not as facts. The finding is resolved only if the new review on the current commit returns `VERDICT: PASS`. The only way around the gate is the skip above, and only when Gemini did not review. If Gemini keeps a finding after a well-supported dispute, fix the code or change the review prompt in a PR.

### When Gemini is unavailable

If Gemini cannot review a commit because of quota, an outage, a timeout, or a missing key, an owner, member, or collaborator can comment `/gemini skip <reason>` on the PR. The `Gemini Review Skip` workflow then sets the status to success for that commit and records who skipped it and why.

- A skip is refused when Gemini did review the commit and found issues. Fix them or dispute them instead.
- It covers one commit. A new push clears it, so each push needs a fresh review or a new skip.
- A reason is required and is shown in the status.

### Branch protection

The check must be listed as required, which a workflow cannot do for itself. A repository admin runs this once:

```
gh api -X POST repos/SharedCode/joltrin/branches/master/protection/required_status_checks/contexts \
  --input - <<< '["Gemini Review"]'
```

Until then the status is informational only. Auto-merge, including the Dependabot workflow, only completes after every required check passes, which then includes this one.

## Questions?

Don't be shy to ask questions in the [Discussions](https://github.com/SharedCode/joltrin/discussions) tab. We are happy to help!
