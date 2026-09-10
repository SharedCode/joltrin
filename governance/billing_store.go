package governance

import (
	"context"
	"fmt"
	"time"

	sop "github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/database"
)

// BillingStore persists commercial billing state - subscriptions, the
// customer-to-tenant mapping, webhook idempotency keys, and enterprise
// inquiries - so it survives a process restart or redeploy. DefaultBillingService
// keeps its existing in-memory maps as a read cache and writes through to a
// BillingStore when one is configured; a nil store preserves the old
// in-memory-only behavior (used by unit tests and Simulate-only demos).
type BillingStore interface {
	GetSubscription(ctx context.Context, tenantID string) (*Subscription, bool, error)
	PutSubscription(ctx context.Context, sub *Subscription) error
	ListSubscriptions(ctx context.Context) ([]*Subscription, error)

	ResolveTenantByCustomer(ctx context.Context, customerID string) (string, bool, error)
	PutCustomerTenant(ctx context.Context, customerID, tenantID string) error
	ListCustomerTenants(ctx context.Context) (map[string]string, error)

	HasProcessedEvent(ctx context.Context, eventID string) (bool, error)
	MarkProcessedEvent(ctx context.Context, eventID string, at time.Time) error
	ListProcessedEvents(ctx context.Context) (map[string]time.Time, error)

	PutInquiry(ctx context.Context, inq *EnterpriseInquiry) error
	ListInquiries(ctx context.Context) ([]*EnterpriseInquiry, error)
}

const (
	billingStoreSubscriptions   = "governance_subscriptions"
	billingStoreCustomerTenants = "governance_customer_tenants"
	billingStoreProcessedEvents = "governance_processed_events"
	billingStoreInquiries       = "governance_inquiries"
)

// JoltrinBillingStore is a BillingStore backed by joltrin's own embedded
// B-Tree engine. Using the engine this repo ships avoids standing up a
// separate database (Redis, Postgres, etc.) just to hold a few KB of
// subscription and idempotency records.
type JoltrinBillingStore struct {
	dbOpts sop.DatabaseOptions
}

// NewJoltrinBillingStore returns a BillingStore rooted at dataPath, in
// Standalone mode (single writer). This intentionally does not enable
// Redis-backed distributed locking: the deployment this backs runs the
// Container App pinned to a single replica, so a single-process embedded
// store is sufficient and keeps infrastructure cost at zero for this piece.
func NewJoltrinBillingStore(dataPath string) *JoltrinBillingStore {
	return &JoltrinBillingStore{
		dbOpts: sop.DatabaseOptions{
			Type:          sop.Standalone,
			StoresFolders: []string{dataPath},
		},
	}
}

func (s *JoltrinBillingStore) GetSubscription(ctx context.Context, tenantID string) (*Subscription, bool, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return nil, false, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, Subscription](ctx, s.dbOpts, billingStoreSubscriptions, trans, nil)
	if err != nil {
		return nil, false, fmt.Errorf("governance/billing: open subscriptions store: %w", err)
	}

	found, err := store.Find(ctx, tenantID, true)
	if err != nil || !found {
		return nil, false, err
	}
	sub, err := store.GetCurrentValue(ctx)
	if err != nil {
		return nil, false, err
	}
	return &sub, true, nil
}

func (s *JoltrinBillingStore) PutSubscription(ctx context.Context, sub *Subscription) error {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("governance/billing: begin write transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, Subscription](ctx, s.dbOpts, billingStoreSubscriptions, trans, nil)
	if err != nil {
		return fmt.Errorf("governance/billing: open subscriptions store: %w", err)
	}
	if _, err := store.Upsert(ctx, sub.TenantID, *sub); err != nil {
		return fmt.Errorf("governance/billing: upsert subscription: %w", err)
	}
	return trans.Commit(ctx)
}

func (s *JoltrinBillingStore) ListSubscriptions(ctx context.Context) ([]*Subscription, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, Subscription](ctx, s.dbOpts, billingStoreSubscriptions, trans, nil)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: open subscriptions store: %w", err)
	}

	var out []*Subscription
	ok, err := store.First(ctx)
	if err != nil {
		return nil, err
	}
	for ok {
		v, err := store.GetCurrentValue(ctx)
		if err != nil {
			return nil, err
		}
		item := v
		out = append(out, &item)
		if ok, err = store.Next(ctx); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *JoltrinBillingStore) ResolveTenantByCustomer(ctx context.Context, customerID string) (string, bool, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return "", false, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, string](ctx, s.dbOpts, billingStoreCustomerTenants, trans, nil)
	if err != nil {
		return "", false, fmt.Errorf("governance/billing: open customer-tenant store: %w", err)
	}

	found, err := store.Find(ctx, customerID, true)
	if err != nil || !found {
		return "", false, err
	}
	tenantID, err := store.GetCurrentValue(ctx)
	if err != nil {
		return "", false, err
	}
	return tenantID, true, nil
}

func (s *JoltrinBillingStore) PutCustomerTenant(ctx context.Context, customerID, tenantID string) error {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("governance/billing: begin write transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, string](ctx, s.dbOpts, billingStoreCustomerTenants, trans, nil)
	if err != nil {
		return fmt.Errorf("governance/billing: open customer-tenant store: %w", err)
	}
	if _, err := store.Upsert(ctx, customerID, tenantID); err != nil {
		return fmt.Errorf("governance/billing: upsert customer-tenant mapping: %w", err)
	}
	return trans.Commit(ctx)
}

func (s *JoltrinBillingStore) ListCustomerTenants(ctx context.Context) (map[string]string, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, string](ctx, s.dbOpts, billingStoreCustomerTenants, trans, nil)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: open customer-tenant store: %w", err)
	}

	out := make(map[string]string)
	ok, err := store.First(ctx)
	if err != nil {
		return nil, err
	}
	for ok {
		k := store.GetCurrentKey()
		v, err := store.GetCurrentValue(ctx)
		if err != nil {
			return nil, err
		}
		out[k.Key] = v
		if ok, err = store.Next(ctx); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *JoltrinBillingStore) HasProcessedEvent(ctx context.Context, eventID string) (bool, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return false, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, time.Time](ctx, s.dbOpts, billingStoreProcessedEvents, trans, nil)
	if err != nil {
		return false, fmt.Errorf("governance/billing: open processed-events store: %w", err)
	}
	found, err := store.Find(ctx, eventID, true)
	return found, err
}

func (s *JoltrinBillingStore) MarkProcessedEvent(ctx context.Context, eventID string, at time.Time) error {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("governance/billing: begin write transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, time.Time](ctx, s.dbOpts, billingStoreProcessedEvents, trans, nil)
	if err != nil {
		return fmt.Errorf("governance/billing: open processed-events store: %w", err)
	}
	if _, err := store.Upsert(ctx, eventID, at); err != nil {
		return fmt.Errorf("governance/billing: upsert processed event: %w", err)
	}
	return trans.Commit(ctx)
}

func (s *JoltrinBillingStore) ListProcessedEvents(ctx context.Context) (map[string]time.Time, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, time.Time](ctx, s.dbOpts, billingStoreProcessedEvents, trans, nil)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: open processed-events store: %w", err)
	}

	out := make(map[string]time.Time)
	ok, err := store.First(ctx)
	if err != nil {
		return nil, err
	}
	for ok {
		k := store.GetCurrentKey()
		v, err := store.GetCurrentValue(ctx)
		if err != nil {
			return nil, err
		}
		out[k.Key] = v
		if ok, err = store.Next(ctx); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *JoltrinBillingStore) PutInquiry(ctx context.Context, inq *EnterpriseInquiry) error {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("governance/billing: begin write transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, EnterpriseInquiry](ctx, s.dbOpts, billingStoreInquiries, trans, nil)
	if err != nil {
		return fmt.Errorf("governance/billing: open inquiries store: %w", err)
	}
	if _, err := store.Upsert(ctx, inq.ID, *inq); err != nil {
		return fmt.Errorf("governance/billing: upsert inquiry: %w", err)
	}
	return trans.Commit(ctx)
}

func (s *JoltrinBillingStore) ListInquiries(ctx context.Context) ([]*EnterpriseInquiry, error) {
	trans, err := database.BeginTransaction(ctx, s.dbOpts, sop.ForReading)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: begin read transaction: %w", err)
	}
	defer trans.Rollback(ctx)

	store, err := database.NewBtree[string, EnterpriseInquiry](ctx, s.dbOpts, billingStoreInquiries, trans, nil)
	if err != nil {
		return nil, fmt.Errorf("governance/billing: open inquiries store: %w", err)
	}

	var out []*EnterpriseInquiry
	ok, err := store.First(ctx)
	if err != nil {
		return nil, err
	}
	for ok {
		v, err := store.GetCurrentValue(ctx)
		if err != nil {
			return nil, err
		}
		item := v
		out = append(out, &item)
		if ok, err = store.Next(ctx); err != nil {
			return nil, err
		}
	}
	return out, nil
}
