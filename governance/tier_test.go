package governance

import (
	"testing"
)

func TestTierCapabilities(t *testing.T) {
	// Core tier
	coreCaps, err := TierCapabilities(TierCore)
	if err != nil {
		t.Fatalf("unexpected error for core tier: %v", err)
	}
	if len(coreCaps) != 9 {
		t.Errorf("expected 9 core capabilities, got %d", len(coreCaps))
	}

	// Pro tier
	proCaps, err := TierCapabilities(TierPro)
	if err != nil {
		t.Fatalf("unexpected error for pro tier: %v", err)
	}
	if len(proCaps) <= len(coreCaps) {
		t.Errorf("pro caps (%d) should exceed core caps (%d)", len(proCaps), len(coreCaps))
	}

	// Enterprise tier
	entCaps, err := TierCapabilities(TierEnterprise)
	if err != nil {
		t.Fatalf("unexpected error for enterprise tier: %v", err)
	}
	if len(entCaps) <= len(proCaps) {
		t.Errorf("enterprise caps (%d) should exceed pro caps (%d)", len(entCaps), len(proCaps))
	}

	// Hosted tier
	hostedCaps, err := TierCapabilities(TierHosted)
	if err != nil {
		t.Fatalf("unexpected error for hosted tier: %v", err)
	}
	if len(hostedCaps) <= len(entCaps) {
		t.Errorf("hosted caps (%d) should exceed enterprise caps (%d)", len(hostedCaps), len(entCaps))
	}

	// Invalid tier
	if _, err := TierCapabilities("invalid_tier"); err == nil {
		t.Errorf("expected error for invalid tier, got nil")
	}
}

func TestFeatureGate(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	if gate.Tier() != TierCore {
		t.Errorf("expected tier %s, got %s", TierCore, gate.Tier())
	}

	// Core capability must be allowed
	if !gate.Allows(CapEmbeddedBTree) {
		t.Errorf("expected CapEmbeddedBTree to be allowed in core")
	}
	if err := gate.Require(CapEmbeddedBTree); err != nil {
		t.Errorf("unexpected error on Require core cap: %v", err)
	}

	// Pro capability must be blocked in core
	if gate.Allows(CapPolicyAsCode) {
		t.Errorf("expected CapPolicyAsCode to be blocked in core")
	}
	if err := gate.Require(CapPolicyAsCode); err == nil {
		t.Errorf("expected error requiring CapPolicyAsCode in core")
	}

	// Dynamic override
	gate.SetOverride(CapPolicyAsCode, true)
	if !gate.Allows(CapPolicyAsCode) {
		t.Errorf("expected CapPolicyAsCode to be allowed after override")
	}
}
