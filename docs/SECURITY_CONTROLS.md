# Security controls

What Joltrin does today for security and privacy, what has a test, and what does not exist yet. Each row points at code or a workflow you can read. Nothing here is a certification. Joltrin has no SOC 2 report, no GDPR assessment and no penetration test, and it makes no zero-knowledge or guaranteed-confidentiality claim.

Status words:

- **Implemented, tested**: the code exists and a test in the repository exercises it.
- **Implemented, not tested**: the code exists and no test was found for it.
- **Library only**: the code exists, but nothing wires it in by default. The caller has to use it.
- **Planned**: written down as intended, no code.
- **Missing**: no code and no plan.

## Audit and integrity

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| Hash-chained audit log, including event metadata | Implemented, tested | `governance/audit.go`, `governance/audit_test.go`, `governance/audit_security_test.go` | `MemoryAuditRecorder` keeps events in memory. `FileAuditRecorder` (`governance/audit_file.go`) also appends them to a local JSON Lines file and refuses to reopen a file that fails verification. Removing the newest events from the end of the file cannot be detected without a copy of the last hash kept somewhere else. No rotation, encryption or database store. |
| Ed25519 signatures on audit events, verified offline | Implemented, tested | `governance/audit_ed25519.go`, `governance/audit_security_test.go` | Key storage, rotation and HSM or KMS signing are Missing. The caller holds the private key. |
| JSON export of the audit log | Implemented, tested | `MemoryAuditRecorder.ExportJSON`, `governance/audit_test.go` | |
| Streaming audit events to a SIEM | Library only | `AuditStreamer` interface in `governance/audit.go` | No connector ships. Splunk, Datadog and Kafka connectors are Missing. |

## Privacy and PII

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| Mask emails, SSNs, card numbers (Luhn checked), phones, IPv4, AWS keys and JWTs before an audit event is stored | Library only, tested | `governance/pii.go`, `governance/audit_security_test.go` | Pattern based. It misses names, addresses, free-text identifiers and anything without a fixed format. Use `RedactingSink` to apply it. It is not applied by default. |
| Keyed pseudonyms so one value can be correlated across events without storing it | Library only, tested | `PIIRedactor.Key` | Rotating the key breaks correlation with older events. |
| Configurable retention and deletion | Missing | | |
| Data residency controls | Missing | | |
| Encryption of agent memory at rest | Not assessed | | This document makes no claim either way. |
| Data subject access and erasure workflow | Missing | | |

## Identity and access

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| OIDC token validation for GitHub, Okta and Microsoft Entra ID | Implemented, tested | `governance/identity.go`, `governance/identity_test.go` | A building block. It is not packaged as a login flow. |
| Role checks on policy evaluation | Implemented, tested | `governance/policy.go`, `governance/policy_test.go`, `rbac_test.go` | |
| Feature gating by tier | Implemented, tested | `governance/tier.go`, `governance/tier_test.go` | Entitlements are not tied to a live billing result on the public site. |
| SAML | Missing | | |
| SCIM provisioning | Missing | | |
| Tenant isolation for a hosted service | Missing | | No hosted service exists. |
| Customer-managed encryption keys | Missing | | |

## Agent action control

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| Verification barrier: a step is blocked until the recorded state it needs exists | Implemented, tested | `verify/`, `docs/AGENT_BARRIER_TESTS.md` | It judges the recorded trace. It does not inspect what a tool really did. |
| Policy-as-code compilation and evaluation | Implemented, tested | `governance/policy.go`, `governance/policy_test.go` | |
| Signed human approvals for sensitive steps (single owner) | Implemented | `docs/PRIVATE_INTENT.md` | Approval routing for teams is Planned. |
| Prompt injection defense | Missing | | The barrier limits what a manipulated agent can do next. It does not detect injected text. |

## Network and logging

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| Outbound fetch guard against internal addresses and cloud metadata, rechecked at dial time | Implemented, tested | `netguard/netguard.go`, `netguard/netguard_test.go` | Does not restrict which public hosts may be reached. |
| Safe relative redirects only | Implemented, tested | `netguard/redirect.go`, `netguard/redirect_test.go` | |
| Newline stripping so user input cannot forge log lines | Implemented | `logsafe/logsafe.go` | Applied where callers use it. Coverage across the codebase was not audited. |

## Supply chain and release

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| Secret scanning in CI | Implemented | `.github/workflows/security.yml` (Gitleaks) | |
| Dependency scan in CI | Implemented | `.github/workflows/security.yml` (Trivy filesystem scan) | |
| Static analysis | Implemented | `.github/workflows/codeql.yml` | |
| Dependabot updates | Implemented | `.github/workflows/dependabot-automerge.yml` | Major version bumps wait for a person. |
| Checksums and SBOM on releases | Implemented | Release assets `SHA256SUMS`, `sbom.spdx.json` | A checksum catches a damaged download, not a compromised release. |
| Signed build provenance, verified by the installer with `JOLTRIN_VERIFY=1` | Implemented | `scripts/install.sh`, README | Releases from v5.11.0 on. Needs the GitHub CLI. |
| Private vulnerability reporting | Implemented | `SECURITY.md` | |

## Compliance

| Item | Status |
| --- | --- |
| SOC 2 | No report. Controls above are not mapped to criteria by an auditor. |
| GDPR | No assessment. The retention, residency and erasure rows above are Missing. |
| Penetration test | None done. |

## How to correct this page

If a row is wrong, open an issue with the file or test that shows it. A claim here should always be backed by something a reader can run or open.
