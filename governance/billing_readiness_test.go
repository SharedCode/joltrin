package governance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func liveConfig() StripeConfig {
	return StripeConfig{
		SecretKey:     "sk_test_placeholder",
		WebhookSecret: "whsec_placeholder",
		ProPriceID:    "price_pro_real",
		SuccessURL:    "https://example.test/app?checkout=success",
		CancelURL:     "https://example.test/app?checkout=canceled",
	}
}

func TestAssessBilling_SimulationMode(t *testing.T) {
	r := AssessBilling(StripeConfig{})
	if r.Mode != "simulated" || !r.Pro.Available || r.Pro.Mode != "simulated" {
		t.Fatalf("expected simulated Pro checkout, got %+v", r)
	}
	if r.Enterprise.Available || r.Enterprise.Mode != "contact_sales" {
		t.Fatalf("Enterprise must stay contact-sales without a price ID, got %+v", r.Enterprise)
	}
}

func TestAssessBilling_LiveMissingConfigNamesVariablesOnly(t *testing.T) {
	r := AssessBilling(StripeConfig{SecretKey: "sk_test_placeholder", ProPriceID: placeholderProPriceID})
	if r.Mode != "live" || r.Pro.Available {
		t.Fatalf("live mode with missing config must not offer checkout, got %+v", r.Pro)
	}
	want := map[string]bool{
		"STRIPE_PRO_PRICE_ID":   true,
		"STRIPE_WEBHOOK_SECRET": true,
		"STRIPE_SUCCESS_URL":    true,
		"STRIPE_CANCEL_URL":     true,
	}
	for _, m := range r.Pro.Missing {
		if !want[m] {
			t.Errorf("unexpected missing entry %q", m)
		}
		delete(want, m)
	}
	if len(want) != 0 {
		t.Errorf("missing entries not reported: %v", want)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "sk_test_placeholder") {
		t.Error("readiness output must not contain the secret key")
	}
}

func TestAssessBilling_LiveFullyConfigured(t *testing.T) {
	r := AssessBilling(liveConfig())
	if !r.Pro.Available || r.Pro.Mode != "stripe" {
		t.Fatalf("expected live Pro checkout, got %+v", r.Pro)
	}
	if r.Enterprise.Available || r.Enterprise.Mode != "contact_sales" {
		t.Fatalf("Enterprise stays contact-sales without its own price, got %+v", r.Enterprise)
	}

	cfg := liveConfig()
	cfg.EnterprisePriceID = "price_ent_real"
	r = AssessBilling(cfg)
	if !r.Enterprise.Available || r.Enterprise.Mode != "stripe" {
		t.Fatalf("a real Enterprise price ID should enable Enterprise checkout, got %+v", r.Enterprise)
	}
}

func TestCreateCheckoutSession_EnterpriseIsContactSalesWithoutPrice(t *testing.T) {
	for _, cfg := range []StripeConfig{{Simulate: true}, liveConfig()} {
		svc := NewDefaultBillingService(cfg, NewFeatureGate(TierCore))
		_, err := svc.CreateCheckoutSession(context.Background(), "t1", "a@example.com", TierEnterprise)
		if !errors.Is(err, ErrEnterpriseContactSales) {
			t.Errorf("simulate=%v: expected ErrEnterpriseContactSales, got %v", cfg.Simulate, err)
		}
	}
}

func TestCreateCheckoutSession_LiveWithoutPriceIDIsRefused(t *testing.T) {
	cfg := liveConfig()
	cfg.ProPriceID = ""
	svc := NewDefaultBillingService(cfg, NewFeatureGate(TierCore))
	_, err := svc.CreateCheckoutSession(context.Background(), "t1", "a@example.com", TierPro)
	if !errors.Is(err, ErrBillingNotConfigured) {
		t.Fatalf("expected ErrBillingNotConfigured, got %v", err)
	}
}

func TestHandleWebhook_LiveWithoutSigningSecretIsRefused(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	svc := NewDefaultBillingService(StripeConfig{SecretKey: "sk_test_placeholder", ProPriceID: "price_pro_real"}, gate)

	payload := ConstructSimulatedWebhookPayload("checkout.session.completed", "evt_unsigned_live", "t1", TierPro)
	_, err := svc.HandleWebhook(context.Background(), payload, "")
	if !errors.Is(err, ErrWebhookNotConfigured) {
		t.Fatalf("expected ErrWebhookNotConfigured, got %v", err)
	}
	if gate.Tier() != TierCore {
		t.Fatalf("a refused webhook must not change the tier, got %s", gate.Tier())
	}
}

func TestHandleWebhook_LiveRejectsMissingAndWrongSignature(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	svc := NewDefaultBillingService(liveConfig(), gate)
	payload := ConstructSimulatedWebhookPayload("checkout.session.completed", "evt_bad_sig", "t1", TierPro)

	if _, err := svc.HandleWebhook(context.Background(), payload, ""); !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Errorf("missing signature: expected ErrInvalidWebhookSignature, got %v", err)
	}
	wrong := GenerateStripeSignatureHeader(payload, "whsec_some_other_secret", time.Now())
	if _, err := svc.HandleWebhook(context.Background(), payload, wrong); !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Errorf("wrong secret: expected ErrInvalidWebhookSignature, got %v", err)
	}
	if gate.Tier() != TierCore {
		t.Fatalf("rejected events must not change the tier, got %s", gate.Tier())
	}
}

func deliver(t *testing.T, svc *DefaultBillingService, secret, id, typ string, obj map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"id":      id,
		"type":    typ,
		"created": time.Now().Unix(),
		"data":    map[string]any{"object": obj},
	})
	sig := GenerateStripeSignatureHeader(payload, secret, time.Now())
	if _, err := svc.HandleWebhook(context.Background(), payload, sig); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
}

func TestSubscriptionLifecycle_PaymentFailureRecoveryAndCancellation(t *testing.T) {
	cfg := liveConfig()
	gate := NewFeatureGate(TierCore)
	svc := NewDefaultBillingService(cfg, gate, NewJoltrinBillingStore(t.TempDir()))
	secret := cfg.WebhookSecret
	ctx := context.Background()

	status := func() SubscriptionStatus {
		sub, _ := svc.GetSubscription(ctx, "t-life")
		return sub.Status
	}

	deliver(t, svc, secret, "evt_life_1", "checkout.session.completed", map[string]any{
		"client_reference_id": "t-life",
		"customer":            "cus_life",
		"subscription":        "sub_life",
		"metadata":            map[string]any{"tier": "pro"},
	})
	if gate.Tier() != TierPro || status() != SubStatusActive {
		t.Fatalf("after checkout: tier=%s status=%s", gate.Tier(), status())
	}

	deliver(t, svc, secret, "evt_life_2", "invoice.payment_failed", map[string]any{"customer": "cus_life"})
	if status() != SubStatusPastDue {
		t.Fatalf("after payment failure: status=%s", status())
	}

	deliver(t, svc, secret, "evt_life_3", "invoice.payment_succeeded", map[string]any{"customer": "cus_life"})
	if status() != SubStatusActive || gate.Tier() != TierPro {
		t.Fatalf("after recovery: tier=%s status=%s", gate.Tier(), status())
	}

	deliver(t, svc, secret, "evt_life_4", "customer.subscription.updated", map[string]any{
		"id": "sub_life", "customer": "cus_life", "status": "canceled",
		"metadata": map[string]any{"tier": "pro"},
	})
	if status() != SubStatusCanceled || gate.Tier() != TierCore {
		t.Fatalf("after cancel update: tier=%s status=%s", gate.Tier(), status())
	}
}

func TestSubscriptionLifecycle_DeletedEventSurvivesRestart(t *testing.T) {
	cfg := liveConfig()
	dir := t.TempDir()
	svc := NewDefaultBillingService(cfg, NewFeatureGate(TierCore), NewJoltrinBillingStore(dir))

	deliver(t, svc, cfg.WebhookSecret, "evt_restart_1", "checkout.session.completed", map[string]any{
		"client_reference_id": "t-restart", "customer": "cus_restart", "subscription": "sub_restart",
	})
	deliver(t, svc, cfg.WebhookSecret, "evt_restart_2", "customer.subscription.deleted", map[string]any{
		"customer": "cus_restart",
	})

	gate2 := NewFeatureGate(TierCore)
	svc2 := NewDefaultBillingService(cfg, gate2, NewJoltrinBillingStore(dir))
	sub, _ := svc2.GetSubscription(context.Background(), "t-restart")
	if sub.Status != SubStatusCanceled {
		t.Fatalf("canceled status must persist across restart, got %s", sub.Status)
	}

	// A redelivered event after restart is still treated as a duplicate.
	payload, _ := json.Marshal(map[string]any{
		"id": "evt_restart_2", "type": "customer.subscription.deleted", "created": time.Now().Unix(),
		"data": map[string]any{"object": map[string]any{"customer": "cus_restart"}},
	})
	sig := GenerateStripeSignatureHeader(payload, cfg.WebhookSecret, time.Now())
	if _, err := svc2.HandleWebhook(context.Background(), payload, sig); !errors.Is(err, ErrDuplicateWebhookEvent) {
		t.Fatalf("expected duplicate after restart, got %v", err)
	}
}
