package main

import (
	"context"
	"net/http"
	"testing"
)

func TestValidateImportURL_RejectsInternalTargets(t *testing.T) {
	rejected := []string{
		"http://127.0.0.1/",
		"http://localhost/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/internal",
		"http://192.168.1.1/",
		"ftp://example.com/file",
		"not-a-url",
		"",
	}
	for _, u := range rejected {
		if err := validateImportURL(u); err == nil {
			t.Errorf("validateImportURL(%q) = nil, want error", u)
		}
	}
}

func TestValidateImportURL_AllowsPublicHTTPS(t *testing.T) {
	if err := validateImportURL("https://example.com/doc.txt"); err != nil {
		t.Errorf("validateImportURL(public https) = %v, want nil", err)
	}
}

func TestIngestImportReader_RejectsInternalURL(t *testing.T) {
	req := IngestSpaceRequest{URL: "http://169.254.169.254/latest/meta-data/iam/security-credentials/"}
	_, _, err := ingestImportReader(context.Background(), req)
	if err == nil {
		t.Fatal("ingestImportReader allowed a request to the cloud metadata endpoint")
	}
}

// TestSSRFSafeHTTPClient_RejectsAtDialTime covers the DNS-rebinding gap that
// validateImportURL alone can't close: a hostname that resolves to a public
// address at request-build time but an internal one by the time the socket
// is actually opened. The client's DialContext must re-resolve and re-check
// on every dial, not just trust an earlier lookup.
func TestSSRFSafeHTTPClient_RejectsAtDialTime(t *testing.T) {
	client := ssrfSafeHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatal("ssrfSafeHTTPClient did not configure a validating DialContext")
	}
	for _, addr := range []string{"127.0.0.1:80", "169.254.169.254:80", "10.0.0.5:443"} {
		if conn, err := transport.DialContext(context.Background(), "tcp", addr); err == nil {
			conn.Close()
			t.Errorf("DialContext(%q) succeeded, want rejection of internal address", addr)
		}
	}
}
