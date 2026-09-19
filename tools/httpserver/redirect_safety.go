package main

import (
	"fmt"
	"html"
	"net/http"
	"net/url"

	"github.com/sharedcode/joltrin/internal/netguard"
)

// isSafeRelativeRedirect is a thin alias over internal/netguard, kept so
// existing call sites in this package don't need to change. See
// internal/netguard for the exploitability investigation, citations, and
// bypass classes this guard was tested against (CodeQL alert 268).
func isSafeRelativeRedirect(target string) bool {
	return netguard.IsSafeRelativeRedirect(target)
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
