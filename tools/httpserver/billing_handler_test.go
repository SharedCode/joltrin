package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/governance"
)

func TestHandleGetPlan(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/billing/plan", nil)
	w := httptest.NewRecorder()

	handleGetPlan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if _, ok := resp["current_tier"]; !ok {
		t.Error("expected 'current_tier' field in response")
	}
	if _, ok := resp["capabilities"]; !ok {
		t.Error("expected 'capabilities' field in response")
	}
	if _, ok := resp["subscription"]; !ok {
		t.Error("expected 'subscription' field in response")
	}
}

func TestHandleCreateCheckoutSession(t *testing.T) {
	reqBody := map[string]any{
		"tier":      "pro",
		"tenant_id": "test-tenant-1",
		"email":     "admin@example.com",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/checkout", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handleCreateCheckoutSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}
	if resp["checkout_url"] == "" {
		t.Error("expected non-empty checkout_url")
	}
}

func TestHandleSimulateCheckout(t *testing.T) {
	gate := getServerFeatureGate()
	gate.SetTier(governance.TierCore)

	req := httptest.NewRequest(http.MethodGet, "/api/billing/checkout/simulate?session_id=cs_sim_123&tenant_id=tenant-sim&tier=pro&redirect=/app", nil)
	w := httptest.NewRecorder()

	handleSimulateCheckout(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d: %s", w.Code, w.Body.String())
	}

	if loc := w.Header().Get("Location"); loc != "/app" {
		t.Errorf("expected redirect to /app, got %s", loc)
	}

	// Verify server FeatureGate was upgraded
	if gate.Tier() != governance.TierPro {
		t.Errorf("expected gate to be upgraded to TierPro, got %s", gate.Tier())
	}
}

func TestHandleSubmitEnterpriseInquiry(t *testing.T) {
	inq := map[string]any{
		"name":      "VP of Engineering",
		"email":     "vp@enterprise-ai.com",
		"company":   "Enterprise AI Systems",
		"team_size": "50-200",
		"use_cases": "Federated MCP memory with Okta SSO",
	}
	body, _ := json.Marshal(inq)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/enterprise-contact", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handleSubmitEnterpriseInquiry(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed decoding JSON: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}
}

func TestHandleBillingWebhook_ValidSignatureAndUpgrade(t *testing.T) {
	secret := "whsec_handler_test_secret"
	svc := getBillingService().(*governance.DefaultBillingService)
	// Temporarily set webhook secret on the service for the test
	testSvc := governance.NewDefaultBillingService(governance.StripeConfig{
		WebhookSecret: secret,
	}, getServerFeatureGate())

	// Swap billing service for this test
	oldSvc := billingService
	billingService = testSvc
	defer func() { billingService = oldSvc }()

	payload := governance.ConstructSimulatedWebhookPayload("checkout.session.completed", "evt_wh_handler_1", "tenant-webhook", governance.TierPro)
	sigHeader := governance.GenerateStripeSignatureHeader(payload, secret, time.Now())

	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", sigHeader)
	w := httptest.NewRecorder()

	handleBillingWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}
	_ = svc
}

func TestHandleBillingWebhook_InvalidSignature_Rejected(t *testing.T) {
	secret := "whsec_handler_test_secret"
	testSvc := governance.NewDefaultBillingService(governance.StripeConfig{
		WebhookSecret: secret,
	}, getServerFeatureGate())

	oldSvc := billingService
	billingService = testSvc
	defer func() { billingService = oldSvc }()

	payload := []byte(`{"id":"evt_attack","type":"checkout.session.completed"}`)
	// Invalid signature header
	sigHeader := "t=1492774577,v1=badbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbad"

	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", sigHeader)
	w := httptest.NewRecorder()

	handleBillingWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for bad signature, got %d", w.Code)
	}
}

func TestHandleBillingWebhook_DuplicateIgnoredGracefully(t *testing.T) {
	secret := "whsec_duplicate_test"
	testSvc := governance.NewDefaultBillingService(governance.StripeConfig{
		WebhookSecret: secret,
	}, getServerFeatureGate())

	oldSvc := billingService
	billingService = testSvc
	defer func() { billingService = oldSvc }()

	payload := governance.ConstructSimulatedWebhookPayload("checkout.session.completed", "evt_duplicate_wh", "tenant-dup2", governance.TierPro)
	sigHeader := governance.GenerateStripeSignatureHeader(payload, secret, time.Now())

	// 1st request -> 200 OK
	req1 := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", bytes.NewReader(payload))
	req1.Header.Set("Stripe-Signature", sigHeader)
	w1 := httptest.NewRecorder()
	handleBillingWebhook(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w1.Code)
	}

	// 2nd request with same event ID -> 200 OK with ignored_duplicate status
	req2 := httptest.NewRequest(http.MethodPost, "/api/billing/webhook", bytes.NewReader(payload))
	req2.Header.Set("Stripe-Signature", sigHeader)
	w2 := httptest.NewRecorder()
	handleBillingWebhook(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for duplicate event to prevent Stripe retry storm, got %d", w2.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp["status"] != "ignored_duplicate" {
		t.Errorf("expected status 'ignored_duplicate', got %v", resp["status"])
	}
}
