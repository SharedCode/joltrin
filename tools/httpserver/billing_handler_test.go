package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/governance"
)

// TestMain isolates config.DatabasePath to a fresh temp directory before any
// test runs. getBillingService() is a package-level sync.Once singleton
// that persists billing state under config.DatabasePath; without this,
// config.DatabasePath is "" during `go test` (main()'s flag.Parse never
// runs), so the singleton would write real files under a relative
// "_billing" path in the source tree, and that state would leak across
// separate `go test` invocations (a duplicate-event false positive once
// bit TestHandleSimulateCheckout locally).
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "joltrin-httpserver-test-*")
	if err != nil {
		panic(err)
	}
	config.DatabasePath = tmpDir

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

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

func TestHandleSimulateCheckout_RejectsOpenRedirect(t *testing.T) {
	gate := getServerFeatureGate()
	gate.SetTier(governance.TierCore)

	req := httptest.NewRequest(http.MethodGet, "/api/billing/checkout/simulate?session_id=cs_sim_evil&tenant_id=tenant-sim&tier=pro&redirect=https://evil.example/phish", nil)
	w := httptest.NewRecorder()

	handleSimulateCheckout(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc == "https://evil.example/phish" {
		t.Fatalf("attacker-controlled redirect target was honored: %s", loc)
	}
}

// TestHandleSimulateCheckout_BypassAttemptsStaySameOrigin is the end-to-end
// half of the CodeQL alert 268 investigation: it drives the real handler
// with a "redirect" query parameter built the way an actual attacker
// request would carry one (via url.Values, so encoding matches the wire
// format exactly) for every bypass class considered, and asserts the
// resulting Location header, when resolved against the site's own origin,
// never ends up pointing at a different host. This is the property that
// actually matters; isSafeRelativeRedirect's own accept/reject choice for
// each payload is exercised separately in redirect_safety_test.go.
func TestHandleSimulateCheckout_BypassAttemptsStaySameOrigin(t *testing.T) {
	gate := getServerFeatureGate()
	gate.SetTier(governance.TierCore)

	payloads := []string{
		"https://evil.example/phish",
		"http://evil.example/phish",
		"//evil.example/phish",
		"///evil.example/phish",
		`/\evil.example/phish`,
		`/\\evil.example/phish`,
		"/%2F%2Fevil.example",
		"/%5Cevil.example",
		"/%252F%252Fevil.example",
		"/\t/evil.example",
		"/\n/evil.example",
		"/\r/evil.example",
		"/\r\nSet-Cookie: pwn=1",
		"/ /evil.example",
		" //evil.example",
		"/../../evil.example",
		"/%2e%2e/evil.example",
		"/@evil.example",
		"/HTTP://evil.example",
		"/JAVASCRIPT:alert(1)",
		"/／evil.example",
	}

	origin := &url.URL{Scheme: "https", Host: "joltrin.example"}

	for _, redirect := range payloads {
		t.Run(redirect, func(t *testing.T) {
			q := url.Values{}
			q.Set("session_id", "cs_sim_bypass")
			q.Set("tenant_id", "tenant-sim")
			q.Set("tier", "pro")
			q.Set("redirect", redirect)

			req := httptest.NewRequest(http.MethodGet, "/api/billing/checkout/simulate?"+q.Encode(), nil)
			w := httptest.NewRecorder()

			handleSimulateCheckout(w, req)

			loc := w.Header().Get("Location")
			if loc == "" {
				t.Fatalf("no Location header set for redirect=%q", redirect)
			}
			resolved, err := origin.Parse(loc)
			if err != nil {
				t.Fatalf("Location header %q from redirect=%q did not parse: %v", loc, redirect, err)
			}
			if resolved.Host != origin.Host {
				t.Errorf("redirect=%q produced off-origin Location %q (resolved host %q, want %q)",
					redirect, loc, resolved.Host, origin.Host)
			}
		})
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
