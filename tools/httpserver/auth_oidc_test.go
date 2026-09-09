package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/governance"
)

func makeSignedTestJWT(claims map[string]any, signingKey []byte) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	input := header + "." + payload
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(input))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return input + "." + sig
}

func setupTestOIDC(tier governance.Tier) []byte {
	signingKey := []byte("oidc-shared-test-secret")

	// Reset sync.Once and set config for clean test isolation
	oidcOnce = sync.Once{}
	config.Tier = tier
	config.OIDCProviders = []governance.OIDCConfig{
		{
			Type:             governance.IdPTypeGitHub,
			ID:               "github",
			DisplayName:      "GitHub",
			ClientID:         "gh-test-id",
			RedirectURL:      "http://localhost:8080/api/auth/oidc/callback",
			SigningKeySecret: signingKey,
			DefaultRole:      "User",
		},
		{
			Type:             governance.IdPTypeOkta,
			ID:               "okta",
			DisplayName:      "Okta",
			Domain:           "company.okta.com",
			ClientID:         "okta-test-id",
			RedirectURL:      "http://localhost:8080/api/auth/oidc/callback",
			SigningKeySecret: signingKey,
			RoleMapping: map[string]string{
				"OktaAdmins": "Admin",
			},
			DefaultRole: "User",
		},
		{
			Type:             governance.IdPTypeEntraID,
			ID:               "entraid",
			DisplayName:      "Microsoft Entra ID",
			TenantID:         "tenant-12345",
			ClientID:         "entra-test-id",
			RedirectURL:      "http://localhost:8080/api/auth/oidc/callback",
			SigningKeySecret: signingKey,
			RoleMapping: map[string]string{
				"Directory.Admins": "Admin",
			},
			DefaultRole: "User",
		},
	}
	return signingKey
}

func TestOIDC_ListProviders(t *testing.T) {
	setupTestOIDC(governance.TierCore)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	w := httptest.NewRecorder()

	handleListAuthProviders(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Providers  []ProviderResponse `json:"providers"`
		ServerTier string             `json:"server_tier"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should include Local, GitHub, Okta, and Entra ID
	if len(resp.Providers) != 4 {
		t.Fatalf("expected 4 providers, got %d", len(resp.Providers))
	}

	for _, p := range resp.Providers {
		if p.ID == "github" && !p.IsEntitled {
			t.Errorf("GitHub should be entitled in Core tier")
		}
		if (p.ID == "okta" || p.ID == "entraid") && p.IsEntitled {
			t.Errorf("%s should not be entitled in Core tier", p.DisplayName)
		}
	}
}

func TestOIDC_AuthorizeRedirect(t *testing.T) {
	setupTestOIDC(governance.TierCore)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/authorize?provider=github", nil)
	w := httptest.NewRecorder()

	handleOIDCAuthorize(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "github.com/login/oauth/authorize") || !strings.Contains(loc, "client_id=gh-test-id") {
		t.Errorf("malformed redirect location: %s", loc)
	}
}

func TestOIDC_TokenAuthentication_GitHubAndOkta(t *testing.T) {
	signingKey := setupTestOIDC(governance.TierCore)

	// 1. GitHub OIDC Token on Core Tier (Allowed)
	ghClaims := map[string]any{
		"sub":   "repo:sharedcode/joltrin:ref:refs/heads/master",
		"iss":   "https://token.actions.githubusercontent.com",
		"aud":   "gh-test-id",
		"actor": "developer-dave",
		"exp":   float64(time.Now().Add(time.Hour).Unix()),
	}
	ghToken := makeSignedTestJWT(ghClaims, signingKey)

	reqBody := `{"provider":"github","token":"` + ghToken + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/token", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	handleOIDCToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for GitHub token, got %d: %s", w.Code, w.Body.String())
	}

	var ghResp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.NewDecoder(w.Body).Decode(&ghResp); err != nil {
		t.Fatalf("failed to parse ghResp: %v", err)
	}
	if ghResp.User.Username != "developer-dave" {
		t.Errorf("expected username developer-dave, got %s", ghResp.User.Username)
	}

	// 2. Okta Token on Core Tier (Blocked - Enterprise Tier Required)
	oktaClaims := map[string]any{
		"sub":    "00u123",
		"iss":    "https://company.okta.com/oauth2/default",
		"aud":    "okta-test-id",
		"email":  "corp@company.com",
		"groups": []any{"OktaAdmins"},
		"exp":    float64(time.Now().Add(time.Hour).Unix()),
	}
	oktaToken := makeSignedTestJWT(oktaClaims, signingKey)

	reqBody = `{"provider":"okta","token":"` + oktaToken + `"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/oidc/token", strings.NewReader(reqBody))
	w = httptest.NewRecorder()

	handleOIDCToken(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for Okta under Core tier, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Okta Token on Enterprise Tier (Allowed & Role Mapped to Admin)
	setupTestOIDC(governance.TierEnterprise)

	req = httptest.NewRequest(http.MethodPost, "/api/auth/oidc/token", strings.NewReader(reqBody))
	w = httptest.NewRecorder()

	handleOIDCToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for Okta under Enterprise tier, got %d: %s", w.Code, w.Body.String())
	}

	var oktaResp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
	}
	if err := json.NewDecoder(w.Body).Decode(&oktaResp); err != nil {
		t.Fatalf("failed to decode oktaResp: %v", err)
	}
	if oktaResp.User.Role != "Admin" {
		t.Errorf("expected Okta user to be mapped to Admin, got %s", oktaResp.User.Role)
	}
}

func TestOIDC_CallbackFlow(t *testing.T) {
	signingKey := setupTestOIDC(governance.TierCore)

	ghClaims := map[string]any{
		"sub":   "sub-user-999",
		"iss":   "https://token.actions.githubusercontent.com",
		"aud":   "gh-test-id",
		"actor": "octocat-dev",
		"exp":   float64(time.Now().Add(time.Hour).Unix()),
	}
	token := makeSignedTestJWT(ghClaims, signingKey)

	// Browser HTML request
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?provider=github&token="+token, nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()

	handleOIDCCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect for HTML callback, got %d", w.Code)
	}
	if w.Header().Get("Location") != "/app" {
		t.Errorf("expected redirect to /app, got %s", w.Header().Get("Location"))
	}

	// Verify session cookie was set
	cookies := w.Result().Cookies()
	hasTokenCookie := false
	for _, c := range cookies {
		if c.Name == "sop_access_token" && c.Value != "" {
			hasTokenCookie = true
		}
	}
	if !hasTokenCookie {
		t.Errorf("expected sop_access_token session cookie to be set")
	}
}
