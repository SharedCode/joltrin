# Roadmap and platform support

## Roadmap

**Shipped and tested today**: the Go core engine, Python bindings (`sop4py`, on PyPI), C# bindings (`Sop`, on NuGet), the WebAssembly browser demo, the standalone HTTP Data Manager, and the interactive AI agent memory checkpointing demo.

**In progress, code exists in-repo**: Java bindings (`sop4j`), complete with tests, blocked on Maven Central Portal credential setup rather than on missing functionality (see [`docs/RELEASE_PROCESS_JAVA_STATUS.md`](RELEASE_PROCESS_JAVA_STATUS.md)). Rust bindings (`sop4rs`), with tests and examples in-repo, not yet published to crates.io.

**Proposed, not yet implemented**: the swarm job distribution, `Await`, and `MapReduce` helpers described in [`ai/SWARM_DESIGN.md`](../ai/SWARM_DESIGN.md), which that document itself labels "Proposal / Vision" rather than shipped.

This list reflects what is actually in the repository at the time of writing. It is not a committed release schedule.

## Cross-Platform

CI (`.github/workflows/ci.yml`) builds, vets, and runs the core unit tests (`inmemory`, `btree`, `common`, `cache`, `encoding`, `database`) on `ubuntu-latest`, `macos-latest`, and `windows-latest` on every push and pull request, as three independent, parallel jobs. `macos-latest` runs on Apple Silicon (arm64), so that leg also verifies arm64 for free.

The Redis- and Cassandra-backed integration and stress test suites stay Linux-only: GitHub Actions' `services:` containers require a Linux-hosted runner, so those specific suites are not run on macOS or Windows today. That is a real gap in what is verified there, not a hidden one. All core unit test packages (`inmemory`, `btree`, `common`, `cache`, `encoding`, `database`) run and pass cleanly across all three operating systems (Linux, macOS, and Windows).
