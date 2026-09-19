package main

import "testing"

// isSafeRelativeRedirect is a thin alias over internal/netguard.
// IsSafeRelativeRedirect; the exhaustive bypass-attempt suite lives in
// internal/netguard/redirect_test.go so it isn't duplicated here. This is a
// smoke test confirming the wrapper actually delegates.
func TestIsSafeRelativeRedirect_DelegatesToNetguard(t *testing.T) {
	unsafe := []string{"", "https://evil.example/", "//evil.example/", `/\evil.example`}
	for _, target := range unsafe {
		if isSafeRelativeRedirect(target) {
			t.Errorf("isSafeRelativeRedirect(%q) = true, want false", target)
		}
	}

	safe := []string{"/", "/app", "/app?checkout=success&tier=pro"}
	for _, target := range safe {
		if !isSafeRelativeRedirect(target) {
			t.Errorf("isSafeRelativeRedirect(%q) = false, want true", target)
		}
	}
}
