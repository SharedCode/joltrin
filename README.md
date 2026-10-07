<div align="center">

<img src="docs/assets/joltrin-org-logo.jpg" alt="Joltrin logo" width="160" />

# Joltrin

**Independent, deterministic verification for AI agent actions.**

[joltrinhq.com](https://joltrinhq.com/) · [Live barrier demo](https://joltrinhq.com/agents/) · [Docs](https://joltrinhq.com/docs/) · [Arena](https://joltrinhq.com/arena/)

[![CI](https://github.com/SharedCode/joltrin/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/SharedCode/joltrin/actions/workflows/ci.yml)
[![Go Tests](https://github.com/SharedCode/joltrin/actions/workflows/go.yml/badge.svg?event=push&branch=master)](https://github.com/SharedCode/joltrin/actions/workflows/go.yml)
[![codecov](https://codecov.io/gh/SharedCode/sop/branch/master/graph/badge.svg)](https://app.codecov.io/github/SharedCode/sop)
[![Release](https://img.shields.io/github/v/release/SharedCode/joltrin)](https://github.com/SharedCode/joltrin/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/sharedcode/joltrin/v5.svg)](https://pkg.go.dev/github.com/sharedcode/joltrin/v5)
[![License](https://img.shields.io/github/license/SharedCode/joltrin)](LICENSE)

</div>

Joltrin (formerly SOP) is an open-source Go library that checks an AI agent's action against the recorded trace before the action commits. The agent proposes. A separate verifier decides, using fixed preconditions and the steps the server has actually recorded. It does not use the agent's claims, its prompt context, its self-evaluation, or its confidence.

**The problem.** An agent can propose a destructive action and report success from its own context. If the same agent also judges the action safe, both judgments can fail for the same reason. That correlated failure is the production risk, and asking the agent to be careful does not remove it.

**What Joltrin does.** The agent emits intent. Joltrin checks the intent against the trace and the runbook's rules, outside the agent's context window. The check and the commit are one call, so a step is recorded only if it passed. A step whose required state is missing is refused. A claim that something happened is not evidence that it happened.

<p align="center">
  <a href="https://youtu.be/F0jYkBHJluI">
    <img src="docs/assets/joltrin-demo.gif" alt="Joltrin demo. Click to watch the video." width="760" />
  </a>
</p>

<table align="center">
  <tr>
    <td align="center"><a href="https://www.youtube.com/watch?v=F0jYkBHJluI"><img src="https://img.youtube.com/vi/F0jYkBHJluI/hqdefault.jpg" alt="Joltrin full demo on YouTube" width="360" /></a><br /><sub>Full demo</sub></td>
    <td align="center"><a href="https://www.youtube.com/shorts/oS1bfq_ZDkg"><img src="https://img.youtube.com/vi/oS1bfq_ZDkg/hqdefault.jpg" alt="Joltrin short on YouTube" width="200" /></a><br /><sub>Short</sub></td>
  </tr>
</table>

## A blocked step, then the safe order

A production database drop is blocked until the backup is taken and validated:

```text
drop_prod_db      blocked, backup_validated is missing
validate_backup   blocked, backup_taken is missing
take_backup       allowed
validate_backup   allowed
drop_prod_db      allowed
```

The blocked results are normal payloads, not exceptions, and they tell the agent what to do next:

```json
{
  "blocked_by": "precondition",
  "missing_state": "backup_validated",
  "established_by_steps": ["validate_backup"]
}
```

Read a block as a replan signal. Retrying the same call with different parameters will not change the answer. Repeated blocks also carry `attempts` and `next`, which says whether to run the establishing steps or stop and ask. With memory turned on, the server counts which rules block runs and how many of those runs recover, so you can see whether a rule is catching bad plans or is too strict.

## Try it in five minutes

```bash
go run github.com/sharedcode/joltrin/v5/examples/verify_barrier@latest   # the barrier blocks a database drop until a backup is validated
go run github.com/sharedcode/joltrin/v5/examples/agent_team@latest       # Jira, Grafana, AWS, and PagerDuty agents finish tasks behind the barrier
go run github.com/sharedcode/joltrin/v5/examples/quickstart@latest       # the embedded B-Tree that holds the state
```

No clone needed, only Go 1.26.8 or newer. The first run downloads the modules and compiles, which took about 10 seconds on a MacBook Air, and later runs are quick. To read the code, or to run `./scripts/demo.sh --memory`, clone the repo.

Or skip the install. These run in your browser with no backend:

| Live experience | What you do |
| :--- | :--- |
| [Agent barrier](https://joltrinhq.com/agents/) | Try to drop a database before the backup is validated and watch the barrier refuse. It runs the real checks, compiled to WebAssembly. |
| [Technical demo](https://joltrinhq.com/) | Run ACID transactions, vector search, and an agent checkpoint resume on the WASM build of the engine. |
| [Arena](https://joltrinhq.com/arena/) | Crash storage nodes and spike load in a cluster simulation. It illustrates the concepts, it is not a live cluster. |

## Connect it to an MCP agent

Any agent that supports MCP can use the barrier. One command downloads a binary of about 6 MB (no Go needed), checks its checksum, and registers it with Claude Code, Codex, and the Gemini CLI, whichever are installed. It takes a few seconds and does not use `sudo`:

```bash
curl -fsSL https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.sh | sh
```

The script, the binary and the checksums all come from the same GitHub release. The checksum catches a damaged or swapped download, not a compromised release, so if that matters to you, set `JOLTRIN_VERIFY=1`. It also verifies the binary's signed build provenance with the GitHub CLI (`gh`, signed in), which does not depend on the checksum file, needs a release from v5.11.0 on, and adds a few seconds. You can also read [`scripts/install.sh`](scripts/install.sh) first, install a specific release with `JOLTRIN_VERSION=v5.11.0`, or skip registering with `JOLTRIN_NO_SETUP=1`. To avoid the script, use the steps below or build from source.

<details>
<summary>By hand, on Windows, or with Go</summary>

**By hand.** Pick the file for your machine from the [latest release](https://github.com/SharedCode/joltrin/releases/latest): `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`, or a `windows-*.exe`. On macOS or Linux:

```bash
os_arch=darwin-arm64   # or darwin-amd64, linux-amd64, linux-arm64
base=https://github.com/SharedCode/joltrin/releases/latest/download
curl -fsSLO "$base/sop-mcp-server-$os_arch" -O "$base/sop-mcp-server-SHA256SUMS"
shasum -a 256 -c --ignore-missing sop-mcp-server-SHA256SUMS   # on Linux: sha256sum -c --ignore-missing
mkdir -p "$HOME/.joltrin/bin" && mv "sop-mcp-server-$os_arch" "$HOME/.joltrin/bin/sop-mcp-server"
chmod +x "$HOME/.joltrin/bin/sop-mcp-server"
"$HOME/.joltrin/bin/sop-mcp-server" setup --apply
```

Move the file somewhere it will stay before running `setup`, because it registers the binary by that path. On Windows, download the `.exe` and the checksum file from the same page and run `.\sop-mcp-server-windows-amd64.exe setup --apply` from the folder you keep it in.

**With Go.**

```bash
go install github.com/sharedcode/joltrin/v5/cmd/sop-mcp-server@latest
"$(go env GOPATH)/bin/sop-mcp-server" setup --apply
```

The first `go install` downloads the Go modules, which took about 15 seconds on a MacBook Air. If your Go is older than 1.26.8, Go also downloads that toolchain once, about 240 MB.

</details>

`setup --apply` registers the server using the binary's full path, which avoids the "Executable not found" failure you get when a folder is not on the `PATH` your agent starts with. It can be run again, for example after an upgrade. Run `setup` without `--apply` to see the commands first.

Then tell your agent: "Use the joltrin tools to run `drop_prod_db` on workflow `db-maintenance` with trace id `t1`." The server refuses until `take_backup` and `validate_backup` have run in that trace, whatever the agent claims. A recorded run with real agents, and what it does not prove, is in [docs/AGENT_BARRIER_TESTS.md](docs/AGENT_BARRIER_TESTS.md). The same check also gates an A2A agent, described in [docs/AGENT_PROTOCOLS.md](docs/AGENT_PROTOCOLS.md).

### Memory and your own runbooks

Add `--lessons <folder>` to `setup` and the server remembers what blocked in earlier runs and tells the next agent. Add `--runbooks <file>` and it enforces your own steps and safety rules from a JSON file instead of the example. Both are optional, and the barrier still checks every call. The details are in [Run the server with memory and your own runbooks](docs/AGENT_PROTOCOLS.md#run-the-server-with-memory-and-your-own-runbooks).

### Agents that hand off work

Jira, Grafana, AWS, and PagerDuty agents hand off work, and every call passes three checks first: the tool is on that agent's allowlist, any claim matches evidence a tool returned, and the steps it depends on have committed (`verify`). The tools are stubs and the checks are real.

<p align="center">
  <img src="docs/assets/agent-team-pagerduty.gif" alt="Terminal recording: PagerDuty, Grafana, and AWS agents resolve an incident while the barrier blocks a skipped step, a made-up number, and out-of-scope calls" width="900" />
</p>

Run it with `./scripts/demo.sh --team`, or watch it replay on [joltrinhq.com](https://joltrinhq.com/#agent-team). When a step is blocked on order, the output includes a `result:` line: the structured block an MCP agent gets back from `execute_step`, with the rule, the missing state, and the steps that would establish it. The tools are stubs and the checks are real. Source: [examples/agent_team](examples/agent_team/main.go).

## What is verified, simulated, and not claimed

| Status | What |
| :--- | :--- |
| Implemented, with tests | The check runs before commit. It uses trace state, not the agent's assertion. Blocked calls return structured results. MCP and A2A share one `verify` package, and the same package compiles to WebAssembly for the browser demo. |
| Demonstrated | Recorded runs with real agents, on one runbook over MCP, in small samples. The write-up lists the limits. |
| Simulated | The tools in the team demo, and the Arena. They show the checks and the concepts, not live systems. |
| Not claimed | Production deployments, customers, third-party benchmarks, or scale. [docs/INVESTORS.md](docs/INVESTORS.md) lists what has and has not been proven. |

**Limits.** The barrier only answers, so whatever performs the real action has to wait for that answer, or an agent can skip it. The `trace_id` names a run and the caller chooses it, so issue one per run. The barrier cannot see facts the trace does not contain. A backup in another cloud account has to be represented by a step you trust, such as one that checks that account and records the result. The barrier does not judge whether an agent's own claim is true. A dry-run mode, where blocked calls are logged and allowed so teams can tune rules before enforcing them, is a possible direction and is not implemented.

## The engine underneath

The verifier sits on an embedded, ACID-compliant B-Tree storage engine, so the trace and the agent's state share one transaction boundary. The same library gives you crash-safe agent memory that another worker can resume, vector search stored next to structured data, Reed-Solomon erasure coding, and swarm task coordination. It is for engineers building agent systems or local-first apps, and for teams running Redis, a queue, and Postgres only to keep one application's state durable. Benchmarks, with their limits, are in [docs/BENCHMARKS.md](docs/BENCHMARKS.md). The long version is in [docs/WHY_JOLTRIN.md](docs/WHY_JOLTRIN.md) and [docs/SOP_ARCHITECTURE_WHITEPAPER.md](docs/SOP_ARCHITECTURE_WHITEPAPER.md).

## Install the library

| Language | Command |
| :--- | :--- |
| Go | `go get github.com/sharedcode/joltrin/v5` |
| Python | `pip install sop4py` |
| C# | `dotnet add package Sop` |
| Container | `docker run ghcr.io/sharedcode/joltrin-quickstart:stable` |

Java and Rust bindings exist in the repo and are not published yet. Version pinning, the note on the old SOP package names, and the full list are in [docs/PACKAGES.md](docs/PACKAGES.md).

## Open core and plans

The engine, vector search, agent memory, and the verification barrier are MIT licensed and stay free. Paid tiers (Pro, and Enterprise on request) add governance on top. Prices and setup are in [docs/MONETIZATION_AND_TIERS.md](docs/MONETIZATION_AND_TIERS.md).

## Documentation

- The barrier: [Agent protocols (MCP, A2A, memory, runbooks)](docs/AGENT_PROTOCOLS.md), [Barrier test results](docs/AGENT_BARRIER_TESTS.md), [Verification engine](docs/MCP_A2A_AND_VERIFICATION_ENGINE.md)
- Start here: [Getting started](docs/GETTING_STARTED.md), [Examples](docs/EXAMPLES.md), [What is Joltrin](docs/WHAT_IS_SOP.md)
- Concepts: [Why Joltrin](docs/WHY_JOLTRIN.md), [Architecture](docs/SOP_ARCHITECTURE_WHITEPAPER.md), [Scalability](docs/SCALABILITY.md)
- Operating it: [Operations and failover](docs/OPERATIONS.md), [Data Manager and tools](docs/SOP_PLATFORM_TOOLS.md), [Azure deployment](infra/azure/README.md), [Kubernetes with Argo CD](deploy/aks/README.md)
- Reference: [Benchmarks](docs/BENCHMARKS.md), [Live demos](docs/LIVE_DEMOS.md), [Roadmap and platform support](docs/ROADMAP.md), [Who it is for](docs/WHO_IS_IT_FOR.md), [Investor notes](docs/INVESTORS.md)

## Contributing

Run `go test ./...` and `gofmt` before opening a pull request, and include tests with your change. See [CONTRIBUTING.md](.github/CONTRIBUTING.md) and [SECURITY.md](.github/SECURITY.md). Questions and ideas go to [GitHub Discussions](https://github.com/SharedCode/joltrin/discussions).

## Releases

See the [changelog](CHANGELOG.md) and the [releases page](https://github.com/SharedCode/joltrin/releases). Maintainers cut releases with [RELEASE_PROCESS.md](RELEASE_PROCESS.md).

<p align="center">
  <sub>MIT License. Built by <a href="https://github.com/sharedcode">SharedCode</a>.</sub>
</p>
