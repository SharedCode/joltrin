package main

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleExportSpace_ContentDisposition_SafeFilename confirms that a
// benign space name produces a well-formed Content-Disposition header with
// the expected filename parameter and does NOT write the raw name as a bare
// string into the header value.
func TestHandleExportSpace_ContentDisposition_SafeFilename(t *testing.T) {
	disposition := buildDisposition("my-space")
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		t.Fatalf("mime.ParseMediaType(%q) = %v, want parseable", disposition, err)
	}
	if params["filename"] != "my-space_export.json" {
		t.Errorf("filename param = %q, want %q", params["filename"], "my-space_export.json")
	}
}

// TestHandleExportSpace_ContentDisposition_NoHeaderInjectionCRLF is the
// regression test for the header injection finding. Before the fix,
// spaceName was concatenated into the header value without escaping.
// A name containing \r\n would terminate the Content-Disposition header and
// start a new, attacker-controlled one. mime.FormatMediaType must encode the
// value so no literal CR or LF reaches the response headers.
func TestHandleExportSpace_ContentDisposition_NoHeaderInjectionCRLF(t *testing.T) {
	// The crafted name tries to inject a Set-Cookie header after the
	// Content-Disposition header value ends with the injected CRLF.
	malicious := "space\r\nSet-Cookie: pwn=1"
	disposition := buildDisposition(malicious)

	// The resulting header value must not contain a bare CR or LF; those
	// bytes are what an HTTP client or proxy would use to parse a new header.
	if strings.Contains(disposition, "\r") || strings.Contains(disposition, "\n") {
		t.Errorf("Content-Disposition value contains bare CR or LF after mime.FormatMediaType: %q", disposition)
	}

	// The value must also be parseable as a single header; mime.ParseMediaType
	// failing here would mean the header is malformed in a different way.
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		// FormatMediaType may return "" (and we fall back to a static name)
		// for inputs it cannot encode safely; that is also acceptable
		// because the fallback is a static, injection-free value.
		if disposition == `attachment; filename="export.json"` {
			return
		}
		t.Fatalf("mime.ParseMediaType(%q) = %v, want parseable or fallback", disposition, err)
	}

	// Whatever filename ended up in the parameter, it must not contain the
	// injected literal bytes or header syntax.
	fn := params["filename"]
	if strings.Contains(fn, "\r") || strings.Contains(fn, "\n") {
		t.Errorf("filename param contains CR/LF: %q", fn)
	}
}

// TestHandleExportSpace_ContentDisposition_NoQuoteEscape confirms that a
// name containing a double-quote cannot break out of the filename token and
// introduce additional Content-Disposition parameters.
func TestHandleExportSpace_ContentDisposition_NoQuoteEscape(t *testing.T) {
	// Before the fix: `attachment; filename="evil"_export.json"` would
	// close the filename token early at the second `"` and leave trailing
	// garbage that some parsers treat as additional parameters.
	malicious := `evil"_extra`
	disposition := buildDisposition(malicious)

	_, _, err := mime.ParseMediaType(disposition)
	if err != nil && disposition != `attachment; filename="export.json"` {
		// Either the value is valid (FormatMediaType quoted the `"`) or it
		// is the safe static fallback. Both are correct; an unparseable,
		// non-fallback value is the bug.
		t.Errorf("mime.ParseMediaType(%q) = %v: header is neither well-formed nor the safe fallback", disposition, err)
	}
}

// TestHandleExportSpace_MissingQueryParams_Returns400 verifies that incomplete
// export requests are rejected before database access.
func TestHandleExportSpace_MissingQueryParams_Returns400(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"no params", ""},
		{"missing name", "?database=mydb"},
		{"missing database", "?name=myspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/space/export"+tc.query, nil)
			rec := httptest.NewRecorder()
			handleExportSpace(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for %q, got %d", tc.query, rec.Code)
			}
		})
	}
}

// buildDisposition is the same logic the handler uses to produce a
// Content-Disposition value, extracted so the table-driven tests above can
// call it directly without standing up a full HTTP server or a real database.
func buildDisposition(spaceName string) string {
	return exportContentDisposition(spaceName)
}
