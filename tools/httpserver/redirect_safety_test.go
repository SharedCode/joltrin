package main

import "testing"

func TestIsSafeRelativeRedirect(t *testing.T) {
	unsafe := []string{
		"",
		"https://evil.example/",
		"http://evil.example/",
		"//evil.example/",
		"//evil.example",
		`/\evil.example/`,
		`/\evil.example`,
		"evil.example",
		"not-a-path",
	}
	for _, target := range unsafe {
		if isSafeRelativeRedirect(target) {
			t.Errorf("isSafeRelativeRedirect(%q) = true, want false", target)
		}
	}

	safe := []string{
		"/",
		"/app",
		"/app?checkout=success&tier=pro",
		"/a/b/c",
	}
	for _, target := range safe {
		if !isSafeRelativeRedirect(target) {
			t.Errorf("isSafeRelativeRedirect(%q) = false, want true", target)
		}
	}
}
