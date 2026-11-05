//! Usage: JOLTRIN_BIN=/path/to/sop-mcp-server cargo run --example guard -- runbooks.json infra session-1 apply
use joltrin_gate::{Decision, Gate};

fn main() {
    let a: Vec<String> = std::env::args().skip(1).collect();
    if a.len() != 4 {
        eprintln!("usage: guard <runbooks.json> <workflow> <session> <step>");
        std::process::exit(2);
    }
    match Gate::new(&a[0], &a[1]).check(&a[2], &a[3]) {
        Ok(Decision::Allowed { trace }) => println!("allowed, steps so far: {trace:?}"),
        Ok(Decision::Blocked {
            reason, run_first, ..
        }) => {
            println!("blocked: {reason}\nrun first: {run_first:?}");
            std::process::exit(1);
        }
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(2);
        }
    }
}
