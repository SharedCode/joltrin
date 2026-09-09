# Joltrin Monetization, Governance & Commercial Architecture Guide

Joltrin follows an **Open-Core and Commercial Governance** architecture. The core database, transaction engine, and agent memory subsystem are, and will always remain, **100% free and open-source under the permissive MIT License**.

Commercial tiers are focused entirely on **enterprise governance, compliance, policy enforcement, multi-tenancy, and managed cloud infrastructure**.

---

## 1. Tier Breakdown & Capability Matrix

| Tier / Edition | Pricing | Distribution | Licensing | Key Capabilities | Implementation Status |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Free / Open-Source Core** | **$0** (Forever) | Embedded Library & CLI | **MIT License** | • Embedded copy-on-write B-Tree storage engine<br>• WAL + 2PC strict ACID transactions<br>• Reed-Solomon erasure coding and bitrot healing<br>• Durable AI agent memory & checkpointed buffers<br>• In-memory 128-d cosine vector similarity<br>• Embedded MCP server (`cmd/sop-mcp-server`)<br>• Embedded A2A agent runtime (`cmd/sop-a2a-agent`)<br>• Local runbook verification barrier (`ai/verify`)<br>• Developer GitHub OIDC authentication | **Available Today** |
| **Pro Governance** | **$49** / team / mo | Self-Hosted Add-on | Commercial | • Declarative Policy-as-Code compilation<br>• Tamper-evident SHA-256 audit lineage<br>• Signed cryptographic audit export<br>• Team-level workspaces and quota controls<br>• Priority MCP gateways and traffic shaping<br>• Automated Stripe Checkout & Customer Portal | **Available Today** (`governance/`) |
| **Enterprise Governance** | **Custom** / Annual Contract | Self-Hosted Enterprise | Commercial | • Enterprise SSO: **Okta** & **Microsoft Entra ID**<br>• Multi-tenant RBAC & tenant isolation boundaries<br>• Real-time audit streaming (SIEM / Kafka)<br>• Custom safety invariant enforcement engine<br>• Fine-grained barrier verification rules<br>• Dedicated enterprise compliance & SLA guarantees | **Foundation Implemented** (`governance/`) |
| **Hosted Cloud** | *Usage-based* | Managed Cloud SaaS | Commercial | • Managed Joltrin instances (zero-ops)<br>• Cloud-hosted MCP hub & multi-agent routing<br>• Multi-region database replication<br>• Managed agent coordination network<br>• Automated off-site snapshots & backup verification | **Planned / In Development** |

---

## 2. Architectural Boundary: The `governance/` Package

To maintain clean separation between the open-source storage engine and commercial extensions, Joltrin isolates all licensing, billing, and enterprise controls inside the decoupled [`governance/`](../governance/) package:

### 1. Capability & Tier Abstractions ([`governance/tier.go`](../governance/tier.go))
- **`Tier`**: Identifies deployment tiers (`TierCore`, `TierPro`, `TierEnterprise`, `TierHosted`).
- **`Capability`**: Granular entitlement flags (e.g. `CapEmbeddedBTree`, `CapDeveloperOIDC`, `CapPolicyAsCode`, `CapEnterpriseSSO`, `CapMultiTenantRBAC`).
- **`FeatureGate`**: Thread-safe runtime capability gate with dynamic tier switching (`SetTier()`) and runtime override toggles. Core B-Tree and transaction engines never execute invasive license checks.

### 2. Stripe Billing & Subscription Engine ([`governance/billing.go`](../governance/billing.go))
- **`BillingService`**: Manages commercial customer subscriptions, Stripe Checkout sessions, and Customer Portal redirects.
- **Cryptographic Signature Verification (`VerifyStripeSignature`)**:
  - Implements standard Stripe `Stripe-Signature` HMAC-SHA256 signature verification.
  - Enforces a 300-second timestamp tolerance window to eliminate replay attacks.
  - Constant-time signature comparison protects against timing side-channel attacks.
- **Idempotent Webhook Processing (`HandleWebhook`)**:
  - Automatically deduplicates re-delivered Stripe events using a thread-safe event cache.
  - Handles `checkout.session.completed`, `customer.subscription.created/updated`, and `customer.subscription.deleted`.
  - Automatically upgrades or downgrades the server's `FeatureGate` tier based on authoritative subscription state.
- **Zero-Credential Simulation Mode**:
  - When `STRIPE_SECRET_KEY` is omitted, the engine automatically operates in deterministic simulation mode.
  - Enables local testing of the complete upgrade/checkout lifecycle without third-party network dependencies.

### 3. Tamper-Evident Audit Logging ([`governance/audit.go`](../governance/audit.go))
- **`AuditEvent`**: Canonical audit records capturing Actor, Action, Resource, Decision (`allow`, `deny`, `violation`), and Predecessor Hash.
- **SHA-256 Hash Chaining**: Every event cryptographically seals the previous event (`PrevHash`), forming a tamper-evident append-only ledger.
- **`VerifyIntegrity()`**: Automated chain traversal that detects any unauthorized database or audit modifications.
- **`AuditSink` & `AuditStreamer`**: Extension interfaces for SIEM, syslog, and Kafka streaming.

### 4. Policy-as-Code & Barrier Extension ([`governance/policy.go`](../governance/policy.go))
- **`PolicyManifest`**: Declarative JSON/YAML specification defining operational runbook steps, roles, and precedence invariants.
- **`CompilePolicy()`**: Compiles declarative manifests into an executable formal verification graph ([`ai/verify.Workflow`](../ai/verify/verify.go)).
- **Safety Gating & Auditing**: Atomically checks preconditions and safety invariants before advancing execution traces, seamlessly recording decisions to the audit ledger.

### 5. Multi-Tenant Workspaces ([`governance/workspace.go`](../governance/workspace.go))
- **`Tenant` & `Workspace`**: Isolates datasets, agent memories, and policies across organizations and teams.
- **Quota Management**: Enforces limits on workspaces, memory spaces, and concurrent agents based on tier entitlements.

### 6. Gateway Extensions ([`governance/gateway.go`](../governance/gateway.go))
- **`GatewayAuthenticator`**: Authenticates MCP and A2A callers via API tokens or mutual TLS.
- **`TierRateLimiter`**: Token bucket rate limiting tailored per tier (Core: 10 req/s; Pro: 100 req/s; Enterprise: 1,000 req/s).
- **`PriorityWeight`**: Enables priority scheduling for mission-critical agent requests.

### 7. Extensible Identity & Enterprise SSO ([`governance/identity.go`](../governance/identity.go))
- **`IdentityProvider` Interface**: Standardizes OIDC/OAuth 2.0 authentication across heterogeneous identity systems.
- **Normalized Claim Mapping**: Translates standard OpenID claims (`sub`, `email`, `preferred_username`, `groups`, `roles`, `tid`) into internal user roles (`Admin`, `User`, `Guest`).
- **Three Supported Identity Providers**:
  - **GitHub OIDC** (`IdPTypeGitHub`): Open-source & developer tier (`TierCore`). Authenticates developers, CI/CD runners, and GitHub Actions workflows via OIDC token exchange.
  - **Okta** (`IdPTypeOkta`): Enterprise tier (`TierEnterprise`, gated by `CapEnterpriseSSO`). Supports org domain discovery, Okta user groups, and custom claim-to-role mappings.
  - **Microsoft Entra ID** (`IdPTypeEntraID`): Enterprise tier (`TierEnterprise`, gated by `CapEnterpriseSSO`). Supports Azure tenant isolation (`tid`), directory roles (`roles`), and Enterprise app registration.
- **Runtime Entitlement Enforcement**: Attempting to authenticate via Okta or Microsoft Entra ID without an active Enterprise tier is blocked with `ErrEnterpriseSSONotLicensed`.

---

## 3. Web & API Surface (`tools/httpserver`)

The standalone HTTP management server provides integrated plan, billing, and enterprise onboarding endpoints:

| Endpoint | Method | Auth | Description |
| :--- | :--- | :--- | :--- |
| `/api/billing/plan` | GET | `withAuth` | Returns active tier, capability matrix, subscription object, and Stripe status. |
| `/api/billing/checkout` | POST | `withAuth` | Creates a Stripe Checkout session (or simulation URL in dev mode). |
| `/api/billing/portal` | POST | `withAuth` | Generates a Stripe Customer Portal link to manage cards or subscriptions. |
| `/api/billing/checkout/simulate` | GET | Public | Dev/sandbox callback simulating successful Stripe checkout completion. |
| `/api/billing/enterprise-contact` | POST | Public | Captures enterprise inquiries (Name, Email, Company, Team Size, Use Cases). |
| `/api/billing/webhook` | POST | Public | Ingests Stripe webhook events with HMAC-SHA256 signature verification. |

### Plan & Billing UI
Inside `/app`, users can open the **Plan & Governance** modal via the sidebar footer badge or credit card button. The UI provides:
1. **Interactive Plan Cards**: Live indicators for Core, Pro, and Enterprise tiers.
2. **One-Click Stripe Checkout**: Redirects to Stripe Checkout with Apple Pay / Google Pay support.
3. **Customer Portal Management**: Active Pro subscribers can view invoices, update cards, or cancel via the Stripe billing portal.
4. **Capability Matrix**: Real-time status table showing every licensed feature.
5. **Enterprise Inquiry Form**: In-app submission form for organizations requiring custom SLAs and Okta/Entra ID setups.

---

## 4. Guarantees for Open-Source Users

1. **No Artificial Capacity Paywalls**: The open-source core will never cap database size, transaction frequency, memory buffer capacity, or local MCP/A2A concurrency.
2. **Permanent MIT Licensing**: All core storage engines, file systems, vector indexes, and the local `ai/verify` verification barrier remain perpetually licensed under the MIT license.
3. **Decoupled Architecture**: Commercial and governance modules interact via clean, decoupled Go interfaces ([`governance/`](../governance/)) rather than invasive runtime licensing locks.
