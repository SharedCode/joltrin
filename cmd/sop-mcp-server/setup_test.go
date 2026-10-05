package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	have  map[string]bool // CLIs that "exist"
	calls [][]string
	fail  map[string]error
	// removeFails makes every "mcp remove" fail, as it does when nothing is
	// registered yet.
	removeFails bool
	// barrier, when set, makes each add wait until that many adds are in flight
	// at once, so a serial run times out.
	barrier  int
	inFlight int
	cond     *sync.Cond
}

func (r *recorder) lookPath(name string) (string, error) {
	if r.have[name] {
		return "/usr/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (r *recorder) run(name string, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	if len(args) > 1 && args[1] == "remove" {
		if r.removeFails {
			return errors.New("no such server")
		}
		return nil
	}
	if r.barrier > 0 {
		r.mu.Lock()
		if r.cond == nil {
			r.cond = sync.NewCond(&r.mu)
		}
		r.inFlight++
		r.cond.Broadcast()
		deadline := time.AfterFunc(2*time.Second, func() { r.mu.Lock(); r.barrier = -1; r.cond.Broadcast(); r.mu.Unlock() })
		for r.inFlight < r.barrier && r.barrier > 0 {
			r.cond.Wait()
		}
		timedOut := r.barrier < 0
		r.mu.Unlock()
		deadline.Stop()
		if timedOut {
			return errors.New("the registrations ran one after another")
		}
	}
	return r.fail[name]
}

func setup(t *testing.T, exe string, rec *recorder, args ...string) (code int, out, errw string) {
	t.Helper()
	var o, e bytes.Buffer
	code = runSetup(args, &o, &e, exe, rec.lookPath, rec.run)
	return code, o.String(), e.String()
}

func TestSetupPrintsFullPathCommandsForEachAgent(t *testing.T) {
	code, out, _ := setup(t, "/Users/me/go/bin/sop-mcp-server", &recorder{})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"claude mcp add --scope user joltrin -- /Users/me/go/bin/sop-mcp-server",
		"codex mcp add joltrin -- /Users/me/go/bin/sop-mcp-server",
		"gemini mcp add --scope user joltrin /Users/me/go/bin/sop-mcp-server",
		"setup --apply",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSetupQuotesAPathWithSpaces(t *testing.T) {
	_, out, _ := setup(t, "/Users/Jane Doe/go/bin/sop-mcp-server", &recorder{})
	if !strings.Contains(out, "-- '/Users/Jane Doe/go/bin/sop-mcp-server'") {
		t.Errorf("a path with a space must be quoted:\n%s", out)
	}
}

func TestSetupLessonsSetsTheMemoryFolderWhereTheAgentSupportsIt(t *testing.T) {
	_, out, _ := setup(t, "/bin/sop-mcp-server", &recorder{}, "--lessons", "/home/me/.joltrin")
	for _, want := range []string{
		"claude mcp add --scope user joltrin -e SOP_LESSONS_DIR=/home/me/.joltrin -- /bin/sop-mcp-server",
		"codex mcp add joltrin --env SOP_LESSONS_DIR=/home/me/.joltrin -- /bin/sop-mcp-server",
		"gemini mcp add --scope user joltrin /bin/sop-mcp-server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gemini mcp add --scope user joltrin -e") {
		t.Error("the Gemini command must not carry -e, which swallows the arguments after it")
	}
}

func TestSetupApplyRegistersOnlyTheAgentsFound(t *testing.T) {
	rec := &recorder{have: map[string]bool{"claude": true}}
	code, out, _ := setup(t, "/bin/sop-mcp-server", rec, "--apply")
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if len(rec.calls) != 2 || rec.calls[0][0] != "claude" || rec.calls[1][0] != "claude" {
		t.Fatalf("want a remove then an add, both to claude, got %v", rec.calls)
	}
	remove := []string{"claude", "mcp", "remove", "--scope", "user", "joltrin"}
	if strings.Join(rec.calls[0], " ") != strings.Join(remove, " ") {
		t.Errorf("first call = %v, want %v", rec.calls[0], remove)
	}
	want := []string{"claude", "mcp", "add", "--scope", "user", "joltrin", "--", "/bin/sop-mcp-server"}
	if strings.Join(rec.calls[1], " ") != strings.Join(want, " ") {
		t.Errorf("second call = %v, want %v", rec.calls[1], want)
	}
	if !strings.Contains(out, "registered") || !strings.Contains(out, "codex is not installed here, skipped") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSetupApplyReportsAFailureAndExitsNonZero(t *testing.T) {
	rec := &recorder{have: map[string]bool{"claude": true, "codex": true}, fail: map[string]error{"claude": errors.New("boom")}}
	code, out, _ := setup(t, "/bin/sop-mcp-server", rec, "--apply")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "failed: boom") {
		t.Errorf("the failure should be shown:\n%s", out)
	}
	if len(rec.calls) != 3 { // claude remove and add, then codex add
		t.Errorf("a failure must not stop the other agents, calls = %v", rec.calls)
	}
}

func TestSetupApplyWithNoAgentCLIExitsNonZero(t *testing.T) {
	code, out, _ := setup(t, "/bin/sop-mcp-server", &recorder{}, "--apply")
	if code != 1 || !strings.Contains(out, "No agent CLI found") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}

func TestSetupRefusesATemporaryGoRunBinary(t *testing.T) {
	rec := &recorder{have: map[string]bool{"claude": true}}
	code, _, errw := setup(t, "/var/folders/ab/T/go-build123/b001/exe/sop-mcp-server", rec, "--apply")
	if code != 1 || len(rec.calls) != 0 {
		t.Errorf("exit %d, calls %v: a go run binary must not be registered", code, rec.calls)
	}
	if !strings.Contains(errw, "go install") {
		t.Errorf("the message should say how to install it:\n%s", errw)
	}
}

func TestSetupUnknownFlagIsAUsageError(t *testing.T) {
	code, _, _ := setup(t, "/bin/sop-mcp-server", &recorder{}, "--nope")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestSetupRunbooksSetsTheEnvironmentWithAnAbsolutePath(t *testing.T) {
	// On Windows an absolute-looking path like /home/me gains a drive letter,
	// so the expected value goes through filepath.Abs like the code does.
	path, err := filepath.Abs("/home/me/my runbooks.json")
	if err != nil {
		t.Fatal(err)
	}
	runbooks := shellQuote("SOP_RUNBOOKS=" + path)
	_, out, _ := setup(t, "/bin/sop-mcp-server", &recorder{}, "--runbooks", "/home/me/my runbooks.json", "--lessons", "/home/me/.joltrin")
	for _, want := range []string{
		"claude mcp add --scope user joltrin -e SOP_LESSONS_DIR=/home/me/.joltrin -e " + runbooks + " -- /bin/sop-mcp-server",
		"codex mcp add joltrin --env SOP_LESSONS_DIR=/home/me/.joltrin --env " + runbooks + " -- /bin/sop-mcp-server",
		"gemini mcp add --scope user joltrin /bin/sop-mcp-server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSetupRunbooksRelativePathBecomesAbsolute(t *testing.T) {
	abs, err := filepath.Abs("runbooks.json")
	if err != nil {
		t.Fatal(err)
	}
	_, out, _ := setup(t, "/bin/sop-mcp-server", &recorder{}, "--runbooks", "runbooks.json")
	if strings.Contains(out, "SOP_RUNBOOKS=runbooks.json") || !strings.Contains(out, shellQuote("SOP_RUNBOOKS="+abs)) {
		t.Errorf("an agent starts the server from any folder, so the path must be absolute:\n%s", out)
	}
}

func TestSetupApplyCanBeRunAgain(t *testing.T) {
	// Nothing is registered yet, so the remove fails. That must not matter.
	rec := &recorder{have: map[string]bool{"claude": true}, removeFails: true}
	code, out, _ := setup(t, "/bin/sop-mcp-server", rec, "--apply")
	if code != 0 || !strings.Contains(out, "registered") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestSetupApplyRegistersAgentsAtTheSameTime(t *testing.T) {
	rec := &recorder{have: map[string]bool{"claude": true, "codex": true, "gemini": true}, barrier: 3}
	code, out, _ := setup(t, "/bin/sop-mcp-server", rec, "--apply")
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	// The report keeps a fixed order however the registrations finish.
	c, x, g := strings.Index(out, "Claude Code"), strings.Index(out, "Codex"), strings.Index(out, "Gemini CLI")
	if !(c >= 0 && c < x && x < g) {
		t.Errorf("the report is out of order:\n%s", out)
	}
}
