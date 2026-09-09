# Joltrin Monetization & Architecture Guide

Joltrin follows an **Open-Core and Governance** architecture. The core database and agent memory engine are, and will always remain, **100% free and open-source under the permissive MIT License**. 

Commercial tiers are focused entirely on **enterprise governance, compliance, policy enforcement, multi-tenancy, and managed cloud infrastructure**.

---

## 1. Tier Breakdown & Capability Matrix

| Tier | Distribution | Licensing | Key Capabilities | Status |
| :--- | :--- | :--- | :--- | :--- |
| **Free / Open-Source Core** | Embedded Library & CLI | **MIT License** | • Copy-on-write B-Tree storage engine<br>• WAL + 2PC strict ACID transactions<br>• Reed-Solomon erasure coding<br>• Durable AI agent memory & checkpointed buffers<br>• In-memory 128-d cosine vector similarity<br>• Embedded MCP server (`cmd/sop-mcp-server`)<br>• Embedded A2A agent runtime (`cmd/sop-a2a-agent`)<br>• Local runbook verification barrier (`ai/verify`)<br>• Developer GitHub OIDC authentication | **Available Today** |
| **Pro Governance** | Team Add-on | Commercial | • Declarative Policy-as-Code compilation<br>• Tamper-evident SHA-256 audit lineage<br>• Signed cryptographic audit export<br>• Team-level workspaces and quota controls<br>• Priority MCP gateways and traffic shaping | **Foundation Implemented** (`governance/`) |
| **Enterprise Governance** | Self-Hosted Enterprise | Commercial | • Enterprise SSO: **Okta** & **Microsoft Entra ID**<br>• Multi-tenant RBAC & tenant isolation<br>• Real-time audit streaming (SIEM / Kafka)<br>• Custom safety invariant enforcement<br>• Fine-grained barrier verification rules<br>• Enterprise compliance & SLA guarantees | **Foundation Implemented** (`governance/`) |
| **Hosted Cloud** | Managed Cloud SaaS | Commercial | • Managed Joltrin instances (zero-ops)<br>• Cloud-hosted MCP hub & multi-agent routing<br>• Multi-region database replication<br>• Managed agent coordination network<br>• Automated off-site snapshots & backup verification | **Planned** |

---

## 2. Architectural Boundary: The `governance/` Package

To maintain clean separation between the open-source engine and commercial extensions, Joltrin introduces the [`governance`](../governance/) package:

### 1. Capability & Tier Abstractions (`governance/tier.go`)
- **`Tier`**: Represents the deployment tier (`TierCore`, `TierPro`, `TierEnterprise`, `TierHosted`).
- **`Capability`**: Granular feature flags (e.g., `CapEmbeddedBTree`, `CapDeveloperOIDC`, `CapEnterpriseSSO`, `CapMultiTenantRBAC`).
- **`FeatureGate`**: Evaluates active entitlements at runtime without polluting the core storage engine.

### 2. Tamper-Evident Audit Logging (`governance/audit.go`)
- **`AuditEvent`**: Canonical audit records capturing Actor, Action, Resource, Decision (`allow`, `deny`, `violation`), and Predecessor Hash.
- **SHA-256 Hash Chaining**: Every event cryptographically seals the previous event (`PrevHash`), forming a tamper-evident blockchain-style ledger.
- **`VerifyIntegrity()`**: Automated chain traversal that detects any unauthorized database or audit modifications.
- **`AuditSink` & `AuditStreamer`**: Extension interfaces for SIEM, syslog, and Kafka streaming.

### 3. Policy-as-Code & Barrier Extension (`governance/policy.go`)
- **`PolicyManifest`**: Declarative JSON/YAML specification defining operational runbook steps and safety rules.
- **`CompilePolicy()`**: Compiles declarative manifests into an executable formal verification graph ([`ai/verify.Workflow`](../ai/verify/verify.go)).
- **Safety Gating & Auditing**: Atomically checks preconditions and safety invariants before advancing execution traces, seamlessly recording decisions to the audit ledger.

### 4. Multi-Tenant Workspaces (`governance/workspace.go`)
- **`Tenant` & `Workspace`**: Isolates datasets, agent memories, and policies across organizations and teams.
- **Quota Management**: Enforces limits on workspaces, memory spaces, and concurrent agents based on tier entitlements.

### 5. Gateway Extensions (`governance/gateway.go`)
- **`GatewayAuthenticator`**: Authenticates MCP and A2A callers via API tokens or mutual TLS.
- **`TierRateLimiter`**: Token bucket rate limiting tailored per tier (e.g., Core: 10 req/s; Pro: 100 req/s; Enterprise: 1,000 req/s).
- **`PriorityWeight`**: Enables priority scheduling for mission-critical agent requests.

### 6. Extensible Identity & Enterprise SSO (`governance/identity.go`)
- **`IdentityProvider` Interface**: Standardizes OIDC/OAuth 2.0 authentication across heterogeneous identity systems without vendor lock-in.
- **Normalized Claim Mapping**: Translates standard OpenID claims (`sub`, `email`, `preferred_username`, `groups`, `roles`, `tid`) into internal user roles (`Admin`, `User`, `Guest`).
- **Three Supported Identity Providers**:
  - **GitHub OIDC** (`IdPTypeGitHub`): Open-source & developer tier (`TierCore`). Authenticates developers, CI/CD runners, and GitHub Actions workflows via OIDC token exchange.
  - **Okta** (`IdPTypeOkta`): Enterprise tier (`TierEnterprise`, gated by `CapEnterpriseSSO`). Supports org domain discovery, Okta user groups, and custom claim-to-role mappings.
  - **Microsoft Entra ID** (`IdPTypeEntraID`): Enterprise tier (`TierEnterprise`, gated by `CapEnterpriseSSO`). Supports Azure tenant isolation (`tid`), directory roles (`roles`), and Enterprise app registration.
- **`IdentityProviderRegistry`**: Thread-safe registry that enforces feature gate capabilities at runtime. Attempting to activate Okta or Microsoft Entra ID without an Enterprise tier returns `ErrEnterpriseSSONotLicensed`.
- **HTTP UI & REST Endpoints**: Implemented in [`tools/httpserver`](../tools/httpserver/auth_oidc.go) via `/api/auth/providers`, `/api/auth/oidc/authorize`, `/api/auth/oidc/callback`, and `/api/auth/oidc/token`.

---

## 3. Guarantees for Open-Source Users

1. **No Artificial Friction**: The open-source core will never impose artificial limits on database size, transactions, memory buffers, or local MCP/A2A concurrency.
2. **Permanent MIT Licensing**: All core database engines, file systems, vector indexes, and the local `ai/verify` verification barrier remain perpetually licensed under the MIT license.
3. **Clean Code Separation**: Governance and commercial hooks exist as clean interfaces and standalone packages rather than invasive licensing checks embedded inside B-tree nodes or WAL records.
