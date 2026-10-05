package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodRunbook = `{
  "workflows": {
    "deploy": {
      "steps": [
        {"id": "run_tests",    "establishes": ["tests_passed"]},
        {"id": "get_approval", "requires": ["tests_passed"], "establishes": ["approved"]},
        {"id": "deploy_prod",  "requires": ["tests_passed", "approved"], "establishes": ["deployed"]}
      ],
      "safety": [{"name": "no-deploy-without-approval", "forbidden": "deployed", "requires": "approved"}]
    }
  }
}`

const badRunbook = `{"workflows":{"deploy":{"steps":[{"id":"a","requires":["typo"]}]}}}`

func writeRunbook(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "runbooks.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func check(t *testing.T, args ...string) (code int, out, errw string) {
	t.Helper()
	var o, e bytes.Buffer
	code = runCheck(args, &o, &e)
	return code, o.String(), e.String()
}

func TestCheckShowsWhatTheBarrierWillEnforce(t *testing.T) {
	code, out, errw := check(t, writeRunbook(t, goodRunbook))
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errw)
	}
	for _, want := range []string{
		"1 workflow",
		"deploy",
		"run_tests",
		"can run first",
		"deploy_prod",
		"needs approved, tests_passed",
		"no-deploy-without-approval",
		`"deployed" cannot be reached unless "approved" is established first`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCheckRefusesABadRunbookWithTheLoadersError(t *testing.T) {
	code, out, errw := check(t, writeRunbook(t, badRunbook))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errw, `requires state "typo"`) {
		t.Errorf("stderr should carry the loader's error:\n%s", errw)
	}
	if strings.Contains(out, "valid") {
		t.Errorf("a bad file must not be called valid:\n%s", out)
	}
}

func TestCheckNeedsAFileThatExists(t *testing.T) {
	if code, _, errw := check(t); code != 2 || !strings.Contains(errw, "usage") {
		t.Errorf("no argument: exit %d, stderr %q", code, errw)
	}
	if code, _, _ := check(t, filepath.Join(t.TempDir(), "missing.json")); code != 1 {
		t.Errorf("missing file: exit %d, want 1", code)
	}
}

func TestSetupRefusesARunbookFileThatWillNotLoad(t *testing.T) {
	rec := &recorder{have: map[string]bool{"claude": true}}
	code, out, errw := setup(t, "/bin/sop-mcp-server", rec, "--apply", "--runbooks", writeRunbook(t, badRunbook))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if len(rec.calls) != 0 {
		t.Errorf("nothing may be registered with a file the server cannot load, calls = %v", rec.calls)
	}
	if !strings.Contains(errw, `requires state "typo"`) {
		t.Errorf("the reason should be shown:\nstdout:\n%s\nstderr:\n%s", out, errw)
	}
}

func TestSetupAcceptsARunbookFileThatLoads(t *testing.T) {
	rec := &recorder{have: map[string]bool{"claude": true}}
	code, out, _ := setup(t, "/bin/sop-mcp-server", rec, "--apply", "--runbooks", writeRunbook(t, goodRunbook))
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(rec.calls) == 0 {
		t.Error("a valid file should still be registered")
	}
}
