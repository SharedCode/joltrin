package netguard

import "net/url"

// IsSafeRelativeRedirect reports whether target is safe to hand to
// http.Redirect without an open-redirect risk: it must be a same-origin,
// relative path, never an absolute URL ("https://evil.example/...") and
// never a protocol-relative one. Browsers resolve both "//evil.example/..."
// and the backslash variant "/\evil.example/..." (some browsers normalize
// a leading backslash to a slash) against evil.example even without a
// scheme, so both of the first two characters must be checked, not just
// the second.
//
// This was investigated for real bypasses (CodeQL alert 268,
// go/unvalidated-url-redirection, flagged a call site using this guard
// because its dataflow analysis doesn't model arbitrary boolean functions
// as sanitizer barriers) rather than assumed safe. The properties below are
// what make the guard sound, verified against real behavior:
//
//   - A relative reference can only become a network-path reference (i.e.
//     get an authority/host) if it begins with two literal '/' characters,
//     or a scheme + ':' if it begins with a letter. Percent-encoding a
//     slash or backslash (%2F, %5C) does not count: encoded delimiters are
//     never re-decoded during scheme/authority parsing. Confirmed against
//     Node's WHATWG URL implementation (same algorithm real browsers use to
//     resolve a Location header) — new URL("/%2F%2Fevil.example", base)
//     resolves same-origin, not to evil.example. See the WHATWG URL
//     Standard, "basic URL parser": https://url.spec.whatwg.org/#url-parsing
//   - The WHATWG parser also strips every ASCII tab/CR/LF from anywhere in
//     the string before parsing (not just the ends), so a payload like
//     "/\t/evil.example" collapses to "//evil.example" in a real browser.
//     That path is still closed here because Go's net/url.Parse rejects any
//     ASCII control byte anywhere in the input before this function ever
//     inspects target[1] or u.Host — see stringContainsCTLByte's use in the
//     unexported parse() in $GOROOT/src/net/url/url.go (verified against
//     go1.23.2 locally: url.Parse("/\t/evil.example") returns
//     "invalid control character in URL"). If a future Go release ever
//     relaxed that rejection, this function would need its own explicit
//     control-byte check; it does not have one today because it doesn't
//     need one, not because the case was overlooked.
//   - A target that begins with '/' can never enter the URL scheme-parsing
//     state (schemes must start with an ASCII letter), so upper/lower/mixed
//     -case scheme smuggling ("/JAVASCRIPT:...", "/HTTP://evil.example")
//     is structurally impossible, not just blocked by luck.
//
// Bypass classes tried and confirmed non-exploitable: absolute URLs,
// protocol-relative and its backslash variant (single and doubled),
// triple-slash, single- and double-percent-encoded slash/backslash,
// embedded tab/CR/LF/vertical-tab/form-feed, plain whitespace, dot-segment
// traversal ("/../evil.example", collapsed safely by both Go's path.Clean
// inside http.Redirect and the WHATWG parser), "@"-prefixed hosts, and a
// Unicode fullwidth solidus. See redirect_test.go for the exact payloads.
func IsSafeRelativeRedirect(target string) bool {
	if target == "" || target[0] != '/' {
		return false
	}
	if len(target) > 1 && (target[1] == '/' || target[1] == '\\') {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return u.Scheme == "" && u.Host == ""
}
