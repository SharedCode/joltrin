// Package barrierdemo runs the self-contained barrier example shared by both commands.
package barrierdemo

import (
	"fmt"
	"io"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

// Pause parses JOLTRIN_DEMO_PAUSE, preserving the example's default pacing.
// Invalid values use 150ms. A zero or negative duration does not sleep.
func Pause(value string) time.Duration {
	if value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return 150 * time.Millisecond
}

// Run demonstrates a refused drop, committed backup steps, and an allowed retry.
// It only changes an in-memory trace and never operates on a real database.
func Run(writer io.Writer, pause time.Duration) error {
	if writer == nil {
		return fmt.Errorf("demo output writer is nil")
	}
	out := output{writer: writer}
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
			{ID: "restore_from_backup", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"rollback_complete"}},
			{ID: "restore_from_backup_post_drop", Requires: []verify.State{"prod_db_dropped"}, Establishes: []verify.State{"rollback_complete"}},
		},
		[]verify.SafetyRule{
			{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
		},
		[]verify.ReachabilityRule{
			{Name: "rollback-always-reachable", Target: "rollback_complete"},
		},
	)
	if err != nil {
		return err
	}

	// Registration-time check: refuse to even serve a runbook whose
	// rollback path can dead-end.
	if err := wf.VerifyReachability(); err != nil {
		return err
	}
	out.println("✓ workflow registered: rollback_complete is reachable from every state")
	out.println()

	trace := verify.NewTrace()

	out.println("agent: \"backup looks fine, dropping prod now\"")
	out.sleep(pause)
	out.print("→ execute_step(drop_prod_db) ... ")
	if err := wf.CheckSafety(trace, "drop_prod_db"); err != nil {
		out.println("BLOCKED")
		out.printf("  barrier certificate: %v\n\n", err)
	} else {
		out.println("allowed (unexpected)")
		return fmt.Errorf("demo expected the first drop to be blocked")
	}
	out.sleep(pause)

	out.println("agent: okay, taking a real backup first")
	out.sleep(pause)
	out.print("→ execute_step(take_backup) ... ")
	if err := wf.CheckSafety(trace, "take_backup"); err != nil {
		return err
	}
	if err := wf.Commit(trace, "take_backup"); err != nil {
		return err
	}
	out.println("committed")
	out.sleep(pause)

	out.print("→ execute_step(validate_backup) ... ")
	if err := wf.CheckSafety(trace, "validate_backup"); err != nil {
		return err
	}
	if err := wf.Commit(trace, "validate_backup"); err != nil {
		return err
	}
	out.println("committed")
	out.sleep(pause)

	out.println()
	out.println("agent: backup is now validated, retrying the drop")
	out.sleep(pause)
	out.print("→ execute_step(drop_prod_db) ... ")
	if err := wf.CheckSafety(trace, "drop_prod_db"); err != nil {
		out.printf("BLOCKED (unexpected): %v\n", err)
		return err
	}
	if err := wf.Commit(trace, "drop_prod_db"); err != nil {
		return err
	}
	out.println("ALLOWED, committed")
	out.println()
	out.printf("trace: %v\n", trace.ExecutedSteps())
	return out.err
}

// output stops writing and sleeping after the first output failure.
type output struct {
	writer io.Writer
	err    error
}

func (o *output) write(text string) {
	if o.err != nil {
		return
	}
	count, err := io.WriteString(o.writer, text)
	if err == nil && count != len(text) {
		err = io.ErrShortWrite
	}
	o.err = err
}
func (o *output) print(values ...any)                 { o.write(fmt.Sprint(values...)) }
func (o *output) println(values ...any)               { o.write(fmt.Sprintln(values...)) }
func (o *output) printf(format string, values ...any) { o.write(fmt.Sprintf(format, values...)) }
func (o *output) sleep(pause time.Duration) {
	if o.err == nil {
		time.Sleep(pause)
	}
}
