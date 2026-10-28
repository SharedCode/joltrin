# How Joltrin compares to policy engines and workflow tools

Joltrin answers one question: has the work that must happen before this action actually been recorded, and by something allowed to record it? If an existing tool already answers your question, use that tool. This page says where that is likely.

Statements about other projects describe their documented design, not a test run by this repository. Check their current documentation before deciding.

## What Joltrin does

A runbook lists steps and the states each step needs. Something trusted records steps for a trace. When an agent or a webhook asks to run a step, Joltrin refuses it unless the states it needs are already in that trace, and the refusal names the missing state. See [Agent protocols](AGENT_PROTOCOLS.md) and [Kubernetes admission gate](KUBERNETES_GATE.md).

Joltrin has two parts that differ in what they prove:

- **The verification barrier** (MCP server, A2A, Kubernetes gate) checks recorded steps. It does not check that a backup happened, only that something holding the commit token said so. The Kubernetes gate keeps its state in memory, on one replica.
- **Private Intent** adds Ed25519-signed evidence and approvals, approvals bound to the exact action and single use, expiry, revocation, and a signed receipt that an in-process `Guard` checks before running the action. See [Private Intent](PRIVATE_INTENT.md), which lists the tests behind each claim and its limits. Its used-nonce set and approval store are in memory, so a restart forgets them. The default receipt lifetime is 60 seconds.

Neither part executes the operation for you or confirms it finished. Enforcement exists only where an integration calls it, such as the admission webhook, or an MCP tool whose handler runs the operation inside `Guard.Run`.

## Comparison

| | Joltrin | OPA / Gatekeeper | Kyverno | Cedar | Workflow engines (Temporal, Argo) |
|---|---|---|---|---|---|
| Main job | Refuse a step until earlier steps are recorded | General policy decisions over input and data | Kubernetes-native policies as YAML | Authorization: may this principal do this action on this resource | Run steps in order and retry them |
| Where it enforces | Where you call it: MCP tool, admission webhook | Anywhere you call it. Gatekeeper is the Kubernetes admission integration | Kubernetes admission | Anywhere you call it | Inside the engine, because it runs the steps |
| Holds history of earlier steps | Yes, a recorded trace per run | Not by itself. History has to be supplied as input or data | Can look up cluster objects, not a run history | No. Evaluation is stateless | Yes, it owns the workflow state |
| Checks who produced evidence | Barrier: a shared commit token. Private Intent: Ed25519 signatures | Can verify signed tokens in policy, if you write that | Can verify image signatures and attestations | Not part of the language | Not its purpose |
| Approval bound to one exact action | Private Intent: yes, single use. Barrier: no | Possible if you encode it in your policy and data | Possible for Kubernetes objects | Natural fit, a request names principal, action and resource | Signals and approvals are supported, bound by your workflow |
| Replay protection, expiry, revocation | Private Intent: yes, with in-memory state. Barrier: no | You build it | You build it | You build it | Partly, through workflow design |
| Maturity | Young project | Widely used, large community | Widely used | Newer, with published formal analysis work | Widely used |

## Where Joltrin is weaker today

- Policy engines are mature, reviewed and widely deployed. Joltrin is not.
- The verification barrier, including the Kubernetes gate, has no signed evidence, no action-bound approvals, no expiry, no replay protection and no revocation. A commit token is its whole trust boundary. Those controls exist in Private Intent, not yet in the gate.
- Private Intent keeps used nonces and approvals in memory, has no transparency log, and a host that can edit its vault and engine can change policy.
- The Kubernetes gate is a single replica with in-memory state.
- Enforcement is only as complete as the places you route through it.

## Where it may be worth using

- You want an agent told exactly which prior step is missing, in a form the agent can act on, without writing the policy and the state tracking yourself.
- Your rule is about order ("validate the backup before dropping the database") and not about who may do what.
- You can give the recording of steps to a different job than the agent being controlled.

OPA with external data, or a Cedar check plus your own step table, can express the same rule. Joltrin's case is that the trace and the refusal message come built in, not that the logic is impossible elsewhere.

## When not to use it

- You need an audited, widely adopted authorization layer. Use OPA, Kyverno or Cedar.
- You need steps run, retried and resumed. Use a workflow engine.
- You need the Kubernetes gate to reject forged evidence. It trusts anyone holding the commit token. Private Intent signs evidence, but it is not wired into the gate.
- You need highly available enforcement on Kubernetes today.
