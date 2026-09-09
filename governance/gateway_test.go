package governance

import (
	"context"
	"testing"
)

func TestGatewayAuthenticatorAndRateLimiter(t *testing.T) {
	ctx := context.Background()

	auth := NewStaticTokenAuthenticator()
	identity := &GatewayIdentity{
		CallerID:    "agent-007",
		TenantID:    "corp-alpha",
		WorkspaceID: "ws-finance",
		Tier:        TierPro,
		Roles:       []string{"Analyst"},
	}
	auth.RegisterToken("secret-token-123", identity)

	// Valid auth
	id, err := auth.Authenticate(ctx, "secret-token-123")
	if err != nil {
		t.Fatalf("failed to authenticate valid token: %v", err)
	}
	if id.CallerID != "agent-007" || id.Tier != TierPro {
		t.Errorf("mismatched identity: %+v", id)
	}

	// Invalid auth
	_, err = auth.Authenticate(ctx, "bad-token")
	if err == nil {
		t.Fatalf("expected error on bad token, got nil")
	}

	// Priority calculation
	if PriorityWeight(TierCore) >= PriorityWeight(TierPro) {
		t.Errorf("Pro priority should exceed Core priority")
	}
	if PriorityWeight(TierPro) >= PriorityWeight(TierEnterprise) {
		t.Errorf("Enterprise priority should exceed Pro priority")
	}

	// Rate limiter
	limiter := NewTierRateLimiter()
	allowed, _, err := limiter.Allow(ctx, identity)
	if err != nil || !allowed {
		t.Fatalf("expected first request to be allowed, got allowed=%v err=%v", allowed, err)
	}
}
