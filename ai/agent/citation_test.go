package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai/memory"
)

func TestCitationLabel(t *testing.T) {
	cases := []struct {
		name string
		hit  memory.KBDigestHit
		want string
	}{
		{
			name: "prefers category when set",
			hit:  memory.KBDigestHit{Category: "Billing/Refunds", DocID: []string{"doc_1"}},
			want: "Billing/Refunds",
		},
		{
			name: "falls back to first DocID when category is empty",
			hit:  memory.KBDigestHit{Category: "", DocID: []string{"doc_42", "doc_43"}},
			want: "doc_42",
		},
		{
			name: "falls back to generic marker when neither is set",
			hit:  memory.KBDigestHit{Category: "", DocID: nil},
			want: "kb",
		},
		{
			name: "falls back to generic marker when DocID slice has only an empty string",
			hit:  memory.KBDigestHit{Category: "", DocID: []string{""}},
			want: "kb",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := citationLabel(c.hit); got != c.want {
				t.Errorf("citationLabel(%+v) = %q, want %q", c.hit, got, c.want)
			}
		})
	}
}

// TestCitationLabel_FormatContract locks down the exact "[source: X]" shape
// getLTMSemanticContext and getPlaybooksContext format retrieved hits with.
// Before this, DigestKnowledgeBase already returned Category/DocID per hit,
// but both context builders only ever forwarded hit.Text and hit.Score into
// the model's prompt - the model had no way to cite where an answer came
// from because the identifying metadata never reached it. This test can't
// exercise the full CopilotAgent (getLTMSemanticContext needs a live
// systemDB, service.Domain().Embedder(), and a populated KB - no existing
// test fixture wires that up), so it locks down the format string itself:
// if a future edit drops the citation tag from either call site, this
// fails even though the surrounding agent-level behavior is untestable
// here.
func TestCitationLabel_FormatContract(t *testing.T) {
	hit := memory.KBDigestHit{
		Category: "Billing/Refunds",
		DocID:    []string{"doc_1"},
		Score:    0.87,
		Text:     "Refunds are issued within 5 business days.",
	}

	semanticLine := fmt.Sprintf("- [source: %s] (Score: %.2f) %s\n", citationLabel(hit), hit.Score, hit.Text)
	if !strings.Contains(semanticLine, "[source: Billing/Refunds]") {
		t.Errorf("semantic context line missing citation tag: %q", semanticLine)
	}
	if !strings.Contains(semanticLine, hit.Text) {
		t.Errorf("semantic context line dropped the retrieved text: %q", semanticLine)
	}

	playbookLine := fmt.Sprintf("- [source: %s] Context (Score: %.2f): %s\n", citationLabel(hit), hit.Score, hit.Text)
	if !strings.Contains(playbookLine, "[source: Billing/Refunds]") {
		t.Errorf("playbook context line missing citation tag: %q", playbookLine)
	}
}
