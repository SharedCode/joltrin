# MCP, A2A, and the Runbook Verification Engine

Real, built, tested code, not a proposal. This document is the audit that preceded writing it, the design rationale, and a map of what landed where.

---

## Phase 1: Existing Implementation Audit (before this work started)

| Item | Status | Evidence |
|---|---|---|
| MCP SDK in `go.mod` | Missing / Green-field | No `modelcontextprotocol` or `mark3labs/mcp-go` entry anywhere in `go.mod`/`go.sum` prior to this work. |
| JSON-RPC / SSE / stdio tool-call handlers | Missing / Green-field | The only `text/event-stream` hit in the repo was `ai/generator/chatgpt_responses_api.go:94`, a client-side header for consuming OpenAI's streaming API, not a server SOP hosts. No `"tools/list"`/`"tools/call"` handling anywhere. |
| A2A Agent Card / `.well-known/agent.json` | Missing / Green-field | No `.well-known` directory, no `AgentCard` type, no task-lifecycle state machine anywhere in the repo. |
| OPFS sync handles / Worker bridge | Partially Implemented (async only) | `demo/persistence.go` and `demo/index.html` implement OPFS persistence via the **async** File System Access API (`createWritable`/`getFile`) on the main thread. The only mentions of `createSyncAccessHandle` in the repo are in comments explaining why it was *not* used (requires a dedicated Worker; see `demo/persistence.go:27` and `demo/index.html:61`). Confirmed by grep before writing this table, not from memory. |
| `tools/httpserver` REST surface | Already Implemented | Real, substantial: auth, sessions, space/config management, an LLM connection handler, an embedder factory, a summarizer (`tools/httpserver/*.go`, 29 files). No MCP or A2A endpoint among them. |
| `ai/vector` ANN index | Already Implemented | Real IVF-style approximate nearest-neighbor index, centroid clustering + cosine similarity (`ai/vector/store.go`). CPU-only, no GPU path. |
| Client-side encryption | Missing / Green-field | No `crypto/aes`, Web Crypto usage, or any encryption-at-rest anywhere in the repo. |

This matches, and was cross-checked against, the same audit already done for [Strategic Architecture & Investor Moat](STRATEGIC_ARCHITECTURE_AND_MOAT.md) §0, written in the same session just before this document.

---

## Phase 2: Feasibility & Marketability

**Marketability.** A static runbook wiki answers "what should happen." An MCP/A2A-exposed runbook with a verification barrier answers "is it actually safe to do this right now," and can refuse the answer an agent wants to hear. That distinction, an operational control plane an autonomous agent can be handed as a tool versus a document it reads and might misinterpret, is the real product difference. Whether there's enough enterprise demand to build a business on that distinction is not something this document can determine; no design partner has evaluated this.

**Feasibility, assessed by actually building it, not estimating it.** Both protocols integrate cleanly with a Go backend: `mark3labs/mcp-go` and `a2aproject/a2a-go` are both real, actively maintained SDKs (the latter is the official Linux Foundation A2A project's own Go implementation) that handle the wire protocol correctly, leaving only the domain logic (the three tool handlers, one `AgentExecutor`) as this repo's responsibility. Total new first-party code: `ai/verify` (2 files + tests), `tools/runbookstore` (1 file), `tools/mcpserver` (1 file + tests), `tools/a2aagent` (2 files + tests). All of it builds, vets, and passes `govulncheck` clean alongside the rest of the repo.

Client-side OPFS integration for either protocol was **not** attempted in this pass: both `tools/mcpserver` and `tools/a2aagent` are server-side Go packages (they don't compile for `js/wasm`, they weren't asked to). Wiring either protocol into the browser demo specifically would be new scope, not a natural extension of what's here.

---

## Phase 3: Implementation Blueprint

### MCP Server  -  `tools/mcpserver`

Three tools, exactly as specified, registered against a shared `tools/runbookstore.Store`:

| Tool | Effect |
|---|---|
| `read_sop` | Read-only. Returns a workflow's steps, their preconditions/postconditions, and its safety rules (`WorkflowDescription`). |
| `validate_step` | Read-only. Runs the barrier check (`ai/verify.Workflow.CheckSafety`) without committing, returns `ValidateStepResult{safe, reason}`. |
| `execute_step` | The barrier certificate itself. Runs the same check; only commits the step to the trace if it passes, returns `ExecuteStepResult{executed, step, trace, blocked, replayed}`. |

All three tools declare a JSON `outputSchema` (`mcp.WithOutputSchema`, `tools/mcpserver/results.go`) and never return a bare error string. A blocked `execute_step` or `validate_step` call is not an MCP error: the request was well-formed and could still succeed once its precondition holds, so it comes back as a normal result with `executed`/`safe` false and a `blocked`/`reason` object naming which check failed (`blocked_by`), the specific state still missing (`missing_state`), and which registered steps would establish it (`established_by_steps`), evidence the caller can act on directly instead of parsing text. Only a genuinely malformed request (an unknown workflow or step) is reported as an MCP tool error (`isError: true`), and even then the structured content lists the server's real inventory (`available_workflows` or `available_steps`) as a correction, not just "no."

`execute_step` also takes an optional `idempotency_key`. Without one, calling it twice for the same step commits twice, which is the real, empirically-reproduced bug this closes: an MCP or A2A client that times out waiting for a response and retries the exact same call, a documented, expected pattern for agent tool calls, would silently duplicate the step in the trace's audit log even though the safety property still held. With a key, a retry that reuses it gets back the exact original outcome (success or the same blocked reason) instead of being recomputed or re-committed, `replayed: true` tells the caller which happened. The cache lives on `ai/verify.Trace` itself (`CheckAndCommitIdempotent`), guarded by the same lock `CheckAndCommit` already uses, so a concurrent retry racing the original request cannot double-commit either, and is capped at 256 keys per trace (`maxIdempotencyKeysPerTrace`), evicting the oldest first, the same bounded-memory reasoning as `runbookstore.DefaultMaxTraces` applied to the per-trace cache instead of the store's trace map. Omitting the key is unaffected: `CheckAndCommit` still exists and behaves exactly as before.

Verified over the real MCP wire protocol, not just as Go function calls: `tools/mcpserver/server_test.go` uses `mcp-go`'s in-process client (`client.NewInProcessClient`) to actually initialize a session and call all three tools, including a trace-isolation test (two `trace_id`s must not leak state into each other), a registration-time rejection test (a workflow with an unreachable rollback state is refused before it can ever be served), coverage for the blocked/unknown-workflow/unknown-step structured shapes, and the retry-does-not-duplicate proof for `idempotency_key`.

### A2A Agent  -  `tools/a2aagent`

One skill, `execute_step`, delegated as an A2A task against the same `tools/runbookstore.Store` the MCP server uses. Task lifecycle exactly as specified: `submitted` → `working` → (`input-required` if the barrier blocks it, `completed` if it doesn't, `failed` if the request itself was malformed). A blocked step goes to `input-required`, not `failed`, deliberately: the task is well-formed and could still succeed once its precondition is met, `failed` would incorrectly say it never can. The `input-required` message carries a `DataPart` with the same `blocked_by` / `missing_state` / `established_by_steps` / `replayed` fields the MCP path returns, alongside the human-readable text, so an A2A caller gets the identical structured signal an MCP caller would rather than only a string to parse. A `failed` task (unknown workflow or step) names the server's actual registered workflows or steps in its message text as a correction. The delegating message's `DataPart` accepts the same optional `idempotency_key`, and the completed task's artifact carries `replayed` too, so a retried task delegation is exactly as safe as a retried MCP tool call, both protocols share the one `Trace.CheckAndCommitIdempotent` cache.

**One correction to the brief this was built from**: the A2A spec (and the SDK) serves the agent card at `/.well-known/agent-card.json`, not `/.well-known/agent.json`. `tools/a2aagent/http.go` wires it up at the SDK's actual constant (`a2asrv.WellKnownAgentCardPath`), not the brief's guessed path, noted explicitly in `tools/a2aagent/agent.go`'s package doc so the discrepancy isn't silently papered over.

**Interoperability, proven, not asserted**: `Test_A2A_MCP_ShareTrace` in `tools/a2aagent/agent_test.go` commits two steps directly against a shared trace, then sends a third step over the real A2A HTTP+JSON-RPC wire protocol (via `httptest.Server` and the real `a2aclient`), and confirms it completes because the trace already reflects the earlier commits. MCP and A2A are reading and writing the same state, not two disconnected demos that happen to share a name.

A real bug this test caught, worth naming because it's the kind of thing that only shows up when you actually run the protocol instead of reading its spec: the A2A SDK's default task store gob-encodes artifact data internally, and gob refuses to encode a named type (`[]verify.StepID`) hidden inside an `any`-typed map value without explicit registration. Fixed by flattening to `[]string` before handing data to the SDK (`tools/a2aagent/agent.go`), not by working around the test.

**Zero-knowledge encryption over OPFS-stored data**, as the brief also asked for: not implemented in this pass. Both `tools/mcpserver` and `tools/a2aagent` are server-side packages with no OPFS involvement at all (see Phase 2). Applying client-side encryption to the *demo's* OPFS data is the scope already covered honestly in [Strategic Architecture & Investor Moat](STRATEGIC_ARCHITECTURE_AND_MOAT.md) §2.2, a real, concrete, unbuilt spec, not duplicated here.

---

## Phase 4: The Verification Engine  -  `ai/verify`

### What this actually is, stated before what it does

The brief asked for a "Neuro-Symbolic Temporal Verification Engine" combining "PhD-level AI research" with "Linear Temporal Logic / Probabilistic Model Checking." What got built is a **explicit-state safety and reachability checker over a finite workflow graph**, real, correct, and tested, but a materially smaller and more honest claim than that framing:

- **No neural component.** Nothing here parses natural language into the workflow graph; `verify.Workflow` is constructed directly from typed Go structs. Turning a natural-language SOP into that graph via an LLM is a real, separate, unbuilt project (prompt an LLM to extract steps/preconditions/postconditions, then have a human or this package's own registration-time `VerifyReachability` check catch what it got wrong), not attempted here.
- **No LTL formula parser, no Büchi automaton construction, no probabilistic model checking.** This checks two specific, well-established property *patterns*, not general temporal logic. Safety checking is the "P is preceded by Q" pattern from Dwyer, Avrunin, and Corbett's property specification patterns (*Patterns in Property Specifications for Finite-State Verification*, ICSE 1999), a real, citable formal-methods reference, checked directly against an accumulated state set rather than compiled into a temporal formula. Reachability checking is plain graph reachability (a fixed-point/BFS-style walk, see `ai/verify/reachability.go`).

For a finite runbook with an enumerable set of states, this is the right level of formalism: correct, small enough to verify by reading the ~150 lines of source, and it caught a real bug in its own test fixture while being written (see below), which is better evidence of correctness than a bigger, unverifiable claim would have been.

### The mechanism, concretely

```go
type State string   // a named fact, e.g. "backup_validated"
type StepID string   // a step in the runbook

type Step struct {
    ID          StepID
    Requires    []State // precondition
    Establishes []State // postcondition
}

type SafetyRule struct {      // "Forbidden never happens without Requires first"
    Name      string
    Forbidden State
    Requires  State
}

type ReachabilityRule struct { // "Target must stay reachable from everywhere"
    Name   string
    Target State
}
```

`Workflow.CheckSafety(trace, next)` is the barrier certificate: called before a step executes, never after. `Workflow.VerifyReachability()` is a static check run once at registration time in `tools/runbookstore.Store.RegisterWorkflow`, a workflow whose rollback path can dead-end is refused registration entirely, it never gets the chance to fail mid-incident.

### The exact scenario from the brief, as a passing test

```go
safety := []SafetyRule{
    {Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
}
reachability := []ReachabilityRule{
    {Name: "rollback-always-reachable", Target: "rollback_complete"},
}
```

`ai/verify/verify_test.go` proves, over this exact fixture: dropping prod is blocked with no prior trace, allowed once a backup is taken and validated, and specifically catches an agent that takes a backup but skips validation before attempting the drop, the literal hallucination-of-a-completed-precondition failure mode the brief named.

### A real limitation, found by testing this, not by inspection

While writing the test fixture above, `VerifyReachability` correctly rejected it: `prod_db_dropped` had no path back to `rollback_complete`, because no step's precondition was `prod_db_dropped` alone. The fix was a real modeling correction (add `restore_from_backup_post_drop`, requiring `prod_db_dropped` directly), documented in detail in `ai/verify/reachability.go`'s doc comment: this checker verifies reachability **per individual state**, not over the full powerset of states that might simultaneously hold in a real trace. A state that never appears as any step's precondition is a dead end under this check even if, in reality, it always co-occurs with some other state that does have a path forward. Every state that must stay "exitable" needs its own explicit step, relying on a concurrently-true state to carry the path forward for it won't be seen. Full conjunctive-state (powerset) reachability would close this gap, at the cost of real state-space explosion risk for larger workflows, a deliberate tradeoff for this package's target (finite runbooks), not an oversight.
