package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/governance"
)

var (
	oidcRegistry      *governance.IdentityProviderRegistry
	serverFeatureGate *governance.FeatureGate
	oidcOnce          sync.Once
	oidcStates        sync.Map // state -> providerID
)

func getOIDCRegistry() (*governance.IdentityProviderRegistry, *governance.FeatureGate) {
	oidcOnce.Do(func() {
		tier := config.Tier
		if tier == "" {
			tier = governance.TierCore
		}
		serverFeatureGate = governance.NewFeatureGate(tier)
		oidcRegistry = governance.NewIdentityProviderRegistry()

		// 1. Register from config.OIDCProviders
		for _, pCfg := range config.OIDCProviders {
			switch pCfg.Type {
			case governance.IdPTypeGitHub:
				_ = oidcRegistry.Register(governance.NewGitHubOIDCProvider(pCfg))
			case governance.IdPTypeOkta:
				_ = oidcRegistry.Register(governance.NewOktaProvider(pCfg))
			case governance.IdPTypeEntraID:
				_ = oidcRegistry.Register(governance.NewEntraIDProvider(pCfg))
			}
		}

		// 2. Register from environment variables if not already registered
		ghClientID := firstNonEmpty(os.Getenv("JOLTRIN_OIDC_GITHUB_CLIENT_ID"), os.Getenv("SOP_OIDC_GITHUB_CLIENT_ID"))
		if ghClientID != "" {
			_ = oidcRegistry.Register(governance.NewGitHubOIDCProvider(governance.OIDCConfig{
				ClientID:    ghClientID,
				RedirectURL: firstNonEmpty(os.Getenv("JOLTRIN_OIDC_GITHUB_REDIRECT_URL"), "/api/auth/oidc/callback"),
				DefaultRole: "User",
			}))
		}

		oktaDomain := firstNonEmpty(os.Getenv("JOLTRIN_OIDC_OKTA_DOMAIN"), os.Getenv("SOP_OIDC_OKTA_DOMAIN"))
		if oktaDomain != "" {
			_ = oidcRegistry.Register(governance.NewOktaProvider(governance.OIDCConfig{
				Domain:      oktaDomain,
				ClientID:    firstNonEmpty(os.Getenv("JOLTRIN_OIDC_OKTA_CLIENT_ID"), os.Getenv("SOP_OIDC_OKTA_CLIENT_ID")),
				RedirectURL: firstNonEmpty(os.Getenv("JOLTRIN_OIDC_OKTA_REDIRECT_URL"), "/api/auth/oidc/callback"),
				DefaultRole: "User",
			}))
		}

		entraTenant := firstNonEmpty(os.Getenv("JOLTRIN_OIDC_ENTRA_TENANT_ID"), os.Getenv("SOP_OIDC_ENTRA_TENANT_ID"))
		if entraTenant != "" {
			_ = oidcRegistry.Register(governance.NewEntraIDProvider(governance.OIDCConfig{
				TenantID:    entraTenant,
				ClientID:    firstNonEmpty(os.Getenv("JOLTRIN_OIDC_ENTRA_CLIENT_ID"), os.Getenv("SOP_OIDC_ENTRA_CLIENT_ID")),
				RedirectURL: firstNonEmpty(os.Getenv("JOLTRIN_OIDC_ENTRA_REDIRECT_URL"), "/api/auth/oidc/callback"),
				DefaultRole: "User",
			}))
		}
	})
	return oidcRegistry, serverFeatureGate
}

func getServerFeatureGate() *governance.FeatureGate {
	_, gate := getOIDCRegistry()
	return gate
}

type ProviderResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	DisplayName  string          `json:"display_name"`
	RequiredTier governance.Tier `json:"required_tier"`
	IsEntitled   bool            `json:"is_entitled"`
	AuthorizeURL string          `json:"authorize_url,omitempty"`
}

func handleListAuthProviders(w http.ResponseWriter, r *http.Request) {
	reg, gate := getOIDCRegistry()

	providers := []ProviderResponse{
		{
			ID:           "local",
			Type:         "local",
			DisplayName:  "Local Credentials",
			RequiredTier: governance.TierCore,
			IsEntitled:   true,
		},
	}

	for _, p := range reg.ListAvailable(gate) {
		providers = append(providers, ProviderResponse{
			ID:           p.ID,
			Type:         string(p.Type),
			DisplayName:  p.DisplayName,
			RequiredTier: p.RequiredTier,
			IsEntitled:   p.IsEntitled,
			AuthorizeURL: fmt.Sprintf("/api/auth/oidc/authorize?provider=%s", p.ID),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"providers":   providers,
		"server_tier": gate.Tier(),
	})
}

func handleOIDCAuthorize(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider"))
	if providerID == "" {
		providerID = "github"
	}

	reg, gate := getOIDCRegistry()
	provider, err := reg.Get(providerID, gate)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}

	state := uuid.NewString()
	nonce := uuid.NewString()
	oidcStates.Store(state, providerID)

	targetURL := provider.AuthorizationURL(state, nonce)
	http.Redirect(w, r, targetURL, http.StatusFound)
}

func handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	token := r.URL.Query().Get("token")
	providerID := r.URL.Query().Get("provider")

	if providerID == "" && state != "" {
		if val, ok := oidcStates.LoadAndDelete(state); ok {
			providerID = val.(string)
		}
	}
	if providerID == "" {
		providerID = "github"
	}

	reg, gate := getOIDCRegistry()
	provider, err := reg.Get(providerID, gate)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}

	// In direct token hand-off or simulated code exchanges, token can be passed directly or as code
	rawToken := token
	if rawToken == "" {
		rawToken = code
	}
	if rawToken == "" {
		writeJSONError(w, http.StatusBadRequest, "missing authorization code or token")
		return
	}

	claims, err := provider.ValidateToken(ctx, rawToken)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, fmt.Sprintf("invalid OIDC token: %v", err))
		return
	}

	user, err := provider.MapClaimsToUser(claims)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to map user claims: %v", err))
		return
	}

	accessToken, refreshToken, err := currentTokenFacade().CreateSession(ctx, user.Username, user.Role)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create user session")
		return
	}

	secure := r.TLS != nil
	setSessionCookies(w, accessToken, refreshToken, secure)

	// If browser HTML navigation, redirect to /app
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/app", http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"user": map[string]any{
			"username": user.Username,
			"role":     user.Role,
			"provider": user.Provider,
		},
	})
}

func handleOIDCToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Provider string `json:"provider"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Token == "" {
		writeJSONError(w, http.StatusBadRequest, "token is required")
		return
	}
	if req.Provider == "" {
		req.Provider = "github"
	}

	reg, gate := getOIDCRegistry()
	provider, err := reg.Get(req.Provider, gate)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}

	claims, err := provider.ValidateToken(r.Context(), req.Token)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, fmt.Sprintf("token validation failed: %v", err))
		return
	}

	user, err := provider.MapClaimsToUser(claims)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("claim mapping failed: %v", err))
		return
	}

	accessToken, refreshToken, err := currentTokenFacade().CreateSession(r.Context(), user.Username, user.Role)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	secure := r.TLS != nil
	setSessionCookies(w, accessToken, refreshToken, secure)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"user": map[string]any{
			"username": user.Username,
			"role":     user.Role,
			"provider": user.Provider,
		},
	})
}
