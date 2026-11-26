# Joltrin Private Intent

Status: the `intent` package, the MCP example and the browser demo are implemented and tested. The team features listed under Pro and Enterprise are not built. Nothing here is deployed as a hosted service.

Private Intent lets an agent act under rules its owner wrote, without giving the agent or the verifier the owner's private wording, and makes the system that holds the credentials check a signed receipt before it acts.

## What it does in one pass

1. The owner writes a rule in a vault: which agent, which tool, which resource, and what must be true first (a signed fact from another system, a signed approval from a named role).
2. An agent proposes an action. The engine answers `ALLOW`, `BLOCK` or `REQUIRE_APPROVAL` and says exactly which prerequisite is missing.
3. Other systems supply the prerequisites as signed artifacts. The agent cannot sign them.
4. On `ALLOW` the engine signs a receipt bound to that exact action and policy. The executor (a `Guard`) runs the action only with a valid, unused receipt.
5. Revoking an approval, evidence or rule makes the next attempt `BLOCK` again.

Try it: [joltrinhq.com/private-intent](https://joltrinhq.com/private-intent/) (simulated, runs the real engine in WebAssembly), or `go run ./examples/private_intent demo`.

## What exists today, and what was reused

| Piece | Where | Reused or new |
| --- | --- | --- |
| Decision semantics for required facts | `verify` safety rules and `Violation.MissingState` | Reused. The engine builds a one-step workflow per decision and asks `verify` which required states are absent. |
| Tamper-evident audit | `governance.MemoryAuditRecorder` | Reused in the example through an audit hook. The core package does not import `governance`, so it stays small in WebAssembly. |
| MCP transport | `mcp-go`, same as `tools/mcpserver` | Reused. |
| WebAssembly demo pattern, site assembly, Playwright setup | `demo-agents`, `scripts/assemble-site.sh`, `tests/` | Reused. |
| Vault, rules, signed evidence and approvals, revocation, receipts, Guard, suggestions | `intent/` | New. |
| Durable B-tree storage | not used | The vault is one sealed file. A B-tree adds nothing at tens of rules, and sealing the whole file is simpler to reason about. Revisit when a team vault holds thousands of records. |

Public APIs of `verify`, `governance` and the MCP server are unchanged.

## Data model

A rule (`intent.Rule`) has an id, a public `Label`, a private `Preference`, owner, purpose, mode (`grant` or `deny`), agent, tool, resource and environment scopes (glob patterns), a list of `Requirement`s, provenance (explicit or accepted suggestion), created and expiry times, a policy version and a revocation state.

An action (`intent.Action`) has a tenant, agent, tool, resource, environment and params. Its hash is SHA-256 over canonical JSON with a domain prefix.

Signed artifacts are Ed25519 over canonical JSON with a per-type domain prefix:

- `Evidence`: a trusted issuer says a state holds for a resource until a time.
- `Approval`: a trusted approver says yes to one action hash, for a role, until a time.
- `Revocation`: withdraws an evidence or approval id.
- `Receipt`: the engine's `ALLOW`, bound to action hash, policy digest and version, rule, evidence and approval ids, with an expiry and a nonce.

### Decision rules

- Default deny. No active matching grant means `BLOCK`.
- A matching deny rule always wins.
- With several matching grants, the union of their requirements applies. The strictest wins.
- Missing evidence gives `BLOCK`. Evidence present but approval missing gives `REQUIRE_APPROVAL`. Both present gives `ALLOW`.
- Expired or revoked rules, evidence and approvals do not count. A tenant mismatch, an incomplete action or an internal error gives `BLOCK`.
- `ALLOW` through `Authorize` spends the approvals it relied on, under the same lock as the decision, so one approval authorizes one action even under concurrent calls. `Evaluate` is the read-only dry run.

## Privacy design

| Property | How it is met | What it does not cover |
| --- | --- | --- |
| Confidentiality of wording | `Preference` is sealed at rest with AES-256-GCM (tenant bound as additional data) and is never written into decisions, receipts, audit entries or the policy digest. Tests assert the secret string is absent from all of them. | The owner's machine and the engine process hold it in memory. A compromised engine host sees it. |
| Integrity | Ed25519 signatures on evidence, approvals, revocations and receipts. The vault file is authenticated by GCM. | The `translog` package can record receipts in an append-only Merkle log, but nothing writes to it by default. A host that can edit the vault and the engine can change policy, and a log operator can show two histories to two parties unless independent monitors compare signed heads, which Joltrin does not run. The digest in the receipt lets an auditor detect that a receipt was made under a different policy than expected. |
| Authenticity | Each issuer key is trusted only for named states, each approver key only for named roles. A backup system key cannot attest `tests_passed`. | Key distribution is manual. |
| Data minimization | The verifier needs only rule ids, labels, requirements, the action hash and the digest. The receipt carries no params. | The action `Params` the caller passes to `Authorize` are visible to the engine. |
| Telemetry | The audit hook receives ids, hashes and verdicts only. | |
| Retention and deletion | `Vault.Revoke` keeps history. `Vault.Delete` removes the rule and its preference. `Export` returns the owner's portable copy. | No automatic retention timers yet. |

### Why not zero-knowledge proofs

The question ZK answers is "prove a fact about data without revealing the data to a verifier that is not trusted with it." Here the verifier is the engine the owner runs, and it must read the rule to match it. The party that must not see the wording is the agent and any downstream log, and that is met by not putting the wording in anything those parties receive. Signatures plus sealed storage give that at a cost of microseconds. A ZK circuit would add proving time, a trusted setup or large proofs, and a much larger audit surface, and it would not remove the need for the executor to check a receipt. ZK becomes worth it if an untrusted third party must verify a preference-dependent condition without learning the preference, for example a customer auditing a vendor's agents. Nothing in the current customer problem needs that, so it is not built.

## Enforcement: the part that makes it real

The engine is advice unless the executor refuses to act without a receipt. The integration pattern, implemented in `examples/private_intent`:

- The agent's only path to the destructive operation is an MCP tool. Behind that tool the server calls `Authorize`, then runs the operation inside `Guard.Run`, which verifies the receipt signature, the verdict, the expiry, that the receipt covers this exact action, and that its nonce is unused, and spends the nonce before running anything.
- Evidence and approvals reach the server as signed files in an inbox directory. The agent has no tool that writes them. A test drops unsigned and mis-signed files into the inbox and shows the tool stays blocked.

If an agent can reach the database directly, with its own credentials or a shell, none of this applies. Joltrin can only protect operations that the integrating system routes through the check. Giving the agent no standing credentials for the protected operation, and letting only the Guard-wrapped executor hold them, is the deployment requirement.

## Threat model

| Threat | Mitigation | Test |
| --- | --- | --- |
| Agent claims a prerequisite happened | Prerequisites are signed by keys the agent does not hold. Prose claims are ignored. | `TestForgedAndWrongEvidence`, `TestAgentCannotForgeInboxFiles` |
| Prompt injection tells the agent to skip the check | The check is outside the model. The executor refuses without a receipt. | `TestToolIsGatedEndToEnd` |
| Forged or edited receipt | Signature over every field. | `TestReceiptForgeryAndGuard`, `FuzzGuardNeverRunsOnGarbage` |
| Receipt reused for another action | Action hash bound in the receipt and recomputed by the Guard. | `TestReceiptForgeryAndGuard` |
| Replay | Single-use nonce in the Guard, short TTL, single-use approvals. | `TestReceiptForgeryAndGuard`, `TestOneApprovalSpentOnceUnderConcurrency` |
| Approval for one action reused for another | Approval bound to the action hash, including params. | `TestApprovalBoundToAction` |
| Stale authority | Expiry on rules, evidence, approvals and receipts. | `TestExpiry`, `TestRuleExpiry` |
| Revocation ignored | Checked on every evaluation. | `TestRevokedApprovalBlocksAgain`, `TestRevokedEvidenceBlocks` |
| Unauthorized revocation | Signer must be the owner key or the key that signed the target. | `TestRogueCannotRevoke` |
| Issuer attests outside its scope | Per-key state and role scopes. | `TestForgedAndWrongEvidence` |
| Cross-tenant access | Engine and vault are per tenant. Artifacts, actions and sealed vaults are tenant checked. | `TestTenantIsolation`, `TestSealedVault` |
| Concurrent double spend | One lock covers decision and approval spend. | `TestOneApprovalSpentOnceUnderConcurrency` with `-race` |
| Policy weakened silently | The policy digest and version are in every receipt. | `TestPolicyDigestChangesWithPolicy` |
| Suggestions turn into permissions | Suggestions are inert, need an expiry and an explicit accept. | `TestSuggestionsStayInert` |
| Malformed input crashes the engine | `Authorize` recovers and fails closed. | `FuzzIngestNeverPanics` |

### Known limits

- Bypass: anything with its own path to the target is unprotected.
- The Guard's used-nonce set and the engine's evidence and approval stores are in memory. A restart forgets used nonces (a receipt can be replayed for the rest of its TTL, default 60 seconds) and forgets approvals (the safe direction: they must be re-issued). A durable store is not built.
- A receipt does not prove the action ran or succeeded. The statement is printed in every receipt.
- Clock skew between the engine, issuers and the Guard shifts expiry decisions. The Guard allows five seconds of skew on a receipt's start time only.
- Keys are files or env values. There is no rotation tool, no HSM or KMS support and no key ceremony. The vault key lives next to the vault in the example, which is fine for a demo and wrong for production.
- Rule matching uses `path.Match` globs. There are no attribute expressions, no blast-radius numbers and no time-window conditions yet, so "require stronger verification during a deployment window" and "approval above a blast radius" are not implemented.
- The browser demo generates keys in the page and plays all three roles. It shows the logic, not a deployment.

## Agent swarms: Scope Lock and Claim Check

Fan out many agents on one job ("review these 40 PRs as fast as you can") and two things go wrong. An agent drifts past its task: it edits code, opens other repos or keeps calling tools. And it reports findings that are not true: a missing nil check on a line that is not there.

`intent/contract.go` covers both, per agent.

- **Contract.** The owner signs one per agent and task: tool globs, resource globs, an optional environment, an action budget and an expiry. A contract that is edited, signed by another key or expired is refused.
- **Scope Lock.** `ScopeLock.Check(action)` returns ALLOW or BLOCK with a reason code (`tool_out_of_scope`, `resource_out_of_scope`, `wrong_agent`, `budget_exhausted`, `contract_expired`). Blocked calls do not spend the budget. Every attempt is recorded.
- **Claim Check.** `ScopeLock.CheckClaim(claim, source)` accepts a finding only if each citation names a resource inside the contract and quotes text that appears in it (whitespace ignored, 12 characters minimum). The caller reads the source, not the agent. A finding with no citation, an invented quote or a file the agent was never given is rejected.
- **Report.** `Summarize(reports...)` returns totals and the list of agents that drifted or reported an unsupported finding, so you read those first.

Limits:

- A lock only sees actions routed through it. An agent with its own credentials to the target is not stopped.
- Claim Check proves the quote exists. It does not prove the conclusion follows from it. A real line can still be misread.
- Scope is glob matching. An agent can still do the wrong thing inside what it was allowed.
- State is in memory. The contract is checked against one owner key, with no rotation or identity provider.

## Use it from n8n

An n8n workflow can write a record only after a human approves that exact record. n8n is treated as an untrusted courier: it asks for a decision and carries a receipt, but it holds no key and cannot sign anything. The check and the write both happen in a small gateway you run next to your data.

```
n8n  --POST /v1/authorize {key, payload}-->  gateway: Authorize (spends the approval)  --> ALLOW + receipt
n8n  --POST /v1/execute {key, payload, receipt}-->  gateway: Guard.Run verifies, then writes records/<key>.txt
owner: private_intent approve-record ./pi <key> <payload-file>     (signs; n8n never sees the key)
```

The gateway rebuilds the action from `key` and `payload`. Nothing in the request can name another tool or resource. The payload is bound into the action as its SHA-256, so an approval covers one payload and a receipt covers one payload.

Try it (local only, nothing is hosted):

```
go run ./examples/private_intent init ./pi
go run ./examples/private_intent serve ./pi                 # 127.0.0.1:8787
```

In n8n, import `examples/private_intent/n8n/private-intent-record.workflow.json`, then create a Header Auth credential named `Joltrin gateway` with name `Authorization` and value `Bearer <contents of ./pi/server/gateway.token>`. Run the workflow. It stops at "Wait for the owner" and asks again every 10 seconds. In another terminal:

```
echo -n "order 42 shipped" > /tmp/p.txt
go run ./examples/private_intent approve-record ./pi order-42 /tmp/p.txt
```

The next poll returns ALLOW, the workflow executes, and `./pi/records/order-42.txt` appears. The wait loop has no cap. Stop the execution from n8n if no approval is coming.

What the tests cover (`go test -race ./internal/examplecmd/private_intent`):

| Case | Result |
| --- | --- |
| No approval | `REQUIRE_APPROVAL`, no receipt, nothing written |
| Approval for a different payload or key | Not covered, stays `REQUIRE_APPROVAL` |
| Receipt used with a changed payload, key, nonce or policy digest | 403 before any write, and the real receipt is not spent by the failed tries |
| Expired approval, expired receipt | Refused |
| Same receipt sent again | 403 `receipt already used` |
| 25 concurrent `authorize` calls on one approval | Exactly one ALLOW |
| 25 concurrent `execute` calls on one receipt | Exactly one write |
| Wrong or missing bearer token, bad key, unknown field, oversize body | 401 or 400 |
| Write fails after verification (record already exists) | 500 and the response says the receipt is spent |

If execution fails or times out after the approval is consumed: the approval is spent at `authorize` and the receipt nonce is spent before the write runs, on purpose, so a crash cannot make a second write possible. A failed or timed-out `execute` therefore needs a new approval. The write itself is a temp file plus a hard link, so a reader never sees half a record and an existing record is never replaced. Between an `ALLOW` and an `execute` that never arrives, the approval is lost. That is the safe direction.

### OpenPGP: where it helps and where it does not

n8n stores the input and output of every node in its execution log. If a record is sensitive, that log is a second copy of it. For that case the sender encrypts the payload to the gateway's OpenPGP public key before n8n sees it (`pgp-init`, then `encrypt`). n8n forwards an armored message it cannot read. The approval binds the SHA-256 of the message n8n carries, so a different message, including the same text encrypted again, is refused before any decryption. The gateway decrypts only inside `Guard.Run`, after the receipt checks out. A message encrypted to a different key fails with a clear error and writes nothing. The private key stays in `server/pgp.key.asc` on the gateway and is never in the workflow, its logs or the test output.

```
go run ./examples/private_intent pgp-init ./pi
go run ./examples/private_intent encrypt ./pi secret.txt > secret.asc
go run ./examples/private_intent approve-record ./pi order-43 secret.asc
```

Limits you should know before relying on it:

- It protects the payload from n8n and from n8n's logs. It does not protect it from the gateway, which decrypts it, or from whoever holds the gateway's private key.
- The owner approves a hash of the ciphertext. To know what they are approving, they must decrypt it with the gateway's key (`gpg --decrypt`) or trust the sender. Joltrin does not show them the plaintext.
- It uses the library's default OpenPGP integrity check (a modification detection code on the message). It does not sign the message, so it does not prove who sent it. The approval and the receipt provide the authorization.
- If n8n itself must read or transform the plaintext, OpenPGP does nothing for you. Skip it.
- The gateway key is a file. There is no rotation, no KMS and no HSM.

### Taint Barrier on the way out

If `server/taint.json` exists, the gateway also runs the [Taint Barrier](SECURITY_CONTROLS.md#taint-barrier) on the plaintext before it is written. A plain payload is screened at `authorize`, so a bad one never spends an approval. An encrypted payload cannot be read then, so it is screened after decryption, and a hit returns 403 and the receipt is spent. A human approval does not override it: an approved, encrypted message that contains a registered production key is still refused.

```json
{ "policy": { "version": 1, "labels": { "prod-secret": { "allow_hosts": ["vault.internal"] } } },
  "values": [ { "label": "prod-secret", "name": "payments-key", "value": "<the value to watch for>" } ] }
```

The values stay on the gateway, and responses and findings carry only the label and name. The record file is not an allowed sink for `prod-secret` in this example, so any record containing it is blocked. It finds values you registered and their common encodings. It does not find a secret nobody registered.

### What this does not do

- The gateway, the rule and the keys are an example you run locally. There is no hosted gateway, no durable store for used receipts (a restart forgets them for the rest of their TTL), and no n8n community node.
- It was tested with Go tests against the HTTP handlers. The workflow file is checked for valid wiring and for secrets, but it has not been run in a real n8n yet. Expect to adjust node settings on import.
- The workflow only calls `http://127.0.0.1:8787`. A remote gateway needs TLS in front of it, which is not set up here.

## Run it

```
go test -race ./intent ./examples/private_intent
go test -run xxx -fuzz FuzzGuardNeverRunsOnGarbage -fuzztime 30s ./intent
go test -run xxx -bench . -benchmem ./intent
go run ./examples/private_intent demo
go run ./examples/private_intent init ./pi      # prints the MCP client config to add
```

Playwright: `npx playwright test tests/private-intent.spec.ts` after `bash scripts/build-site.sh`.

## Library sketch

```go
vault := intent.NewVault("acme", nil)
vault.Add(intent.Rule{ID: "prod-data-deletion", Label: "Production data deletion",
    Preference: "private wording", Owner: "you", Purpose: "protect production data",
    Mode: intent.Grant, Agent: "ops-agent", Tool: "db.delete", Resource: "prod/*", Env: "production",
    Requires: []intent.Requirement{
        {Kind: intent.NeedEvidence, State: "backup_validated", Describe: "verified backup"},
        {Kind: intent.NeedApproval, Role: "production_owner", Describe: "owner approval"}}})

eng, _ := intent.NewEngine(intent.Config{Vault: vault, SigningKey: engineKey, Trust: trust})
decision, receipt := eng.Authorize(action)            // receipt is nil unless ALLOW

guard := intent.NewGuard(eng.ReceiptKey(), nil)       // in the executor process
err := guard.Run(action, receipt, func() error { return dropDatabase() })
```

## Migration

Nothing existing changes. A team already using `verify` through `sop-mcp-server` keeps it. Private Intent adds a second tool path for actions that need owner rules and approvals. A runbook step can map to an `Action` one to one, so a later change can let a `verify` step require an intent decision without changing `verify`'s API.

## Measured cost

From `go test -bench` on one laptop (Apple M4 with an amd64 Go toolchain running under translation, so likely pessimistic; single run, no load test):

| Operation | Time per call |
| --- | --- |
| `Evaluate`, blocked | about 6 microseconds, 50 allocations |
| Sign an approval, ingest it, and `Authorize` to an `ALLOW` with a receipt | about 130 microseconds, 69 allocations |

These are decision costs in process. They say nothing about a deployed service.

## Competitor capability matrix

Sources are vendor documentation and secondary coverage found in a limited set of searches. Cells say what was found, and "not found" means the searches did not turn it up, not that it does not exist. Several vendors were not examined (OpenAI's agent permissions, Invariant Labs, Lasso, Zep and Letta internals, Microsoft Purview agent policy details).

| System | Per-agent / per-tool policy | Default deny | Human approval step | Signed decision or receipt | Private-preference minimization | Local-first and open source |
| --- | --- | --- | --- | --- | --- | --- |
| Amazon Bedrock AgentCore Policy (Cedar) | Yes, Cedar over gateway tools | Yes, forbid beats permit | Not found | Not found | Not found | No, managed service |
| TrueFoundry Cedar guardrails | Yes, down to tool arguments | Yes | Not found | Not found | Not found | No, gateway product |
| Alibaba Cloud Agent Identity (Cedar) | Yes, outbound calls | Gateway bound | Not found | Not found | Not found | No |
| Permit.io MCP Gateway | Yes, trust levels and tool risk | Not confirmed | Yes, step-up approvals | Not found | Not found | Mixed, a docs page calls it upcoming |
| Microsoft Entra Agent ID and Purview | Identity, Conditional Access, DLP applied to agents | Not examined | Not examined | Audit, not signed receipts | Sensitivity labels | No |
| Claude Code permissions and hooks | Yes, deny, ask, allow rules and hooks | Mode dependent | Yes, ask | No | No | Local, product feature not a library |
| Decidio (PyPI) | Approval gate for actions | Not examined | Yes | Yes, portable receipt | Not examined | Python package, license not checked |
| Authproof delegation receipts (IETF draft) | User-signed scope and time | Not examined | User signs before action | Yes, signed receipts and log | Not examined | Draft, SDK not checked |
| Mem0, Zep, Letta | Memory scoped by user, agent, run ids | Not applicable | No | No | Scoping, no consent records found | Mem0 and Graphiti open source |
| Open Policy Agent | General policy engine, can be used for this | Policy dependent | Not built in | No | No | Yes |
| Bedrock Guardrails | Content filters, extended to tool results via SDK hooks | Not applicable | No | No | Content redaction | No |
| Joltrin Private Intent | Yes, glob scopes plus per-key trust scopes | Yes | Yes, action-bound single use | Yes, Ed25519 receipt checked by the executor | Public label and digest only, wording sealed | Yes, MIT, in-process |

Each capability on its own has a competitor. What I did not find, in this limited search, is one open, local-first component that combines sealed private rules, evidence accepted only from scoped signer keys, single-use action-bound approvals and an executor-side receipt check, and that can be read and audited in a few hundred lines. That combination is the claim. It is narrow, and a funded vendor could build it, so the defensibility is in being the simple, inspectable, local option that teams adopt first, not in a technical secret.

## Architectures considered

Scores are 1 to 5, higher is better (so low difficulty and low friction score high). They are my judgement for a small team, not measurements.

| | A. Evidence-gated actions with an Intent Vault | B. ZK preference proofs | C. Memory firewall in front of memory stores | D. Attenuable capability tokens for sub-agents |
| --- | --- | --- | --- | --- |
| Customer urgency | 5 | 2 | 3 | 3 |
| Willingness to pay | 4 | 2 | 3 | 3 |
| Differentiation | 3 | 4 | 2 | 2 |
| Security value | 5 | 3 | 3 | 4 |
| Implementation ease | 4 | 1 | 2 | 3 |
| Adoption ease | 4 | 2 | 2 | 3 |
| Operating cost (low is high) | 5 | 3 | 3 | 5 |
| Defensibility | 3 | 3 | 2 | 2 |
| Demonstrable in 60 seconds | 5 | 1 | 3 | 2 |
| Total | 38 | 21 | 23 | 27 |

A won because it answers the most urgent question platform teams have ("will the agent do something destructive"), reuses the verification engine, and shows its value in one block-then-allow sequence. B is the strongest on differentiation and the weakest on everything a buyer sees. C fights the memory vendors on their ground. D is a good later addition for delegation to sub-agents, and the receipt design leaves room for it.

## Files

- `intent/`: engine, vault, signing, receipts, guard, scope locks and claim checks, suggestions, tests, fuzz targets and benchmarks.
- `examples/private_intent/`: the MCP server, the CLI for the signing roles, the scripted demo and an end-to-end test.
- `demo-intent/`: WebAssembly build and the page served at `/private-intent/`.
- `tests/private-intent.spec.ts`: browser tests for the flow, the privacy claim, tampering and replay, and a phone viewport.
