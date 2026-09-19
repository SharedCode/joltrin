package main

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
)

// isSafeRelativeRedirect reports whether target is safe to hand to
// http.Redirect without an open-redirect risk: it must be a same-origin,
// relative path, never an absolute URL ("https://evil.example/...") and
// never a protocol-relative one. Browsers resolve both "//evil.example/..."
// and the backslash variant "/\evil.example/..." (some browsers normalize
// a leading backslash to a slash) against evil.example even without a
// scheme, so both of the first two characters must be checked, not just
// the second.
//
// CodeQL alert 268 (go/unvalidated-url-redirection) flags the call site in
// billing_handler.go that uses this function as a guard, because its
// dataflow analysis doesn't model arbitrary boolean functions as sanitizer
// barriers. This was investigated for a real bypass rather than dismissed;
// the property below is what makes the guard sound, verified against real
// behavior rather than assumed:
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
// Unicode fullwidth solidus. See redirect_safety_test.go for the exact
// payloads and TestHandleSimulateCheckout_RejectsOpenRedirect variants in
// billing_handler_test.go for the end-to-end handler assertions.
func isSafeRelativeRedirect(target string) bool {
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

// writeExternalLinkInterstitial renders a confirmation page instead of
// auto-redirecting to an external URL, so a crafted link to this server
// can't silently bounce a logged-in user's browser to an attacker's site
// (classic open-redirect phishing). Only http(s) targets are offered as a
// clickable link; anything else is rejected outright.
func writeExternalLinkInterstitial(w http.ResponseWriter, target string) {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "unsupported external link", http.StatusBadRequest)
		return
	}
	escaped := html.EscapeString(target)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Leaving this site</title></head>
<body>
<p>This document points to an external link:</p>
<p><code>%s</code></p>
<p><a href="%s" rel="noopener noreferrer">Continue to this link</a></p>
</body></html>`, escaped, escaped)
}
