package main

import (
	"fmt"
	"io"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

// demoStep is one decision the barrier made during the demo.
type demoStep struct {
	Step     string `json:"step"`
	Decision string `json:"decision"` // "blocked" or "allowed"
	Reason   string `json:"reason,omitempty"`
	Say      string `json:"-"` // what the agent says before this step, for the text output
}

type demoResult struct {
	Steps []demoStep `json:"steps"`
	Trace []string   `json:"trace"`
}

const demoUsage = "usage: sop-mcp-server demo [--json]\n"

// playDemo runs the barrier on the db-maintenance runbook: an agent tries to
// drop the production database, is blocked, takes and validates a backup, and
// is then allowed. It is the same runbook and the same check the server runs.
func playDemo() (demoResult, error) {
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		return demoResult{}, err
	}
	trace := verify.NewTrace()
	var res demoResult

	attempts := []struct{ step, say string }{
		{"drop_prod_db", `agent: "backup looks fine, dropping prod now"`},
		{"take_backup", "agent: takes a real backup first, then validates it"},
		{"validate_backup", ""},
		{"drop_prod_db", "agent: backup is validated, retrying the drop"},
	}
	for i, a := range attempts {
		err := wf.CheckAndCommit(trace, verify.StepID(a.step))
		switch {
		case err == nil:
			res.Steps = append(res.Steps, demoStep{Step: a.step, Decision: "allowed", Say: a.say})
		case verify.IsViolation(err) && i == 0:
			res.Steps = append(res.Steps, demoStep{Step: a.step, Decision: "blocked", Reason: err.Error(), Say: a.say})
		default:
			return demoResult{}, fmt.Errorf("%s: %w", a.step, err)
		}
	}
	for _, s := range trace.ExecutedSteps() {
		res.Trace = append(res.Trace, string(s))
	}
	return res, nil
}

// runDemoArgs implements "sop-mcp-server demo": it needs no Go and no agent and
// finishes at once. With --json it prints each decision as one JSON document.
// It returns the process exit code.
func runDemoArgs(args []string, out, errw io.Writer) int {
	if isHelp(args) {
		fmt.Fprint(out, demoUsage)
		return 0
	}
	args, asJSON := popFlag(args, "--json")
	if len(args) != 0 {
		fmt.Fprint(errw, demoUsage)
		return 2
	}
	res, err := playDemo()
	if err != nil {
		fmt.Fprintln(errw, "demo:", err)
		return 1
	}
	if asJSON {
		if err := writeJSON(out, res); err != nil {
			fmt.Fprintln(errw, "demo: could not write the result:", err)
			return 1
		}
		return 0
	}
	for i, s := range res.Steps {
		if s.Say != "" {
			if i > 0 {
				fmt.Fprintln(out)
			}
			fmt.Fprintln(out, s.Say)
		}
		switch s.Decision {
		case "blocked":
			fmt.Fprintf(out, "  %-17s BLOCKED  %s\n", s.Step, s.Reason)
		default:
			fmt.Fprintf(out, "  %-17s ALLOWED\n", s.Step)
		}
	}
	fmt.Fprintf(out, "\ntrace: %v\n", res.Trace)
	return 0
}
