package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func cli(t *testing.T, args ...string) (code int, serve bool, out, errw string) {
	t.Helper()
	var o, e bytes.Buffer
	code, serve = dispatch(args, &o, &e)
	return code, serve, o.String(), e.String()
}

func TestNoArgumentsAndTheStdioAliasesServe(t *testing.T) {
	for _, args := range [][]string{nil, {"stdio"}, {"serve"}} {
		code, serve, out, errw := cli(t, args...)
		if !serve || code != 0 || out != "" || errw != "" {
			t.Errorf("%v: serve=%v code=%d out=%q err=%q, want a silent start", args, serve, code, out, errw)
		}
	}
}

func TestHelpListsEveryCommandAndNeverStartsAServer(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, serve, out, _ := cli(t, arg)
		if serve || code != 0 {
			t.Errorf("%s: serve=%v code=%d", arg, serve, code)
		}
		for _, want := range []string{"setup", "check", "demo", "version", "SOP_RUNBOOKS", "SOP_LESSONS_DIR"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: help is missing %q:\n%s", arg, want, out)
			}
		}
	}
}

func TestVersionPrintsOneLine(t *testing.T) {
	for _, arg := range []string{"version", "-v", "--version"} {
		code, serve, out, _ := cli(t, arg)
		if serve || code != 0 || !strings.HasPrefix(out, "sop-mcp-server ") || strings.Count(out, "\n") != 1 {
			t.Errorf("%s: serve=%v code=%d out=%q", arg, serve, code, out)
		}
	}
}

func TestAnUnknownCommandIsAUsageErrorNotASilentServer(t *testing.T) {
	code, serve, out, errw := cli(t, "bogus")
	if serve || code != 2 || out != "" {
		t.Errorf("serve=%v code=%d out=%q", serve, code, out)
	}
	if !strings.Contains(errw, `unknown command "bogus"`) || !strings.Contains(errw, "help") {
		t.Errorf("stderr should name the command and point at help:\n%s", errw)
	}
}

func TestSubcommandHelpIsNotAnError(t *testing.T) {
	for _, sub := range []string{"setup", "check", "demo"} {
		code, serve, out, errw := cli(t, sub, "--help")
		if serve || code != 0 {
			t.Errorf("%s --help: serve=%v code=%d err=%q", sub, serve, code, errw)
		}
		if !strings.Contains(out+errw, "Usage") && !strings.Contains(out+errw, "usage") {
			t.Errorf("%s --help should show usage:\nout=%s\nerr=%s", sub, out, errw)
		}
	}
}

func TestCheckJSONIsMachineReadable(t *testing.T) {
	path := writeRunbook(t, goodRunbook)
	for _, args := range [][]string{{"check", "--json", path}, {"check", path, "--json"}} {
		code, _, out, errw := cli(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, stderr:\n%s", args, code, errw)
		}
		var got struct {
			Valid     bool `json:"valid"`
			Workflows []struct {
				Name  string `json:"name"`
				Steps []struct {
					ID          string   `json:"id"`
					Requires    []string `json:"requires"`
					Establishes []string `json:"establishes"`
				} `json:"steps"`
				Safety []struct {
					Name      string `json:"name"`
					Forbidden string `json:"forbidden"`
					Requires  string `json:"requires"`
				} `json:"safety"`
			} `json:"workflows"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v: not JSON: %v\n%s", args, err, out)
		}
		if !got.Valid || len(got.Workflows) != 1 || got.Workflows[0].Name != "deploy" ||
			len(got.Workflows[0].Steps) != 3 || len(got.Workflows[0].Safety) != 1 ||
			got.Workflows[0].Safety[0].Name != "no-deploy-without-approval" {
			t.Errorf("%v: unexpected result: %+v", args, got)
		}
	}
}

func TestCheckJSONReportsALoadErrorAsJSONToo(t *testing.T) {
	code, _, out, _ := cli(t, "check", "--json", writeRunbook(t, badRunbook))
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	var got struct {
		Valid bool   `json:"valid"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Valid || !strings.Contains(got.Error, `"typo"`) {
		t.Errorf("want {valid:false,error:...}, got %q (%v)", out, err)
	}
}

func TestDemoJSONListsEachDecisionInOrder(t *testing.T) {
	code, _, out, _ := cli(t, "demo", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Steps []struct {
			Step     string `json:"step"`
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		} `json:"steps"`
		Trace []string `json:"trace"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := []string{"blocked", "allowed", "allowed", "allowed"}
	if len(got.Steps) != 4 {
		t.Fatalf("steps = %+v", got.Steps)
	}
	for i, d := range want {
		if got.Steps[i].Decision != d {
			t.Errorf("step %d (%s) decision = %s, want %s", i, got.Steps[i].Step, got.Steps[i].Decision, d)
		}
	}
	if got.Steps[0].Reason == "" || strings.Join(got.Trace, ",") != "take_backup,validate_backup,drop_prod_db" {
		t.Errorf("reason or trace wrong: %+v", got)
	}
}
