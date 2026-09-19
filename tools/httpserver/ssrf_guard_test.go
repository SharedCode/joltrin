package main

import (
	"context"
	"testing"
)

// validateImportURL and ssrfSafeHTTPClient are thin aliases over
// internal/netguard; the exhaustive test suite lives in
// internal/netguard/netguard_test.go so it isn't duplicated here. This is a
// smoke test confirming the wrapper actually delegates.
func TestValidateImportURL_DelegatesToNetguard(t *testing.T) {
	if err := validateImportURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("validateImportURL allowed the cloud metadata endpoint")
	}
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
