package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestScalingBlocksBadCallsAndFinishes(t *testing.T) {
	var buf bytes.Buffer
	tm, err := runScaling(&buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"BLOCKED order",
		"BLOCKED grounding: claimed cpu_pct=97, evidence E1 says cpu_pct=91",
		"BLOCKED scope: to=20 exceeds the approved limit of 6",
		"BLOCKED scope: aws.terminate_instances is not on aws-agent's allowlist",
		"ok: checkout-asg scaled 4 -> 6",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if got := len(tm.trace.ExecutedSteps()); got != 3 {
		t.Errorf("executed %d steps, want 3 (blocked calls must not commit)", got)
	}
}

func TestIncidentBlocksBadCallsAndResolves(t *testing.T) {
	var buf bytes.Buffer
	tm, err := runIncident(&buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`BLOCKED order: step "pagerduty.resolve" requires state "recovery_confirmed"`,
		"BLOCKED grounding: claimed error_rate_pct=40, evidence E1 says error_rate_pct=14",
		"BLOCKED scope: services=3 exceeds the approved limit of 1",
		"BLOCKED scope: aws.rollback_deploy is not on pagerduty-agent's allowlist",
		"ok: PD-77 resolved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if got := len(tm.trace.ExecutedSteps()); got != 5 {
		t.Errorf("executed %d steps, want 5 (blocked calls must not commit)", got)
	}
}
