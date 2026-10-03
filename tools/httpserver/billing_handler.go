package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5/governance"
)

var (
	billingService     governance.BillingService
	billingServiceOnce sync.Once

	billingRateLimiter     *governance.TierRateLimiter
	billingRateLimiterOnce sync.Once
)

// getBillingRateLimiter returns the shared tier-aware token-bucket limiter
// (governance/gateway.go) guarding billing endpoints that either call out to
// Stripe or aren't behind requireAuth/withAuth, so an automated flood can't
// run up the Stripe API bill or spam webhook processing.
func getBillingRateLimiter() *governance.TierRateLimiter {
	billingRateLimiterOnce.Do(func() {
		billingRateLimiter = governance.NewTierRateLimiter()
	})
	return billingRateLimiter
}

// clientIPKey extracts a best-effort client identifier for rate limiting.
// Azure Container Apps' ingress sets X-Forwarded-For; RemoteAddr is the
// fallback for direct/local connections.
func clientIPKey(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.Index(fwd, ","); idx != -1 {
			return strings.TrimSpace(fwd[:idx])
		}
		return strings.TrimSpace(fwd)
	}
	return r.RemoteAddr
}

// checkBillingRateLimit applies the tier-aware rate limit to a billing
// request, keyed by client IP since these endpoints run pre-auth. Returns
// false and writes a 429 response if the caller should be throttled; the
// caller must return immediately when this returns false. Fails open on an
// internal limiter error so a limiter bug cannot take down billing.
func checkBillingRateLimit(w http.ResponseWriter, r *http.Request) bool {
	identity := &governance.GatewayIdentity{
		TenantID: clientIPKey(r),
		Tier:     getServerFeatureGate().Tier(),
	}
	allowed, retryAfter, err := getBillingRateLimiter().Allow(r.Context(), identity)
	if err != nil {
		return true
	}
	if !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
		writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded, retry later")
		return false
	}
	return true
}

// loadStripeConfig reads Stripe settings through getenv so tests can supply
// their own environment. Each setting also accepts a JOLTRIN_STRIPE_* form.
//
// The Azure deployment stores the literal "unset" in Key Vault for a Stripe
// secret that has not been provided yet (Key Vault rejects empty values).
// That placeholder is treated as empty, so an unconfigured deployment stays in
// simulation mode instead of trying to use "unset" as a key.
func loadStripeConfig(getenv func(string) string) governance.StripeConfig {
	getenv = ignoreUnset(getenv)
	secretKey := firstNonEmpty(getenv("STRIPE_SECRET_KEY"), getenv("JOLTRIN_STRIPE_SECRET_KEY"))
	webhookSecret := firstNonEmpty(getenv("STRIPE_WEBHOOK_SECRET"), getenv("JOLTRIN_STRIPE_WEBHOOK_SECRET"))
	publishableKey := firstNonEmpty(getenv("STRIPE_PUBLISHABLE_KEY"), getenv("JOLTRIN_STRIPE_PUBLISHABLE_KEY"))
	proPriceID := firstNonEmpty(getenv("STRIPE_PRO_PRICE_ID"), getenv("JOLTRIN_STRIPE_PRO_PRICE_ID"))
	entPriceID := firstNonEmpty(getenv("STRIPE_ENTERPRISE_PRICE_ID"), getenv("JOLTRIN_STRIPE_ENTERPRISE_PRICE_ID"))
	simulateStr := strings.ToLower(getenv("STRIPE_SIMULATE"))
	simulate := simulateStr == "true" || simulateStr == "1" || secretKey == ""
	managedStr := strings.ToLower(getenv("STRIPE_MANAGED_PAYMENTS"))

	// Stripe requires absolute return URLs. JOLTRIN_PUBLIC_URL (the
	// site's public origin, no trailing slash) builds them; without it
	// the relative defaults only work in simulation mode and billing
	// readiness reports the URL variables as missing.
	successDefault, cancelDefault := "/app?checkout=success", "/app?checkout=canceled"
	if base := strings.TrimRight(getenv("JOLTRIN_PUBLIC_URL"), "/"); base != "" {
		successDefault = base + "/app?checkout=success&session_id={CHECKOUT_SESSION_ID}"
		cancelDefault = base + "/app?checkout=canceled"
	}

	return governance.StripeConfig{
		SecretKey:         secretKey,
		WebhookSecret:     webhookSecret,
		PublishableKey:    publishableKey,
		ProPriceID:        proPriceID,
		EnterprisePriceID: entPriceID,
		SuccessURL:        firstNonEmpty(getenv("STRIPE_SUCCESS_URL"), successDefault),
		CancelURL:         firstNonEmpty(getenv("STRIPE_CANCEL_URL"), cancelDefault),
		Simulate:          simulate,
		ManagedPayments:   managedStr == "true" || managedStr == "1",
	}
}

func ignoreUnset(getenv func(string) string) func(string) string {
	return func(k string) string {
		if v := getenv(k); v != "unset" {
			return v
		}
		return ""
	}
}

func getBillingService() governance.BillingService {
	billingServiceOnce.Do(func() {
		gate := getServerFeatureGate()

		cfg := loadStripeConfig(os.Getenv)
		if ready := governance.AssessBilling(cfg); ready.Mode == "live" && !ready.Pro.Available {
			fmt.Fprintf(os.Stderr, "billing: live Stripe key set but Pro checkout is off, missing: %s\n", strings.Join(ready.Pro.Missing, ", "))
		}

		// Subscriptions, webhook idempotency keys, and enterprise inquiries are
		// persisted under a dedicated subfolder of the server's own data path
		// (joltrin's own embedded store) so they survive a restart/redeploy
		// instead of living only in process memory. Falls back to the same
		// default as the -database flag if unset, rather than silently
		// collapsing to a relative path in the current working directory.
		dbPath := config.DatabasePath
		if dbPath == "" {
			dbPath = "/tmp/sop_data"
		}
		billingDataPath := filepath.Join(dbPath, "_billing")
		store := governance.NewJoltrinBillingStore(billingDataPath)
		billingService = governance.NewDefaultBillingService(cfg, gate, store)
	})
	return billingService
}

func handleGetPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	gate := getServerFeatureGate()
	activeTier := gate.Tier()
	caps, _ := governance.TierCapabilities(activeTier)

	tenantID := "default"
	sub, err := getBillingService().GetSubscription(r.Context(), tenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to get subscription: "+err.Error())
		return
	}

	cfg := getBillingService().Config()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"current_tier":      activeTier,
		"capabilities":      caps,
		"subscription":      sub,
		"stripe_configured": cfg.SecretKey != "" && !cfg.Simulate,
		"publishable_key":   cfg.PublishableKey,
		"simulate_mode":     cfg.Simulate,
		"checkout":          governance.AssessBilling(cfg),
	})
}

func handleCreateCheckoutSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkBillingRateLimit(w, r) {
		return
	}

	var req struct {
		Tier     string `json:"tier"`
		TenantID string `json:"tenant_id"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	targetTier := governance.Tier(strings.ToLower(strings.TrimSpace(req.Tier)))
	if targetTier == "" {
		targetTier = governance.TierPro
	}
	if targetTier != governance.TierPro && targetTier != governance.TierEnterprise {
		writeJSONError(w, http.StatusBadRequest, "unsupported target tier: "+string(targetTier))
		return
	}

	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = "default"
	}

	sess, err := getBillingService().CreateCheckoutSession(r.Context(), tenantID, req.Email, targetTier)
	if errors.Is(err, governance.ErrEnterpriseContactSales) {
		writeJSONError(w, http.StatusConflict, "Enterprise is contact-sales only. Use the enterprise contact form.")
		return
	}
	if errors.Is(err, governance.ErrBillingNotConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "checkout is not available yet: billing is not fully configured")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create checkout session: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"checkout_url": sess.URL,
		"session_id":   sess.ID,
		"tier":         sess.Tier,
		"simulate":     getBillingService().Config().Simulate,
	})
}

// publicCheckoutOrigins may call the checkout endpoint from a browser. The
// marketing site is static, so its Pro form reaches this server cross-origin.
var publicCheckoutOrigins = map[string]bool{
	"https://joltrinhq.com":     true,
	"https://www.joltrinhq.com": true,
}

// handlePublicCheckout lets the static site start a Pro Checkout Session
// without a login. The tier and tenant are fixed server side, the caller only
// supplies an email, and the plan is granted by the signed webhook, never by
// this request. It is rate limited like the other pre-auth billing routes.
func handlePublicCheckout(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); publicCheckoutOrigins[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkBillingRateLimit(w, r) {
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.Email))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "a valid email is required")
		return
	}

	sess, err := getBillingService().CreateCheckoutSession(r.Context(), "public", addr.Address, governance.TierPro)
	if errors.Is(err, governance.ErrBillingNotConfigured) {
		writeJSONError(w, http.StatusServiceUnavailable, "checkout is not available yet: billing is not fully configured")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create checkout session")
		return
	}
	if getBillingService().Config().Simulate {
		// A simulated session URL is relative to this API host and would
		// grant a plan without payment, so never hand it to the public site.
		writeJSONError(w, http.StatusServiceUnavailable, "checkout is not available yet: billing is in simulation mode")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"checkout_url": sess.URL,
		"session_id":   sess.ID,
	})
}

func handleCreatePortalSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !checkBillingRateLimit(w, r) {
		return
	}

	var req struct {
		CustomerID string `json:"customer_id"`
		ReturnURL  string `json:"return_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	customerID := req.CustomerID
	if customerID == "" {
		sub, _ := getBillingService().GetSubscription(r.Context(), "default")
		if sub != nil && sub.CustomerID != "" {
			customerID = sub.CustomerID
		}
	}

	if customerID == "" {
		writeJSONError(w, http.StatusBadRequest, "no active customer ID found")
		return
	}

	portalURL, err := getBillingService().CreatePortalSession(r.Context(), customerID, req.ReturnURL)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create portal session: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"portal_url": portalURL,
	})
}

func handleSimulateCheckout(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	tenantID := r.URL.Query().Get("tenant_id")
	tierStr := r.URL.Query().Get("tier")
	redirectURL := r.URL.Query().Get("redirect")

	if tenantID == "" {
		tenantID = "default"
	}
	tier := governance.Tier(tierStr)
	if tier == "" {
		tier = governance.TierPro
	}

	// Only meaningful in simulation mode. With live Stripe keys this route
	// would otherwise be an unauthenticated way to grant a paid tier.
	cfg := getBillingService().Config()
	if !cfg.Simulate {
		http.NotFound(w, r)
		return
	}

	// Deliver simulated webhook internally to trigger authoritative state update
	payload := governance.ConstructSimulatedWebhookPayload("checkout.session.completed", sessionID, tenantID, tier)
	sig := ""
	if cfg.WebhookSecret != "" {
		sig = governance.GenerateStripeSignatureHeader(payload, cfg.WebhookSecret, time.Now())
	}
	_, err := getBillingService().HandleWebhook(r.Context(), payload, sig)
	if err != nil && !strings.Contains(err.Error(), "duplicate") {
		writeJSONError(w, http.StatusInternalServerError, "failed to simulate checkout activation: "+err.Error())
		return
	}

	if redirectURL == "" || !isSafeRelativeRedirect(redirectURL) {
		redirectURL = "/app?checkout=success&tier=" + string(tier)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func handleSubmitEnterpriseInquiry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var inq governance.EnterpriseInquiry
	if err := json.NewDecoder(r.Body).Decode(&inq); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	saved, err := getBillingService().SubmitEnterpriseInquiry(r.Context(), &inq)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Thanks. We read every enterprise inquiry personally and will follow up soon.",
		"inquiry": saved,
	})
}

func handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// This endpoint is public. With no signing secret there is no way to
	// tell a real Stripe event from a forged one, so refuse instead of
	// accepting unsigned events.
	if getBillingService().Config().WebhookSecret == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "webhook signing secret is not configured")
		return
	}

	// Limit body size to 1MB
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed reading payload")
		return
	}

	sigHeader := r.Header.Get("Stripe-Signature")
	ev, err := getBillingService().HandleWebhook(r.Context(), payload, sigHeader)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "duplicate") {
			// Acknowledge duplicate deliveries gracefully with 200 OK so Stripe does not endlessly retry
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "ignored_duplicate",
				"message": err.Error(),
			})
			return
		}
		writeJSONError(w, status, "webhook processing failed: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"event_id": ev.ID,
		"type":     ev.Type,
	})
}
