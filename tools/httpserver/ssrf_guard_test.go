package main

import (
	"context"
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
