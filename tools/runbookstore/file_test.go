package runbookstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5/verify"
)

const deployRunbook = `{
  "workflows": {
    "deploy": {
      "steps": [
        {"id": "run_tests",   "establishes": ["tests_passed"]},
        {"id": "get_approval","requires": ["tests_passed"], "establishes": ["approved"]},
        {"id": "deploy_prod", "requires": ["tests_passed", "approved"], "establishes": ["deployed"]},
        {"id": "rollback",    "requires": ["tests_passed"], "establishes": ["rolled_back"]},
        {"id": "rollback_after_deploy", "requires": ["deployed"], "establishes": ["rolled_back"]}
      ],
      "safety": [
        {"name": "no-deploy-without-approval", "forbidden": "deployed", "requires": "approved"}
      ],
      "reachability": [
        {"name": "rollback-reachable", "target": "rolled_back"}
      ]
    }
  }
}`

func TestLoad_RegistersARunbookThatTheBarrierEnforces(t *testing.T) {
	store := New()
	names, err := Load(store, strings.NewReader(deployRunbook))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "deploy" {
		t.Fatalf("names = %v", names)
	}
	wf, ok := store.Workflow("deploy")
	if !ok {
		t.Fatal("deploy is not registered")
	}
	trace := store.TraceFor("t1")

	// The destructive step is blocked until its steps have run, in any order the file allows.
	if err := wf.CheckAndCommit(trace, "deploy_prod"); !verify.IsViolation(err) {
		t.Fatalf("deploy_prod must be blocked at first, got %v", err)
	}
	for _, s := range []verify.StepID{"run_tests", "get_approval", "deploy_prod"} {
		if err := wf.CheckAndCommit(trace, s); err != nil {
			t.Fatalf("%s should run in order: %v", s, err)
		}
	}
	// A fresh trace cannot borrow that progress.
	if err := wf.CheckAndCommit(store.TraceFor("t2"), "deploy_prod"); !verify.IsViolation(err) {
		t.Fatalf("a new trace must start blocked, got %v", err)
	}
}

func TestLoad_BlockReportsTheFixFromTheFile(t *testing.T) {
	store := New()
	if _, err := Load(store, strings.NewReader(deployRunbook)); err != nil {
		t.Fatal(err)
	}
	wf, _ := store.Workflow("deploy")
	err := wf.CheckSafety(store.TraceFor("t"), "deploy_prod")
	var v *verify.Violation
	if !asViolation(err, &v) {
		t.Fatalf("want a violation, got %v", err)
	}
	if v.MissingState != "tests_passed" {
		t.Errorf("missing state = %q, want tests_passed", v.MissingState)
	}
	if got := wf.StepsThatEstablish(v.MissingState); len(got) != 1 || got[0] != "run_tests" {
		t.Errorf("fix = %v, want [run_tests]", got)
	}
}

func asViolation(err error, v **verify.Violation) bool {
	e, ok := err.(*verify.Violation)
	if ok {
		*v = e
	}
	return ok
}

func TestLoad_RefusesBadFiles(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"not json", `{`, "invalid JSON"},
		{"misspelled key", `{"workflows":{"w":{"setps":[]}}}`, "unknown field"},
		{"no workflows", `{"workflows":{}}`, "no workflows found"},
		{"empty workflow name", `{"workflows":{"":{"steps":[{"id":"a","establishes":["x"]}]}}}`, "name is empty"},
		{"no steps", `{"workflows":{"w":{"steps":[]}}}`, "no steps"},
		{"step without id", `{"workflows":{"w":{"steps":[{"establishes":["x"]}]}}}`, "no id"},
		{"duplicate step", `{"workflows":{"w":{"steps":[{"id":"a","establishes":["x"]},{"id":"a","establishes":["y"]}]}}}`, "duplicate step"},
		{"empty state name", `{"workflows":{"w":{"steps":[{"id":"a","establishes":[""]}]}}}`, "empty established state"},
		{"requires a state nothing establishes", `{"workflows":{"w":{"steps":[{"id":"a","requires":["typo"],"establishes":["x"]}]}}}`, `requires state "typo"`},
		{"rule forbids a state nothing establishes", `{"workflows":{"w":{"steps":[{"id":"a","establishes":["x"]}],"safety":[{"name":"r","forbidden":"nope","requires":"x"}]}}}`, "would never apply"},
		{"rule requires a state nothing establishes", `{"workflows":{"w":{"steps":[{"id":"a","establishes":["x"]}],"safety":[{"name":"r","forbidden":"x","requires":"nope"}]}}}`, "could never be reached"},
		{"rule without a name", `{"workflows":{"w":{"steps":[{"id":"a","establishes":["x"]}],"safety":[{"forbidden":"x","requires":"x"}]}}}`, "has no name"},
		{"reachability target nothing establishes", `{"workflows":{"w":{"steps":[{"id":"a","establishes":["x"]}],"reachability":[{"name":"r","target":"nope"}]}}}`, "no step establishes it"},
		{"dead end under a reachability rule", `{"workflows":{"w":{"steps":[
			{"id":"a","establishes":["s1"]},
			{"id":"b","requires":["s1"],"establishes":["s2"]},
			{"id":"c","requires":["s1"],"establishes":["goal"]}],
			"reachability":[{"name":"r","target":"goal"}]}}}`, "s2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := New()
			names, err := Load(store, strings.NewReader(c.doc))
			if err == nil {
				t.Fatalf("expected an error, registered %v", names)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err, c.want)
			}
			if got := store.WorkflowNames(); len(got) != 0 {
				t.Errorf("a refused file must register nothing, got %v", got)
			}
		})
	}
}

func TestLoad_IsAllOrNothing(t *testing.T) {
	store := New()
	doc := `{"workflows":{
		"good":{"steps":[{"id":"a","establishes":["x"]}]},
		"bad":{"steps":[{"id":"a","requires":["missing"],"establishes":["y"]}]}}}`
	if _, err := Load(store, strings.NewReader(doc)); err == nil {
		t.Fatal("expected an error")
	}
	if got := store.WorkflowNames(); len(got) != 0 {
		t.Errorf("the valid workflow must not be registered when another is invalid, got %v", got)
	}
}

func TestLoad_NamesAreSortedAndMultipleWorkflowsWork(t *testing.T) {
	store := New()
	doc := `{"workflows":{
		"zeta":{"steps":[{"id":"a","establishes":["x"]}]},
		"alpha":{"steps":[{"id":"a","establishes":["x"]}]}}}`
	names, err := Load(store, strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "alpha,zeta" {
		t.Errorf("names = %v", names)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runbooks.json")
	if err := os.WriteFile(path, []byte(deployRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	store := New()
	names, err := LoadFile(store, path)
	if err != nil || len(names) != 1 {
		t.Fatalf("names=%v err=%v", names, err)
	}

	if _, err := LoadFile(New(), filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing file must be an error")
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"workflows":{"w":{"steps":[{"id":"a","requires":["typo"],"establishes":["x"]}]}}}`), 0o600)
	_, err = LoadFile(New(), bad)
	if err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Errorf("the error should name the file, got %v", err)
	}
}
