package governance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// IdentityProviderType enumerates supported identity and SSO protocols.
type IdentityProviderType string

const (
	// IdPTypeGitHub represents GitHub OIDC and OAuth authentication (available in Core/Free tier).
	IdPTypeGitHub IdentityProviderType = "github"

	// IdPTypeOkta represents Okta Enterprise OIDC / SAML SSO (Enterprise tier).
	IdPTypeOkta IdentityProviderType = "okta"

	// IdPTypeEntraID represents Microsoft Entra ID (formerly Azure Active Directory) SSO (Enterprise tier).
	IdPTypeEntraID IdentityProviderType = "entraid"

	// IdPTypeGenericOIDC represents standard RFC 7519 / OpenID Connect Core 1.0 providers.
	IdPTypeGenericOIDC IdentityProviderType = "oidc"
)

var (
	ErrInvalidToken             = errors.New("governance: invalid or expired identity token")
	ErrTokenExpired             = errors.New("governance: token has expired")
	ErrInvalidIssuer            = errors.New("governance: token issuer does not match configured issuer")
	ErrInvalidAudience          = errors.New("governance: token audience does not match configured client_id")
	ErrProviderNotRegistered    = errors.New("governance: identity provider not found")
	ErrEnterpriseSSONotLicensed = errors.New("governance: enterprise SSO requires an Enterprise tier license (CapEnterpriseSSO)")
)

// IdentityClaims represents normalized OIDC/OAuth standard identity claims.
type IdentityClaims struct {
	Subject           string         `json:"sub"`
	Email             string         `json:"email,omitempty"`
	PreferredUsername string         `json:"preferred_username,omitempty"`
	Name              string         `json:"name,omitempty"`
	Groups            []string       `json:"groups,omitempty"`
	Roles             []string       `json:"roles,omitempty"`
	Issuer            string         `json:"iss"`
	Audience          string         `json:"aud"`
	TenantID          string         `json:"tid,omitempty"`        // Microsoft Entra ID tenant GUID
	Repository        string         `json:"repository,omitempty"` // GitHub OIDC workflow repository
	RepositoryOwner   string         `json:"repository_owner,omitempty"`
	Actor             string         `json:"actor,omitempty"` // GitHub OIDC trigger actor
	ExpiresAt         int64          `json:"exp"`
	IssuedAt          int64          `json:"iat"`
	RawClaims         map[string]any `json:"raw_claims,omitempty"`
}

// AuthenticatedUser represents the mapped internal user record after claim translation.
type AuthenticatedUser struct {
	Username string   `json:"username"`
	Email    string   `json:"email,omitempty"`
	Role     string   `json:"role"`
	TenantID string   `json:"tenant_id,omitempty"`
	Provider string   `json:"provider"`
	Groups   []string `json:"groups,omitempty"`
}

// OIDCConfig defines configuration parameters for an identity provider instance.
type OIDCConfig struct {
	Type             IdentityProviderType `json:"type"`
	ID               string               `json:"id"`
	DisplayName      string               `json:"display_name"`
	IssuerURL        string               `json:"issuer_url"`
	ClientID         string               `json:"client_id"`
	ClientSecret     string               `json:"client_secret,omitempty"`
	RedirectURL      string               `json:"redirect_url"`
	Scopes           []string             `json:"scopes"`
	TenantID         string               `json:"tenant_id,omitempty"` // Microsoft Entra ID Tenant GUID
	Domain           string               `json:"domain,omitempty"`    // Okta Domain e.g. "company.okta.com"
	DefaultRole      string               `json:"default_role,omitempty"`
	RoleMapping      map[string]string    `json:"role_mapping,omitempty"` // IdP Group/Role -> Joltrin Role ("Admin", "User", "Guest")
	SkipIssuerCheck  bool                 `json:"skip_issuer_check,omitempty"`
	SigningKeySecret []byte               `json:"-"`
}

// IdentityProvider is the extensible interface for identity providers.
type IdentityProvider interface {
	Type() IdentityProviderType
	ID() string
	DisplayName() string
	RequiredTier() Tier
	AuthorizationURL(state, nonce string) string
	ValidateToken(ctx context.Context, rawToken string) (*IdentityClaims, error)
	MapClaimsToUser(claims *IdentityClaims) (*AuthenticatedUser, error)
}

// ProviderSummary is a public descriptor for display in login pages and client SDKs.
type ProviderSummary struct {
	ID           string               `json:"id"`
	Type         IdentityProviderType `json:"type"`
	DisplayName  string               `json:"display_name"`
	RequiredTier Tier                 `json:"required_tier"`
	IsEntitled   bool                 `json:"is_entitled"`
	AuthURL      string               `json:"auth_url,omitempty"`
}

// -----------------------------------------------------------------------------
// Base OIDC Implementation
// -----------------------------------------------------------------------------

type BaseOIDCProvider struct {
	cfg        OIDCConfig
	authURL    string
	tokenURL   string
	reqTier    Tier
	signingKey []byte
}

func (b *BaseOIDCProvider) Type() IdentityProviderType {
	return b.cfg.Type
}

func (b *BaseOIDCProvider) ID() string {
	if b.cfg.ID != "" {
		return b.cfg.ID
	}
	return string(b.cfg.Type)
}

func (b *BaseOIDCProvider) DisplayName() string {
	if b.cfg.DisplayName != "" {
		return b.cfg.DisplayName
	}
	return string(b.cfg.Type)
}

func (b *BaseOIDCProvider) RequiredTier() Tier {
	return b.reqTier
}

func (b *BaseOIDCProvider) AuthorizationURL(state, nonce string) string {
	baseURL := b.authURL
	if baseURL == "" {
		baseURL = strings.TrimSuffix(b.cfg.IssuerURL, "/") + "/v1/authorize"
	}

	scopes := b.cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}

	params := url.Values{}
	params.Set("client_id", b.cfg.ClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", b.cfg.RedirectURL)
	params.Set("scope", strings.Join(scopes, " "))
	params.Set("state", state)
	if nonce != "" {
		params.Set("nonce", nonce)
	}

	if strings.Contains(baseURL, "?") {
		return baseURL + "&" + params.Encode()
	}
	return baseURL + "?" + params.Encode()
}

func (b *BaseOIDCProvider) ValidateToken(ctx context.Context, rawToken string) (*IdentityClaims, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, ErrInvalidToken
	}

	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: JWT must have 3 segments", ErrInvalidToken)
	}

	// Signature verification if signing key secret is configured
	if len(b.signingKey) > 0 {
		mac := hmac.New(sha256.New, b.signingKey)
		mac.Write([]byte(parts[0] + "." + parts[1]))
		expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
			return nil, fmt.Errorf("%w: signature mismatch", ErrInvalidToken)
		}
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard base64 fallback
		if missing := len(parts[1]) % 4; missing != 0 {
			parts[1] += strings.Repeat("=", 4-missing)
		}
		payloadBytes, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("%w: failed to decode JWT payload: %v", ErrInvalidToken, err)
		}
	}

	var raw map[string]any
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal claims JSON: %v", ErrInvalidToken, err)
	}

	claims := &IdentityClaims{
		RawClaims: raw,
	}

	if sub, ok := raw["sub"].(string); ok {
		claims.Subject = sub
	}
	if email, ok := raw["email"].(string); ok {
		claims.Email = email
	}
	if uname, ok := raw["preferred_username"].(string); ok {
		claims.PreferredUsername = uname
	}
	if name, ok := raw["name"].(string); ok {
		claims.Name = name
	}
	if iss, ok := raw["iss"].(string); ok {
		claims.Issuer = iss
	}
	if tid, ok := raw["tid"].(string); ok {
		claims.TenantID = tid
	}
	if repo, ok := raw["repository"].(string); ok {
		claims.Repository = repo
	}
	if owner, ok := raw["repository_owner"].(string); ok {
		claims.RepositoryOwner = owner
	}
	if actor, ok := raw["actor"].(string); ok {
		claims.Actor = actor
	}

	// Audience can be string or []string
	switch aud := raw["aud"].(type) {
	case string:
		claims.Audience = aud
	case []any:
		if len(aud) > 0 {
			claims.Audience = fmt.Sprint(aud[0])
		}
	}

	// Groups / Roles
	extractStringSlice := func(key string) []string {
		if val, ok := raw[key]; ok {
			if slice, ok := val.([]any); ok {
				out := make([]string, 0, len(slice))
				for _, item := range slice {
					if str, ok := item.(string); ok {
						out = append(out, str)
					}
				}
				return out
			}
		}
		return nil
	}

	claims.Groups = extractStringSlice("groups")
	claims.Roles = extractStringSlice("roles")

	// Timestamps
	if exp, ok := raw["exp"].(float64); ok {
		claims.ExpiresAt = int64(exp)
	}
	if iat, ok := raw["iat"].(float64); ok {
		claims.IssuedAt = int64(iat)
	}

	// Validation
	now := time.Now().Unix()
	if claims.ExpiresAt > 0 && claims.ExpiresAt < (now-60) { // 60-second clock skew allowance
		return nil, ErrTokenExpired
	}

	if !b.cfg.SkipIssuerCheck && b.cfg.IssuerURL != "" {
		expectedIss := strings.TrimSuffix(b.cfg.IssuerURL, "/")
		actualIss := strings.TrimSuffix(claims.Issuer, "/")
		if actualIss != expectedIss {
			return nil, fmt.Errorf("%w: expected %q got %q", ErrInvalidIssuer, expectedIss, actualIss)
		}
	}

	if b.cfg.ClientID != "" && claims.Audience != "" && claims.Audience != b.cfg.ClientID {
		return nil, fmt.Errorf("%w: expected %q got %q", ErrInvalidAudience, b.cfg.ClientID, claims.Audience)
	}

	return claims, nil
}

func (b *BaseOIDCProvider) MapClaimsToUser(claims *IdentityClaims) (*AuthenticatedUser, error) {
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = claims.Subject
	}
	if username == "" {
		username = "user"
	}

	role := b.cfg.DefaultRole
	if role == "" {
		role = "User"
	}

	// Evaluate role mappings against groups and roles
	if b.cfg.RoleMapping != nil {
		allRoles := append([]string(nil), claims.Groups...)
		allRoles = append(allRoles, claims.Roles...)
		for _, r := range allRoles {
			if targetRole, ok := b.cfg.RoleMapping[r]; ok {
				role = targetRole
				break
			}
		}
	}

	return &AuthenticatedUser{
		Username: username,
		Email:    claims.Email,
		Role:     role,
		TenantID: claims.TenantID,
		Provider: string(b.cfg.Type),
		Groups:   claims.Groups,
	}, nil
}

// -----------------------------------------------------------------------------
// Provider: GitHub OIDC / OAuth (Core / Free Tier)
// -----------------------------------------------------------------------------

type GitHubOIDCProvider struct {
	BaseOIDCProvider
}

// NewGitHubOIDCProvider constructs an identity provider configured for GitHub OIDC / OAuth.
func NewGitHubOIDCProvider(cfg OIDCConfig) *GitHubOIDCProvider {
	if cfg.IssuerURL == "" {
		cfg.IssuerURL = "https://token.actions.githubusercontent.com"
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "GitHub"
	}
	cfg.Type = IdPTypeGitHub

	p := &GitHubOIDCProvider{
		BaseOIDCProvider: BaseOIDCProvider{
			cfg:        cfg,
			authURL:    "https://github.com/login/oauth/authorize",
			tokenURL:   "https://github.com/login/oauth/access_token",
			reqTier:    TierCore,
			signingKey: cfg.SigningKeySecret,
		},
	}
	return p
}

func (g *GitHubOIDCProvider) MapClaimsToUser(claims *IdentityClaims) (*AuthenticatedUser, error) {
	user, err := g.BaseOIDCProvider.MapClaimsToUser(claims)
	if err != nil {
		return nil, err
	}
	// Prefer GitHub actor if present (Actions workflow runner)
	if claims.Actor != "" {
		user.Username = claims.Actor
	}
	return user, nil
}

// -----------------------------------------------------------------------------
// Provider: Okta OIDC SSO (Enterprise Tier)
// -----------------------------------------------------------------------------

type OktaProvider struct {
	BaseOIDCProvider
}

// NewOktaProvider constructs an identity provider configured for Okta SSO.
func NewOktaProvider(cfg OIDCConfig) *OktaProvider {
	domain := strings.TrimPrefix(strings.TrimPrefix(cfg.Domain, "https://"), "http://")
	if domain == "" && cfg.IssuerURL != "" {
		if u, err := url.Parse(cfg.IssuerURL); err == nil {
			domain = u.Host
		}
	}
	if domain == "" {
		domain = "dev-company.okta.com"
	}

	if cfg.IssuerURL == "" {
		cfg.IssuerURL = fmt.Sprintf("https://%s/oauth2/default", domain)
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Okta"
	}
	cfg.Type = IdPTypeOkta

	authURL := fmt.Sprintf("https://%s/oauth2/v1/authorize", domain)

	p := &OktaProvider{
		BaseOIDCProvider: BaseOIDCProvider{
			cfg:        cfg,
			authURL:    authURL,
			tokenURL:   fmt.Sprintf("https://%s/oauth2/v1/token", domain),
			reqTier:    TierEnterprise,
			signingKey: cfg.SigningKeySecret,
		},
	}
	return p
}

// -----------------------------------------------------------------------------
// Provider: Microsoft Entra ID SSO (Enterprise Tier)
// -----------------------------------------------------------------------------

type EntraIDProvider struct {
	BaseOIDCProvider
}

// NewEntraIDProvider constructs an identity provider configured for Microsoft Entra ID (Azure AD).
func NewEntraIDProvider(cfg OIDCConfig) *EntraIDProvider {
	tenantID := cfg.TenantID
	if tenantID == "" {
		tenantID = "common"
	}

	if cfg.IssuerURL == "" {
		cfg.IssuerURL = fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Microsoft Entra ID"
	}
	cfg.Type = IdPTypeEntraID

	authURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", tenantID)
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID)

	p := &EntraIDProvider{
		BaseOIDCProvider: BaseOIDCProvider{
			cfg:        cfg,
			authURL:    authURL,
			tokenURL:   tokenURL,
			reqTier:    TierEnterprise,
			signingKey: cfg.SigningKeySecret,
		},
	}
	return p
}

// -----------------------------------------------------------------------------
// Identity Provider Registry
// -----------------------------------------------------------------------------

// IdentityProviderRegistry stores and resolves active identity providers.
type IdentityProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]IdentityProvider
}

// NewIdentityProviderRegistry initializes a new provider registry.
func NewIdentityProviderRegistry() *IdentityProviderRegistry {
	return &IdentityProviderRegistry{
		providers: make(map[string]IdentityProvider),
	}
}

// Register registers an identity provider instance.
func (r *IdentityProviderRegistry) Register(p IdentityProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := strings.ToLower(strings.TrimSpace(p.ID()))
	if id == "" {
		return errors.New("governance: identity provider ID cannot be empty")
	}
	r.providers[id] = p
	return nil
}

// Get resolves an identity provider by ID or Type and enforces license entitlement via FeatureGate.
func (r *IdentityProviderRegistry) Get(id string, gate *FeatureGate) (IdentityProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotRegistered, id)
	}

	// Enforce Enterprise tier entitlement for enterprise providers
	if p.RequiredTier() == TierEnterprise {
		if gate == nil || !gate.Allows(CapEnterpriseSSO) {
			return nil, fmt.Errorf("%w: provider %q requires TierEnterprise (current: %s)",
				ErrEnterpriseSSONotLicensed, p.DisplayName(), gate.Tier())
		}
	}

	return p, nil
}

// ListAvailable returns summaries of registered providers along with tier entitlement indicators.
func (r *IdentityProviderRegistry) ListAvailable(gate *FeatureGate) []ProviderSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ProviderSummary, 0, len(r.providers))
	for _, p := range r.providers {
		entitled := true
		if p.RequiredTier() == TierEnterprise {
			entitled = gate != nil && gate.Allows(CapEnterpriseSSO)
		}
		out = append(out, ProviderSummary{
			ID:           p.ID(),
			Type:         p.Type(),
			DisplayName:  p.DisplayName(),
			RequiredTier: p.RequiredTier(),
			IsEntitled:   entitled,
			AuthURL:      p.AuthorizationURL("state-req", "nonce-req"),
		})
	}
	return out
}
