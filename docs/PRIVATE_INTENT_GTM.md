# Private Intent: customer and revenue plan

Nothing here is a forecast. There are no Private Intent customers, no measured demand and no revenue. Every number below is either arithmetic or a target to test.

## Who it is for first

Platform and DevOps teams of roughly 5 to 50 engineers that already let an AI agent touch infrastructure (CI/CD, Kubernetes, cloud operations, database maintenance) and have a person who would be blamed if it deleted something. Their sentence is: "We want the automation, but we cannot let an agent take a dangerous action without a control we can show to someone."

Not the first customer: enterprises that need SSO, a key management service and compliance exports before they will try anything. Those are listed as not built, so selling to them first would mean selling a roadmap.

## Messages by reader

| Reader | They care about | Say this |
| --- | --- | --- |
| Team running agent swarms | Fast parallel runs they can still trust | "Give each agent a signed contract for its task. Anything outside it is blocked and logged, and a finding that quotes text that is not in the file is rejected. You read the agents that drifted first." |
| Engineering lead | Shipping agents without a Friday-night incident | "Your agent can propose anything. It can only delete production with a backup attested by the backup system and your owner's approval, checked where the credentials live." |
| Security team | Evidence, not promises | "Every approval is signed, bound to one action, single use and revocable. Receipts name the policy by digest, so you can tell if the rules changed." |
| DevOps engineer | Not another dashboard | "A Go package and one MCP tool. Run the example in five minutes. The rules are a file you can read." |
| Enterprise buyer | Risk and audit | "Open core you can inspect today. Identity provider and key management integrations are on the roadmap and not available yet." |

## Pitch (30 seconds)

AI agents are starting to run production tasks, and a prompt is not a control. Joltrin Private Intent puts your operating rules outside the model. The agent asks, Joltrin says which prerequisite is missing, other systems sign the proof and a named person signs the approval, and the system holding the credentials runs the action only with a receipt for that exact action. It is open source, it runs locally, and your private wording never leaves your vault. Try it in the browser in under a minute.

## Live demo script (about 60 seconds)

1. Open `/private-intent/`. Say: "This agent wants to delete a production database. It is all simulated." (5s)
2. Press 1. The banner says Blocked: verified backup and owner approval required. Say: "It names what is missing. The agent did not get to argue." (10s)
3. Press 2, then 1. Banner says approval required. Say: "The backup system signed, not the agent. Now it needs a person." (10s)
4. Press 3, then 1. Allowed, receipt verified. Open Developer view. Say: "The receipt names this action and this policy. The executor checks it." (15s)
5. Press 1 again. Approval required again. Say: "One approval, one action." (5s)
6. Press 3, then Revoke, then 1. Still blocked. Say: "Revoke and it stops." (10s)
7. Point at the two panels: "This side has your wording. The verifier side has an id and a fingerprint." (5s)

## Funnel and what to measure

| Step | Where it happens today | Measured with | Honest status |
| --- | --- | --- | --- |
| 1. Discover | Homepage callout, `/private-intent/` | Plausible: page views | Plausible must be connected to the joltrinhq.com domain for events to count |
| 2. Try without an account | The demo | `intent_attempt` | Built |
| 3. See a block | The demo | `intent_attempt` followed by `intent_backup` | Built |
| 4. Understand what is missing | Banner text | none | Built, qualitative |
| 5. Integrate | `go run ./examples/private_intent` | `docs_click`, `github_click`, later repo clones | Built, not measured |
| 6. Hit the single-owner limit | Teams section on the page | `intent_pro_click` | Built |
| 7. Request Pro | Existing Request Pro flow | `pro_click`, requests received | Existing flow |
| 8. Design-partner onboarding | Manual | Calendar and notes | Not a product |
| 9. Production milestone | Agreed per customer | See below | Not started |
| 10. Recurring revenue | Stripe checkout | Paid subscriptions | Existing checkout, delivery manual |

Measure activation as "ran the example against a real MCP client and got a blocked response", which a visit count cannot show. The open question is how to see it without telemetry in the open-source binary. Ask in the onboarding call instead of adding tracking.

Production milestone for a design partner, proposed: one agent in their real workflow runs a destructive operation only through the Guard for two weeks, with at least one real block that the team agrees was correct.

## Packaging and price

The spec proposes Pro at $49 per team per month. What can be said from evidence:

- A card payment of $49 loses about 2.9% plus $0.30, roughly $1.72, at the standard published US rate (check current Stripe pricing). Net is about $47.
- Infrastructure adds nothing: the demo is static WebAssembly on GitHub Pages and the library runs in the customer's process. The existing Azure app has a $25 monthly budget alert and is not involved.
- The cost is people. Shared team rules, approval routing and a team view are not built, so Pro today is delivered by hand. If one onboarding takes a few hours, a $49 subscription does not cover it for many months. The earlier commercial review of this repository reached the same conclusion on the existing Pro offer.

Recommendation, to be tested and not assumed: sell the first three to five teams as paid design partners (a setup fee plus a monthly fee, set after the first discovery calls tell you what they value), keep $49 per team as the list price for the self-serve plan once shared policy and approval routing are built, and do not market self-serve Pro before then. The Teams section on the page already says "design partners, not self-serve yet".

Free stays safe: the engine, the Guard, signed approvals and revocation are all free. Nothing the free tier needs to be secure is held back for Pro.

## Customer discovery plan (first 30 days)

Week 1: list 30 teams to talk to from places where this problem is discussed (agent and MCP communities, platform engineering forums, people who have written about agents deleting data), and send a short, specific note asking for 20 minutes. Do not pitch in the note. Prepare five questions:

1. What do your agents touch in production today, and what do you block by hand?
2. Tell me about the last time an agent did something you did not expect.
3. Who approves a risky change today, and how does the agent know?
4. What would you need to see to let an agent run one destructive task?
5. What are you using for this now, and what is wrong with it?

Weeks 2 and 3: hold the calls. Run the demo only after question 5. Record, for each team, the exact operation they would gate first and who would approve it. Ask for a 30-minute pairing session to wire the example into one of their tools.

Week 4: choose one to three teams that have a specific operation, a named approver and a willingness to pay for setup. Agree the production milestone and the price in writing. Decide whether to continue based on this test: at least three teams, unprompted, name an operation they would gate this month. If fewer than three do, the problem or the framing is wrong, and the next step is more calls, not more features.

## Onboarding sequence for a design partner

1. Call: pick one operation, one approver, one evidence source (for example the backup job).
2. Day 1: generate keys for the evidence source and the approver, write the rule, run the example locally.
3. Day 2 to 3: put the Guard in the one executor that holds the credentials, and remove the agent's direct credentials for that operation.
4. Week 1: review every block and allow together. Fix rules that were too strict or too loose.
5. Week 2: agree whether the milestone was met.

## What would change this plan

If teams say they already get this from their gateway (the Cedar-based gateways and Permit's step-up approvals do much of the policy half), lead with the parts they do not have: evidence from systems the agent cannot influence, single-use approvals bound to the action, and the executor-side receipt check. If they do not use agents on infrastructure yet, there is no urgency to sell into.
