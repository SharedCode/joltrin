package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/sharedcode/joltrin/governance"
)

var (
	billingService     governance.BillingService
	billingServiceOnce sync.Once
)

func getBillingService() governance.BillingService {
	billingServiceOnce.Do(func() {
		gate := getServerFeatureGate()

		secretKey := firstNonEmpty(os.Getenv("STRIPE_SECRET_KEY"), os.Getenv("JOLTRIN_STRIPE_SECRET_KEY"))
		webhookSecret := firstNonEmpty(os.Getenv("STRIPE_WEBHOOK_SECRET"), os.Getenv("JOLTRIN_STRIPE_WEBHOOK_SECRET"))
		publishableKey := firstNonEmpty(os.Getenv("STRIPE_PUBLISHABLE_KEY"), os.Getenv("JOLTRIN_STRIPE_PUBLISHABLE_KEY"))
		proPriceID := firstNonEmpty(os.Getenv("STRIPE_PRO_PRICE_ID"), os.Getenv("JOLTRIN_STRIPE_PRO_PRICE_ID"))
		entPriceID := firstNonEmpty(os.Getenv("STRIPE_ENTERPRISE_PRICE_ID"), os.Getenv("JOLTRIN_STRIPE_ENTERPRISE_PRICE_ID"))
		simulateStr := strings.ToLower(os.Getenv("STRIPE_SIMULATE"))
		simulate := simulateStr == "true" || simulateStr == "1" || secretKey == ""

		cfg := governance.StripeConfig{
			SecretKey:         secretKey,
			WebhookSecret:     webhookSecret,
			PublishableKey:    publishableKey,
			ProPriceID:        proPriceID,
			EnterprisePriceID: entPriceID,
			SuccessURL:        firstNonEmpty(os.Getenv("STRIPE_SUCCESS_URL"), "/app?checkout=success"),
			CancelURL:         firstNonEmpty(os.Getenv("STRIPE_CANCEL_URL"), "/app?checkout=canceled"),
			Simulate:          simulate,
		}

		billingService = governance.NewDefaultBillingService(cfg, gate)
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
	})
}

func handleCreateCheckoutSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

func handleCreatePortalSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	// Deliver simulated webhook internally to trigger authoritative state update
	payload := governance.ConstructSimulatedWebhookPayload("checkout.session.completed", sessionID, tenantID, tier)
	_, err := getBillingService().HandleWebhook(r.Context(), payload, "")
	if err != nil && !strings.Contains(err.Error(), "duplicate") {
		writeJSONError(w, http.StatusInternalServerError, "failed to simulate checkout activation: "+err.Error())
		return
	}

	if redirectURL == "" {
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
