package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleViewer_ExternalDocIDShowsInterstitialNotDirectRedirect(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/viewer?docID=https://github.com/SharedCode/joltrin/README.md&text=hello", nil)
	rec := httptest.NewRecorder()

	handleViewer(rec, req)

	// A direct 302 to an attacker-suppliable docID is an open redirect; the
	// handler must show a confirmation page instead, never auto-navigate.
	if rec.Code == http.StatusFound {
		t.Fatalf("handleViewer auto-redirected to an external docID instead of showing an interstitial")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 interstitial page, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "https://github.com/SharedCode/joltrin/README.md") {
		t.Fatalf("interstitial page did not mention the external URL: %s", rec.Body.String())
	}
}

func TestHandleViewer_RejectsUnsupportedSchemeDocID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/viewer?docID=file:///etc/passwd", nil)
	rec := httptest.NewRecorder()

	handleViewer(rec, req)

	if rec.Code == http.StatusFound {
		t.Fatalf("handleViewer auto-redirected to a file:// docID")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported scheme, got %d: %s", rec.Code, rec.Body.String())
	}
}
