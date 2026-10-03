// Package main shows agents (Jira, Grafana, AWS, PagerDuty) finishing a task by
// handing work to each other, with three checks in front of every call:
//
//  1. scope:     an agent may only call tools on its own allowlist, within limits.
//  2. grounding: a claim must match evidence a tool actually returned.
//  3. order:     ai/verify blocks a step until the steps it depends on committed.
//
// The tools are stubs. The checks are real. Run with:
//
//	go run ./examples/agent_team
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/sharedcode/joltrin/v5/verify"
)

// Call is one tool call an agent wants to make.
type Call struct {
	Agent string
	Tool  string
	Args  map[string]int
	// CiteID names a piece of evidence and CiteKey/CiteVal the value the
	// agent says it holds.
	CiteID, CiteKey string
	CiteVal         int
}

// allow lists the tools each agent may call.
var allow = map[string]map[string]bool{
	"jira-agent":      {"jira.get_ticket": true},
	"pagerduty-agent": {"pagerduty.get_incident": true, "pagerduty.resolve": true},
	"grafana-agent":   {"grafana.query": true, "grafana.recheck": true},
	"aws-agent":       {"aws.scale_up": true, "aws.rollback_deploy": true},
}

// limits caps numeric arguments: tool -> arg -> max approved value.
var limits = map[string]map[string]int{
	"aws.scale_up":        {"to": 6},
	"aws.rollback_deploy": {"services": 1},
}

type team struct {
	out      io.Writer
	wf       *verify.Workflow
	trace    *verify.Trace
	evidence map[string]map[string]int // evidence id -> facts a tool returned
}

func newTeam(out io.Writer, steps []verify.Step, rules []verify.SafetyRule) (*team, error) {
	wf, err := verify.NewWorkflow(steps, rules, nil)
	if err != nil {
		return nil, err
	}
	return &team{out: out, wf: wf, trace: verify.NewTrace(), evidence: map[string]map[string]int{}}, nil
}

// do runs the three checks, then the stub tool.
func (t *team) do(c Call) {
	fmt.Fprintf(t.out, "%-15s -> %s\n", c.Agent, c.Tool)
	if !allow[c.Agent][c.Tool] {
		fmt.Fprintf(t.out, "  BLOCKED scope: %s is not on %s's allowlist\n", c.Tool, c.Agent)
		return
	}
	for arg, max := range limits[c.Tool] {
		if v := c.Args[arg]; v > max {
			fmt.Fprintf(t.out, "  BLOCKED scope: %s=%d exceeds the approved limit of %d\n", arg, v, max)
			return
		}
	}
	if c.CiteID != "" {
		got, ok := t.evidence[c.CiteID][c.CiteKey]
		if !ok || got != c.CiteVal {
			fmt.Fprintf(t.out, "  BLOCKED grounding: claimed %s=%d, evidence %s says %s=%d\n", c.CiteKey, c.CiteVal, c.CiteID, c.CiteKey, got)
			return
		}
	}
	if err := t.wf.CheckSafety(t.trace, verify.StepID(c.Tool)); err != nil {
		fmt.Fprintf(t.out, "  BLOCKED order: %v\n", err)
		return
	}
	if err := t.wf.Commit(t.trace, verify.StepID(c.Tool)); err != nil {
		fmt.Fprintf(t.out, "  error: %v\n", err)
		return
	}
	t.stub(c)
}

// stub is the fake tool backend.
func (t *team) stub(c Call) {
	switch c.Tool {
	case "jira.get_ticket":
		fmt.Fprintln(t.out, "  ok: OPS-412 checkout p99 latency above 2s")
	case "pagerduty.get_incident":
		fmt.Fprintln(t.out, "  ok: PD-77 checkout 5xx started right after deploy 214")
	case "grafana.query":
		if _, ok := t.wf.Steps["pagerduty.get_incident"]; ok {
			t.evidence["E1"] = map[string]int{"error_rate_pct": 14, "deploy": 214}
			fmt.Fprintln(t.out, "  ok: E1 error_rate_pct=14 deploy=214")
			return
		}
		t.evidence["E1"] = map[string]int{"p99_ms": 2400, "cpu_pct": 91}
		fmt.Fprintln(t.out, "  ok: E1 p99_ms=2400 cpu_pct=91")
	case "grafana.recheck":
		t.evidence["E2"] = map[string]int{"error_rate_pct": 1}
		fmt.Fprintln(t.out, "  ok: E2 error_rate_pct=1")
	case "aws.scale_up":
		fmt.Fprintf(t.out, "  ok: checkout-asg scaled %d -> %d\n", c.Args["from"], c.Args["to"])
	case "aws.rollback_deploy":
		fmt.Fprintln(t.out, "  ok: checkout rolled back 214 -> 213")
	case "pagerduty.resolve":
		fmt.Fprintln(t.out, "  ok: PD-77 resolved")
	}
}

func (t *team) finish() {
	fmt.Fprintf(t.out, "\ntrace: %v\n", t.trace.ExecutedSteps())
}

// runScaling: a latency alert. Scale only if the data supports it.
func runScaling(out io.Writer) (*team, error) {
	t, err := newTeam(out,
		[]verify.Step{
			{ID: "jira.get_ticket", Establishes: []verify.State{"ticket_read"}},
			{ID: "grafana.query", Requires: []verify.State{"ticket_read"}, Establishes: []verify.State{"evidence_confirmed"}},
			{ID: "aws.scale_up", Requires: []verify.State{"evidence_confirmed"}, Establishes: []verify.State{"scaled"}},
		},
		[]verify.SafetyRule{{Name: "no-scale-without-evidence", Forbidden: "scaled", Requires: "evidence_confirmed"}},
	)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "task: checkout p99 alert. Scale the service only if the data supports it.")
	fmt.Fprintln(out, "")
	t.do(Call{Agent: "jira-agent", Tool: "jira.get_ticket"})
	// The AWS agent jumps ahead before anyone has looked at a graph.
	t.do(Call{Agent: "aws-agent", Tool: "aws.scale_up", Args: map[string]int{"from": 4, "to": 6}})
	t.do(Call{Agent: "grafana-agent", Tool: "grafana.query"})
	// Hallucinated number: the evidence says 91.
	t.do(Call{Agent: "aws-agent", Tool: "aws.scale_up", Args: map[string]int{"from": 4, "to": 6}, CiteID: "E1", CiteKey: "cpu_pct", CiteVal: 97})
	// Scope creep: bigger than approved, then a tool nobody granted.
	t.do(Call{Agent: "aws-agent", Tool: "aws.scale_up", Args: map[string]int{"from": 4, "to": 20}, CiteID: "E1", CiteKey: "cpu_pct", CiteVal: 91})
	t.do(Call{Agent: "aws-agent", Tool: "aws.terminate_instances"})
	// Grounded, in scope, in order.
	t.do(Call{Agent: "aws-agent", Tool: "aws.scale_up", Args: map[string]int{"from": 4, "to": 6}, CiteID: "E1", CiteKey: "cpu_pct", CiteVal: 91})
	t.finish()
	return t, nil
}

// runIncident: a PagerDuty page. Roll back only with evidence, resolve only
// after recovery is confirmed.
func runIncident(out io.Writer) (*team, error) {
	t, err := newTeam(out,
		[]verify.Step{
			{ID: "pagerduty.get_incident", Establishes: []verify.State{"incident_read"}},
			{ID: "grafana.query", Requires: []verify.State{"incident_read"}, Establishes: []verify.State{"evidence_confirmed"}},
			{ID: "aws.rollback_deploy", Requires: []verify.State{"evidence_confirmed"}, Establishes: []verify.State{"rolled_back"}},
			{ID: "grafana.recheck", Requires: []verify.State{"rolled_back"}, Establishes: []verify.State{"recovery_confirmed"}},
			{ID: "pagerduty.resolve", Requires: []verify.State{"recovery_confirmed"}, Establishes: []verify.State{"resolved"}},
		},
		[]verify.SafetyRule{
			{Name: "no-rollback-without-evidence", Forbidden: "rolled_back", Requires: "evidence_confirmed"},
			{Name: "no-resolve-without-recovery", Forbidden: "resolved", Requires: "recovery_confirmed"},
		},
	)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "task: PagerDuty PD-77. Roll back only with evidence, resolve only after recovery is confirmed.")
	fmt.Fprintln(out, "")
	t.do(Call{Agent: "pagerduty-agent", Tool: "pagerduty.get_incident"})
	// "It looks quiet, closing it." Nobody has rolled back or checked anything.
	t.do(Call{Agent: "pagerduty-agent", Tool: "pagerduty.resolve"})
	t.do(Call{Agent: "aws-agent", Tool: "aws.rollback_deploy", Args: map[string]int{"services": 1}})
	t.do(Call{Agent: "grafana-agent", Tool: "grafana.query"})
	// Hallucinated number: the evidence says 14.
	t.do(Call{Agent: "aws-agent", Tool: "aws.rollback_deploy", Args: map[string]int{"services": 1}, CiteID: "E1", CiteKey: "error_rate_pct", CiteVal: 40})
	// Scope creep: three services when one was approved, then a tool the
	// PagerDuty agent was never given.
	t.do(Call{Agent: "aws-agent", Tool: "aws.rollback_deploy", Args: map[string]int{"services": 3}, CiteID: "E1", CiteKey: "error_rate_pct", CiteVal: 14})
	t.do(Call{Agent: "pagerduty-agent", Tool: "aws.rollback_deploy", Args: map[string]int{"services": 1}})
	t.do(Call{Agent: "aws-agent", Tool: "aws.rollback_deploy", Args: map[string]int{"services": 1}, CiteID: "E1", CiteKey: "error_rate_pct", CiteVal: 14})
	t.do(Call{Agent: "grafana-agent", Tool: "grafana.recheck"})
	t.do(Call{Agent: "pagerduty-agent", Tool: "pagerduty.resolve", CiteID: "E2", CiteKey: "error_rate_pct", CiteVal: 1})
	t.finish()
	return t, nil
}

func main() {
	if _, err := runScaling(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("----------------------------------------")
	fmt.Println()
	if _, err := runIncident(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
