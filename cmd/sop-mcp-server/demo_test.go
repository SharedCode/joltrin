package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestDemoBlocksTheDropThenAllowsItInOrder(t *testing.T) {
	var out bytes.Buffer
	if code := runDemoArgs(nil, &out, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	s := out.String()
	if strings.Count(s, "BLOCKED") != 1 || strings.Count(s, "ALLOWED") != 3 {
		t.Errorf("want one block and three allowed steps:\n%s", s)
	}
	blocked := strings.Index(s, "BLOCKED")
	for _, step := range []string{"take_backup", "validate_backup", "drop_prod_db"} {
		if i := strings.LastIndex(s, step); i < blocked {
			t.Errorf("%s should be allowed only after the block:\n%s", step, s)
		}
	}
	for _, want := range []string{"backup_validated", "trace: [take_backup validate_backup drop_prod_db]"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

// A command whose output cannot be written must fail, not exit 0 with the
// result cut off. A closed pipe is the usual cause.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestAFailedJSONWriteIsAFailedCommand(t *testing.T) {
	var errw bytes.Buffer
	if code := runDemoArgs([]string{"--json"}, brokenWriter{}, &errw); code != 1 || !strings.Contains(errw.String(), "broken pipe") {
		t.Errorf("demo --json: exit %d, stderr %q", code, errw.String())
	}
	errw.Reset()
	if code := runCheck([]string{"--json", writeRunbook(t, goodRunbook)}, brokenWriter{}, &errw); code != 1 || !strings.Contains(errw.String(), "broken pipe") {
		t.Errorf("check --json: exit %d, stderr %q", code, errw.String())
	}
}
