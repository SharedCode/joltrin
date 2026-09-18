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
