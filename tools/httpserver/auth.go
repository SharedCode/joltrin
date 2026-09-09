package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	aidb "github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/database"
	"golang.org/x/crypto/bcrypt"
)

func getSystemDB(ctx context.Context) (*aidb.Database, error) {
	dbOpts, err := getSystemDBOptions(ctx)
	if err != nil {
		return nil, err
	}
	return aidb.NewDatabase(dbOpts), nil
}

// UserRecord stores minimal credential and role data for the built-in auth model.
type UserRecord struct {
	Username     string `json:"username"`
	Role         string `json:"role"`
	PasswordHash string `json:"password_hash,omitempty"`
}

// SessionRecord holds a bearer token for the short-lived V1 login facade.
// A B-tree-backed implementation can replace this later for cross-machine enforcement.
type SessionRecord struct {
	Token            string
	RefreshToken     string
	Username         string
	Role             string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
}

type TokenFacade interface {
	CreateToken(ctx context.Context, username, role string) (string, error)
	CreateSession(ctx context.Context, username, role string) (string, string, error)
	Refresh(ctx context.Context, refreshToken string) (string, string, error)
	ValidateToken(ctx context.Context, token string) (*UserRecord, error)
	RevokeToken(ctx context.Context, token string)
}

const (
	defaultSessionTTL = 30 * time.Minute
	defaultRefreshTTL = 7 * 24 * time.Hour
	sessionStoreName  = "sessions"
)

type SessionStore struct {
	mu         sync.RWMutex
	ttl        time.Duration
	refreshTTL time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &SessionStore{ttl: ttl, refreshTTL: defaultRefreshTTL}
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type signedAccessClaims struct {
	Subject   string `json:"sub"`
	Role      string `json:"role"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	TokenID   string `json:"jti"`
}

func tokenSigningSecret() []byte {
	if secret := strings.TrimSpace(os.Getenv("SOP_SESSION_SECRET")); secret != "" {
		return []byte(secret)
	}
	if secret := strings.TrimSpace(config.SessionSecret); secret != "" {
		return []byte(secret)
	}
	return []byte("sop-session-default-secret")
}

func base64urlEncode(data []byte) string {
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(data), "=")
}

func base64urlDecode(data string) ([]byte, error) {
	if missing := len(data) % 4; missing != 0 {
		data += strings.Repeat("=", 4-missing)
	}
	return base64.URLEncoding.DecodeString(data)
}

func signAccessToken(username, role string, issuedAt, expiresAt time.Time, tokenID string) (string, error) {
	header := base64urlEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := signedAccessClaims{
		Subject:   username,
		Role:      role,
		IssuedAt:  issuedAt.Unix(),
		ExpiresAt: expiresAt.Unix(),
		TokenID:   tokenID,
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64urlEncode(claimsJSON)
	input := header + "." + payload
	mac := hmac.New(sha256.New, tokenSigningSecret())
	_, _ = mac.Write([]byte(input))
	signature := base64urlEncode(mac.Sum(nil))
	return input + "." + signature, nil
}

func parseAndVerifySignedAccessToken(token string) (*signedAccessClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid signed token")
	}
	header, payload, signature := parts[0], parts[1], parts[2]
	mac := hmac.New(sha256.New, tokenSigningSecret())
	_, _ = mac.Write([]byte(header + "." + payload))
	expected := base64urlEncode(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return nil, fmt.Errorf("invalid signature")
	}

	claimsJSON, err := base64urlDecode(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid payload")
	}
	var claims signedAccessClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid claims")
	}
	if claims.Subject == "" || claims.Role == "" || claims.TokenID == "" {
		return nil, fmt.Errorf("invalid claims")
	}
	if time.Now().UTC().Unix() >= claims.ExpiresAt {
		return nil, fmt.Errorf("expired session")
	}
	return &claims, nil
}

func (s *SessionStore) CreateToken(ctx context.Context, username, role string) (string, error) {
	store, tx, err := s.getStore(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	opaqueToken, err := newToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	accessToken, err := signAccessToken(username, role, now, now.Add(s.ttl), opaqueToken)
	if err != nil {
		return "", err
	}
	record := SessionRecord{Token: accessToken, Username: username, Role: role, IssuedAt: now, ExpiresAt: now.Add(s.ttl)}

	if ok, err := store.Add(ctx, accessToken, record); !ok || err != nil {
		if err != nil {
			return "", fmt.Errorf("failed to create token: %v", err)
		}
		return "", fmt.Errorf("failed to create token, collision")
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit token: %v", err)
	}
	return accessToken, nil
}

func (s *SessionStore) CreateSession(ctx context.Context, username, role string) (string, string, error) {
	store, tx, err := s.getStore(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	opaqueAccessToken, err := newToken()
	if err != nil {
		return "", "", err
	}
	accessToken, err := signAccessToken(username, role, now, now.Add(s.ttl), opaqueAccessToken)
	if err != nil {
		return "", "", err
	}
	refreshToken, err := newToken()
	if err != nil {
		return "", "", err
	}
	record := SessionRecord{
		Token:            accessToken,
		RefreshToken:     refreshToken,
		Username:         username,
		Role:             role,
		IssuedAt:         now,
		ExpiresAt:        now.Add(s.ttl),
		RefreshExpiresAt: now.Add(s.refreshTTL),
	}

	if ok, err := store.Add(ctx, accessToken, record); !ok || err != nil {
		if err != nil {
			return "", "", fmt.Errorf("failed to create session: %v", err)
		}
		return "", "", fmt.Errorf("failed to create session, token may already exist")
	}
	if ok, err := store.Add(ctx, refreshToken, record); !ok || err != nil {
		// Attempt to roll back the access token if the refresh token fails
		store.Remove(ctx, accessToken)
		if err != nil {
			return "", "", fmt.Errorf("failed to create refresh session: %v", err)
		}
		return "", "", fmt.Errorf("failed to create refresh session, token may already exist")
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("failed to commit session: %v", err)
	}
	return accessToken, refreshToken, nil
}

func (s *SessionStore) Refresh(ctx context.Context, refreshToken string) (string, string, error) {
	store, tx, err := s.getStore(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)

	found, err := store.Find(ctx, refreshToken, false)
	if err != nil {
		return "", "", fmt.Errorf("failed to get refresh token details: %v", err)
	}
	if !found {
		return "", "", fmt.Errorf("invalid refresh token")
	}
	r, err := store.GetCurrentValue(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get refresh token value: %v", err)
	}

	if time.Now().UTC().After(r.RefreshExpiresAt) {
		if err := removeSessionRecord(ctx, store, r); err != nil {
			log.Warn("Refresh: failed to remove expired refresh session", "error", err)
		}
		if err := tx.Commit(ctx); err != nil {
			log.Warn("Refresh: failed to commit expired refresh token cleanup", "error", err)
		}
		return "", "", fmt.Errorf("expired refresh token")
	}

	now := time.Now().UTC()
	opaqueAccessToken, err := newToken()
	if err != nil {
		return "", "", err
	}
	accessToken, err := signAccessToken(r.Username, r.Role, now, r.ExpiresAt, opaqueAccessToken)
	if err != nil {
		return "", "", err
	}
	newRefreshToken, err := newToken()
	if err != nil {
		return "", "", err
	}

	newRecord := SessionRecord{
		Token:            accessToken,
		RefreshToken:     newRefreshToken,
		Username:         r.Username,
		Role:             r.Role,
		IssuedAt:         now,
		ExpiresAt:        r.ExpiresAt,
		RefreshExpiresAt: r.RefreshExpiresAt,
	}

	// Add new tokens
	if ok, err := store.Add(ctx, accessToken, newRecord); !ok || err != nil {
		if err != nil {
			return "", "", fmt.Errorf("failed to create new access token: %v", err)
		}
		return "", "", fmt.Errorf("failed to create new access token, collision")
	}
	if ok, err := store.Add(ctx, newRefreshToken, newRecord); !ok || err != nil {
		store.Remove(ctx, accessToken) // Rollback
		if err != nil {
			return "", "", fmt.Errorf("failed to create new refresh token: %v", err)
		}
		return "", "", fmt.Errorf("failed to create new refresh token, collision")
	}

	// Remove old tokens
	store.Remove(ctx, r.Token)
	store.Remove(ctx, r.RefreshToken)

	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("failed to commit refresh: %v", err)
	}
	return accessToken, newRefreshToken, nil
}

func (s *SessionStore) ValidateToken(ctx context.Context, token string) (*UserRecord, error) {
	if claims, err := parseAndVerifySignedAccessToken(token); err == nil {
		return &UserRecord{Username: claims.Subject, Role: claims.Role}, nil
	}

	store, tx, err := s.getStore(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	found, err := store.Find(ctx, token, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get token details: %v", err)
	}
	if !found {
		return nil, fmt.Errorf("invalid session")
	}
	r, err := store.GetCurrentValue(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get token value: %v", err)
	}

	now := time.Now().UTC()
	if now.After(r.ExpiresAt) {
		log.Debug("ValidateToken: expired session",
			"token_prefix", tokenPrefix(token),
			"expires_at", r.ExpiresAt)
		if err := removeSessionRecord(ctx, store, r); err != nil {
			log.Warn("ValidateToken: failed to remove expired session", "error", err)
		}
		if err := tx.Commit(ctx); err != nil {
			log.Warn("ValidateToken: failed to commit expired session cleanup", "error", err)
		}
		return nil, fmt.Errorf("expired session")
	}

	if err := tx.Commit(ctx); err != nil {
		log.Warn("failed to commit token validation", "error", err)
	}

	log.Debug("ValidateToken: successful validation",
		"token_prefix", tokenPrefix(token),
		"expires_at", r.ExpiresAt)

	return &UserRecord{Username: r.Username, Role: r.Role}, nil
}

func (s *SessionStore) RevokeToken(ctx context.Context, token string) {
	store, tx, err := s.getStore(ctx)
	if err != nil {
		log.Warn("failed to get store for token revocation", "error", err)
		return
	}
	defer tx.Rollback(ctx)

	found, err := store.Find(ctx, token, false)
	if err != nil {
		log.Warn("failed to get token details for revocation", "error", err)
		return
	}
	if !found {
		// If the token itself is not found, it might have been a refresh token.
		// We don't have an index for that, so we can't easily revoke.
		// This is a limitation of the current design.
		// A full implementation might require iterating or a secondary index.
		return
	}
	r, err := store.GetCurrentValue(ctx)
	if err != nil {
		log.Warn("failed to get token value for revocation", "error", err)
		return
	}

	// Remove both the access and refresh tokens to fully invalidate the session.
	store.Remove(ctx, r.Token)
	store.Remove(ctx, r.RefreshToken)
	if err := tx.Commit(ctx); err != nil {
		log.Warn("failed to commit token revocation", "error", err)
	}
}

var (
	tokenFacade     TokenFacade
	tokenFacadeOnce sync.Once
)

func currentTokenFacade() TokenFacade {
	tokenFacadeOnce.Do(func() {
		tokenFacade = NewSessionStore(sessionTTL())
	})
	if tokenFacade == nil {
		tokenFacade = NewSessionStore(sessionTTL())
	}
	// Allow TTL to be updated if config changes, without resetting the whole store.
	if s, ok := tokenFacade.(*SessionStore); ok && s.ttl != sessionTTL() {
		func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.ttl = sessionTTL()
		}()
	}
	return tokenFacade
}

func sessionTTL() time.Duration {
	if config.SessionTokenTTLMinutes > 0 {
		return time.Duration(config.SessionTokenTTLMinutes) * time.Minute
	}
	return defaultSessionTTL
}

func tokenPrefix(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func normalizeRole(role string) string {
	role = strings.TrimSpace(role)
	switch role {
	case sop.RoleAdmin, sop.RoleUser, sop.RoleGuest:
		return role
	default:
		return sop.RoleUser
	}
}

func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(strings.TrimSpace(password)), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func extractBearerToken(authHeader string) string {
	parts := strings.SplitN(strings.TrimSpace(authHeader), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func (c *Config) authFromAuthorization(authHeader string) (string, string) {
	// Protected endpoints should use real session-token validation only.
	// Do not accept legacy username:password bearer tokens here.
	return "", ""
}

func (c *Config) SetUser(username, password, role string) error {
	username = normalizeUsername(username)
	if username == "" || strings.TrimSpace(password) == "" {
		return fmt.Errorf("username and password are required")
	}

	for _, existing := range c.Users {
		if normalizeUsername(existing.Username) == username {
			return fmt.Errorf("username already exists")
		}
	}

	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	c.Users = append(c.Users, UserRecord{
		Username:     username,
		Role:         normalizeRole(role),
		PasswordHash: hash,
	})
	return nil
}

func isAdminRoleInContext(ctx context.Context) bool {
	auth := sop.GetAuthFromContext(ctx)
	for _, role := range auth.Roles {
		if strings.EqualFold(role, sop.RoleAdmin) {
			return true
		}
	}
	return auth.IsSystem
}

func (c *Config) IsAdminOverride(ctx context.Context, username, password, legacyToken string) bool {
	if ok, user, err := c.Authenticate(username, password); err == nil && ok && user != nil && (user.Role == sop.RoleAdmin || strings.EqualFold(user.Username, "root")) {
		return true
	}

	if legacyToken != "" {
		if ok, user, err := c.AuthenticateBearerToken(ctx, legacyToken); err == nil && ok && user != nil && (user.Role == sop.RoleAdmin || strings.EqualFold(user.Username, "root")) {
			return true
		}
	}

	return false
}

func (c *Config) AuthenticateBearerToken(ctx context.Context, token string) (bool, *UserRecord, error) {
	if token == "" {
		log.Warn("AuthenticateBearerToken: empty token")
		return false, nil, nil
	}

	log.Debug("AuthenticateBearerToken: validating token", "token_prefix", tokenPrefix(token))
	user, err := currentTokenFacade().ValidateToken(ctx, token)
	if err != nil {
		log.Warn("AuthenticateBearerToken: token rejected", "error", err)
		return false, nil, err
	}
	log.Debug("AuthenticateBearerToken: token accepted", "user", user.Username, "role", user.Role, "token_prefix", tokenPrefix(token))
	return true, user, nil
}

func (c *Config) Authenticate(username, password string) (bool, *UserRecord, error) {
	provider, err := c.resolveAuthProvider()
	if err != nil {
		return false, nil, err
	}
	return provider.Authenticate(context.Background(), username, password)
}

func authTokenFromRequest(r *http.Request) string {
	if token := extractBearerToken(r.Header.Get("Authorization")); token != "" {
		return token
	}
	if cookie, err := r.Cookie("sop_access_token"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Debug("requireAuth: handler invocation", "handler", handlerName(next), "method", r.Method, "path", r.URL.Path)
		if !hasConfigFileOnDisk() {
			log.Debug("requireAuth: skipping session validation", "path", r.URL.Path, "method", r.Method, "reason", "config file not present on disk")
			next(w, r)
			return
		}

		token := authTokenFromRequest(r)
		log.Debug("requireAuth: checking auth token", "path", r.URL.Path, "has_token", token != "")
		if token == "" {
			log.Warn("requireAuth: unauthenticated redirect", "path", r.URL.Path, "reason", "missing session token", "method", r.Method)
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}

		log.Debug("requireAuth: executing token validation", "path", r.URL.Path, "token_prefix", tokenPrefix(token), "cookie_present", r.Header.Get("Cookie") != "")
		ok, user, err := config.AuthenticateBearerToken(r.Context(), token)
		if err != nil || !ok {
			reason := "invalid or expired session token"
			if err != nil {
				reason = strings.ToLower(err.Error())
			}
			log.Warn("requireAuth: unauthenticated redirect", "path", r.URL.Path, "reason", reason, "error", err, "ok", ok, "method", r.Method, "token_prefix", tokenPrefix(token))
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		log.Debug("requireAuth: token accepted", "path", r.URL.Path, "user", user.Username, "role", user.Role)

		// Add user info to context for downstream handlers
		authCtx := sop.AuthContext{
			UserID: user.Username,
			Roles:  []string{user.Role},
		}
		ctx := sop.ContextWithAuth(r.Context(), authCtx)
		next(w, r.WithContext(ctx))
	}
}

func parseRefreshRequest(r *http.Request) (string, error) {
	if cookie, err := r.Cookie("sop_refresh_token"); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return strings.TrimSpace(cookie.Value), nil
	}

	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", err
	}
	return strings.TrimSpace(req.RefreshToken), nil
}

func setSessionCookies(w http.ResponseWriter, accessToken, refreshToken string, secure bool) {
	accessTTL := sessionTTL()
	if accessTTL <= 0 {
		accessTTL = defaultSessionTTL
	}
	refreshTTL := defaultRefreshTTL

	http.SetCookie(w, &http.Cookie{
		Name:     "sop_access_token",
		Value:    accessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(accessTTL.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "sop_refresh_token",
		Value:    refreshToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(refreshTTL.Seconds()),
	})
}

func handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	refreshToken, err := parseRefreshRequest(r)
	if err != nil || refreshToken == "" {
		http.Error(w, "invalid refresh token", http.StatusBadRequest)
		return
	}

	accessToken, newRefreshToken, err := currentTokenFacade().Refresh(r.Context(), refreshToken)
	if err != nil {
		http.Error(w, "invalid or expired refresh token", http.StatusUnauthorized)
		return
	}

	secure := r.TLS != nil
	setSessionCookies(w, accessToken, newRefreshToken, secure)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"access_token":  accessToken,
		"refresh_token": newRefreshToken,
		"token_type":    "Bearer",
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message, "message": message})
}

func parseLoginRequest(r *http.Request) (string, string, error) {
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return "", "", err
		}
		return r.FormValue("username"), r.FormValue("password"), nil
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", "", err
	}
	return req.Username, req.Password, nil
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username, password, err := parseLoginRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ok, user, err := config.Authenticate(username, password)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "authentication failed")
		return
	}
	if !ok || user == nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if existingToken := authTokenFromRequest(r); existingToken != "" {
		validatedUser, validateErr := currentTokenFacade().ValidateToken(r.Context(), existingToken)
		if validateErr == nil && validatedUser != nil && strings.EqualFold(validatedUser.Username, user.Username) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        "ok",
				"access_token":  existingToken,
				"refresh_token": "",
				"token_type":    "Bearer",
				"user": map[string]any{
					"username": validatedUser.Username,
					"role":     validatedUser.Role,
				},
			})
			return
		}
	}

	accessToken, refreshToken, err := currentTokenFacade().CreateSession(r.Context(), user.Username, user.Role)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	secure := r.TLS != nil
	setSessionCookies(w, accessToken, refreshToken, secure)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "Bearer",
		"user": map[string]any{
			"username": user.Username,
			"role":     user.Role,
		},
	})
}

func removeSessionRecord(ctx context.Context, store btree.BtreeInterface[string, SessionRecord], record SessionRecord) error {
	if removed, err := store.Remove(ctx, record.Token); err != nil {
		return fmt.Errorf("failed to remove access token: %w", err)
	} else if !removed {
		log.Debug("removeSessionRecord: access token already absent", "token_prefix", tokenPrefix(record.Token))
	}

	if record.RefreshToken != "" {
		if removed, err := store.Remove(ctx, record.RefreshToken); err != nil {
			return fmt.Errorf("failed to remove refresh token: %w", err)
		} else if !removed {
			log.Debug("removeSessionRecord: refresh token already absent", "token_prefix", tokenPrefix(record.RefreshToken))
		}
	}

	return nil
}

func (s *SessionStore) getStore(ctx context.Context) (btree.BtreeInterface[string, SessionRecord], sop.Transaction, error) {
	db, err := getSystemDB(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("session store failed to get system db: %v", err)
	}
	tx, err := db.BeginTransaction(ctx, sop.ForWriting, -1)
	if err != nil {
		return nil, nil, fmt.Errorf("session store failed to begin transaction: %v", err)
	}

	options := sop.ConfigureStore(sessionStoreName, true, 1000, "System sessions", sop.SmallData, "")
	options.IsPrimitiveKey = true
	store, err := database.NewBtree[string, SessionRecord](ctx, db.Options(), sessionStoreName, tx, nil, options)
	if err != nil {
		tx.Rollback(ctx)
		return nil, nil, fmt.Errorf("session store failed to open/create store: %v", err)
	}

	return store, tx, nil
}
