package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sharedcode/joltrin/v5/verify"
)

const gateUsage = `usage: sop-mcp-server gate [--json] [--state-dir DIR] [--reset] <runbooks.json> <workflow> <session> [step]

Asks the barrier about one step and remembers the answer between calls, so a
hook or script that runs once per command can use the same ordering rules as an
agent. The steps already allowed for <session> are kept in a small file.

  allowed   exit 0, the step is recorded
  blocked   exit 1, nothing is recorded, the reason and the steps to run first are printed
  usage     exit 2
  --reset   forget the recorded steps for <session> (no step needed)
`

// gateState is the file kept for one session: the steps the barrier allowed,
// in order. Replaying them rebuilds the trace.
type gateState struct {
	Workflow string   `json:"workflow"`
	Steps    []string `json:"steps"`
}

// gateResult is the machine-readable form of "gate --json".
type gateResult struct {
	Workflow string   `json:"workflow"`
	Session  string   `json:"session"`
	Step     string   `json:"step"`
	Decision string   `json:"decision"` // "allowed" or "blocked"
	Reason   string   `json:"reason,omitempty"`
	Next     string   `json:"next,omitempty"`
	RunFirst []string `json:"run_first,omitempty"`
	Trace    []string `json:"trace"`
}

var gateSessionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// gateDefaultDir is where session files live when --state-dir is not given.
func gateDefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "joltrin", "gate"), nil
}

func gateFile(dir, session string) string { return filepath.Join(dir, session+".json") }

// loadGateTrace rebuilds a trace from the saved steps of a session. A missing
// file is an empty trace. Steps that the runbook no longer accepts are an
// error, because carrying on would hide that the rules changed under the run.
func loadGateTrace(path, workflow string, wf *verify.Workflow) (*verify.Trace, []string, error) {
	trace := verify.NewTrace()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return trace, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var st gateState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, nil, fmt.Errorf("saved state %s is not valid JSON: %w", path, err)
	}
	if st.Workflow != workflow {
		return nil, nil, fmt.Errorf("session was recorded for workflow %q, not %q; use another session or --reset", st.Workflow, workflow)
	}
	for _, s := range st.Steps {
		if err := wf.CheckAndCommit(trace, verify.StepID(s)); err != nil {
			return nil, nil, fmt.Errorf("saved step %q no longer passes the runbook (%v); use another session or --reset", s, err)
		}
	}
	return trace, st.Steps, nil
}

// saveGateState writes the session file atomically so a crash cannot leave half
// a file. The folder and file are private to the user.
func saveGateState(dir, path string, st gateState) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".gate-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// runGate implements "sop-mcp-server gate". Exit codes follow "try": 0 allowed,
// 1 blocked or a failure, 2 a usage error or an unknown workflow or step.
func runGate(args []string, out, errw io.Writer) int {
	if isHelp(args) {
		fmt.Fprint(out, gateUsage)
		return 0
	}
	args, asJSON := popFlag(args, "--json")
	args, reset := popFlag(args, "--reset")
	dir := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--state-dir" {
			if i+1 >= len(args) {
				fmt.Fprint(errw, gateUsage)
				return 2
			}
			dir = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	args = rest
	need := 4
	if reset {
		need = 3
	}
	if len(args) != need {
		fmt.Fprint(errw, gateUsage)
		return 2
	}
	file, name, session := args[0], args[1], args[2]
	if !gateSessionRE.MatchString(session) {
		fmt.Fprintln(errw, "gate: session must be 1 to 128 letters, digits, dots, dashes or underscores, and start with a letter or digit")
		return 2
	}
	if dir == "" {
		d, err := gateDefaultDir()
		if err != nil {
			fmt.Fprintln(errw, "gate: no default state folder, pass --state-dir:", err)
			return 1
		}
		dir = d
	}
	path := gateFile(dir, session)
	if reset {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(errw, "gate:", err)
			return 1
		}
		fmt.Fprintf(out, "session %s cleared\n", session)
		return 0
	}

	store, names, err := loadRunbooks(file)
	if err != nil {
		fmt.Fprintln(errw, "gate:", err)
		return 1
	}
	wf, ok := store.Workflow(name)
	if !ok {
		fmt.Fprintf(errw, "gate: no workflow %q in %s. It has: %s\n", name, file, strings.Join(names, ", "))
		return 2
	}
	step := args[3]
	if _, ok := wf.Steps[verify.StepID(step)]; !ok {
		fmt.Fprintf(errw, "gate: unknown step %q in %q\n", step, name)
		return 2
	}

	trace, saved, err := loadGateTrace(path, name, wf)
	if err != nil {
		fmt.Fprintln(errw, "gate:", err)
		return 1
	}
	res := gateResult{Workflow: name, Session: session, Step: step, Trace: append([]string{}, saved...)}
	cerr := wf.CheckAndCommit(trace, verify.StepID(step))
	code := 0
	switch {
	case cerr == nil:
		res.Decision = "allowed"
		res.Trace = append(res.Trace, step)
		if err := saveGateState(dir, path, gateState{Workflow: name, Steps: res.Trace}); err != nil {
			fmt.Fprintln(errw, "gate: could not save the session:", err)
			return 1
		}
	default:
		var v *verify.Violation
		if !errors.As(cerr, &v) {
			fmt.Fprintln(errw, "gate:", cerr)
			return 1
		}
		res.Decision = "blocked"
		res.Reason = v.Message
		res.Next = wf.NextAction(v)
		for _, id := range wf.StepsThatEstablish(v.MissingState) {
			res.RunFirst = append(res.RunFirst, string(id))
		}
		code = 1
	}

	if asJSON {
		if werr := writeJSON(out, res); werr != nil {
			fmt.Fprintln(errw, "gate: could not write the result:", werr)
			return 1
		}
		return code
	}
	if res.Decision == "allowed" {
		fmt.Fprintf(out, "allowed %s\n", step)
		return 0
	}
	fmt.Fprintf(errw, "blocked %s: %s\n", step, res.Reason)
	if len(res.RunFirst) > 0 {
		fmt.Fprintf(errw, "run first: %s\n", strings.Join(res.RunFirst, ", "))
	}
	return code
}
