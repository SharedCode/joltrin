# Changelog

## Unreleased

## v5.9.0

- `sop-mcp-server` can remember what blocked in earlier runs. Set `SOP_LESSONS_DIR` and it records each block once per run, tells the next agent in the MCP server instructions when it connects, and keeps a `LESSONS.md` that can be added to a `CLAUDE.md` or `AGENTS.md`. It is off by default and advice only: the barrier checks every call as before. Lessons come from the server, expire after 30 days, and stop applying when the runbook changes.
- A blocked `execute_step` result that is replayed because an `idempotency_key` was reused now carries a `hint` saying it is the original answer and to retry with a new key after running the missing steps. The decision is unchanged, and the field is absent otherwise.
- The homepage agent demo and the `/agents/` demo show the structured block result (`blocked_by`, `missing_state`, `established_by_steps`). New tests pin those field names for MCP clients and for code that imports `verify`.
- Renamed the example tool `aws.scale_up` to `aws.scale_out` in `examples/agent_team`.
- Fixed node locks staying held after a commit failed because the caller's context was canceled. Cleanup after a failed commit now runs without the caller's cancellation, so the locks are released at once instead of expiring after the commit maxTime (15 minutes by default), which had made other transactions on the same nodes give up.
- `docs/AGENT_BARRIER_TESTS.md` records runs on whether agents act on block feedback and whether remembering earlier blocks helps, with Claude, Codex, and Gemini, and what the runs do not show.
- `sop-server` can create its root admin user on first start from the `JOLTRIN_ROOT_PASSWORD` environment variable (at least 16 characters). It only runs when no config file exists, so it can never replace an existing root password, and the password is hashed before it is written. The first-run wizard accepts only loopback callers, which a hosted container on a distroless image cannot satisfy, so the Azure deployment could never get past "server is not configured yet". The Azure Bicep and deploy workflow now pass the secret from Key Vault when the `JOLTRIN_ROOT_PASSWORD` repository secret is set.
- `execute_step` and `validate_step` now refuse a call with no `trace_id`. Before, every caller that left it out shared one empty-id trace, so a run that forgot it could use the steps another run had finished, such as a validated backup. The schema already marked it required, and A2A already refused it.

## v5.8.2

- The whole root module now builds and vets from outside the repo with no go.work. The root go.mod requires the published `ai`, `jsondb`, `search` and `adapters/nats` modules (all v0.1.0), so `tools/httpserver`, the remaining examples and the `confighub` tests resolve, and `go mod tidy` completes. `github.com/sharedcode/joltrin/ai` is now installable on its own.
- Added docs/AGENT_BARRIER_TESTS.md, a recorded run of the MCP barrier with a real agent, including what it does not show.

## v5.8.1

- `go get github.com/sharedcode/joltrin/v5` followed by importing `v5/database` or `v5/governance` now works from outside the repo. These packages import the Redis adapter, `incfs` and `infs`, which had no published versions and were not declared in the root go.mod, so the build failed with "no required module provides package". Those modules are now tagged (`adapters/redis`, `adapters/cassandra`, `infs`, `incfs` at v0.1.0) and required by the root module.
- Fixed the native build image used by the release workflow. It did not copy `adapters/nats/go.mod`, so the v5.8.0 release build failed in `go mod download`.
- Packages that still need the unpublished `ai` and `jsondb` modules (`tools/httpserver`, some examples) only build inside the repo workspace.

## v5.8.0

### Verify
- Moved the verification barrier from `github.com/sharedcode/joltrin/ai/verify` to `github.com/sharedcode/joltrin/v5/verify`. The `ai` module depended on modules that have no published versions, so `go get` on it failed from outside this repo. `verify` only uses the standard library, so `go get github.com/sharedcode/joltrin/v5/verify` now works on its own. Update imports from `joltrin/ai/verify` to `joltrin/v5/verify`.

### Billing
- `GET /api/billing/plan` now returns a `checkout` block that says whether Pro and Enterprise can be bought and which environment variables are missing. It lists variable names only, never values, so an operator can see why checkout is off without reading logs. The same check is available as `governance.AssessBilling`.
- Live mode (a Stripe secret key is set) now refuses every webhook until `STRIPE_WEBHOOK_SECRET` is configured. Before, a live server with no signing secret skipped signature verification and would accept a forged `checkout.session.completed`. The public webhook route also returns 503 when no signing secret exists at all.
- `/api/billing/checkout/simulate` returns 404 unless the server is in simulation mode. With live keys it was an unauthenticated way to grant a paid tier.
- Pro checkout is refused in live mode until a real Pro price ID, a webhook secret, and absolute success and cancel URLs are all present, instead of failing at Stripe with a placeholder price.
- Enterprise stays contact-sales unless a real `STRIPE_ENTERPRISE_PRICE_ID` is configured. The checkout endpoint answers 409 for it.
- Added `JOLTRIN_PUBLIC_URL` to build the absolute Stripe return URLs. The fallback return URLs now use `joltrinhq.com` instead of `joltrin.com`.
- Tests added for missing configuration, simulation mode, invalid and missing webhook signatures, duplicate events (including after a restart), and the payment failed, recovered, canceled, and deleted subscription lifecycle.

### Azure
- The Bicep stack takes `stripeProPriceId`, `stripeEnterprisePriceId`, and `publicBaseUrl` and passes them to the Container App as plain environment variables. Secrets still go through Key Vault references. All Stripe values default to empty, and an empty secret is stored as `unset` and treated as empty by the server, so an unconfigured deployment stays in simulation mode.
- `deploy-azure.yml` passes the new values from repository variables. Set the two Stripe secrets in GitHub rather than directly in Key Vault, because each deploy re-applies them from the workflow inputs.

### Docs and README
- The README is now a single screen of positioning, one quickstart, the strongest verified numbers, install commands, and links. The longer material moved to `docs/`: `BENCHMARKS.md`, `LIVE_DEMOS.md`, `AGENT_PROTOCOLS.md`, `WHY_JOLTRIN.md`, `WHO_IS_IT_FOR.md`, `INVESTORS.md`, `ROADMAP.md`, `EXAMPLES.md`, and `PACKAGES.md`.
- `docs/MONETIZATION_AND_TIERS.md` documents the Stripe environment variables and the new readiness block. `infra/azure/README.md` documents which Stripe values are secret.

### Website
- Shortened the homepage: a plain hero with one primary action, the three live experiences right below it, and a single open-core pricing section. Removed the investor-style business model, why-now, personas, value stack, and enterprise essay sections.
- Pro is now "Request Pro" with an email fallback on the static site. Removed the Apple Pay, Google Pay, instant provisioning, registry access, and annual price claims that the site could not back up.
- Canonical, Open Graph, and Twitter URLs, and `demo/CNAME`, now use `joltrinhq.com`.
- Hid the engine status pill below very wide screens so the header no longer wraps, and removed the stale "v1.0" badge.
- Added `tests/homepage.spec.ts` covering the hero, the live experience links, pricing wording, the Pro request fallback, metadata, and horizontal overflow.

### Maintenance since v5.7.0
- Fixed the deep sleep scheduler goroutine leak and a discarded `tx.Commit` error in the sleep cycle (#407).
- The A2A agent card now sets its protocol version (#408).
- Added a CI check that `go get` with no version resolves to the latest tag (#405) and fixed the Windows `fs` exclusion after the `/v5` rename (#404).
- Added the Cmd+K command palette to all three demo sites (#406).
- Bumped `jackson-databind` to 2.21.7 and gated merges on a per-commit Gemini Review status (#409).
- Fixed the codecov badge and stale SOP-era names in the README (#410).

### Versioning
- All bindings (Python, Rust, Java, C#) and the server `VERSION` are aligned at 5.8.0 through `scripts/update_version.sh`. They had stayed at 5.6.0 through the v5.7.0 tag.

## v5.7.0

### Breaking: Module Path
- The root module is now `github.com/sharedcode/joltrin/v5`. Every tagged release from v2.0.0 through v5.6.0 was not `go get`-able by any external consumer: Go requires a `/vN` path suffix on any v2+ tag when a go.mod is present, and root's go.mod never had one. Confirmed by trying to depend on v5.6.0 from a fresh module outside this repo, `go mod tidy` rejects it outright. If you were consuming joltrin via a git submodule or a local replace directive instead of `go get`, update your import paths to add `/v5` after `joltrin`. The 8 nested modules (`ai`, `incfs`, `infs`, `jsondb`, `search`, `adapters/cassandra`, `adapters/nats`, `adapters/redis`) keep their existing paths, only references to the root module changed. Three internal packages (`inredck`, `logsafe`, `netguard`) that nested modules imported across the old shared-prefix path moved out of `internal/` as a result, since Go's internal-package visibility is scoped by the literal import path, not by directory tree.

### AI Features
- Added `ai/ledger`: durable, crash-recoverable execution history for agent runs built on the existing `sop.BlobStore` abstraction. Run creation and completion, append-only events, checkpoints, resume from the latest valid checkpoint after an interruption, and deterministic replay.
- Added `ai/replay`: records `ai/verify` barrier decisions into a run's ledger and replays a run's recorded decision history against a candidate workflow, flagging any step that would now be newly blocked or newly allowed before a policy change ships.
- `adapters/nats`: optional NATS bridge publishing `ai/verify` barrier decisions as a side effect, without altering the barrier's own decision.

### Reliability
- Bounded the previously unlimited node-lock-acquisition retry loop in `phase1Commit`, the core two-phase-commit transaction path. A transaction starved by concurrent contention now fails fast with a specific error instead of spinning to the full 15-minute transaction timeout. Closes a long-standing intermittent CI failure in `Test_ConcurrentCommitsComplexUpdateConflicts`.
- Removed a redundant Go module cache step in CI that collided with `actions/setup-go`'s own caching and intermittently broke the integration test job.

### Security
- Stopped logging raw tool-execution errors that could carry an API key from a provider call. The tool name is sanitized before it reaches a log line, and quoted tool names in error messages use `%q` instead of manually quoted strings a single quote in the input could break out of. Closes 5 CodeQL alerts: clear-text logging, log injection, unsafe quoting.

### Demo Sites
- Added a mobile navigation menu to all three demo experiences (technical demo, agent verification barrier, arena); the desktop nav had no small-screen fallback.
- Fixed the Arena topology diagram overlapping on narrow viewports: it now scrolls horizontally at its natural size instead of compressing.
- Replaced the header icon with the actual Joltrin logo mark across all three sites.
- Fixed missing Open Graph share images and a footer link pointing at a domain that does not yet resolve.
- Fixed a missing GitHub icon on the technical demo and agent barrier pages: the latest `lucide` icon bundle dropped that icon entirely.

### Test Coverage
- Added real coverage for `RunWorkflow`'s step dispatch and validation, `IngestAgent`'s config and dependency resolution, `PrepareDoctorDataset`, and the swarm store's `GetResults`.

### Cleanup
- Removed 25 dead functions and handlers across `ai/agent` and related packages, all superseded or never wired up.

### Bug Fixes
- Fixed a bug in export header injection and transaction debug output.
- Fixed the quickstart and nocov Dockerfiles to copy `adapters/nats/go.mod`.

## v5.6.0

### Security
- Closed the remaining open CodeQL alert classes across the codebase: shell injection in sop-daemon, unsafe quoting in agent errors, log injection (ai module, sop-daemon, tools/httpserver, fs, database), clear-text logging of a transient API key, path injection in environment switch/delete/create, two open-redirect variants (including a backslash-encoded bypass), SSRF in space ingest-from-URL, uncontrolled/overflowing allocation sizes in node slot length and KB digest limits, an incorrect integer conversion in uint parsing, and DOM XSS/sanitization gaps in the web UI.
- Extracted the SSRF and redirect-safety guards that were duplicated across `tools/httpserver` into a single `internal/netguard` package, with the exploitability investigation and citations kept in the package doc comment.
- Hardened `os.RemoveAll` call sites (both the storage engine and `fs.defaultFileIO`) against catastrophic paths, and closed a real gap in that guard on Windows.
- Bumped `golang.org/x/crypto` and `cel-go` to close 3 known dependency advisories.

### Bug Fixes
- `resolveTemplate` (ai/agent script engine) returned `nil` instead of the correct value whenever a template field lookup fell back to a "value"-wrapped map - a shadowed variable meant the fallback's success never reached the check that decided whether to return it.
- `StoreCursor.Next` silently swallowed real store errors while skipping rows that didn't match a filter, returning "no more items" instead of the actual error - same shadowing root cause.
- Removed `BaselineReActEngine`, a superseded ReAct loop implementation that was never constructed anywhere in the codebase; its dead fenced-code-block branch would have silently dropped tool calls if it had ever run.
- Removed a stale cursor-restore leftover in `VectorizeCategories` copy-pasted from a sibling function that didn't apply here, and a duplicate-user-message append in the Anthropic client that never reached the actual API request but would have broken it had it ever been "fixed" instead of understood.
- Fixed a flaky billing checkout test and closed a gap where a stale `*FeatureGate` reference could be picked up across test resets.

### AI Features
- Retrieved knowledge-base passages injected into agent context now carry a citable `[source: X]` tag, and the model is instructed to cite it inline in its answer.
- The HTTP chat SSE stream now emits a separate `citations` event with the distinct `[source: X]` labels a model's answer cited, in first-appearance order, so a UI can render them as structured references instead of having to parse the prose.
- `execute_step`/`validate_step` now return structured blocked/malformed results instead of opaque failures, and `execute_step` accepts an optional idempotency key so retries are safe.

### CI & Test Coverage
- The `ai` module (a separate `go.mod` under the `go.work` workspace) and 5 other previously-uncovered workspace modules (`search`, `jsondb`, `incfs`, `adapters/cassandra`, `adapters/redis`) now build, vet, and test in CI - closing a real gap where `./...` from the root module silently never reached them.
- Widened the root module's CI unit-test step from a hardcoded package list to the same exclusion-based pattern already used for build/vet.
- Cleared a staticcheck sweep across the codebase: typed context keys that were previously untyped strings, dead code removal in `tools/httpserver`, `bindings/main` tests, the core storage engine, and several `ai/agent` files, plus missing test assertions on results that were computed but never checked.

## v5.5.0

### DevSecOps Pipeline
- **CodeQL SAST** for Go and JS/TS, running on push/PR to master plus a weekly schedule.
- **Secret, dependency, and container config scanning**: Gitleaks (via the free CLI, not the paid GitHub Action), Trivy for filesystem/config/image scans, and govulncheck, all gated to block on critical/high findings and uploaded to GitHub code scanning as SARIF.
- **Non-root Docker containers**: the nocov and bindings build images now run as non-root.
- **Claude Code PR review and remediation workflows**, plus local `make security-scan` / `make lint-sec` / `make sca` / `make secrets-scan` / `make iac-scan` targets mirroring what CI checks.
- Documented the pipeline's security controls in `SECURITY.md`.

### Azure Container Apps Deployment
- Added a Bicep stack (`infra/azure/`) for `tools/httpserver`: Container Registry (admin disabled, pull via managed identity only), Key Vault holding the Stripe keys, Log Analytics with 30-day retention, a Container Apps environment, and the app itself, pinned to a single replica to keep this least-cost and because the storage engine has no documented multi-process write-safety guarantee.
- Added a monthly cost budget with 50/75/90/100% alert thresholds and Azure Monitor metric alerts on CPU/memory/restart-loop.
- Added `.github/workflows/deploy-azure.yml`: OIDC-authenticated (no stored client secret), Trivy-scanned before push, deploys only on merge to master.
- Added `make deploy-check` / `make lint-infra`.

### Billing
- **Fixed a real bug**: subscriptions, webhook idempotency keys, the customer-tenant map, and enterprise inquiries lived only in in-process memory, so a server restart or redeploy silently wiped every paying customer's subscription. All four are now persisted through joltrin's own embedded B-Tree engine instead of adding a new external dependency.
- Wired the previously unused tier-aware rate limiter into the checkout and portal endpoints, and added retry-with-backoff on Stripe 429/5xx responses.
- The static GitHub Pages demo no longer shows a fake "checkout succeeded" or "inquiry received" screen when the billing backend isn't actually reachable; it says so and points to a real mailto fallback instead.

### Testing
- Raised coverage on the packages Codecov measures (`btree`, `inmemory`, `fs`, `common`) from 85.3% to 87.7%.
- Fixed a stale Codecov project slug (`SharedCode/sop`, left over from the joltrin rebrand) that was pointing the badge and coverage uploads at the wrong project.

## v5.4.0

### Website & Docs
- **Copy pass on the marketing site** (`demo/index.html`): rewrote the hero, verification barrier intro, business-value section, "why now" section, business model, pricing, enterprise value, and enterprise contact form for a more direct, conversational tone. Removed all em dashes, leftover buzzwords ("paradigm", "mission-critical"), and an unverifiable "1 business day" response-time claim from both the frontend copy and the `/api/billing/enterprise-contact` success message.
- **README**: shrunk the logo from 480px to 120px so it reads as a mark next to the title instead of a full hero graphic. Added a "Cutting a Release" section documenting the actual `scripts/update_version.sh` / `build_release.sh` / `verify_release.sh` flow, and fixed a stale `go get ...@v0.1.0` example.

## v5.3.8

### Highlights
- **Full ecosystem version alignment**: Synchronized all core Go modules and language bindings (Python `sop4py`, C# `Sop`, Java, Rust) to release version v5.3.8.
- **Zero known vulnerabilities**: Pinned Go toolchain 1.26.8 across all modules and Docker containers, resolving 7 standard library CVEs and verifying 0 called vulnerabilities with `govulncheck`.
- **Deterministic container and CI test validation**: Resolved direct I/O failover simulation in root/container environments and restored full race-detector test coverage in CI.

### Reliability
- **Container test runner hardening**: Updated `Test_EC_Failover_Reinstate_FastForward_Short` in `infs/integrationtests` to utilize the DirectIO simulator and `fs.TriggerFailover` instead of filesystem chmod manipulations, eliminating permission bypasses in container and root execution contexts.
- **Race detector coverage**: Verified clean execution across core packages under Go race detection (`inmemory`, `btree`, `common`, `fs`, `cache`, `cel`, `encoding`, `database`, `jsondb`, `search`).

### Performance
- **Reproducible cache latency**: Verified 62.09 ns/op L1 MRU hit latency and 66.17 us/op under heavy mixed-cache workloads.
- **Vector store throughput**: Benchmarked end-to-end ACID transaction pipeline in `ai/vector` at 16.23 ms for batch 50-vector upsert, KNN query, and durable commit.
- **CI benchmark stabilization**: Calibrated benchmark iteration scaling in performance workflows to prevent runner timeouts.

### Developer Experience
- **Quickstart zero-dependency demo**: Validated `examples/quickstart` and published container image (`ghcr.io/sharedcode/joltrin-quickstart`) providing instant zero-configuration testing.
- **Unified versioning**: Enhanced `scripts/update_version.sh` for atomic multi-language version updates.

### AI/Data
- **Knowledge Base compilation**: Verified and generated complete base knowledge base in `ai/sop_base_knowledge.json` containing 1,257 knowledge items and 1,397 categories.
- **Query and Join execution**: Verified Adaptive Hash Join planning in AI agent runtime executing in 1.10 ms.

### Security
- **Go toolchain update**: Upgraded all modules and Dockerfiles to Go 1.26.8, addressing CVEs in `net/url`, `html/template`, `crypto/tls`, `encoding/asn1`, `net/http`, and `golang.org/x/net/idna`.
- **Dependency hardening**: Bumped `go-git` to v5.19.2 and `cel-go` to v0.29.0 to eliminate open Dependabot advisories.

### CI/CD
- **Gated delivery pipeline**: Decoupled production promotion into `.github/workflows/promote.yml` so pending manual approvals do not block or fail continuous delivery status.
- **Test execution fix**: Explicitly configured package test targets in `.github/workflows/go.yml` for comprehensive coverage reporting with race detector.

### Documentation
- **Badge cleanup**: Removed retired Go Report Card badge, added dynamically resolved Go version, license, and CI build status badges.
- **Architecture and Investor analysis**: Added detailed architectural positioning ("Why SOP?") and multi-horizon product roadmap.

### Breaking Changes
- None. Full backward compatibility maintained for all storage layouts, transaction protocols, and APIs.

### Upgrade Notes
- Go 1.26.8 or later is recommended when building from source. No data migration required for existing stores.

## v5.3.7
- **Descending iterators for the in-memory B-Tree**: `AllDesc()` and `RangeDesc(from, to)` walk keys newest-first; `RangeDesc` seeks straight to the high bound. Both covered by unit tests; quickstart shows a newest-first scan.
- **Fix: iteration errors no longer swallowed in the Data Manager item stream** (`tools/httpserver`): a shadowed `err` inside the paging loop meant `store.Next` failures never reached the error log or terminated the loop condition.
- **Fix: `tools/confighub` knowledge-base tests now skip on fresh clones** instead of failing when the gitignored local `tools/config.json` is absent.
- **Security: jackson-databind 2.21.4 to 2.21.5** in the Java binding (closes the case-insensitive deserialization bypass advisory, the last open Dependabot alert).
- **Lint cleanup**: removed a stray debug print in the item search path, replaced nil Contexts with `context.Background()` in the in-memory B-Tree wrapper, finished the typed context key for the vectorized-spaces map, normalized error strings, dropped dead nil checks.

## v5.3.6
- **Maintenance rebuild**: fixed v5.3.5/v5.3.6 release pipeline failures by building the whole `knowledge_compiler` package and bumping the build image to Go 1.26.4. No library code changes.

## v5.3.5
- **B-Tree node slot allocation and L1 cache handling optimized** for better throughput on hot paths.
- **L2 cache eviction policy reworked** for standalone mode -- smarter eviction under memory pressure.
- **`NewBtree`/`OpenBtree` safe multi-open**: the same named B-Tree can now be opened multiple times within a single transaction without data races. Stress tests updated to cover this.
- **Range-over-func iterators for the in-memory B-Tree**: `All()` and `Range(from, to)` return `iter.Seq2` so callers can `for k, v := range b3.Range(102, 104)`. Range seeks straight to the start key; both are covered by unit tests. Quickstart example and demo GIF updated to use them.
- **Security: cleared all 32 Dependabot alerts** (7 critical). Go: golang.org/x/crypto 0.52.0, golang.org/x/net 0.55.0, go-git/v5 5.19.1, go-billy/v5 5.9.0, cloudflare/circl 1.6.3. Java binding: jackson-databind 2.19.0 to 2.21.4 (fixes CVE PolymorphicTypeValidator bypass, array subtype allowlist bypass, InetSocketAddress SSRF, case-insensitive deserialization bypass).
- **Gated delivery pipeline** (`.github/workflows/deliver.yml`): every push to master runs build, tests, container packaging to GHCR (`sop-quickstart`), and a staging smoke test. Production promotion (image `:stable` tag plus GitHub Pages site deploy) sits behind a manual approval on the `production` environment.
- **Quickstart example** (`examples/quickstart`): zero-infrastructure in-memory B-Tree walkthrough (add, find, update, ordered scan). Packaged as a distroless container via `Dockerfile.quickstart`.
- **README demo GIF** recorded from the quickstart run; project site landing page added (`index.md`).
- **Root directory cleanup**: 26 documentation files moved to `docs/`, 13 shell scripts moved to `scripts/`. README and workflow references updated. Root now contains only source code, standard project files (LICENSE, CONTRIBUTING, etc.), and build configs.

## SOP V2 build 54 (Upcoming)
- **Gate 1 Advanced KB Routing**: Major enhancements to specialized focused routing for knowledge base queries.
    - **Root Category Navigation**: Query `omni:<kb>` to display all root categories with item counts and subcategory information.
        - Example: `omni:sop` shows all top-level categories in the SOP knowledge base
        - Provides directory-style exploration without needing to know category names upfront
        - Navigation hints included for deeper exploration (e.g., "Navigate: omni:sop:language")
        - **Pagination**: 20 categories per page with `:page:<number>` or `/page/<number>` syntax
            - `omni:sop:page:2` or `omni:sop/page/2` - View page 2 of root categories
            - `omni:sop:language:page:3` or `omni:sop/language/page/3` - View page 3 of subcategories
            - Supports both `:` and `/` as separators (matches user's query style)
            - Shows page info: "(Page 2 of 5, showing 21-40 of 87)"
            - Navigation hints: "Previous: omni:sop:page:1 | Next: omni:sop:page:3"
            - LLM filtering suggestion for large sets (>40 categories)
    - **`:llm <instruction>` Meta-Token**: Added support for explicit LLM post-processing instructions using `:llm` suffix (e.g., `omni:sop:operations:performance:llm summarize top 3`).
        - **Clean Query Separation**: The `:llm` meta-token is automatically stripped from the KB search query and treated as post-retrieval guidance.
        - **TaskContextClassification Fields**: Added `CleanQuery` and `LLMInstruction` fields to properly separate user intent from meta-commands.
        - **Three-Way Routing**: Intelligent decision-making based on result count and `:llm` presence:
            - `:llm` present → LLM processes with instruction (highest priority)
            - 1-5 matches → Direct display (no LLM)
            - 6+ matches → Automatic LLM summarization
    - **Flexible Hierarchy Support**: Full support for any-depth category paths (e.g., `omni:kb:cat1:subcat1.1:subsubcat1.1.1:...`).
    - **Subcategory Navigation**: When a category path has no direct items (and no `:llm` instruction), automatically returns child categories with item counts and descriptions as navigation hints.
    - **Enhanced Parsing**: New `stripLLMInstruction()` function ensures consistent meta-token extraction across all query patterns.
    - **Architecture Improvements**: 
        - `getSubcategories()` function for root and path-level category display
        - `buildKBEnrichedQuery()` now uses clean queries without meta-tokens for proper LLM context
        - `trySpecializedFocusedRouting()` handles root navigation, flexible hierarchy, and meta-token parsing
        - Comprehensive test coverage for all routing patterns and hierarchy depths
    - **Roadmap - Quoted Text Search**: Proposed support for combined category + text queries (e.g., `omni:sop:language bindings "java tutorial"`).
    - **Documentation Updates**: Updated `AI_COPILOT.md`, `AI_COPILOT_USAGE.md`, and `IMPLEMENTATION.md` with comprehensive routing guides including root category navigation.

## SOP V2 build 53 (Upcoming)
- **Schema Format Enhancement**: Introduced flat schema format for better LLM understanding and correlation with Store Relations.
    - **New Fields**: Added `FlatSchema`, `KeyFields`, and `ValueFields` to `StoreInfo` for improved schema representation.
    - **Flat Schema**: Uses flat format without prefixes (e.g., `{"key": "string", "first_name": "string", "age": "number"}`) that directly correlates with relation field names.
    - **Field Lists**: `KeyFields` and `ValueFields` arrays explicitly identify which fields belong to the Key vs Value, enabling LLMs to correctly prefix fields when generating SQL-like predicates.
    - **LLM Compatibility**: The flat format follows JSON Schema standards and eliminates cognitive load for LLMs when mapping relation field names to schema fields.
    - **Backward Compatibility**: The legacy prefixed `Schema` field (e.g., `{"Key": "string", "Value.first_name": "string"}`) is maintained for existing tools and will be deprecated in a future major version.
    - **Automatic Inference**: Schema inference automatically populates both formats during B-Tree item insertion.
    - **Updated Instructions**: AI Copilot prompts now reference the flat schema format and guide LLMs to use `key_fields` and `value_fields` for proper field prefix determination.
- **Refactor**: Refactored `IndexSpecification` and `StoreInfo` to separate sorting logic from index definitions.
    - **CEL Expression**: The `CELexpression` field in `StoreInfo` is now the primary source for custom sorting logic.
    - **IndexSpecification**: Now strictly defines the fields used for indexing and optimization.
    - **Backward Compatibility**: Existing data stores where `IndexSpecification` contained the CEL expression are automatically detected and supported. No manual migration is required.
    - **Benefit**: This separation allows for cleaner metadata and enables the "Dual-Mode" architecture where native Go comparers and dynamic CEL expressions can coexist and interoperate seamlessly.
- **AI Copilot & Chat**:
    - **Structured Execution Results**: Enhanced the Script Execution Engine to emit structured events for every execution step (`step_start`, `record`, `outputs`). This ensures consistent real-time feedback for long-running scripts.
    - **Step Visibility**: The Chat interface and Script Runner now clearly demarcate each step (e.g., "**Step 1:** select"), providing better observability into the agent's reasoning process.
    - **Execution Indexing**: Implemented robust step indexing propagation to track progress across complex control flows and streamed results.
    - **Grounded Join Repair**: Tightened `execute_script` join guidance so multi-store repair prefers researched `relation + target` paths over invented join mappings, and clarified recovery prompts now preserve validation category, suggested fix examples, and attempted mappings when escalation to clarification is required.
- **UI Enhancements (Data Manager)**:
    - **CEL Editor**: Added a dedicated modal for editing `StoreInfo.CELexpression` with auto-generation capabilities based on Index Specifications.
    - **Bulk Delete**: Implemented a selection column in the data grid allowing users to select and delete multiple items at once.
    - **Mobile Support**: Improved responsiveness for mobile devices, including a fullscreen mode for the AI Chat Assistant.
    - **UX Improvements**: Added "Escape" key support for closing modals and improved column resizing behavior.

## SOP V2 build 52
- **Added a Data Browser utility**: A web-based tool to inspect and navigate SOP B-Tree repositories.
    - **Store Listing**: View all B-Trees in a registry.
    - **Data Grid**: Browse key/value pairs with pagination.
    - **Navigation**: Seamlessly navigate between data pages (Next/Previous).
    - **Search**: Find specific records using complex key inputs.
    - **JSON Inspection**: View complex value structures as formatted JSON.
