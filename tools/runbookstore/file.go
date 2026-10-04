package runbookstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/v5/verify"
)

// A runbook file lets a team describe its own steps, preconditions, and safety
// rules in JSON, with no Go code. For example:
//
//	{
//	  "workflows": {
//	    "deploy": {
//	      "steps": [
//	        {"id": "run_tests",   "establishes": ["tests_passed"]},
//	        {"id": "deploy_prod", "requires": ["tests_passed"], "establishes": ["deployed"]}
//	      ],
//	      "safety": [
//	        {"name": "no-deploy-without-tests", "forbidden": "deployed", "requires": "tests_passed"}
//	      ]
//	    }
//	  }
//	}
//
// Every state a step or rule needs must be established by some step, and a
// rule's forbidden state must be established by some step. A name that nothing
// produces is almost always a typo, and it would leave a step that can never
// run or a rule that never applies, so the file is refused instead.

type fileStep struct {
	ID          string   `json:"id"`
	Requires    []string `json:"requires"`
	Establishes []string `json:"establishes"`
}

type fileRule struct {
	Name      string `json:"name"`
	Forbidden string `json:"forbidden"`
	Requires  string `json:"requires"`
}

type fileReachability struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type fileWorkflow struct {
	Steps        []fileStep         `json:"steps"`
	Safety       []fileRule         `json:"safety"`
	Reachability []fileReachability `json:"reachability"`
}

type fileDoc struct {
	Workflows map[string]fileWorkflow `json:"workflows"`
}

// LoadFile reads runbook definitions from the JSON file at path and registers
// them in store. It returns the registered names, sorted. See Load.
func LoadFile(store *Store, path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("runbooks: %w", err)
	}
	defer f.Close()
	names, err := Load(store, f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return names, nil
}

// Load reads runbook definitions as JSON and registers them in store. It is
// all or nothing: if any runbook is invalid, none is registered. Unknown fields
// are errors, so a misspelled key is caught instead of ignored.
func Load(store *Store, r io.Reader) ([]string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("runbooks: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc fileDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("runbooks: invalid JSON: %w", err)
	}
	if len(doc.Workflows) == 0 {
		return nil, fmt.Errorf(`runbooks: no workflows found, expected {"workflows": {"name": {"steps": [...]}}}`)
	}

	names := make([]string, 0, len(doc.Workflows))
	for name := range doc.Workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	built := make(map[string]*verify.Workflow, len(names))
	for _, name := range names {
		wf, err := buildWorkflow(name, doc.Workflows[name])
		if err != nil {
			return nil, fmt.Errorf("runbooks: workflow %q: %w", name, err)
		}
		if err := wf.VerifyReachability(); err != nil {
			return nil, fmt.Errorf("runbooks: workflow %q: %w", name, err)
		}
		built[name] = wf
	}
	for _, name := range names {
		if err := store.RegisterWorkflow(name, built[name]); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func buildWorkflow(name string, w fileWorkflow) (*verify.Workflow, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("the workflow name is empty")
	}
	if len(w.Steps) == 0 {
		return nil, fmt.Errorf("it has no steps")
	}

	established := map[string]bool{}
	steps := make([]verify.Step, 0, len(w.Steps))
	for i, s := range w.Steps {
		if strings.TrimSpace(s.ID) == "" {
			return nil, fmt.Errorf("step %d has no id", i+1)
		}
		step := verify.Step{ID: verify.StepID(s.ID)}
		for _, st := range s.Requires {
			if strings.TrimSpace(st) == "" {
				return nil, fmt.Errorf("step %q has an empty required state", s.ID)
			}
			step.Requires = append(step.Requires, verify.State(st))
		}
		for _, st := range s.Establishes {
			if strings.TrimSpace(st) == "" {
				return nil, fmt.Errorf("step %q has an empty established state", s.ID)
			}
			step.Establishes = append(step.Establishes, verify.State(st))
			established[st] = true
		}
		steps = append(steps, step)
	}

	for _, s := range w.Steps {
		for _, st := range s.Requires {
			if !established[st] {
				return nil, fmt.Errorf("step %q requires state %q, but no step establishes it, so the step could never run", s.ID, st)
			}
		}
	}

	rules := make([]verify.SafetyRule, 0, len(w.Safety))
	for i, r := range w.Safety {
		if strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("safety rule %d has no name", i+1)
		}
		if r.Forbidden == "" || r.Requires == "" {
			return nil, fmt.Errorf("safety rule %q needs both forbidden and requires", r.Name)
		}
		if !established[r.Forbidden] {
			return nil, fmt.Errorf("safety rule %q forbids state %q, but no step establishes it, so the rule would never apply", r.Name, r.Forbidden)
		}
		if !established[r.Requires] {
			return nil, fmt.Errorf("safety rule %q requires state %q, but no step establishes it, so the forbidden state could never be reached", r.Name, r.Requires)
		}
		rules = append(rules, verify.SafetyRule{Name: r.Name, Forbidden: verify.State(r.Forbidden), Requires: verify.State(r.Requires)})
	}

	reach := make([]verify.ReachabilityRule, 0, len(w.Reachability))
	for i, r := range w.Reachability {
		if strings.TrimSpace(r.Name) == "" || r.Target == "" {
			return nil, fmt.Errorf("reachability rule %d needs a name and a target", i+1)
		}
		if !established[r.Target] {
			return nil, fmt.Errorf("reachability rule %q targets state %q, but no step establishes it", r.Name, r.Target)
		}
		reach = append(reach, verify.ReachabilityRule{Name: r.Name, Target: verify.State(r.Target)})
	}

	return verify.NewWorkflow(steps, rules, reach)
}
