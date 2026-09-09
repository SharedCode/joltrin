package governance

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidGatewayToken = errors.New("governance: invalid or expired gateway token")
	ErrRateLimitExceeded   = errors.New("governance: gateway rate limit exceeded")
)

// GatewayIdentity contains tenant, workspace, and role attributes of an MCP/A2A caller.
type GatewayIdentity struct {
	CallerID    string   `json:"caller_id"`
	TenantID    string   `json:"tenant_id"`
	WorkspaceID string   `json:"workspace_id"`
	Tier        Tier     `json:"tier"`
	Roles       []string `json:"roles"`
}

// GatewayAuthenticator verifies incoming caller credentials on MCP and A2A gateways.
type GatewayAuthenticator interface {
	Authenticate(ctx context.Context, token string) (*GatewayIdentity, error)
}

// RateLimiter controls throughput and request scheduling based on operational tiers.
type RateLimiter interface {
	Allow(ctx context.Context, identity *GatewayIdentity) (bool, time.Duration, error)
}

// PriorityCalculator returns a numeric scheduling weight for an identity (higher = higher priority).
func PriorityWeight(tier Tier) int {
	switch tier {
	case TierHosted:
		return 20
	case TierEnterprise:
		return 10
	case TierPro:
		return 5
	default:
		return 1
	}
}

// StaticTokenAuthenticator is an in-memory authenticator mapping API tokens to GatewayIdentities.
type StaticTokenAuthenticator struct {
	mu     sync.RWMutex
	tokens map[string]*GatewayIdentity
}

// NewStaticTokenAuthenticator returns a new static token authenticator.
func NewStaticTokenAuthenticator() *StaticTokenAuthenticator {
	return &StaticTokenAuthenticator{
		tokens: make(map[string]*GatewayIdentity),
	}
}

// RegisterToken registers a token for a given identity.
func (a *StaticTokenAuthenticator) RegisterToken(token string, identity *GatewayIdentity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens[token] = identity
}

// Authenticate verifies the token.
func (a *StaticTokenAuthenticator) Authenticate(ctx context.Context, token string) (*GatewayIdentity, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	id, ok := a.tokens[token]
	if !ok {
		return nil, ErrInvalidGatewayToken
	}
	return id, nil
}

type bucket struct {
	tokens     float64
	capacity   float64
	refillRate float64
	lastRefill time.Time
}

// TierRateLimiter enforces tier-differentiated token bucket rate limits per tenant.
type TierRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

// NewTierRateLimiter constructs a TierRateLimiter.
func NewTierRateLimiter() *TierRateLimiter {
	return &TierRateLimiter{
		buckets: make(map[string]*bucket),
	}
}

func (rl *TierRateLimiter) getBucketLocked(tenantID string, tier Tier) *bucket {
	b, ok := rl.buckets[tenantID]
	if ok {
		return b
	}

	rate := 10.0
	capacity := 20.0

	switch tier {
	case TierPro:
		rate = 100.0
		capacity = 200.0
	case TierEnterprise:
		rate = 1000.0
		capacity = 2000.0
	case TierHosted:
		rate = 5000.0
		capacity = 10000.0
	}

	b = &bucket{
		tokens:     capacity,
		capacity:   capacity,
		refillRate: rate,
		lastRefill: time.Now(),
	}
	rl.buckets[tenantID] = b
	return b
}

// Allow evaluates whether an incoming request from the identity is admitted.
func (rl *TierRateLimiter) Allow(ctx context.Context, identity *GatewayIdentity) (bool, time.Duration, error) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b := rl.getBucketLocked(identity.TenantID, identity.Tier)

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = b.tokens + (elapsed * b.refillRate)
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, 0, nil
	}

	needed := 1.0 - b.tokens
	retryAfter := time.Duration((needed / b.refillRate) * float64(time.Second))
	return false, retryAfter, nil
}
