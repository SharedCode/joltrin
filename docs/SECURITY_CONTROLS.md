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
| Hash-chained audit log, including event metadata | Implemented, tested | `governance/audit.go`, `governance/audit_test.go`, `governance/audit_security_test.go` | `MemoryAuditRecorder` keeps events in memory. `FileAuditRecorder` (`governance/audit_file.go`) also appends them to a local JSON Lines file and refuses to reopen a file that fails verification. Removing the newest events from the end of the file is detected only if you store `LastHash()` outside the log and reopen with `OpenFileAuditRecorderAnchored`. Events stay in memory for the life of the process. No rotation, encryption or database store. |
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
| Prompt injection defense | Missing | | The barrier limits what a manipulated agent can do next. It does not detect injected text. The [Taint Barrier](#taint-barrier) below limits one consequence of an injection: labeled data leaving in a tool call. |

## Taint Barrier

Information-flow control plus tripwires for agent tool calls, in the `taint` package. You label values (`secret`, `pii`, `customer-data`) and register them. Each tool call is checked before it runs. If its arguments contain a labeled value, in one of the encodings below, and the call's destination is not allowed for that label, the call is blocked. Planted fake credentials (honeytokens) block on any appearance. It is part of the open-source Go module (MIT). It is not a sandbox and it is not a prompt injection detector.

| Control | Status | Evidence | Limit |
| --- | --- | --- | --- |
| Labeled values kept only as keyed HMAC-SHA256 fingerprints, with a per-session random key. The raw value is not stored, logged or returned | Implemented, tested | `taint/registry.go`, `TestNoRawValueInOutputs` | Fingerprints are per process. A restart makes a new key and the registry has to be filled again. A rolling prefilter, also keyed, is stored next to each fingerprint and every candidate is confirmed with HMAC. |
| Matching in common encodings: plain, case and whitespace changes, zero-width and soft-hyphen characters, reversed, hex, percent-encoding (including double and partial), base64 and base64url at every byte alignment, base32, and pieces of 12 or more characters of values of 24 or more | Implemented, tested | `taint/normalize.go`, `TestVariantsAreBlocked`, `TestChunkedExfil`, `FuzzNormalize`, `FuzzScanFindsRegisteredValue` | Exact match on those forms only. Paraphrase, other encodings (double base64, gzip, encryption, character substitution, steganography) and pieces under 12 characters sent out of order are not caught. |
| Private key blocks found by structure, with no registration: armored PGP private key blocks, PEM and OpenSSH private key headers, including spaced, zero-width, percent-encoded and base64 forms. They get the label `private_key`, so the default-deny policy blocks them at every sink unless the policy lists that label | Implemented, tested | `taint/keyblock.go`, `taint/keyblock_test.go` | Matches the header line only, not the key body, so a key sent with its header removed is not caught. Public keys, certificates and armored PGP messages are not flagged. Encodings outside the list (double base64, gzip, encryption) are not caught. A docs-writing agent that quotes a key block will be blocked, so use observe mode first or list `private_key` for local tools. |
| OpenPGP with ASCII armor: encrypt, optionally sign, decrypt, require a signature from a named key, clear-sign and verify. A tampered message returns an error and no plaintext | Implemented, tested | `pgpkit/pgpkit.go`, `pgpkit/pgpkit_test.go` | A library, not a service. Private keys can be passphrase protected, but keeping the key file and the passphrase safe is the caller's job. No rotation, expiry, revocation, KMS or HSM. Encryption hides text from whoever carries it, not from the private key holder. Not wired into the MCP server or the Taint Barrier. |
| Values under 8 characters (after normalization) and over 4096 bytes are refused | Implemented, tested | `TestShortAndBadValuesAreRefused` | Short values collide with ordinary text. A low-entropy value of 8 or more characters can still be guessed by something that can query the check. |
| Per-label allowlist of egress hosts, tools and file paths, default deny, JSON policy that rejects unknown fields | Implemented, tested | `taint/policy.go`, `TestSinkPolicy`, `TestPolicyParsing` | A call that names a destination is judged by the destination, so a host in the arguments that the tool later contacts is not seen. Hosts are matched by name. It does not resolve DNS or check the address (see `netguard`). |
| Blocked calls return a machine-readable reason and the handles of the values found, never the value | Implemented, tested | `Verdict`, `TestNoRawValueInOutputs`, `tools/mcpserver/taint_test.go` | The handle is a name you chose and must not be sensitive. |
| Honeytokens: format-shaped fake credentials (AWS access key id and secret, GitHub token, generic API key) with a checksum marker. Any appearance in a call is blocked, alerted and recorded as a violation, even to an allowed host | Implemented, tested | `taint/honeytoken.go`, `TestHoneytokens`, `TestHoneytokenFormatAndMarker` | They cannot authenticate because they are random and were never issued. For AWS and the generic form that is a probability argument, not a guarantee from the provider. The GitHub form has a deliberately wrong CRC32 suffix. A honeytoken only helps if it is planted where the agent reads it, and it fires only if it appears in a call you route through the check. |
| Every decision (allow, block, honeytoken trip) recorded in the hash-chained audit log, masked with `PIIRedactor`, with an Ed25519 signature when the sink has a signer. Receipts verify offline | Implemented, tested | `taint/barrier.go`, `VerifyReceipt`, `VerifyExport`, `TestAuditChainAndReceiptsVerifyOffline`, `TestAuditPIIMasking` | Without a signer the chain is hash-linked but unsigned. A call that cannot be audited is blocked in enforce mode. The limits of the audit log in [Audit and integrity](#audit-and-integrity) apply. |
| Observe mode: log what would be blocked and allow the call. Honeytoken trips still alert | Implemented, tested | `TestObserveMode`, `SOP_MODE=observe` | Nothing is stopped in this mode. |
| CLI: `sop-mcp-server taint check` (exit 0 allowed, 1 blocked, 2 usage) and `taint honeytoken` | Implemented, tested | `cmd/sop-mcp-server/taint.go`, `cmd/sop-mcp-server/taint_test.go` | The values file holds raw secrets, so the CLI refuses one that other users can read. Each run builds a new registry. |
| MCP tool `check_tool_call`, added when `SOP_TAINT_POLICY` and `SOP_TAINT_VALUES` are set | Implemented, tested | `tools/mcpserver/taint.go`, `tools/mcpserver/taint_test.go` | When the agent calls it, the agent is checking its own plan, so the answer is advice. A caller can also probe it for whether a guess is in the registry. Enforce from a hook or proxy the agent cannot skip. |
| Routing every tool call of a given agent through the check | Library only | `taint.Barrier.Check` | Nothing intercepts an agent's tool calls by default. The caller wires the check into the place that executes calls. |
| Matching data split across several calls, or by a model that rewrites it | Missing | | Each call is judged alone. |
| Detecting injected instructions | Missing | | See the prompt injection row above. |
| Protecting data that was never labeled | Missing | | Only registered values are tracked. |

### Threat model note

Mapped to the OWASP Top 10 for LLM Applications (2025 numbering). The mapping says which part of each risk the control addresses. It is not a claim of coverage.

| Risk | Attack | What the Taint Barrier does | What it does not do |
| --- | --- | --- | --- |
| LLM01 Prompt Injection | Text the agent reads tells it to send a secret to an attacker | Blocks the resulting tool call when it carries a labeled value to a destination the label does not allow, whatever the injected text said | Does not detect or remove the injected text, and does not stop other harm the injection causes |
| LLM02 Sensitive Information Disclosure | A value the agent can see leaves through a tool argument, a URL or a file, possibly encoded | Blocks labeled values in the listed encodings at a sink that is not allowed. Honeytokens give a high-confidence signal that something is reading and sending what it should not | Misses paraphrase, unlisted encodings and unlabeled data. Does not control what the model says to the user |
| LLM06 Excessive Agency | An agent with a broad tool, such as HTTP or file write, uses it to move data | Adds a per-label destination allowlist in front of those tools, default deny | Does not limit what the tool can do with data that is not labeled. The verification barrier in `verify/` governs step order, not data flow |

Nothing in this section is a compliance claim.

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
