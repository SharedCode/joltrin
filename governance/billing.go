package governance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidWebhookSignature = errors.New("governance/billing: invalid Stripe webhook signature")
	ErrStripeTimestampExpired  = errors.New("governance/billing: webhook timestamp outside tolerance window")
	ErrSubscriptionNotFound    = errors.New("governance/billing: subscription not found")
	ErrDuplicateWebhookEvent   = errors.New("governance/billing: duplicate webhook event already processed")
	ErrInvalidPriceOrTier      = errors.New("governance/billing: unrecognized pricing plan or target tier")
	ErrBillingNotConfigured    = errors.New("governance/billing: Stripe credentials not configured")
	ErrEnterpriseContactSales  = errors.New("governance/billing: Enterprise is contact-sales only, no Enterprise price is configured")
	ErrWebhookNotConfigured    = errors.New("governance/billing: webhook signing secret is not configured")
)

// Placeholder price IDs the service falls back to when none is configured.
// They are never valid Stripe prices, so a live deployment that still holds
// one must not offer checkout for that tier.
const (
	placeholderProPriceID        = "price_joltrin_pro_monthly"
	placeholderEnterprisePriceID = "price_joltrin_enterprise_annual"
)

// SubscriptionStatus represents the lifecycle state of a commercial license subscription.
type SubscriptionStatus string

const (
	SubStatusActive     SubscriptionStatus = "active"
	SubStatusTrialing   SubscriptionStatus = "trialing"
	SubStatusPastDue    SubscriptionStatus = "past_due"
	SubStatusCanceled   SubscriptionStatus = "canceled"
	SubStatusIncomplete SubscriptionStatus = "incomplete"
)

// Subscription represents an organization's active commercial entitlement.
type Subscription struct {
	ID                 string             `json:"id"`
	TenantID           string             `json:"tenant_id"`
	CustomerID         string             `json:"customer_id"`
	PlanTier           Tier               `json:"plan_tier"`
	Status             SubscriptionStatus `json:"status"`
	CurrentPeriodStart time.Time          `json:"current_period_start"`
	CurrentPeriodEnd   time.Time          `json:"current_period_end"`
	CancelAtPeriodEnd  bool               `json:"cancel_at_period_end"`
	PaymentMethodBrand string             `json:"payment_method_brand,omitempty"`
	PaymentMethodLast4 string             `json:"payment_method_last4,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// CheckoutSession represents an initialized Stripe Checkout flow.
type CheckoutSession struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	Tier          Tier   `json:"tier"`
	TenantID      string `json:"tenant_id"`
	CustomerID    string `json:"customer_id,omitempty"`
	CustomerEmail string `json:"customer_email,omitempty"`
}

// EnterpriseInquiry captures an enterprise lead / custom deployment request.
type EnterpriseInquiry struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Company      string    `json:"company"`
	Role         string    `json:"role,omitempty"`
	CompanySize  string    `json:"company_size,omitempty"`
	TeamSize     string    `json:"team_size,omitempty"` // Legacy alias for CompanySize
	Tier         string    `json:"tier,omitempty"`
	UseCases     string    `json:"use_cases,omitempty"`
	ApproxAgents string    `json:"approx_agents,omitempty"`
	Website      string    `json:"website,omitempty"`
	IDP          string    `json:"idp,omitempty"`
	Deployment   string    `json:"deployment,omitempty"`
	Message      string    `json:"message,omitempty"`
	Status       string    `json:"status"` // "new", "contacted", "qualified"
	CreatedAt    time.Time `json:"created_at"`
}

// StripeConfig defines configuration for Stripe billing and webhooks.
type StripeConfig struct {
	SecretKey         string `json:"secret_key"`
	WebhookSecret     string `json:"webhook_secret"`
	PublishableKey    string `json:"publishable_key"`
	ProPriceID        string `json:"pro_price_id"`
	EnterprisePriceID string `json:"enterprise_price_id"`
	SuccessURL        string `json:"success_url"`
	CancelURL         string `json:"cancel_url"`
	Simulate          bool   `json:"simulate"`
	// ManagedPayments routes checkout through Stripe Managed Payments, where
	// Stripe handles indirect tax, fraud, and transaction support. The account
	// must be enrolled or Stripe rejects the session.
	ManagedPayments bool `json:"managed_payments"`
}

// managedPaymentsAPIVersion is the Stripe API version required to create
// Checkout Sessions with managed_payments enabled. Stripe documents
// 2025-03-31.basil as the minimum.
const managedPaymentsAPIVersion = "2025-03-31.basil"

// TierCheckout describes whether a tier can be bought through checkout.
// Mode is one of "stripe", "simulated", "contact_sales", or "unavailable".
// Missing lists environment variable names only, never values.
type TierCheckout struct {
	Available bool     `json:"available"`
	Mode      string   `json:"mode"`
	Missing   []string `json:"missing,omitempty"`
}

// BillingReadiness is a secret-free summary of what the current Stripe
// configuration can actually do, so an operator can see why checkout is off
// without reading logs or the Key Vault.
type BillingReadiness struct {
	Mode       string       `json:"mode"` // "live" or "simulated"
	Pro        TierCheckout `json:"pro"`
	Enterprise TierCheckout `json:"enterprise"`
}

func isAbsoluteURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// AssessBilling reports which tiers can complete checkout with cfg. Live
// checkout needs a real price, a webhook signing secret (otherwise a paid
// customer would never be upgraded), and absolute return URLs. Enterprise
// stays contact-sales until a real Enterprise price ID is configured.
func AssessBilling(cfg StripeConfig) BillingReadiness {
	live := cfg.SecretKey != "" && !cfg.Simulate

	hasPro := cfg.ProPriceID != "" && cfg.ProPriceID != placeholderProPriceID
	hasEnt := cfg.EnterprisePriceID != "" && cfg.EnterprisePriceID != placeholderEnterprisePriceID

	r := BillingReadiness{Mode: "simulated"}
	if live {
		r.Mode = "live"
	}

	switch {
	case !live:
		r.Pro = TierCheckout{Available: true, Mode: "simulated"}
	default:
		var missing []string
		if !hasPro {
			missing = append(missing, "STRIPE_PRO_PRICE_ID")
		}
		if cfg.WebhookSecret == "" {
			missing = append(missing, "STRIPE_WEBHOOK_SECRET")
		}
		if !isAbsoluteURL(cfg.SuccessURL) {
			missing = append(missing, "STRIPE_SUCCESS_URL")
		}
		if !isAbsoluteURL(cfg.CancelURL) {
			missing = append(missing, "STRIPE_CANCEL_URL")
		}
		if len(missing) == 0 {
			r.Pro = TierCheckout{Available: true, Mode: "stripe"}
		} else {
			r.Pro = TierCheckout{Mode: "unavailable", Missing: missing}
		}
	}

	if !hasEnt {
		r.Enterprise = TierCheckout{Mode: "contact_sales"}
	} else if !live {
		r.Enterprise = TierCheckout{Available: true, Mode: "simulated"}
	} else {
		r.Enterprise = r.Pro
		r.Enterprise.Missing = nil
		for _, m := range r.Pro.Missing {
			if m != "STRIPE_PRO_PRICE_ID" {
				r.Enterprise.Missing = append(r.Enterprise.Missing, m)
			}
		}
		if len(r.Enterprise.Missing) == 0 {
			r.Enterprise.Available, r.Enterprise.Mode = true, "stripe"
		} else {
			r.Enterprise.Available, r.Enterprise.Mode = false, "unavailable"
		}
	}
	return r
}

// VerifyStripeSignature cryptographically verifies a Stripe-Signature header using HMAC-SHA256.
func VerifyStripeSignature(payload []byte, sigHeader string, secret string, tolerance time.Duration) error {
	if secret == "" {
		return errors.New("governance/billing: webhook secret is required for verification")
	}
	if sigHeader == "" {
		return fmt.Errorf("%w: missing Stripe-Signature header", ErrInvalidWebhookSignature)
	}

	if tolerance <= 0 {
		tolerance = 300 * time.Second // 5 minute default tolerance
	}

	var timestamp int64 = -1
	signatures := make([][]byte, 0, 2)

	pairs := strings.Split(sigHeader, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		if key == "t" {
			ts, err := strconv.ParseInt(val, 10, 64)
			if err == nil {
				timestamp = ts
			}
		} else if key == "v1" {
			sigBytes, err := hex.DecodeString(val)
			if err == nil {
				signatures = append(signatures, sigBytes)
			}
		}
	}

	if timestamp == -1 {
		return fmt.Errorf("%w: missing timestamp 't' in header", ErrInvalidWebhookSignature)
	}
	if len(signatures) == 0 {
		return fmt.Errorf("%w: no v1 signature present in header", ErrInvalidWebhookSignature)
	}

	// Verify timestamp tolerance against replay attacks
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if time.Duration(diff)*time.Second > tolerance {
		return fmt.Errorf("%w: timestamp delta %ds exceeds tolerance %v", ErrStripeTimestampExpired, diff, tolerance)
	}

	// Compute expected signature: HMAC-SHA256(secret, "${timestamp}.${payload}")
	signedPayload := fmt.Sprintf("%d.%s", timestamp, string(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expectedSig := mac.Sum(nil)

	// Constant-time comparison across all v1 signatures
	matched := false
	for _, sig := range signatures {
		if hmac.Equal(sig, expectedSig) {
			matched = true
			break
		}
	}

	if !matched {
		return fmt.Errorf("%w: signature mismatch", ErrInvalidWebhookSignature)
	}

	return nil
}

// StripeWebhookEvent represents a parsed Stripe event envelope.
type StripeWebhookEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object map[string]any `json:"object"`
	} `json:"data"`
}

// BillingService defines the commercial subscription management lifecycle.
type BillingService interface {
	Config() StripeConfig
	CreateCheckoutSession(ctx context.Context, tenantID, email string, tier Tier) (*CheckoutSession, error)
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)
	HandleWebhook(ctx context.Context, payload []byte, sigHeader string) (*StripeWebhookEvent, error)
	GetSubscription(ctx context.Context, tenantID string) (*Subscription, error)
	SetSubscription(ctx context.Context, sub *Subscription) error
	SubmitEnterpriseInquiry(ctx context.Context, inq *EnterpriseInquiry) (*EnterpriseInquiry, error)
	ListEnterpriseInquiries(ctx context.Context) ([]*EnterpriseInquiry, error)
}

// DefaultBillingService is a production-ready, thread-safe implementation of BillingService.
type DefaultBillingService struct {
	mu              sync.RWMutex
	cfg             StripeConfig
	httpClient      *http.Client
	gate            *FeatureGate
	store           BillingStore                  // durable backing store; nil means in-memory only
	subscriptions   map[string]*Subscription      // tenantID -> Subscription (read cache)
	customerMap     map[string]string             // customerID -> tenantID (read cache)
	processedEvents map[string]time.Time          // eventID -> processedAt (idempotency, read cache)
	inquiries       map[string]*EnterpriseInquiry // inquiryID -> inquiry (read cache)
}

// NewDefaultBillingService constructs a new BillingService. Passing a
// BillingStore makes subscriptions, webhook idempotency keys, and enterprise
// inquiries durable across restarts; every write goes through to it, and its
// contents hydrate the in-memory read cache at construction time. Omitting
// it (as existing tests and Simulate-only demos do) keeps the previous
// in-memory-only behavior.
func NewDefaultBillingService(cfg StripeConfig, gate *FeatureGate, store ...BillingStore) *DefaultBillingService {
	if cfg.ProPriceID == "" {
		cfg.ProPriceID = placeholderProPriceID
	}
	if cfg.EnterprisePriceID == "" {
		cfg.EnterprisePriceID = placeholderEnterprisePriceID
	}
	if cfg.SecretKey == "" {
		cfg.Simulate = true
	}

	s := &DefaultBillingService{
		cfg:             cfg,
		httpClient:      &http.Client{Timeout: 15 * time.Second},
		gate:            gate,
		subscriptions:   make(map[string]*Subscription),
		customerMap:     make(map[string]string),
		processedEvents: make(map[string]time.Time),
		inquiries:       make(map[string]*EnterpriseInquiry),
	}
	if len(store) > 0 {
		s.store = store[0]
	}
	if s.store != nil {
		s.hydrateFromStore(context.Background())
	}
	return s
}

// hydrateFromStore loads durable billing state into the in-memory read cache.
// Called once at construction; failures are logged to stderr rather than
// treated as fatal, since a fresh/empty store is a normal first-run state.
func (s *DefaultBillingService) hydrateFromStore(ctx context.Context) {
	if subs, err := s.store.ListSubscriptions(ctx); err == nil {
		for _, sub := range subs {
			s.subscriptions[sub.TenantID] = sub
		}
	} else {
		fmt.Fprintf(os.Stderr, "governance/billing: hydrate subscriptions: %v\n", err)
	}
	if cm, err := s.store.ListCustomerTenants(ctx); err == nil {
		s.customerMap = cm
	} else {
		fmt.Fprintf(os.Stderr, "governance/billing: hydrate customer map: %v\n", err)
	}
	if pe, err := s.store.ListProcessedEvents(ctx); err == nil {
		s.processedEvents = pe
	} else {
		fmt.Fprintf(os.Stderr, "governance/billing: hydrate processed events: %v\n", err)
	}
	if inqs, err := s.store.ListInquiries(ctx); err == nil {
		for _, inq := range inqs {
			s.inquiries[inq.ID] = inq
		}
	} else {
		fmt.Fprintf(os.Stderr, "governance/billing: hydrate inquiries: %v\n", err)
	}
}

func (s *DefaultBillingService) Config() StripeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// stripeRequest performs a form-encoded POST against the Stripe API with
// bounded retry-with-backoff on 429 (rate limited) and 5xx (Stripe outage)
// responses; 4xx-other-than-429 responses are returned immediately since a
// retry won't change a malformed or rejected request. This is the graceful
// degradation layer for a commercial billing surface: a single Stripe blip
// shouldn't fail a customer's checkout outright.
func (s *DefaultBillingService) stripeRequest(ctx context.Context, method, url, body, apiVersion string) (*http.Response, []byte, error) {
	const maxAttempts = 3
	backoff := 500 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if err != nil {
			return nil, nil, fmt.Errorf("governance/billing: failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+s.cfg.SecretKey)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if apiVersion != "" {
			req.Header.Set("Stripe-Version", apiVersion)
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("governance/billing: stripe api error: %w", err)
		} else {
			bodyBytes, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				return nil, nil, fmt.Errorf("governance/billing: failed reading stripe response: %w", readErr)
			}
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return resp, bodyBytes, nil
			}
			lastErr = fmt.Errorf("governance/billing: stripe api returned status %d: %s", resp.StatusCode, string(bodyBytes))
		}

		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	return nil, nil, lastErr
}

// CreateCheckoutSession generates a Stripe Checkout session or a deterministic simulated session.
func (s *DefaultBillingService) CreateCheckoutSession(ctx context.Context, tenantID, email string, tier Tier) (*CheckoutSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tenantID == "" {
		tenantID = "default"
	}

	var priceID string
	switch tier {
	case TierPro:
		priceID = s.cfg.ProPriceID
	case TierEnterprise:
		if s.cfg.EnterprisePriceID == placeholderEnterprisePriceID {
			return nil, ErrEnterpriseContactSales
		}
		priceID = s.cfg.EnterprisePriceID
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidPriceOrTier, tier)
	}

	if !s.cfg.Simulate && s.cfg.SecretKey != "" {
		readiness := AssessBilling(s.cfg)
		tc := readiness.Pro
		if tier == TierEnterprise {
			tc = readiness.Enterprise
		}
		if !tc.Available {
			return nil, fmt.Errorf("%w: missing %s", ErrBillingNotConfigured, strings.Join(tc.Missing, ", "))
		}
	}

	// Simulation mode when no live Stripe secret key is present
	if s.cfg.Simulate || s.cfg.SecretKey == "" {
		sessionID := "cs_sim_" + uuid.NewString()[:8]
		successURL := s.cfg.SuccessURL
		if successURL == "" {
			successURL = "/app?checkout=success&tier=" + string(tier)
		}
		simURL := fmt.Sprintf("/api/billing/checkout/simulate?session_id=%s&tenant_id=%s&tier=%s&redirect=%s",
			sessionID, url.QueryEscape(tenantID), string(tier), url.QueryEscape(successURL))

		return &CheckoutSession{
			ID:            sessionID,
			URL:           simURL,
			Tier:          tier,
			TenantID:      tenantID,
			CustomerEmail: email,
		}, nil
	}

	// Production Stripe API call
	data := url.Values{}
	data.Set("mode", "subscription")
	// Stripe rejects payment_method_types on a Managed Payments session
	// ("Managed Payments handles this parameter for you").
	if !s.cfg.ManagedPayments {
		data.Set("payment_method_types[0]", "card")
	}
	data.Set("line_items[0][price]", priceID)
	data.Set("line_items[0][quantity]", "1")
	data.Set("client_reference_id", tenantID)
	data.Set("metadata[tenant_id]", tenantID)
	data.Set("metadata[tier]", string(tier))

	if email != "" {
		data.Set("customer_email", email)
	}

	successURL := s.cfg.SuccessURL
	if successURL == "" {
		successURL = "https://joltrinhq.com/app?checkout=success&session_id={CHECKOUT_SESSION_ID}"
	}
	cancelURL := s.cfg.CancelURL
	if cancelURL == "" {
		cancelURL = "https://joltrinhq.com/app?checkout=canceled"
	}
	data.Set("success_url", successURL)
	data.Set("cancel_url", cancelURL)

	apiVersion := ""
	if s.cfg.ManagedPayments {
		data.Set("managed_payments[enabled]", "true")
		apiVersion = managedPaymentsAPIVersion
	}

	resp, bodyBytes, err := s.stripeRequest(ctx, http.MethodPost, "https://api.stripe.com/v1/checkout/sessions", data.Encode(), apiVersion)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("governance/billing: stripe api rejected checkout session (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var parsed struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return nil, fmt.Errorf("governance/billing: failed to unmarshal session response: %w", err)
	}

	return &CheckoutSession{
		ID:            parsed.ID,
		URL:           parsed.URL,
		Tier:          tier,
		TenantID:      tenantID,
		CustomerEmail: email,
	}, nil
}

// CreatePortalSession creates a Stripe Customer Portal session.
func (s *DefaultBillingService) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.cfg.Simulate || s.cfg.SecretKey == "" {
		if returnURL == "" {
			returnURL = "/app"
		}
		return "/api/billing/portal/simulate?customer_id=" + url.QueryEscape(customerID) + "&return_url=" + url.QueryEscape(returnURL), nil
	}

	data := url.Values{}
	data.Set("customer", customerID)
	if returnURL != "" {
		data.Set("return_url", returnURL)
	}

	resp, body, err := s.stripeRequest(ctx, http.MethodPost, "https://api.stripe.com/v1/billing_portal/sessions", data.Encode(), "")
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("governance/billing: stripe portal error: %s", string(body))
	}

	var parsed struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	return parsed.URL, nil
}

// HandleWebhook processes incoming Stripe webhook events idempotently and verifies HMAC signatures.
func (s *DefaultBillingService) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) (*StripeWebhookEvent, error) {
	// 1. Signature Verification. A live deployment must never accept an
	// unsigned event: without a signing secret anyone could post a forged
	// checkout.session.completed and upgrade the server's tier.
	if s.cfg.WebhookSecret == "" && !s.cfg.Simulate && s.cfg.SecretKey != "" {
		return nil, ErrWebhookNotConfigured
	}
	if s.cfg.WebhookSecret != "" {
		if err := VerifyStripeSignature(payload, sigHeader, s.cfg.WebhookSecret, 300*time.Second); err != nil {
			return nil, err
		}
	}

	// 2. Parse Event Envelope
	var ev StripeWebhookEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("governance/billing: failed to decode webhook JSON: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 3. Idempotency Check
	if _, exists := s.processedEvents[ev.ID]; exists {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateWebhookEvent, ev.ID)
	}
	processedAt := time.Now().UTC()
	s.processedEvents[ev.ID] = processedAt
	if s.store != nil {
		if err := s.store.MarkProcessedEvent(ctx, ev.ID, processedAt); err != nil {
			return nil, fmt.Errorf("governance/billing: persist processed event: %w", err)
		}
	}

	// Clean up old events cache (> 24 hours)
	if len(s.processedEvents) > 5000 {
		cutoff := time.Now().Add(-24 * time.Hour)
		for id, t := range s.processedEvents {
			if t.Before(cutoff) {
				delete(s.processedEvents, id)
			}
		}
	}

	// 4. Inner Data Object
	obj := ev.Data.Object
	if obj == nil {
		return &ev, nil
	}

	now := time.Now().UTC()
	var affectedTenant, affectedCustomer string

	switch ev.Type {
	case "checkout.session.completed":
		tenantID := "default"
		if meta, ok := obj["metadata"].(map[string]any); ok {
			if tid, ok := meta["tenant_id"].(string); ok && tid != "" {
				tenantID = tid
			}
		}
		if ref, ok := obj["client_reference_id"].(string); ok && ref != "" {
			tenantID = ref
		}

		targetTier := TierPro
		if meta, ok := obj["metadata"].(map[string]any); ok {
			if t, ok := meta["tier"].(string); ok && t != "" {
				targetTier = Tier(t)
			}
		}

		subID, _ := obj["subscription"].(string)
		if subID == "" {
			subID = "sub_" + uuid.NewString()[:8]
		}
		customerID, _ := obj["customer"].(string)

		sub := &Subscription{
			ID:                 subID,
			TenantID:           tenantID,
			CustomerID:         customerID,
			PlanTier:           targetTier,
			Status:             SubStatusActive,
			CurrentPeriodStart: now,
			CurrentPeriodEnd:   now.AddDate(0, 1, 0),
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		s.subscriptions[tenantID] = sub
		if customerID != "" {
			s.customerMap[customerID] = tenantID
		}
		affectedTenant, affectedCustomer = tenantID, customerID

		// Dynamically upgrade server FeatureGate
		if s.gate != nil {
			s.gate.SetTier(targetTier)
		}

	case "customer.subscription.created", "customer.subscription.updated":
		subID, _ := obj["id"].(string)
		customerID, _ := obj["customer"].(string)
		statusStr, _ := obj["status"].(string)

		tenantID := s.customerMap[customerID]
		if tenantID == "" {
			if meta, ok := obj["metadata"].(map[string]any); ok {
				if tid, ok := meta["tenant_id"].(string); ok {
					tenantID = tid
				}
			}
		}
		if tenantID == "" {
			tenantID = "default"
		}

		targetTier := TierPro
		if meta, ok := obj["metadata"].(map[string]any); ok {
			if t, ok := meta["tier"].(string); ok && t != "" {
				targetTier = Tier(t)
			}
		}

		status := SubStatusActive
		if statusStr == "past_due" {
			status = SubStatusPastDue
		} else if statusStr == "canceled" {
			status = SubStatusCanceled
		} else if statusStr == "trialing" {
			status = SubStatusTrialing
		}

		existing, ok := s.subscriptions[tenantID]
		if !ok {
			existing = &Subscription{
				ID:        subID,
				TenantID:  tenantID,
				CreatedAt: now,
			}
			s.subscriptions[tenantID] = existing
		}
		existing.CustomerID = customerID
		existing.PlanTier = targetTier
		existing.Status = status
		existing.UpdatedAt = now
		affectedTenant, affectedCustomer = tenantID, customerID

		if s.gate != nil {
			if status == SubStatusActive || status == SubStatusTrialing {
				s.gate.SetTier(targetTier)
			} else if status == SubStatusCanceled {
				s.gate.SetTier(TierCore)
			}
		}

	case "customer.subscription.deleted":
		customerID, _ := obj["customer"].(string)
		tenantID := s.customerMap[customerID]
		if tenantID == "" {
			tenantID = "default"
		}

		if sub, ok := s.subscriptions[tenantID]; ok {
			sub.Status = SubStatusCanceled
			sub.UpdatedAt = now
			affectedTenant = tenantID
		}

		// Revert to open-source core tier
		if s.gate != nil {
			s.gate.SetTier(TierCore)
		}

	case "invoice.payment_succeeded":
		customerID, _ := obj["customer"].(string)
		tenantID := s.customerMap[customerID]
		if tenantID != "" {
			if sub, ok := s.subscriptions[tenantID]; ok {
				sub.Status = SubStatusActive
				sub.UpdatedAt = now
				affectedTenant = tenantID
				if s.gate != nil {
					s.gate.SetTier(sub.PlanTier)
				}
			}
		}

	case "invoice.payment_failed":
		customerID, _ := obj["customer"].(string)
		tenantID := s.customerMap[customerID]
		if tenantID != "" {
			if sub, ok := s.subscriptions[tenantID]; ok {
				sub.Status = SubStatusPastDue
				sub.UpdatedAt = now
				affectedTenant = tenantID
			}
		}
	}

	if s.store != nil && affectedTenant != "" {
		if sub, ok := s.subscriptions[affectedTenant]; ok {
			if err := s.store.PutSubscription(ctx, sub); err != nil {
				return nil, fmt.Errorf("governance/billing: persist subscription: %w", err)
			}
		}
		if affectedCustomer != "" {
			if err := s.store.PutCustomerTenant(ctx, affectedCustomer, affectedTenant); err != nil {
				return nil, fmt.Errorf("governance/billing: persist customer mapping: %w", err)
			}
		}
	}

	return &ev, nil
}

// GetSubscription returns the current subscription for a tenant.
func (s *DefaultBillingService) GetSubscription(ctx context.Context, tenantID string) (*Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if tenantID == "" {
		tenantID = "default"
	}

	sub, ok := s.subscriptions[tenantID]
	if !ok {
		// Return implicit core tier subscription
		return &Subscription{
			ID:                 "sub_core_implicit",
			TenantID:           tenantID,
			PlanTier:           TierCore,
			Status:             SubStatusActive,
			CurrentPeriodStart: time.Now().UTC(),
			CurrentPeriodEnd:   time.Now().UTC().AddDate(100, 0, 0),
		}, nil
	}

	copy := *sub
	return &copy, nil
}

// SetSubscription manually registers or updates a subscription.
func (s *DefaultBillingService) SetSubscription(ctx context.Context, sub *Subscription) error {
	if sub == nil {
		return errors.New("governance/billing: subscription cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.subscriptions[sub.TenantID] = sub
	if sub.CustomerID != "" {
		s.customerMap[sub.CustomerID] = sub.TenantID
	}
	if s.store != nil {
		if err := s.store.PutSubscription(ctx, sub); err != nil {
			return fmt.Errorf("governance/billing: persist subscription: %w", err)
		}
		if sub.CustomerID != "" {
			if err := s.store.PutCustomerTenant(ctx, sub.CustomerID, sub.TenantID); err != nil {
				return fmt.Errorf("governance/billing: persist customer mapping: %w", err)
			}
		}
	}

	if s.gate != nil && sub.Status == SubStatusActive {
		s.gate.SetTier(sub.PlanTier)
	}

	return nil
}

// SubmitEnterpriseInquiry records a custom enterprise deployment inquiry.
func (s *DefaultBillingService) SubmitEnterpriseInquiry(ctx context.Context, inq *EnterpriseInquiry) (*EnterpriseInquiry, error) {
	if inq == nil {
		return nil, errors.New("governance/billing: inquiry cannot be nil")
	}
	if inq.Email == "" {
		return nil, errors.New("governance/billing: email is required for enterprise contact")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if inq.ID == "" {
		inq.ID = "inq_" + uuid.NewString()[:8]
	}
	inq.Status = "new"
	inq.CreatedAt = time.Now().UTC()

	s.inquiries[inq.ID] = inq
	if s.store != nil {
		if err := s.store.PutInquiry(ctx, inq); err != nil {
			return nil, fmt.Errorf("governance/billing: persist inquiry: %w", err)
		}
	}
	return inq, nil
}

// ListEnterpriseInquiries retrieves all submitted inquiries.
func (s *DefaultBillingService) ListEnterpriseInquiries(ctx context.Context) ([]*EnterpriseInquiry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]*EnterpriseInquiry, 0, len(s.inquiries))
	for _, inq := range s.inquiries {
		res = append(res, inq)
	}
	return res, nil
}

// ConstructSimulatedWebhookPayload creates a deterministic Stripe JSON event payload for tests/simulations.
func ConstructSimulatedWebhookPayload(eventType, eventID, tenantID string, tier Tier) []byte {
	if eventID == "" {
		eventID = "evt_sim_" + uuid.NewString()[:8]
	}
	payload := map[string]any{
		"id":      eventID,
		"type":    eventType,
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{
				"id":                  "sub_sim_" + uuid.NewString()[:8],
				"client_reference_id": tenantID,
				"customer":            "cus_sim_" + uuid.NewString()[:8],
				"status":              "active",
				"metadata": map[string]any{
					"tenant_id": tenantID,
					"tier":      string(tier),
				},
			},
		},
	}
	bytes, _ := json.Marshal(payload)
	return bytes
}

// GenerateStripeSignatureHeader produces a valid Stripe-Signature header for testing.
func GenerateStripeSignatureHeader(payload []byte, secret string, timestamp time.Time) string {
	ts := timestamp.Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, string(payload))))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("t=%d,v1=%s", ts, sig)
}
