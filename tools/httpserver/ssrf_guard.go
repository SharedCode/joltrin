package main

import (
	"net/http"

	"github.com/sharedcode/joltrin/internal/netguard"
)

// validateImportURL and ssrfSafeHTTPClient are thin aliases over
// internal/netguard, kept so existing call sites in this package don't need
// to change. See internal/netguard for the guarantees and the DNS-rebinding
// rationale.
func validateImportURL(rawURL string) error {
	return netguard.ValidateFetchURL(rawURL)
}

func ssrfSafeHTTPClient() *http.Client {
	return netguard.SafeClient()
}
