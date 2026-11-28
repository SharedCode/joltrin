package sealed_secret

import (
	"bytes"
	"strings"
	"testing"
)

func TestDemoRunsAndNeverPrintsTheSecret(t *testing.T) {
	var out bytes.Buffer
	if err := Run(&out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	for _, want := range []string{"charge accepted", "blocked: plain text", "blocked: base64", "refused (not signed)", "refused (signed by another key)", "verifies offline"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sk-live-") {
		t.Errorf("output contains key text:\n%s", got)
	}
}
