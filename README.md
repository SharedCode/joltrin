<div align="center">

<img src="docs/assets/joltrin-org-logo.jpg" alt="Joltrin logo" width="160" />

# Joltrin

**Durable memory and a verification barrier for AI agents, in one embedded Go library.**

[joltrinhq.com](https://joltrinhq.com/) · [Technical demo](https://joltrinhq.com/) · [Arena](https://joltrinhq.com/arena/) · [Agent barrier](https://joltrinhq.com/agents/)

[![CI](https://github.com/SharedCode/joltrin/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/SharedCode/joltrin/actions/workflows/ci.yml)
[![Go Tests](https://github.com/SharedCode/joltrin/actions/workflows/go.yml/badge.svg?event=push&branch=master)](https://github.com/SharedCode/joltrin/actions/workflows/go.yml)
[![codecov](https://codecov.io/gh/SharedCode/sop/branch/master/graph/badge.svg)](https://app.codecov.io/github/SharedCode/sop)
[![Release](https://img.shields.io/github/v/release/SharedCode/joltrin)](https://github.com/SharedCode/joltrin/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/sharedcode/joltrin/v5.svg)](https://pkg.go.dev/github.com/sharedcode/joltrin/v5)
[![License](https://img.shields.io/github/license/SharedCode/joltrin)](LICENSE)

</div>

Joltrin (formerly SOP) is an ACID-compliant B-Tree storage engine that runs inside your process. For AI agents it provides three things in one library: memory that survives a crash and can be resumed by another worker, vector search stored next to structured data in the same transaction, and a verification barrier that blocks a risky action until its preconditions are proven.

**Who it is for.** Engineers building agent systems, edge or local-first apps, and teams running Redis, a queue, and Postgres only to keep one application's state durable.

**Why it matters.** An agent that can call tools needs more than a good prompt. It needs state that survives a failure and a check that runs before the action, not after. Joltrin puts both in the same process and the same transaction boundary, so there is no network hop and no separate service to operate.

<p align="center">
  <a href="https://youtu.be/F0jYkBHJluI">
    <img src="docs/assets/joltrin-demo.gif" alt="Joltrin demo. Click to watch the video." width="760" />
  </a>
</p>

## Try it in five minutes

```bash
git clone https://github.com/sharedcode/joltrin.git && cd joltrin
go run ./examples/quickstart      # ordered B-Tree with point, range, and descending scans
./scripts/demo.sh --barrier       # the barrier blocks a database drop until a backup is validated
./scripts/demo.sh --memory        # an agent crashes mid-task and a peer resumes from the B-Tree
./scripts/demo.sh --team          # Jira, Grafana, AWS, and PagerDuty agents finish tasks behind the barrier
```

Or skip the install. These run entirely in your browser with no backend:

| Live experience | What you do |
| :--- | :--- |
| [Technical demo](https://joltrinhq.com/) | Run ACID transactions, vector search, and an agent checkpoint resume on the WASM build of the engine. |
| [Agent barrier](https://joltrinhq.com/agents/) | Try to drop a database before the backup is validated and watch the barrier refuse. |
| [Arena](https://joltrinhq.com/arena/) | Crash storage nodes and spike load in a cluster simulation. It illustrates the concepts, it is not a live cluster. |

## Test it with your own AI agent

Any agent that supports MCP can use the barrier. You can download a binary of about 6 MB, or build it with Go.

**Download (no Go needed).** Pick the file for your machine from the [latest release](https://github.com/SharedCode/joltrin/releases/latest): `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`, or a `windows-*.exe`. On macOS or Linux:

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

**Build with Go.**

```bash
go install github.com/sharedcode/joltrin/v5/cmd/sop-mcp-server@latest
"$(go env GOPATH)/bin/sop-mcp-server" setup --apply
```

`setup --apply` registers the server with Claude Code, Codex, and the Gemini CLI, whichever are installed, using the binary's full path. That avoids the "Executable not found" failure you get when Go's bin folder is not on your `PATH`. Run `setup` without `--apply` to see the commands first.

The first `go install` downloads the Go modules. If your Go is older than 1.26.8, Go also downloads that toolchain once, about 240 MB, so the first run takes a few minutes. Later installs are quick. The download above skips all of that.

Then tell your agent: "Use the joltrin tools to run `drop_prod_db` on workflow `db-maintenance` with trace id `t1`." The server refuses until `take_backup` and `validate_backup` have run in that trace, whatever the agent claims. A recorded run with real agents, and what it does not prove, is in [docs/AGENT_BARRIER_TESTS.md](docs/AGENT_BARRIER_TESTS.md).

Two limits to know. The `trace_id` names a run and the caller chooses it, so issue one per run in your integration. And the barrier only answers: whatever performs the real action has to wait for that answer, or an agent can skip it.

### Memory and your own runbooks

Add `--lessons <folder>` to `setup` and the server remembers what blocked in earlier runs and tells the next agent. Add `--runbooks <file>` and it enforces your own steps and safety rules from a JSON file instead of the example. Both are optional, and the barrier still checks every call. The details are in [Run the server with memory and your own runbooks](docs/AGENT_PROTOCOLS.md#run-the-server-with-memory-and-your-own-runbooks).

## Agents that hand off work

Jira, Grafana, AWS, and PagerDuty agents hand off work, and every call passes three checks first: the tool is on that agent's allowlist, any claim matches evidence a tool returned, and the steps it depends on have committed (`verify`).

<p align="center">
  <img src="docs/assets/agent-team-pagerduty.gif" alt="Terminal recording: PagerDuty, Grafana, and AWS agents resolve an incident while the barrier blocks a skipped step, a made-up number, and out-of-scope calls" width="900" />
</p>

Run it with `./scripts/demo.sh --team`, or watch it replay on [joltrinhq.com](https://joltrinhq.com/#agent-team). When a step is blocked on order, the output includes a `result:` line: the structured block an MCP agent gets back from `execute_step`, with the rule, the missing state, and the steps that would establish it. The tools are stubs and the checks are real. Source: [examples/agent_team](examples/agent_team/main.go).

## What is verified

- **Latency.** About 6.9 microseconds per B-Tree write or read, over 140,000 ops/sec with WAL logging, from the repo's own harness on a 2015 dual-core MacBook Pro. Reproduce it with `go run ./tools/benchmark`. Details and limits are in [docs/BENCHMARKS.md](docs/BENCHMARKS.md).
- **Correctness.** The race detector runs on the core engine packages in CI, `govulncheck` runs on every push, and the build and unit tests run on Linux, macOS, and Windows.
- **Agent safety.** `verify` is served over both MCP and A2A, and the same barrier compiles to WebAssembly for the browser demo. See [docs/AGENT_PROTOCOLS.md](docs/AGENT_PROTOCOLS.md).
- **Packages.** Published on PyPI (`sop4py`) and NuGet (`Sop`), plus the Go module.

There are no documented production deployments or paying customers yet, and no third-party benchmarks. [docs/INVESTORS.md](docs/INVESTORS.md) lists what has and has not been proven.

## How it works

```
  Application
       |
       |  one in-process call
       v
+----------------------------------------------------------+
| Joltrin engine                                           |
|  copy-on-write B-Tree      WAL + two-phase commit (ACID) |
|  vector similarity search  Reed-Solomon erasure coding   |
|  swarm task coordination   verify barrier (MCP, A2A)  |
+----------------------------------------------------------+
```

Storage, queues, and coordination share one transaction boundary. If a worker dies, its uncommitted work rolls back and another worker takes the task. The long version is in [docs/WHY_JOLTRIN.md](docs/WHY_JOLTRIN.md) and [docs/SOP_ARCHITECTURE_WHITEPAPER.md](docs/SOP_ARCHITECTURE_WHITEPAPER.md).

## Install

| Language | Command |
| :--- | :--- |
| Go | `go get github.com/sharedcode/joltrin/v5` |
| Python | `pip install sop4py` |
| C# | `dotnet add package Sop` |
| Container | `docker run ghcr.io/sharedcode/joltrin-quickstart:stable` |

Java and Rust bindings exist in the repo and are not published yet. Version pinning, the naming note about the old SOP names, and the full package list are in [docs/PACKAGES.md](docs/PACKAGES.md).

## Open core and plans

The engine, vector search, agent memory, and the verification barrier are MIT licensed and stay free. Paid tiers (Pro, and Enterprise on request) add governance on top. Prices and setup are in [docs/MONETIZATION_AND_TIERS.md](docs/MONETIZATION_AND_TIERS.md).

## Documentation

- Start here: [What is Joltrin](docs/WHAT_IS_SOP.md), [Getting started](docs/GETTING_STARTED.md), [Examples](docs/EXAMPLES.md)
- Concepts: [Why Joltrin](docs/WHY_JOLTRIN.md), [Architecture](docs/SOP_ARCHITECTURE_WHITEPAPER.md), [Agent protocols](docs/AGENT_PROTOCOLS.md), [Scalability](docs/SCALABILITY.md)
- Operating it: [Operations and failover](docs/OPERATIONS.md), [Data Manager and tools](docs/SOP_PLATFORM_TOOLS.md), [Azure deployment](infra/azure/README.md), [Kubernetes with Argo CD](deploy/aks/README.md)
- Reference: [Benchmarks](docs/BENCHMARKS.md), [Live demos](docs/LIVE_DEMOS.md), [Roadmap and platform support](docs/ROADMAP.md), [Who it is for](docs/WHO_IS_IT_FOR.md), [Investor notes](docs/INVESTORS.md)

## Contributing

Run `go test ./...` and `gofmt` before opening a pull request, and include tests with your change. See [CONTRIBUTING.md](.github/CONTRIBUTING.md) and [SECURITY.md](.github/SECURITY.md). Questions and ideas go to [GitHub Discussions](https://github.com/SharedCode/joltrin/discussions).

## Releases

See the [changelog](CHANGELOG.md) and the [releases page](https://github.com/SharedCode/joltrin/releases). Maintainers cut releases with [RELEASE_PROCESS.md](RELEASE_PROCESS.md).

<p align="center">
  <sub>MIT License. Built by <a href="https://github.com/sharedcode">SharedCode</a>.</sub>
</p>
