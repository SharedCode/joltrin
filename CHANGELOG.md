# Changelog

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
