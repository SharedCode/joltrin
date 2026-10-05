package main

import (
	"fmt"
	"io"
	"os"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

// runDemo implements "sop-mcp-server demo": the barrier blocking a database
// drop until a backup is taken and validated, using the same db-maintenance
// runbook and the same check the server runs. It needs no Go and no agent, and
// finishes at once. It returns the process exit code.
func runDemo(out io.Writer) int {
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		fmt.Fprintln(out, "demo:", err)
		return 1
	}
	trace := verify.NewTrace()

	fmt.Fprintln(out, `agent: "backup looks fine, dropping prod now"`)
	err = wf.CheckAndCommit(trace, "drop_prod_db")
	if !verify.IsViolation(err) {
		fmt.Fprintln(out, "demo: drop_prod_db should have been blocked, got:", err)
		return 1
	}
	fmt.Fprintf(out, "  drop_prod_db      BLOCKED  %v\n", err)

	fmt.Fprintln(out, "\nagent: takes a real backup first, then validates it")
	for _, step := range []verify.StepID{"take_backup", "validate_backup"} {
		if err := wf.CheckAndCommit(trace, step); err != nil {
			fmt.Fprintf(out, "demo: %s: %v\n", step, err)
			return 1
		}
		fmt.Fprintf(out, "  %-17s ALLOWED\n", step)
	}

	fmt.Fprintln(out, "\nagent: backup is validated, retrying the drop")
	if err := wf.CheckAndCommit(trace, "drop_prod_db"); err != nil {
		fmt.Fprintf(out, "demo: drop_prod_db: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "  drop_prod_db      ALLOWED")
	fmt.Fprintf(out, "\ntrace: %v\n", trace.ExecutedSteps())
	return 0
}

// demoMain wires runDemo to the real output.
func demoMain() int { return runDemo(os.Stdout) }
