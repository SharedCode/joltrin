package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gateRunbook = `{"workflows":{"infra":{
 "steps":[
  {"id":"plan","establishes":["planned"]},
  {"id":"apply","requires":["planned"],"establishes":["applied"]}],
 "safety":[{"name":"no-apply-without-plan","forbidden":"applied","requires":"planned"}]}}}`

func gateSetup(t *testing.T) (runbook, stateDir string) {
	t.Helper()
	dir := t.TempDir()
	runbook = filepath.Join(dir, "runbooks.json")
	if err := os.WriteFile(runbook, []byte(gateRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	return runbook, filepath.Join(dir, "state")
}

func gate(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = runGate(args, &o, &e)
	return code, o.String(), e.String()
}

func TestGateBlocksApplyUntilPlanIsRecorded(t *testing.T) {
	rb, sd := gateSetup(t)

	code, _, errOut := gate(t, "--state-dir", sd, rb, "infra", "s1", "apply")
	if code != 1 || !strings.Contains(errOut, "blocked apply") || !strings.Contains(errOut, "run first: plan") {
		t.Fatalf("apply first: code=%d stderr=%q", code, errOut)
	}

	if code, out, _ := gate(t, "--state-dir", sd, rb, "infra", "s1", "plan"); code != 0 || !strings.Contains(out, "allowed plan") {
		t.Fatalf("plan: code=%d out=%q", code, out)
	}

	// A separate call, as a hook would make it, sees the recorded plan.
	if code, out, _ := gate(t, "--state-dir", sd, rb, "infra", "s1", "apply"); code != 0 || !strings.Contains(out, "allowed apply") {
		t.Fatalf("apply after plan: code=%d out=%q", code, out)
	}
}

func TestGateSessionsAreIndependent(t *testing.T) {
	rb, sd := gateSetup(t)
	gate(t, "--state-dir", sd, rb, "infra", "a", "plan")
	if code, _, _ := gate(t, "--state-dir", sd, rb, "infra", "b", "apply"); code != 1 {
		t.Fatalf("session b must not inherit session a, code=%d", code)
	}
}

func TestGateBlockedStepRecordsNothing(t *testing.T) {
	rb, sd := gateSetup(t)
	gate(t, "--state-dir", sd, rb, "infra", "s", "apply")
	code, out, _ := gate(t, "--json", "--state-dir", sd, rb, "infra", "s", "plan")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var res gateResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != "allowed" || len(res.Trace) != 1 || res.Trace[0] != "plan" {
		t.Fatalf("trace after a blocked apply and an allowed plan = %v", res.Trace)
	}
}

func TestGateJSONReportsReasonAndRunFirst(t *testing.T) {
	rb, sd := gateSetup(t)
	code, out, _ := gate(t, "--json", "--state-dir", sd, rb, "infra", "s", "apply")
	var res gateResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if code != 1 || res.Decision != "blocked" || res.Reason == "" || len(res.RunFirst) != 1 || res.RunFirst[0] != "plan" {
		t.Fatalf("code=%d result=%+v", code, res)
	}
}

func TestGateResetForgetsTheSession(t *testing.T) {
	rb, sd := gateSetup(t)
	gate(t, "--state-dir", sd, rb, "infra", "s", "plan")
	if code, _, _ := gate(t, "--state-dir", sd, "--reset", rb, "infra", "s"); code != 0 {
		t.Fatalf("reset code=%d", code)
	}
	if code, _, _ := gate(t, "--state-dir", sd, rb, "infra", "s", "apply"); code != 1 {
		t.Fatalf("apply after reset must be blocked, code=%d", code)
	}
}

func TestGateRejectsBadInput(t *testing.T) {
	rb, sd := gateSetup(t)
	for name, args := range map[string][]string{
		"path in session":  {"--state-dir", sd, rb, "infra", "../x", "plan"},
		"empty session":    {"--state-dir", sd, rb, "infra", "", "plan"},
		"unknown step":     {"--state-dir", sd, rb, "infra", "s", "nope"},
		"unknown workflow": {"--state-dir", sd, rb, "nope", "s", "plan"},
		"missing step":     {"--state-dir", sd, rb, "infra", "s"},
	} {
		if code, _, _ := gate(t, args...); code != 2 {
			t.Errorf("%s: code=%d, want 2", name, code)
		}
	}
}

func TestGateRefusesStateThatNoLongerMatchesTheRunbook(t *testing.T) {
	rb, sd := gateSetup(t)
	gate(t, "--state-dir", sd, rb, "infra", "s", "plan")
	// Tamper with the saved file so it lists a step the runbook never allows first.
	if err := os.WriteFile(filepath.Join(sd, "s.json"), []byte(`{"workflow":"infra","steps":["apply"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := gate(t, "--state-dir", sd, rb, "infra", "s", "plan")
	if code != 1 || !strings.Contains(errOut, "no longer passes the runbook") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestDispatchKnowsGate(t *testing.T) {
	var o, e bytes.Buffer
	if code, serve := dispatch([]string{"gate", "--help"}, &o, &e); code != 0 || serve || !strings.Contains(o.String(), "usage: sop-mcp-server gate") {
		t.Fatalf("code=%d serve=%v out=%q", code, serve, o.String())
	}
}
