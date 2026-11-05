# joltrin-gate

Rust client for `sop-mcp-server gate`. Ask whether an agent step may run, given the steps already allowed in this session. The rules come from a runbook file. See [docs/AGENT_PROTOCOLS.md](../../docs/AGENT_PROTOCOLS.md).

It starts the `sop-mcp-server` binary for each question. There is no native library to link and no Go needed on the machine. It checks ordering only. It does not run the step.

This is separate from `bindings/rust` (`sop`), which wraps the storage engine through a C library.

## Try it

The `gate` command is newer than the latest release, so build the binary from this repo (needs Go), then run the example:

```bash
go build -o /tmp/sop-mcp-server ./cmd/sop-mcp-server
export JOLTRIN_BIN=/tmp/sop-mcp-server

cd bindings/rust-gate
cat > /tmp/runbooks.json <<'JSON'
{"workflows":{"infra":{
 "steps":[
  {"id":"plan","establishes":["planned"]},
  {"id":"apply","requires":["planned"],"establishes":["applied"]}],
 "safety":[{"name":"no-apply-without-plan","forbidden":"applied","requires":"planned"}]}}}
JSON

cargo run --example guard -- /tmp/runbooks.json infra demo apply   # blocked, run plan first
cargo run --example guard -- /tmp/runbooks.json infra demo plan    # allowed
cargo run --example guard -- /tmp/runbooks.json infra demo apply   # allowed now
```

If the binary cannot be found, the error says to install it or set `JOLTRIN_BIN`.

## Use it

Clone the repo and add a path dependency:

```toml
[dependencies]
joltrin-gate = { path = "../joltrin/bindings/rust-gate" }
```

```rust
use joltrin_gate::{Decision, Gate};

let gate = Gate::new("runbooks.json", "infra");
match gate.check("session-1", "apply")? {
    Decision::Allowed { trace } => println!("go ahead, steps so far: {trace:?}"),
    Decision::Blocked { reason, run_first, .. } => println!("{reason}. Run {run_first:?} first"),
}
```

A blocked step is a normal answer, not an error. `Error::Usage` means an unknown workflow, step or bad session name. `Error::Failed` carries the gate's message for anything else, such as a runbook that cannot be read.

## Test

```bash
cargo test                                   # unit tests only
JOLTRIN_BIN=/path/to/sop-mcp-server cargo test   # also runs against the real binary
```

Not on crates.io yet.
