package main

import (
	"reflect"
	"testing"
)

func TestExtractCitations_FindsDistinctLabelsInOrder(t *testing.T) {
	text := "The onboarding process takes 3 steps [source: onboarding-guide]. " +
		"Refunds are handled separately [source: billing-faq], and the same " +
		"onboarding rule applies again here [source: onboarding-guide]."

	got := extractCitations(text)
	want := []string{"onboarding-guide", "billing-faq"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractCitations() = %v, want %v", got, want)
	}
}

func TestExtractCitations_NoCitationsReturnsNil(t *testing.T) {
	got := extractCitations("Plain answer with no citation markup at all.")
	if len(got) != 0 {
		t.Errorf("extractCitations() = %v, want empty", got)
	}
}

func TestExtractCitations_IgnoresUnclosedTag(t *testing.T) {
	got := extractCitations("Answer with a broken tag [source: never closed")
	if len(got) != 0 {
		t.Errorf("extractCitations() = %v, want empty for an unclosed tag", got)
	}
}

func TestExtractCitations_TrimsWhitespaceInLabel(t *testing.T) {
	got := extractCitations("See [source:   kb-42  ] for details.")
	want := []string{"kb-42"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractCitations() = %v, want %v", got, want)
	}
}
