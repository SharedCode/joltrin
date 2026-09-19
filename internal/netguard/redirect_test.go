package netguard

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
		if IsSafeRelativeRedirect(target) {
			t.Errorf("IsSafeRelativeRedirect(%q) = true, want false", target)
		}
	}

	safe := []string{
		"/",
		"/app",
		"/app?checkout=success&tier=pro",
		"/a/b/c",
	}
	for _, target := range safe {
		if !IsSafeRelativeRedirect(target) {
			t.Errorf("IsSafeRelativeRedirect(%q) = false, want true", target)
		}
	}
}

// TestIsSafeRelativeRedirect_RejectedBypassAttempts is the record of the
// exploitability investigation for CodeQL alert 268
// (go/unvalidated-url-redirection). Each entry here is a payload class that
// must be rejected outright by IsSafeRelativeRedirect itself: either it can
// form a real network-path/scheme reference in Go's own net/url, or it
// contains a byte the WHATWG URL parser (what a real browser uses to
// resolve a Location header) treats specially in a way Go's parser doesn't
// warn about on its own.
func TestIsSafeRelativeRedirect_RejectedBypassAttempts(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"triple slash", "///evil.example/phish"},
		{"doubled backslash", `/\\evil.example/phish`},
		{"embedded tab (WHATWG strips tab from anywhere in the string; collapses to //evil.example)", "/\t/evil.example"},
		{"embedded newline", "/\n/evil.example"},
		{"embedded carriage return", "/\r/evil.example"},
		{"CRLF header-injection attempt", "/\r\nSet-Cookie: pwn=1"},
		{"embedded vertical tab", "/\v/evil.example"},
		{"embedded form feed", "/\f/evil.example"},
		{"leading space (not a leading slash at all)", " //evil.example"},
		{"null byte", "/\x00evil.example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if IsSafeRelativeRedirect(c.target) {
				t.Errorf("IsSafeRelativeRedirect(%q) = true, want false (bypass class: %s)", c.target, c.name)
			}
		})
	}
}

// TestIsSafeRelativeRedirect_AcceptedButVerifiedHarmless covers payloads that
// IsSafeRelativeRedirect correctly lets through as-is (it returns true, so
// the caller uses them unmodified) because they look suspicious but cannot
// actually produce an off-origin redirect: they have no way to introduce a
// literal "//" or "scheme:" prefix once decoded by any real URL parser.
// Confirmed against Node's WHATWG URL implementation (the same resolution
// algorithm browsers apply to a Location header) — every one of these
// resolves to the same origin as the page, not to evil.example. See the
// citations in the IsSafeRelativeRedirect doc comment.
func TestIsSafeRelativeRedirect_AcceptedButVerifiedHarmless(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"percent-encoded double slash never re-decoded as authority", "/%2F%2Fevil.example"},
		{"percent-encoded backslash never re-decoded as authority", "/%5Cevil.example"},
		{"double percent-encoded slash stays literal text", "/%252F%252Fevil.example"},
		{"embedded space is not a URI delimiter", "/ /evil.example"},
		{"dot-segment traversal never introduces a host", "/../../evil.example"},
		{"encoded dot-segment traversal never introduces a host", "/%2e%2e/evil.example"},
		{"at-sign has no authority to attach userinfo to", "/@evil.example"},
		{"scheme syntax requires the string to start with a letter, not '/'", "/HTTP://evil.example"},
		{"same: no scheme state reachable after a leading slash", "/JAVASCRIPT:alert(1)"},
		{"non-ASCII fullwidth solidus is not a URI delimiter", "/／evil.example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !IsSafeRelativeRedirect(c.target) {
				t.Errorf("IsSafeRelativeRedirect(%q) = false, want true (documented harmless: %s)", c.target, c.name)
			}
		})
	}
}
