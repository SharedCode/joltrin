package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
)

type testAuthProvider struct {
	name string
}

func (p *testAuthProvider) Name() string { return p.name }

func (p *testAuthProvider) Authenticate(ctx context.Context, username, password string) (bool, *UserRecord, error) {
	if username == "alice" && password == "pw" {
		return true, &UserRecord{Username: "alice", Role: sop.RoleUser}, nil
	}
	return false, nil, nil
}

func TestAuthenticateUser_ValidCredentials(t *testing.T) {
	cfg := Config{}

	if err := cfg.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	ok, user, err := cfg.Authenticate("root", "secret-password")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !ok {
		t.Fatal("Authenticate() = false, want true")
	}
	if user == nil || user.Username != "root" || user.Role != sop.RoleAdmin {
		t.Fatalf("Authenticate() returned unexpected user: %+v", user)
	}
}

func TestAuthenticateUser_InvalidPassword(t *testing.T) {
	cfg := Config{}

	if err := cfg.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	ok, user, err := cfg.Authenticate("root", "wrong-password")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if ok {
		t.Fatal("Authenticate() = true, want false")
	}
	if user != nil {
		t.Fatalf("Authenticate() returned unexpected user: %+v", user)
	}
}

func TestAuthenticateUser_TrimsWhitespaceInStoredPassword(t *testing.T) {
	cfg := Config{}

	if err := cfg.SetUser("root", "  secret-password  ", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	ok, user, err := cfg.Authenticate("root", "  secret-password  ")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !ok {
		t.Fatal("Authenticate() = false, want true")
	}
	if user == nil || user.Username != "root" || user.Role != sop.RoleAdmin {
		t.Fatalf("Authenticate() returned unexpected user: %+v", user)
	}
}

func TestSaveAndLoadConfig_PersistsRootUserCredentials(t *testing.T) {
	defer func() { config = Config{} }()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	cfg := Config{ConfigFile: configFile}
	if err := cfg.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}
	config = cfg

	if err := saveConfig(); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}

	config = Config{}
	if err := loadConfig(configFile); err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	ok, user, err := config.Authenticate("root", "secret-password")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !ok {
		t.Fatal("Authenticate() = false, want true after save/load")
	}
	if user == nil || user.Username != "root" || user.Role != sop.RoleAdmin {
		t.Fatalf("Authenticate() returned unexpected user: %+v", user)
	}
}

func TestAuthenticate_LegacyRootPasswordFallback(t *testing.T) {
	cfg := Config{RootPassword: "legacy-secret"}

	ok, user, err := cfg.Authenticate("root", "legacy-secret")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !ok {
		t.Fatal("Authenticate() = false, want true")
	}
	if user == nil || user.Username != "root" || user.Role != sop.RoleAdmin {
		t.Fatalf("Authenticate() returned unexpected user: %+v", user)
	}
}

func TestHandleRoot_RedirectsToSetupWizardWhenExplicitConfigIsMissing(t *testing.T) {
	config = Config{}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer os.Chdir(oldWd)

	config.ConfigFile = filepath.Join(tmpDir, "config.json")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handleRoot(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/app" {
		t.Fatalf("expected redirect to /app, got %q", got)
	}
}

func TestFindExistingConfigFile_PrefersExplicitConfigPath(t *testing.T) {
	config = Config{}

	parentDir := t.TempDir()
	childDir := filepath.Join(parentDir, "child")
	if err := os.Mkdir(childDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(parentDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(childDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	config.ConfigFile = filepath.Join(parentDir, "config.json")

	if got := findExistingConfigFile(); got != config.ConfigFile {
		t.Fatalf("findExistingConfigFile() = %q, want %q", got, config.ConfigFile)
	}
}

func TestFindExistingConfigFile_IgnoresPackageLocalConfigWhenCwdHasNone(t *testing.T) {
	config = Config{}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer os.Chdir(oldWd)

	if got := findExistingConfigFile(); got != "" {
		t.Fatalf("findExistingConfigFile() = %q, want empty when cwd has no config.json", got)
	}
}

func TestHandleRoot_ShowsLoginPageWhenConfigExists(t *testing.T) {
	config = Config{}
	config.ConfigFile = filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config.ConfigFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handleRoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Joltrin Login") && !strings.Contains(w.Body.String(), "SOP Login") {
		t.Fatalf("expected login page HTML, got %q", w.Body.String())
	}
}

func TestRequireAuth_RedirectsToLoginForProtectedAppPage(t *testing.T) {
	config = Config{}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}
	config.ConfigFile = filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config.ConfigFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	w := httptest.NewRecorder()

	requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not run when not authenticated")
	}))(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/login?next=%2Fapp" {
		t.Fatalf("expected redirect to /login?next=/app, got %q", got)
	}
}

func TestHandleLogin_SetsSessionCookies(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"root","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	cookies := w.Result().Cookies()
	foundAccess := false
	foundRefresh := false
	for _, cookie := range cookies {
		switch cookie.Name {
		case "sop_access_token":
			foundAccess = true
		case "sop_refresh_token":
			foundRefresh = true
		}
	}
	if !foundAccess || !foundRefresh {
		t.Fatalf("expected both sop_access_token and sop_refresh_token cookies, got %v", cookies)
	}
}

func TestHandleLogin_ReturnsRefreshToken(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"root","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatalf("expected access_token and refresh_token, got %+v", resp)
	}
}

func TestHandleRefresh_ReturnsNewAccessToken(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	access, refresh, err := currentTokenFacade().(*SessionStore).CreateSession(context.Background(), "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if access == "" || refresh == "" {
		t.Fatalf("unexpected empty token values")
	}

	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == "" {
		t.Fatalf("expected a renewed access_token, got %+v", resp)
	}
}

func TestHandleRefresh_SetsSessionCookie(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	oldFacade := tokenFacade
	store := NewSessionStore(time.Minute)
	tokenFacade = store
	defer func() { tokenFacade = oldFacade }()

	_, refresh, err := store.CreateSession(context.Background(), "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	cookies := w.Result().Cookies()
	var found bool
	for _, cookie := range cookies {
		if cookie.Name == "sop_access_token" && cookie.Value != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sop_access_token cookie to be set on refresh response, got cookies=%v", cookies)
	}
}

func TestHandleRefresh_SetsPersistentCookies(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	oldFacade := tokenFacade
	store := NewSessionStore(time.Minute)
	tokenFacade = store
	defer func() { tokenFacade = oldFacade }()

	_, refresh, err := store.CreateSession(context.Background(), "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	for _, cookie := range w.Result().Cookies() {
		switch cookie.Name {
		case "sop_access_token", "sop_refresh_token":
			if cookie.MaxAge <= 0 {
				t.Fatalf("expected %s cookie to be persistent (MaxAge>0), got MaxAge=%d", cookie.Name, cookie.MaxAge)
			}
		}
	}
}

func TestValidateToken_AcceptsStatelessAccessTokenAfterSessionRemoval(t *testing.T) {
	withIsolatedSessionStore(t)
	store := NewSessionStore(30 * time.Minute)
	ctx := context.Background()

	access, _, err := store.CreateSession(ctx, "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	st, tx, err := store.getStore(ctx)
	if err != nil {
		t.Fatalf("getStore() error = %v", err)
	}
	defer tx.Rollback(ctx)

	if removed, err := st.Remove(ctx, access); err != nil || !removed {
		t.Fatalf("Remove() = (%v, %v), want session record removed", removed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	user, err := store.ValidateToken(ctx, access)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if user == nil || !strings.EqualFold(user.Username, "root") || !strings.EqualFold(user.Role, sop.RoleAdmin) {
		t.Fatalf("ValidateToken() = %+v, want root admin user", user)
	}
}

func TestValidateToken_DoesNotExtendSessionWhenNotNearExpiry(t *testing.T) {
	withIsolatedSessionStore(t)
	store := NewSessionStore(30 * time.Minute)
	ctx := context.Background()
	access, _, err := store.CreateSession(ctx, "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	st, tx, err := store.getStore(ctx)
	if err != nil {
		t.Fatalf("getStore() error = %v", err)
	}
	defer tx.Rollback(ctx)

	if found, err := st.Find(ctx, access, false); err != nil || !found {
		t.Fatalf("Find() = (%v, %v), want found session", err, found)
	}
	record, err := st.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValue() error = %v", err)
	}
	expectedExpiresAt := time.Now().UTC().Add(10 * time.Minute)
	record.ExpiresAt = expectedExpiresAt
	if ok, err := st.UpdateCurrentValue(ctx, record); err != nil || !ok {
		t.Fatalf("UpdateCurrentValue() = (%v, %v), want success", ok, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	oldFacade := tokenFacade
	tokenFacade = store
	defer func() { tokenFacade = oldFacade }()

	if _, err := store.ValidateToken(ctx, access); err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	st2, tx2, err := store.getStore(ctx)
	if err != nil {
		t.Fatalf("getStore() after validation error = %v", err)
	}
	defer tx2.Rollback(ctx)

	if found, err := st2.Find(ctx, access, false); err != nil || !found {
		t.Fatalf("Find() = (%v, %v), want session found", err, found)
	}
	updated, err := st2.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValue() after validation error = %v", err)
	}
	if !updated.ExpiresAt.Equal(expectedExpiresAt) {
		t.Fatalf("ValidateToken() extended expiry unexpectedly: got %v, want %v", updated.ExpiresAt, expectedExpiresAt)
	}
}

func TestAuthenticateBearerToken_RejectsExpiredSessionToken(t *testing.T) {
	withIsolatedSessionStore(t)
	store := NewSessionStore(time.Nanosecond)
	ctx := context.Background()
	access, _, err := store.CreateSession(ctx, "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	st, tx, err := store.getStore(ctx)
	if err != nil {
		t.Fatalf("getStore() error = %v", err)
	}
	defer tx.Rollback(ctx)
	if found, err := st.Find(ctx, access, false); err != nil || !found {
		t.Fatalf("Find() = (%v, %v), want found session", err, found)
	}
	record, err := st.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValue() error = %v", err)
	}
	record.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if ok, err := st.UpdateCurrentValue(ctx, record); err != nil || !ok {
		t.Fatalf("UpdateCurrentValue() = (%v, %v), want success", ok, err)
	}

	oldFacade := tokenFacade
	tokenFacade = store
	defer func() { tokenFacade = oldFacade }()

	ok, user, err := (&Config{}).AuthenticateBearerToken(context.Background(), access)
	if err == nil || ok || user != nil {
		t.Fatalf("AuthenticateBearerToken() = (%v, %+v, %v), want expired session rejected", ok, user, err)
	}

	st2, tx2, err := store.getStore(ctx)
	if err != nil {
		t.Fatalf("getStore() after expiry error = %v", err)
	}
	defer tx2.Rollback(ctx)
	if found, err := st2.Find(ctx, access, false); err != nil || found {
		t.Fatalf("Find() after expiry = (%v, %v), want expired session removed", err, found)
	}
}

func TestAuthenticateBearerToken_AcceptsSignedAccessTokenForRemoteClient(t *testing.T) {
	withIsolatedSessionStore(t)
	oldConfig := config
	config = Config{SessionSecret: "remote-client-test-secret"}
	defer func() { config = oldConfig }()

	store := NewSessionStore(time.Minute)
	access, _, err := store.CreateSession(context.Background(), "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	ok, user, err := (&Config{}).AuthenticateBearerToken(context.Background(), access)
	if err != nil {
		t.Fatalf("AuthenticateBearerToken() error = %v", err)
	}
	if !ok || user == nil || !strings.EqualFold(user.Username, "root") {
		t.Fatalf("AuthenticateBearerToken() = (%v, %+v), want authenticated remote client token", ok, user)
	}
}

func TestConfig_Authenticate_UsesRegisteredAuthProvider(t *testing.T) {
	providerName := "test-provider"

	authProviderMu.Lock()
	oldFactory, existed := authProviderRegistry[providerName]
	authProviderRegistry[providerName] = func(*Config) (AuthProvider, error) {
		return &testAuthProvider{name: providerName}, nil
	}
	authProviderMu.Unlock()
	defer func() {
		authProviderMu.Lock()
		if existed {
			authProviderRegistry[providerName] = oldFactory
		} else {
			delete(authProviderRegistry, providerName)
		}
		authProviderMu.Unlock()
	}()

	cfg := Config{AuthProviderName: providerName}
	ok, user, err := cfg.Authenticate("alice", "pw")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !ok || user == nil || user.Username != "alice" || user.Role != sop.RoleUser {
		t.Fatalf("Authenticate() = (%v, %+v), want provider-authenticated user", ok, user)
	}
}

func TestAuthenticateBearerToken_RejectsLegacyCredentialToken(t *testing.T) {
	cfg := Config{}
	if err := cfg.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	ok, user, err := cfg.AuthenticateBearerToken(context.Background(), "root:secret-password")
	if err == nil && ok {
		t.Fatalf("AuthenticateBearerToken() = (%v, %+v, %v), want legacy credential bearer token rejected", ok, user, err)
	}
}

func TestHandleLogin_ReusesCurrentSessionToken(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	store := NewSessionStore(time.Minute)
	oldFacade := tokenFacade
	tokenFacade = store
	defer func() { tokenFacade = oldFacade }()

	existingToken, _, err := store.CreateSession(context.Background(), "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"root","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+existingToken)
	w := httptest.NewRecorder()

	handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken != existingToken {
		t.Fatalf("expected existing session token to be reused, got %q", resp.AccessToken)
	}
}

func TestHandleLogin_ReturnsSessionToken(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"root","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AccessToken == "" {
		t.Fatal("expected access_token in login response")
	}
	if resp.TokenType != "Bearer" {
		t.Fatalf("expected token_type Bearer, got %q", resp.TokenType)
	}

	ok, user, err := config.AuthenticateBearerToken(context.Background(), resp.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateBearerToken() error = %v", err)
	}
	if !ok || user == nil || user.Username != "root" {
		t.Fatalf("AuthenticateBearerToken() = (%v, %+v), want authenticated root user", ok, user)
	}
}

func TestSessionTTL_UsesConfigValue(t *testing.T) {
	config = Config{SessionTokenTTLMinutes: 45}
	if got := sessionTTL(); got != 45*time.Minute {
		t.Fatalf("sessionTTL() = %v, want %v", got, 45*time.Minute)
	}
}

func TestHandleLoginPage_RendersLoginScreen(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()

	handleLoginPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Joltrin Login") && !strings.Contains(w.Body.String(), "SOP Login") {
		t.Fatalf("expected login page HTML, got %q", w.Body.String())
	}
}

func TestHandleLogin_FormEncodedCredentials(t *testing.T) {
	withIsolatedSessionStore(t)
	config = Config{SystemDB: config.SystemDB}
	if err := config.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("grant_type=password&username=root&password=secret-password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthFromAuthorization_DoesNotAcceptLegacyRootPassword(t *testing.T) {
	cfg := Config{RootPassword: "legacy-secret"}

	username, password := cfg.authFromAuthorization("Bearer legacy-secret")
	if username != "" || password != "" {
		t.Fatalf("authFromAuthorization() = (%q, %q), want empty values for legacy root-password fallback", username, password)
	}
}

func TestIsAdminOverride_DoesNotAcceptLegacyRootPassword(t *testing.T) {
	cfg := Config{RootPassword: "legacy-secret"}

	if ok := cfg.IsAdminOverride(context.Background(), "", "", "legacy-secret"); ok {
		t.Fatal("IsAdminOverride() = true, want false for legacy root-password fallback")
	}
}

func TestIsAdminOverride_AcceptsSessionToken(t *testing.T) {
	withIsolatedSessionStore(t)
	cfg := Config{SystemDB: config.SystemDB}
	if err := cfg.SetUser("root", "secret-password", sop.RoleAdmin); err != nil {
		t.Fatalf("SetUser() error = %v", err)
	}

	token, err := currentTokenFacade().CreateToken(context.Background(), "root", sop.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	if ok := cfg.IsAdminOverride(context.Background(), "", "", token); !ok {
		t.Fatal("IsAdminOverride() = false, want true for session token")
	}
}

func TestSetUser_DuplicateUsername(t *testing.T) {
	cfg := Config{}

	if err := cfg.SetUser("root", "first", sop.RoleAdmin); err != nil {
		t.Fatalf("first SetUser() error = %v", err)
	}

	if err := cfg.SetUser("root", "second", sop.RoleUser); err == nil {
		t.Fatal("SetUser() expected duplicate username error, got nil")
	}
}
