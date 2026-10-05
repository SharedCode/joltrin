package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDemoBlocksTheDropThenAllowsItInOrder(t *testing.T) {
	var out bytes.Buffer
	if code := runDemo(&out); code != 0 {
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
