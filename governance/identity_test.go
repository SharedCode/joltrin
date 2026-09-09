package governance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func createTestJWT(claims map[string]any, signingKey []byte) string {
	headerJSON := `{"alg":"HS256","typ":"JWT"}`
	header := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))

	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)

	input := header + "." + payload
	if len(signingKey) > 0 {
		mac := hmac.New(sha256.New, signingKey)
		mac.Write([]byte(input))
		sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		return input + "." + sig
	}
	return input + ".dummy-sig"
}

func TestGitHubOIDCProvider(t *testing.T) {
	ctx := context.Background()
	signingKey := []byte("secret-key-1234")

	cfg := OIDCConfig{
		ClientID:         "gh-client-id",
		RedirectURL:      "http://localhost:8080/callback",
		SigningKeySecret: signingKey,
		DefaultRole:      "User",
	}

	gh := NewGitHubOIDCProvider(cfg)
	if gh.Type() != IdPTypeGitHub || gh.RequiredTier() != TierCore {
		t.Fatalf("unexpected type/tier: %v / %v", gh.Type(), gh.RequiredTier())
	}

	authURL := gh.AuthorizationURL("test-state", "test-nonce")
	if !strings.Contains(authURL, "github.com/login/oauth/authorize") || !strings.Contains(authURL, "client_id=gh-client-id") {
		t.Errorf("malformed GitHub auth URL: %s", authURL)
	}

	// Valid token test
	claimsMap := map[string]any{
		"sub":              "repo:sharedcode/joltrin:ref:refs/heads/master",
		"iss":              "https://token.actions.githubusercontent.com",
		"aud":              "gh-client-id",
		"actor":            "octocat",
		"repository":       "sharedcode/joltrin",
		"repository_owner": "sharedcode",
		"exp":              float64(time.Now().Add(time.Hour).Unix()),
	}
	rawToken := createTestJWT(claimsMap, signingKey)

	claims, err := gh.ValidateToken(ctx, rawToken)
	if err != nil {
		t.Fatalf("failed to validate token: %v", err)
	}
	if claims.Actor != "octocat" || claims.Repository != "sharedcode/joltrin" {
		t.Errorf("mismatched claims: %+v", claims)
	}

	user, err := gh.MapClaimsToUser(claims)
	if err != nil {
		t.Fatalf("failed to map claims: %v", err)
	}
	if user.Username != "octocat" || user.Role != "User" {
		t.Errorf("mismatched user: %+v", user)
	}
}

func TestOktaProvider(t *testing.T) {
	ctx := context.Background()
	signingKey := []byte("okta-test-secret")

	cfg := OIDCConfig{
		Domain:           "company.okta.com",
		ClientID:         "okta-client-123",
		RedirectURL:      "https://app.joltrin.com/auth/callback",
		SigningKeySecret: signingKey,
		RoleMapping: map[string]string{
			"JoltrinAdmins": "Admin",
			"Developers":    "User",
		},
	}

	okta := NewOktaProvider(cfg)
	if okta.RequiredTier() != TierEnterprise {
		t.Errorf("Okta must require TierEnterprise, got %s", okta.RequiredTier())
	}

	authURL := okta.AuthorizationURL("okta-state", "okta-nonce")
	if !strings.Contains(authURL, "company.okta.com/oauth2/v1/authorize") {
		t.Errorf("malformed Okta auth URL: %s", authURL)
	}

	// Role mapping verification
	claimsMap := map[string]any{
		"sub":                "00u1234567890",
		"iss":                "https://company.okta.com/oauth2/default",
		"aud":                "okta-client-123",
		"email":              "alice@company.com",
		"preferred_username": "alice",
		"groups":             []any{"JoltrinAdmins", "Engineering"},
		"exp":                float64(time.Now().Add(time.Hour).Unix()),
	}
	rawToken := createTestJWT(claimsMap, signingKey)

	claims, err := okta.ValidateToken(ctx, rawToken)
	if err != nil {
		t.Fatalf("failed to validate Okta token: %v", err)
	}

	user, err := okta.MapClaimsToUser(claims)
	if err != nil {
		t.Fatalf("failed to map claims: %v", err)
	}
	if user.Role != "Admin" || user.Username != "alice" {
		t.Errorf("expected mapped role Admin and username alice, got %+v", user)
	}
}

func TestEntraIDProvider(t *testing.T) {
	ctx := context.Background()
	signingKey := []byte("entraid-test-secret")

	tenantGUID := "88888888-4444-4444-4444-121212121212"
	cfg := OIDCConfig{
		TenantID:         tenantGUID,
		ClientID:         "entra-client-xyz",
		RedirectURL:      "https://app.joltrin.com/auth/callback",
		SigningKeySecret: signingKey,
		RoleMapping: map[string]string{
			"Directory.ReadWrite.All": "Admin",
		},
		DefaultRole: "User",
	}

	entra := NewEntraIDProvider(cfg)
	if entra.RequiredTier() != TierEnterprise {
		t.Errorf("Entra ID must require TierEnterprise, got %s", entra.RequiredTier())
	}

	authURL := entra.AuthorizationURL("entra-state", "entra-nonce")
	expectedHost := fmt.Sprintf("login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenantGUID)
	if !strings.Contains(authURL, expectedHost) {
		t.Errorf("malformed Entra ID auth URL: %s", authURL)
	}

	claimsMap := map[string]any{
		"sub":                "entra-user-guid",
		"iss":                fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantGUID),
		"aud":                "entra-client-xyz",
		"tid":                tenantGUID,
		"email":              "bob@tenant.onmicrosoft.com",
		"preferred_username": "bob@tenant.onmicrosoft.com",
		"roles":              []any{"Directory.ReadWrite.All"},
		"exp":                float64(time.Now().Add(time.Hour).Unix()),
	}
	rawToken := createTestJWT(claimsMap, signingKey)

	claims, err := entra.ValidateToken(ctx, rawToken)
	if err != nil {
		t.Fatalf("failed to validate Entra token: %v", err)
	}
	if claims.TenantID != tenantGUID {
		t.Errorf("expected tenant ID %s, got %s", tenantGUID, claims.TenantID)
	}

	user, err := entra.MapClaimsToUser(claims)
	if err != nil {
		t.Fatalf("failed to map Entra claims: %v", err)
	}
	if user.Role != "Admin" || user.TenantID != tenantGUID {
		t.Errorf("mismatched user: %+v", user)
	}
}

func TestIdentityProviderRegistry_TierEnforcement(t *testing.T) {
	reg := NewIdentityProviderRegistry()

	gh := NewGitHubOIDCProvider(OIDCConfig{ClientID: "gh"})
	okta := NewOktaProvider(OIDCConfig{ClientID: "okta"})
	entra := NewEntraIDProvider(OIDCConfig{ClientID: "entra"})

	_ = reg.Register(gh)
	_ = reg.Register(okta)
	_ = reg.Register(entra)

	coreGate := NewFeatureGate(TierCore)
	enterpriseGate := NewFeatureGate(TierEnterprise)

	// 1. GitHub is available in Core tier
	resolvedGH, err := reg.Get("github", coreGate)
	if err != nil {
		t.Fatalf("unexpected error getting GitHub provider on Core tier: %v", err)
	}
	if resolvedGH.DisplayName() != "GitHub" {
		t.Errorf("unexpected provider: %v", resolvedGH)
	}

	// 2. Okta requires Enterprise tier -> blocked on Core
	_, err = reg.Get("okta", coreGate)
	if err == nil {
		t.Fatalf("expected error getting Okta on Core gate, got nil")
	}
	if !errors.Is(err, ErrEnterpriseSSONotLicensed) {
		t.Errorf("expected ErrEnterpriseSSONotLicensed, got: %v", err)
	}

	// 3. Microsoft Entra ID requires Enterprise tier -> blocked on Core
	_, err = reg.Get("entraid", coreGate)
	if err == nil {
		t.Fatalf("expected error getting Entra ID on Core gate, got nil")
	}
	if !errors.Is(err, ErrEnterpriseSSONotLicensed) {
		t.Errorf("expected ErrEnterpriseSSONotLicensed, got: %v", err)
	}

	// 4. On Enterprise gate, Okta and Entra ID resolve successfully
	resolvedOkta, err := reg.Get("okta", enterpriseGate)
	if err != nil || resolvedOkta == nil {
		t.Fatalf("failed to resolve Okta on Enterprise gate: %v", err)
	}

	resolvedEntra, err := reg.Get("entraid", enterpriseGate)
	if err != nil || resolvedEntra == nil {
		t.Fatalf("failed to resolve Entra ID on Enterprise gate: %v", err)
	}

	// 5. List available summaries
	summaries := reg.ListAvailable(coreGate)
	if len(summaries) != 3 {
		t.Fatalf("expected 3 registered providers, got %d", len(summaries))
	}
	for _, s := range summaries {
		if s.Type == IdPTypeGitHub && !s.IsEntitled {
			t.Errorf("GitHub should be entitled in Core")
		}
		if (s.Type == IdPTypeOkta || s.Type == IdPTypeEntraID) && s.IsEntitled {
			t.Errorf("%s should NOT be entitled in Core", s.DisplayName)
		}
	}
}
