package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sharedcode/joltrin/v5"
	"golang.org/x/crypto/bcrypt"
)

// AuthProvider is the extension point for external identity providers such as AD,
// Entra ID, LDAP, SAML, or OIDC.
//
// The built-in local provider remains the default, while additional providers can
// be registered without changing the main auth flow.
type AuthProvider interface {
	Name() string
	Authenticate(ctx context.Context, username, password string) (bool, *UserRecord, error)
}

type LocalAuthProvider struct {
	cfg *Config
}

func (p *LocalAuthProvider) Name() string {
	return "local"
}

func (p *LocalAuthProvider) Authenticate(ctx context.Context, username, password string) (bool, *UserRecord, error) {
	username = normalizeUsername(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return false, nil, nil
	}

	for i := range p.cfg.Users {
		if normalizeUsername(p.cfg.Users[i].Username) != username {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(p.cfg.Users[i].PasswordHash), []byte(password)); err == nil {
			user := p.cfg.Users[i]
			return true, &user, nil
		}
		return false, nil, nil
	}

	if strings.EqualFold(username, "root") && strings.TrimSpace(p.cfg.RootPassword) != "" && strings.TrimSpace(password) == strings.TrimSpace(p.cfg.RootPassword) {
		return true, &UserRecord{Username: "root", Role: sop.RoleAdmin}, nil
	}

	return false, nil, nil
}

var (
	authProviderRegistry = map[string]func(*Config) (AuthProvider, error){}
	authProviderMu       sync.RWMutex
)

func RegisterAuthProvider(name string, factory func(*Config) (AuthProvider, error)) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		panic("auth provider name cannot be empty")
	}
	authProviderMu.Lock()
	defer authProviderMu.Unlock()
	authProviderRegistry[name] = factory
}

func (c *Config) resolveAuthProvider() (AuthProvider, error) {
	name := strings.TrimSpace(strings.ToLower(c.AuthProviderName))
	if name == "" {
		return &LocalAuthProvider{cfg: c}, nil
	}

	factory, ok := func() (func(*Config) (AuthProvider, error), bool) {
		authProviderMu.RLock()
		defer authProviderMu.RUnlock()
		f, ok := authProviderRegistry[name]
		return f, ok
	}()
	if !ok {
		return nil, fmt.Errorf("unknown auth provider %q", name)
	}
	return factory(c)
}
