package governance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStripeRequest_RetriesOn500ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"stripe unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"cs_ok"}`))
	}))
	defer srv.Close()

	svc := NewDefaultBillingService(StripeConfig{SecretKey: "sk_test"}, nil)

	resp, body, err := svc.stripeRequest(context.Background(), http.MethodPost, srv.URL, "", "")
	if err != nil {
		t.Fatalf("expected eventual success after retries, got err: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != `{"id":"cs_ok"}` {
		t.Fatalf("unexpected response: status=%d body=%s", resp.StatusCode, body)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts (2 failures + 1 success), got %d", got)
	}
}

func TestStripeRequest_NoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request"}`))
	}))
	defer srv.Close()

	svc := NewDefaultBillingService(StripeConfig{SecretKey: "sk_test"}, nil)
	resp, _, err := svc.stripeRequest(context.Background(), http.MethodPost, srv.URL, "", "")
	if err != nil {
		t.Fatalf("expected the 400 to be returned rather than treated as a transport error: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 passthrough, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 attempt (no retry on 4xx), got %d", got)
	}
}

func TestJoltrinBillingStore_SubscriptionRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewJoltrinBillingStore(t.TempDir())

	if _, found, err := store.GetSubscription(ctx, "tenant-a"); err != nil {
		t.Fatalf("GetSubscription on empty store: %v", err)
	} else if found {
		t.Fatal("expected no subscription on empty store")
	}

	sub := &Subscription{
		ID:         "sub_123",
		TenantID:   "tenant-a",
		CustomerID: "cus_123",
		PlanTier:   TierPro,
		Status:     SubStatusActive,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.PutSubscription(ctx, sub); err != nil {
		t.Fatalf("PutSubscription: %v", err)
	}

	got, found, err := store.GetSubscription(ctx, "tenant-a")
	if err != nil || !found {
		t.Fatalf("GetSubscription after put: found=%v err=%v", found, err)
	}
	if got.CustomerID != "cus_123" || got.PlanTier != TierPro {
		t.Fatalf("unexpected subscription round-trip: %+v", got)
	}

	subs, err := store.ListSubscriptions(ctx)
	if err != nil || len(subs) != 1 {
		t.Fatalf("ListSubscriptions: got %d, err %v", len(subs), err)
	}
}

func TestJoltrinBillingStore_ProcessedEventIdempotency(t *testing.T) {
	ctx := context.Background()
	store := NewJoltrinBillingStore(t.TempDir())

	if seen, err := store.HasProcessedEvent(ctx, "evt_1"); err != nil || seen {
		t.Fatalf("expected evt_1 unseen, got seen=%v err=%v", seen, err)
	}
	if err := store.MarkProcessedEvent(ctx, "evt_1", time.Now().UTC()); err != nil {
		t.Fatalf("MarkProcessedEvent: %v", err)
	}
	if seen, err := store.HasProcessedEvent(ctx, "evt_1"); err != nil || !seen {
		t.Fatalf("expected evt_1 seen after marking, got seen=%v err=%v", seen, err)
	}
}

func TestJoltrinBillingStore_CustomerTenantMapping(t *testing.T) {
	ctx := context.Background()
	store := NewJoltrinBillingStore(t.TempDir())

	if err := store.PutCustomerTenant(ctx, "cus_1", "tenant-a"); err != nil {
		t.Fatalf("PutCustomerTenant: %v", err)
	}
	tenantID, found, err := store.ResolveTenantByCustomer(ctx, "cus_1")
	if err != nil || !found || tenantID != "tenant-a" {
		t.Fatalf("ResolveTenantByCustomer: tenantID=%q found=%v err=%v", tenantID, found, err)
	}
}

func TestJoltrinBillingStore_InquiryRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewJoltrinBillingStore(t.TempDir())

	inq := &EnterpriseInquiry{ID: "inq_1", Name: "Jane Doe", Email: "jane@example.com", Status: "new"}
	if err := store.PutInquiry(ctx, inq); err != nil {
		t.Fatalf("PutInquiry: %v", err)
	}
	list, err := store.ListInquiries(ctx)
	if err != nil || len(list) != 1 || list[0].Email != "jane@example.com" {
		t.Fatalf("ListInquiries: got %+v, err %v", list, err)
	}
}

// TestDefaultBillingService_SurvivesRestart is the scenario this store exists
// for: a Container App restart should not wipe a paying customer's
// subscription. It simulates a restart by constructing two BillingService
// instances against the same on-disk store.
func TestDefaultBillingService_SurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	store1 := NewJoltrinBillingStore(dataDir)
	gate1 := NewFeatureGate(TierCore)
	svc1 := NewDefaultBillingService(StripeConfig{Simulate: true}, gate1, store1)

	sub := &Subscription{
		ID:         "sub_restart",
		TenantID:   "tenant-restart",
		CustomerID: "cus_restart",
		PlanTier:   TierEnterprise,
		Status:     SubStatusActive,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := svc1.SetSubscription(ctx, sub); err != nil {
		t.Fatalf("SetSubscription: %v", err)
	}

	// Simulate a restart: a brand new process, new service, same data directory.
	store2 := NewJoltrinBillingStore(dataDir)
	gate2 := NewFeatureGate(TierCore)
	svc2 := NewDefaultBillingService(StripeConfig{Simulate: true}, gate2, store2)

	got, err := svc2.GetSubscription(ctx, "tenant-restart")
	if err != nil {
		t.Fatalf("GetSubscription after restart: %v", err)
	}
	if got.PlanTier != TierEnterprise || got.Status != SubStatusActive {
		t.Fatalf("subscription did not survive restart, got %+v", got)
	}
}

func TestStripeRequest_SendsAPIVersionHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Stripe-Version")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	svc := NewDefaultBillingService(StripeConfig{SecretKey: "sk_test"}, nil)
	if _, _, err := svc.stripeRequest(context.Background(), http.MethodPost, srv.URL, "", managedPaymentsAPIVersion); err != nil {
		t.Fatal(err)
	}
	if got != managedPaymentsAPIVersion {
		t.Fatalf("Stripe-Version = %q, want %q", got, managedPaymentsAPIVersion)
	}
}

type captureTransport struct {
	body    string
	version string
}

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(r.Body)
	c.body = string(b)
	c.version = r.Header.Get("Stripe-Version")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1"}`)),
		Request:    r,
	}, nil
}

func checkoutForm(t *testing.T, managed bool) (form url.Values, version string) {
	t.Helper()
	cfg := liveConfig()
	cfg.ManagedPayments = managed
	svc := NewDefaultBillingService(cfg, NewFeatureGate(TierCore))
	cap := &captureTransport{}
	svc.httpClient = &http.Client{Transport: cap}
	if _, err := svc.CreateCheckoutSession(context.Background(), "t1", "a@example.com", TierPro); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	form, err := url.ParseQuery(cap.body)
	if err != nil {
		t.Fatal(err)
	}
	return form, cap.version
}

func TestCheckoutSession_ManagedPaymentsOmitsPaymentMethodTypes(t *testing.T) {
	form, version := checkoutForm(t, true)
	if form.Get("managed_payments[enabled]") != "true" {
		t.Fatalf("managed_payments[enabled] missing: %v", form)
	}
	if _, present := form["payment_method_types[0]"]; present {
		t.Fatal("Stripe rejects payment_method_types on a Managed Payments session")
	}
	if version != managedPaymentsAPIVersion {
		t.Fatalf("Stripe-Version = %q, want %q", version, managedPaymentsAPIVersion)
	}
}

func TestCheckoutSession_WithoutManagedPaymentsStillRestrictsToCards(t *testing.T) {
	form, version := checkoutForm(t, false)
	if form.Get("payment_method_types[0]") != "card" {
		t.Fatalf("expected card payment method, got %v", form)
	}
	if form.Has("managed_payments[enabled]") || version != "" {
		t.Fatalf("managed payments leaked into a normal session: %v version=%q", form, version)
	}
}
