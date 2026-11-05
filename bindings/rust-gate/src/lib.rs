//! Rust client for `sop-mcp-server gate`.
//!
//! The gate answers one question: may this step run now, given the steps already
//! allowed in this session? The rules live in a runbook file. This crate starts
//! the `sop-mcp-server` binary for each question, so it needs no native library
//! and works the same on Linux, macOS and Windows.
//!
//! ```no_run
//! use joltrin_gate::{Gate, Decision};
//!
//! let gate = Gate::new("runbooks.json", "infra");
//! match gate.check("session-1", "apply")? {
//!     Decision::Allowed { .. } => println!("go ahead"),
//!     Decision::Blocked { run_first, .. } => println!("run {run_first:?} first"),
//! }
//! # Ok::<(), joltrin_gate::Error>(())
//! ```

use std::ffi::OsString;
use std::fmt;
use std::io;
use std::path::{Path, PathBuf};
use std::process::Command;

use serde::Deserialize;

/// Environment variable that points at the `sop-mcp-server` binary.
pub const BIN_ENV: &str = "JOLTRIN_BIN";

/// The answer for one step.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Decision {
    /// The step may run. The gate recorded it, so later steps can rely on it.
    Allowed {
        /// Steps allowed so far in this session, including this one.
        trace: Vec<String>,
    },
    /// The step must wait. Nothing was recorded.
    Blocked {
        /// Why the barrier refused the step.
        reason: String,
        /// What to do next, in words.
        next: String,
        /// Steps that would make this one legal.
        run_first: Vec<String>,
        /// Steps allowed so far in this session.
        trace: Vec<String>,
    },
}

impl Decision {
    pub fn is_allowed(&self) -> bool {
        matches!(self, Decision::Allowed { .. })
    }
}

/// What went wrong when asking.
#[derive(Debug)]
pub enum Error {
    /// The binary could not be started. Install it or set `JOLTRIN_BIN`.
    BinaryNotFound { tried: PathBuf, source: io::Error },
    /// Another I/O problem while running the binary.
    Io(io::Error),
    /// The gate rejected the request: unknown workflow or step, or a bad
    /// session name. The message is the gate's own.
    Usage(String),
    /// The gate failed for another reason, such as a runbook that cannot be read
    /// or saved state that no longer matches the rules.
    Failed(String),
    /// The gate printed something this crate does not understand.
    BadOutput(String),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::BinaryNotFound { tried, source } => write!(
                f,
                "could not start {} ({source}). Install sop-mcp-server or set {BIN_ENV} to its path",
                tried.display()
            ),
            Error::Io(e) => write!(f, "gate I/O error: {e}"),
            Error::Usage(m) => write!(f, "gate rejected the request: {m}"),
            Error::Failed(m) => write!(f, "gate failed: {m}"),
            Error::BadOutput(m) => write!(f, "unexpected gate output: {m}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::BinaryNotFound { source, .. } => Some(source),
            Error::Io(e) => Some(e),
            _ => None,
        }
    }
}

#[derive(Deserialize)]
struct Wire {
    decision: String,
    #[serde(default)]
    reason: String,
    #[serde(default)]
    next: String,
    #[serde(default)]
    run_first: Vec<String>,
    #[serde(default)]
    trace: Vec<String>,
}

/// Turns the gate's `--json` output into a [`Decision`].
pub fn parse_decision(json: &str) -> Result<Decision, Error> {
    let w: Wire = serde_json::from_str(json).map_err(|e| Error::BadOutput(e.to_string()))?;
    match w.decision.as_str() {
        "allowed" => Ok(Decision::Allowed { trace: w.trace }),
        "blocked" => Ok(Decision::Blocked {
            reason: w.reason,
            next: w.next,
            run_first: w.run_first,
            trace: w.trace,
        }),
        other => Err(Error::BadOutput(format!("unknown decision {other:?}"))),
    }
}

/// A runbook workflow to ask about.
#[derive(Debug, Clone)]
pub struct Gate {
    binary: OsString,
    runbooks: PathBuf,
    workflow: String,
    state_dir: Option<PathBuf>,
}

impl Gate {
    /// Uses the binary named by `JOLTRIN_BIN`, or `sop-mcp-server` from `PATH`.
    pub fn new(runbooks: impl AsRef<Path>, workflow: impl Into<String>) -> Self {
        let binary = std::env::var_os(BIN_ENV).unwrap_or_else(|| "sop-mcp-server".into());
        Gate {
            binary,
            runbooks: runbooks.as_ref().to_path_buf(),
            workflow: workflow.into(),
            state_dir: None,
        }
    }

    /// Uses this binary instead of `JOLTRIN_BIN` or `PATH`.
    pub fn binary(mut self, path: impl Into<OsString>) -> Self {
        self.binary = path.into();
        self
    }

    /// Keeps session files here instead of the user cache folder.
    pub fn state_dir(mut self, dir: impl AsRef<Path>) -> Self {
        self.state_dir = Some(dir.as_ref().to_path_buf());
        self
    }

    /// Asks whether `step` may run in `session`. An allowed step is recorded.
    /// A blocked step is a normal answer, not an error.
    pub fn check(&self, session: &str, step: &str) -> Result<Decision, Error> {
        let mut cmd = self.command();
        cmd.arg("--json").args(self.state_args());
        cmd.arg(&self.runbooks)
            .arg(&self.workflow)
            .arg(session)
            .arg(step);
        let out = self.run(cmd)?;
        let stdout = String::from_utf8_lossy(&out.stdout);
        if !stdout.trim().is_empty() {
            return parse_decision(&stdout);
        }
        Err(Self::failure(&out))
    }

    /// Forgets the steps recorded for `session`.
    pub fn reset(&self, session: &str) -> Result<(), Error> {
        let mut cmd = self.command();
        cmd.arg("--reset").args(self.state_args());
        cmd.arg(&self.runbooks).arg(&self.workflow).arg(session);
        let out = self.run(cmd)?;
        if out.status.success() {
            Ok(())
        } else {
            Err(Self::failure(&out))
        }
    }

    fn command(&self) -> Command {
        let mut cmd = Command::new(&self.binary);
        cmd.arg("gate");
        cmd
    }

    fn state_args(&self) -> Vec<OsString> {
        match &self.state_dir {
            Some(d) => vec!["--state-dir".into(), d.clone().into_os_string()],
            None => Vec::new(),
        }
    }

    fn run(&self, mut cmd: Command) -> Result<std::process::Output, Error> {
        cmd.output().map_err(|e| {
            if e.kind() == io::ErrorKind::NotFound {
                Error::BinaryNotFound {
                    tried: PathBuf::from(&self.binary),
                    source: e,
                }
            } else {
                Error::Io(e)
            }
        })
    }

    fn failure(out: &std::process::Output) -> Error {
        let msg = String::from_utf8_lossy(&out.stderr).trim().to_string();
        match out.status.code() {
            Some(2) => Error::Usage(msg),
            _ => Error::Failed(msg),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_allowed() {
        let d = parse_decision(r#"{"workflow":"infra","session":"s","step":"plan","decision":"allowed","trace":["plan"]}"#).unwrap();
        assert_eq!(
            d,
            Decision::Allowed {
                trace: vec!["plan".into()]
            }
        );
        assert!(d.is_allowed());
    }

    #[test]
    fn parses_blocked() {
        let d = parse_decision(
            r#"{"workflow":"infra","session":"s","step":"apply","decision":"blocked","reason":"needs planned","next":"run plan","run_first":["plan"],"trace":[]}"#,
        )
        .unwrap();
        match d {
            Decision::Blocked {
                run_first, reason, ..
            } => {
                assert_eq!(run_first, vec!["plan"]);
                assert_eq!(reason, "needs planned");
            }
            _ => panic!("want blocked"),
        }
    }

    #[test]
    fn rejects_unknown_decision_and_junk() {
        assert!(matches!(
            parse_decision(r#"{"decision":"maybe"}"#),
            Err(Error::BadOutput(_))
        ));
        assert!(matches!(
            parse_decision("not json"),
            Err(Error::BadOutput(_))
        ));
    }

    #[test]
    fn missing_binary_gives_a_clear_error() {
        let g = Gate::new("r.json", "w").binary("/no/such/sop-mcp-server");
        let err = g.check("s", "x").unwrap_err();
        assert!(matches!(err, Error::BinaryNotFound { .. }));
        assert!(err.to_string().contains("JOLTRIN_BIN"));
    }
}
