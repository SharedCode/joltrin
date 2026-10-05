package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

// loadRunbooks loads a runbook file with the loader the server uses, so a file
// that passes here is one the server will start with.
func loadRunbooks(path string) (*runbookstore.Store, []string, error) {
	store := runbookstore.New()
	names, err := runbookstore.LoadFile(store, path)
	return store, names, err
}

// runCheck implements "sop-mcp-server check <file>": it loads the file and
// prints what the barrier will enforce. It returns the process exit code.
func runCheck(args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "usage: sop-mcp-server check <runbooks.json>")
		return 2
	}
	store, names, err := loadRunbooks(args[0])
	if err != nil {
		fmt.Fprintln(errw, "check:", err)
		return 1
	}

	noun := "workflows"
	if len(names) == 1 {
		noun = "workflow"
	}
	fmt.Fprintf(out, "%s loads: %d %s.\n", args[0], len(names), noun)
	for _, name := range names {
		wf, _ := store.Workflow(name)
		fmt.Fprintf(out, "\n%s\n", name)
		for _, s := range orderedSteps(wf) {
			fmt.Fprintf(out, "  %-24s %s\n", s.ID, needs(s))
		}
		for _, r := range wf.Safety {
			fmt.Fprintf(out, "  rule %s: %q cannot be reached unless %q is established first\n", r.Name, r.Forbidden, r.Requires)
		}
		for _, r := range wf.Reachability {
			fmt.Fprintf(out, "  reachability %s: %q must stay reachable from every state\n", r.Name, r.Target)
		}
	}
	return 0
}

// orderedSteps lists the steps that need nothing first, then the rest, each
// group by ID, so the output does not depend on map order.
func orderedSteps(wf *verify.Workflow) []verify.Step {
	steps := make([]verify.Step, 0, len(wf.Steps))
	for _, s := range wf.Steps {
		steps = append(steps, s)
	}
	sort.Slice(steps, func(i, j int) bool {
		a, b := steps[i], steps[j]
		if (len(a.Requires) == 0) != (len(b.Requires) == 0) {
			return len(a.Requires) == 0
		}
		return a.ID < b.ID
	})
	return steps
}

func needs(s verify.Step) string {
	if len(s.Requires) == 0 {
		return "can run first"
	}
	req := make([]string, len(s.Requires))
	for i, r := range s.Requires {
		req[i] = string(r)
	}
	sort.Strings(req)
	return "needs " + strings.Join(req, ", ")
}

// checkMain wires runCheck to the real streams.
func checkMain(args []string) int {
	return runCheck(args, os.Stdout, os.Stderr)
}
