//! Runs against a real binary. Set JOLTRIN_BIN to a built sop-mcp-server.
//! Without it these tests print a note and pass, so `cargo test` works anywhere.

use joltrin_gate::{Decision, Error, Gate, BIN_ENV};
use std::fs;
use std::path::PathBuf;

const RUNBOOK: &str = r#"{"workflows":{"infra":{
 "steps":[
  {"id":"plan","establishes":["planned"]},
  {"id":"apply","requires":["planned"],"establishes":["applied"]}],
 "safety":[{"name":"no-apply-without-plan","forbidden":"applied","requires":"planned"}]}}}"#;

fn setup(name: &str) -> Option<(Gate, PathBuf)> {
    if std::env::var_os(BIN_ENV).is_none() {
        eprintln!("skipping: set {BIN_ENV} to run against a real sop-mcp-server");
        return None;
    }
    let dir = std::env::temp_dir().join(format!("joltrin-gate-{name}-{}", std::process::id()));
    let _ = fs::remove_dir_all(&dir);
    fs::create_dir_all(&dir).unwrap();
    let rb = dir.join("runbooks.json");
    fs::write(&rb, RUNBOOK).unwrap();
    let gate = Gate::new(&rb, "infra").state_dir(dir.join("state"));
    Some((gate, dir))
}

#[test]
fn apply_is_blocked_until_plan_is_recorded() {
    let Some((gate, dir)) = setup("order") else {
        return;
    };
    match gate.check("s1", "apply").unwrap() {
        Decision::Blocked { run_first, .. } => assert_eq!(run_first, vec!["plan"]),
        d => panic!("want blocked, got {d:?}"),
    }
    assert!(gate.check("s1", "plan").unwrap().is_allowed());
    match gate.check("s1", "apply").unwrap() {
        Decision::Allowed { trace } => assert_eq!(trace, vec!["plan", "apply"]),
        d => panic!("want allowed, got {d:?}"),
    }
    gate.reset("s1").unwrap();
    assert!(!gate.check("s1", "apply").unwrap().is_allowed());
    let _ = fs::remove_dir_all(dir);
}

#[test]
fn unknown_step_is_a_usage_error() {
    let Some((gate, dir)) = setup("usage") else {
        return;
    };
    assert!(matches!(gate.check("s2", "nope"), Err(Error::Usage(_))));
    let _ = fs::remove_dir_all(dir);
}
