package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLog = `{"event":"execute_step","trace_id":"a","step":"run_tests","outcome":"allowed"}
{"event":"execute_step","trace_id":"a","step":"deploy_prod","outcome":"observed","blocked_by":"no_deploy_without_approval"}
{"event":"execute_step","trace_id":"b","step":"deploy_prod","outcome":"blocked","blocked_by":"no_deploy_without_approval"}
{"event":"execute_step","trace_id":"b","step":"run_tests","outcome":"replayed"}
not json
{"event":"other"}
`

func writeLog(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "decisions.log")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReportCountsOutcomesRulesAndSkippedLines(t *testing.T) {
	res, err := buildReport(strings.NewReader(sampleLog), "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Decisions != 4 || res.Traces != 2 || res.Allowed != 1 || res.Replayed != 1 || res.Observed != 1 || res.Blocked != 1 || res.Skipped != 2 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if len(res.ByRule) != 1 || res.ByRule[0].Name != "no_deploy_without_approval" || res.ByRule[0].Count != 2 {
		t.Errorf("unexpected rules: %+v", res.ByRule)
	}
}

func TestReportTextAndJSON(t *testing.T) {
	p := writeLog(t, sampleLog)
	var o, e bytes.Buffer
	if code := runReport([]string{p}, &o, &e); code != 0 {
		t.Fatalf("exit %d: %s", code, e.String())
	}
	for _, want := range []string{"Decisions: 4 across 2 traces", "would have been blocked", "no_deploy_without_approval"} {
		if !strings.Contains(o.String(), want) {
			t.Errorf("text missing %q:\n%s", want, o.String())
		}
	}
	o.Reset()
	if code := runReport([]string{"--json", p}, &o, &e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var res reportResult
	if err := json.Unmarshal(o.Bytes(), &res); err != nil || res.Decisions != 4 {
		t.Fatalf("bad json %v: %s", err, o.String())
	}
}

func TestReportFailsClearly(t *testing.T) {
	var o, e bytes.Buffer
	if code := runReport([]string{filepath.Join(t.TempDir(), "nope")}, &o, &e); code != 1 || !strings.Contains(e.String(), "SOP_DECISION_LOG") {
		t.Errorf("missing file: code %d, %q", code, e.String())
	}
	e.Reset()
	if code := runReport([]string{writeLog(t, "junk\n")}, &o, &e); code != 1 || !strings.Contains(e.String(), "no execute_step decisions") {
		t.Errorf("empty log: code %d, %q", code, e.String())
	}
	e.Reset()
	if code := runReport(nil, &o, &e); code != 2 {
		t.Errorf("no args: code %d", code)
	}
}
