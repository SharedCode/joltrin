package governance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestVerifyStripeSignature_Valid(t *testing.T) {
	secret := "whsec_test_secret_12345"
	payload := []byte(`{"id":"evt_123","type":"checkout.session.completed"}`)
	now := time.Now()

	header := GenerateStripeSignatureHeader(payload, secret, now)
	err := VerifyStripeSignature(payload, header, secret, 300*time.Second)
	if err != nil {
		t.Fatalf("expected valid signature, got error: %v", err)
	}
}

func TestVerifyStripeSignature_TamperedPayload(t *testing.T) {
	secret := "whsec_test_secret_12345"
	payload := []byte(`{"id":"evt_123","type":"checkout.session.completed"}`)
	now := time.Now()

	header := GenerateStripeSignatureHeader(payload, secret, now)
	tamperedPayload := []byte(`{"id":"evt_123","type":"checkout.session.tampered"}`)

	err := VerifyStripeSignature(tamperedPayload, header, secret, 300*time.Second)
	if !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Fatalf("expected ErrInvalidWebhookSignature, got %v", err)
	}
}

func TestVerifyStripeSignature_ExpiredTimestampReplayAttack(t *testing.T) {
	secret := "whsec_test_secret_12345"
	payload := []byte(`{"id":"evt_123"}`)
	// 10 minutes ago
	oldTime := time.Now().Add(-10 * time.Minute)

	header := GenerateStripeSignatureHeader(payload, secret, oldTime)
	err := VerifyStripeSignature(payload, header, secret, 300*time.Second)
	if !errors.Is(err, ErrStripeTimestampExpired) {
		t.Fatalf("expected ErrStripeTimestampExpired, got: %v", err)
	}
}

func TestVerifyStripeSignature_MalformedHeader(t *testing.T) {
	secret := "whsec_test_secret_12345"
	payload := []byte(`{"id":"evt_123"}`)

	if err := VerifyStripeSignature(payload, "", secret, 300*time.Second); err == nil {
		t.Fatal("expected error for empty header")
	}

	if err := VerifyStripeSignature(payload, "invalid_header", secret, 300*time.Second); err == nil {
		t.Fatal("expected error for malformed header")
	}
}

func TestDefaultBillingService_CreateCheckoutSession_Simulate(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	svc := NewDefaultBillingService(StripeConfig{Simulate: true}, gate)

	sess, err := svc.CreateCheckoutSession(context.Background(), "tenant-42", "alice@example.com", TierPro)
	if err != nil {
		t.Fatalf("failed to create checkout session: %v", err)
	}

	if sess.Tier != TierPro {
		t.Errorf("expected TierPro, got %s", sess.Tier)
	}
	if sess.TenantID != "tenant-42" {
		t.Errorf("expected tenant-42, got %s", sess.TenantID)
	}
	if sess.URL == "" {
		t.Error("expected checkout URL to be populated")
	}
}

func TestDefaultBillingService_Webhook_CheckoutCompleted_UpgradesTier(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	secret := "whsec_webhook_secret_abc"
	svc := NewDefaultBillingService(StripeConfig{WebhookSecret: secret}, gate)

	if gate.Tier() != TierCore {
		t.Fatalf("expected initial TierCore, got %s", gate.Tier())
	}
	if gate.Allows(CapPolicyAsCode) {
		t.Fatalf("expected CapPolicyAsCode to be disallowed in TierCore")
	}

	payload := ConstructSimulatedWebhookPayload("checkout.session.completed", "evt_checkout_101", "tenant-alpha", TierPro)
	sigHeader := GenerateStripeSignatureHeader(payload, secret, time.Now())

	ev, err := svc.HandleWebhook(context.Background(), payload, sigHeader)
	if err != nil {
		t.Fatalf("HandleWebhook failed: %v", err)
	}
	if ev.Type != "checkout.session.completed" {
		t.Errorf("expected checkout.session.completed, got %s", ev.Type)
	}

	// Verify tenant subscription
	sub, err := svc.GetSubscription(context.Background(), "tenant-alpha")
	if err != nil {
		t.Fatalf("failed to get subscription: %v", err)
	}
	if sub.PlanTier != TierPro {
		t.Errorf("expected PlanTier Pro, got %s", sub.PlanTier)
	}
	if sub.Status != SubStatusActive {
		t.Errorf("expected SubStatusActive, got %s", sub.Status)
	}

	// Verify server FeatureGate was dynamically upgraded
	if gate.Tier() != TierPro {
		t.Errorf("expected gate to upgrade to TierPro, got %s", gate.Tier())
	}
	if !gate.Allows(CapPolicyAsCode) {
		t.Error("expected CapPolicyAsCode to be allowed after upgrade to Pro")
	}
}

func TestDefaultBillingService_Webhook_IdempotencyDuplicateEvent(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	secret := "whsec_idempotent_test"
	svc := NewDefaultBillingService(StripeConfig{WebhookSecret: secret}, gate)

	payload := ConstructSimulatedWebhookPayload("checkout.session.completed", "evt_duplicate_test", "tenant-dup", TierPro)
	sigHeader := GenerateStripeSignatureHeader(payload, secret, time.Now())

	// First execution should succeed
	_, err := svc.HandleWebhook(context.Background(), payload, sigHeader)
	if err != nil {
		t.Fatalf("first webhook handling failed: %v", err)
	}

	// Second execution with same event ID should fail with ErrDuplicateWebhookEvent
	_, err = svc.HandleWebhook(context.Background(), payload, sigHeader)
	if !errors.Is(err, ErrDuplicateWebhookEvent) {
		t.Fatalf("expected ErrDuplicateWebhookEvent on duplicate delivery, got %v", err)
	}
}

func TestDefaultBillingService_Webhook_SubscriptionLifecycle_CanceledRevertsToCore(t *testing.T) {
	gate := NewFeatureGate(TierCore)
	svc := NewDefaultBillingService(StripeConfig{Simulate: true}, gate)

	// 1. Manually set Pro subscription
	sub := &Subscription{
		ID:         "sub_lifecycle_1",
		TenantID:   "tenant-lifecycle",
		CustomerID: "cus_lifecycle_1",
		PlanTier:   TierPro,
		Status:     SubStatusActive,
	}
	_ = svc.SetSubscription(context.Background(), sub)

	if gate.Tier() != TierPro {
		t.Fatalf("expected TierPro after subscription set, got %s", gate.Tier())
	}

	// 2. Deliver customer.subscription.deleted webhook
	payloadMap := map[string]any{
		"id":      "evt_sub_deleted",
		"type":    "customer.subscription.deleted",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":       "sub_lifecycle_1",
				"customer": "cus_lifecycle_1",
				"status":   "canceled",
			},
		},
	}
	payloadBytes, _ := json.Marshal(payloadMap)

	_, err := svc.HandleWebhook(context.Background(), payloadBytes, "")
	if err != nil {
		t.Fatalf("failed to process subscription deletion: %v", err)
	}

	// 3. Verify gate reverted to TierCore
	if gate.Tier() != TierCore {
		t.Errorf("expected gate to revert to TierCore on cancellation, got %s", gate.Tier())
	}
}

func TestDefaultBillingService_EnterpriseInquiry(t *testing.T) {
	svc := NewDefaultBillingService(StripeConfig{Simulate: true}, nil)

	inq, err := svc.SubmitEnterpriseInquiry(context.Background(), &EnterpriseInquiry{
		Name:     "Jane Doe",
		Email:    "jane@enterprise-corp.com",
		Company:  "Enterprise Corp",
		TeamSize: "100-500",
		UseCases: "Multi-tenant agent memory with Okta SSO and SIEM audit streaming",
	})
	if err != nil {
		t.Fatalf("SubmitEnterpriseInquiry failed: %v", err)
	}
	if inq.ID == "" {
		t.Error("expected inquiry ID to be generated")
	}

	list, err := svc.ListEnterpriseInquiries(context.Background())
	if err != nil {
		t.Fatalf("ListEnterpriseInquiries failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 inquiry, got %d", len(list))
	}
	if list[0].Email != "jane@enterprise-corp.com" {
		t.Errorf("expected jane@enterprise-corp.com, got %s", list[0].Email)
	}
}
