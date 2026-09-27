package common

// Consolidated from: managebtree_args_test.go, managebtree_error_test.go, managebtree_refetch_test.go
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func Test_ManageBtree_OpenNewBtree_Cases(t *testing.T) {
	ctx := context.Background()
	cmp := func(a, b int) int {
		if a < b {
			return -1
		} else if a > b {
			return 1
		}
		return 0
	}
	type preFn func(t *testing.T, ctx context.Context, trans sop.Transaction)
	cases := []struct {
		name        string
		op          string
		begin       bool
		storeName   string
		so          sop.StoreOptions
		pre         preFn
		expectErr   bool
		expectEnded bool
	}{
		{name: "open_nil_transaction_error", op: "open", begin: false, storeName: "", pre: nil, expectErr: true},
		{name: "open_not_begun_error", op: "open", begin: false, storeName: "s1", pre: nil, expectErr: true},
		{name: "open_empty_name_error", op: "open", begin: true, storeName: "", pre: nil, expectErr: true},
		{name: "open_nonexistent_store_rolls_back", op: "open", begin: true, storeName: "does_not_exist", pre: nil, expectErr: true, expectEnded: true},
		{name: "new_not_begun_error", op: "new", begin: false, so: sop.StoreOptions{Name: "x"}, pre: nil, expectErr: true},
		{name: "new_empty_name_error", op: "new", begin: true, so: sop.StoreOptions{Name: ""}, pre: nil, expectErr: true},
		{name: "new_with_ttl_path_success", op: "new", begin: true, so: sop.StoreOptions{Name: "ttl_store", SlotLength: 2, CacheConfig: &sop.StoreCacheConfig{StoreInfoCacheDuration: time.Minute, IsStoreInfoCacheTTL: true}}},
		{name: "new_incompatible_config_rolls_back", op: "new", begin: true, so: sop.StoreOptions{Name: "store_incompat", SlotLength: 6}, pre: func(t *testing.T, ctx context.Context, trans sop.Transaction) {
			t.Helper()
			t2 := trans.GetPhasedTransaction().(*Transaction)
			_ = t2.StoreRepository.Add(ctx, sop.StoreInfo{Name: "store_incompat", SlotLength: 4})
		}, expectErr: true, expectEnded: true},
		{name: "new_duplicate_in_transaction_success", op: "new-dup", begin: true, so: sop.StoreOptions{Name: "dup_store", SlotLength: 2}, expectErr: false, expectEnded: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.op == "open" && !tc.begin && tc.storeName == "" && tc.expectErr {
				if _, err := OpenBtree[int, int](ctx, "", nil, nil); err == nil {
					t.Fatalf("expected error for nil transaction")
				}
				return
			}
			trans, _ := newMockTransaction(t, sop.ForWriting, -1)
			if tc.begin {
				_ = trans.Begin(ctx)
			} else if trans.HasBegun() {
				_ = trans.Close()
			}
			if tc.pre != nil {
				tc.pre(t, ctx, trans)
			}
			switch tc.op {
			case "open":
				_, err := OpenBtree[int, int](ctx, tc.storeName, trans, cmp)
				if tc.expectErr {
					if err == nil {
						t.Fatalf("expected error, got nil")
					}
				} else if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case "new":
				_, err := NewBtree[int, int](ctx, tc.so, trans, cmp)
				if tc.expectErr {
					if err == nil {
						t.Fatalf("expected error, got nil")
					}
				} else if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case "new-dup":
				if _, err := NewBtree[int, int](ctx, tc.so, trans, cmp); err != nil {
					t.Fatalf("unexpected first NewBtree error: %v", err)
				}
				if _, err := NewBtree[int, int](ctx, tc.so, trans, cmp); err != nil {
					t.Fatalf("unexpected error on second NewBtree: %v", err)
				}
			}
			if tc.expectEnded && trans.HasBegun() {
				t.Fatalf("expected transaction to be ended after error/rollback")
			}
			_ = trans.Close()
		})
	}
}

type errStoreRepo struct{ e error }

func (e *errStoreRepo) Add(ctx context.Context, stores ...sop.StoreInfo) error { return nil }
func (e *errStoreRepo) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	return nil, nil
}
func (e *errStoreRepo) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return nil, nil
}
func (e *errStoreRepo) GetAll(ctx context.Context) ([]string, error) { return nil, nil }
func (e *errStoreRepo) GetWithTTL(ctx context.Context, isTTL bool, d time.Duration, names ...string) ([]sop.StoreInfo, error) {
	return nil, e.e
}
func (e *errStoreRepo) Remove(ctx context.Context, names ...string) error               { return nil }
func (e *errStoreRepo) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error { return nil }

func Test_RefetchAndMerge_StoreInfoFetchError(t *testing.T) {
	ctx := context.Background()
	so := sop.StoreOptions{Name: "rfm_storeinfo_err", SlotLength: 8, IsValueDataInNodeSegment: true}
	ns := sop.NewStoreInfo(so)
	si := StoreInterface[PersonKey, Person]{}
	tr := &Transaction{registry: mocks.NewMockRegistry(false), l2Cache: mocks.NewMockClient(), l1Cache: cache.GetGlobalL1Cache(mocks.NewMockClient()), blobStore: mocks.NewMockBlobStore(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), false), StoreRepository: &errStoreRepo{e: errors.New("boom")}}
	si.ItemActionTracker = newItemActionTracker[PersonKey, Person](ns, tr.l2Cache, tr.blobStore, tr.logger)
	nrw := newNodeRepository[PersonKey, Person](tr, ns)
	si.NodeRepository = nrw
	si.backendNodeRepository = nrw.nodeRepositoryBackend
	b3, err := btree.New(ns, &si.StoreInterface, Compare)
	if err != nil {
		t.Fatal(err)
	}
	closure := refetchAndMergeClosure(&si, b3, tr.StoreRepository)
	if err := closure(ctx); err == nil {
		t.Fatalf("expected error from GetWithTTL, got nil")
	}
}

func mustNewBtree[TK btree.Ordered, TV any](t *testing.T, si sop.StoreOptions, comparer btree.ComparerFunc[TK]) (sop.Transaction, btree.BtreeInterface[TK, TV]) {
	t.Helper()
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatalf("newMockTransaction error: %v", err)
	}
	trans.Begin(ctx)
	b3, err := NewBtree[TK, TV](context.Background(), si, trans, comparer)
	if err != nil {
		t.Fatalf("NewBtree error: %v", err)
	}
	return trans, b3
}
func seedStoreWithOne(t *testing.T, name string, inNode bool, key PersonKey, value Person) {
	t.Helper()
	si := sop.StoreOptions{Name: name, SlotLength: 8, IsUnique: false, IsValueDataInNodeSegment: inNode, LeafLoadBalancing: false}
	trans, b3 := mustNewBtree[PersonKey, Person](t, si, Compare)
	ok, err := b3.Add(context.Background(), key, value)
	if !ok || err != nil {
		t.Fatalf("seed Add failed: ok=%v err=%v", ok, err)
	}
	trans.Commit(context.Background())
}

func Test_AreFetchedItemsIntact_Cases(t *testing.T) {
	ctx := context.Background()
	reg := mocks.NewMockRegistry(false)
	nr := &nodeRepositoryBackend{transaction: &Transaction{registry: reg}}
	if ok, err := nr.areFetchedItemsIntact(ctx, nil); err != nil || !ok {
		t.Fatalf("expected ok for empty input, got ok=%v err=%v", ok, err)
	}
	si := sop.NewStoreInfo(sop.StoreOptions{Name: "afi", SlotLength: 8, IsValueDataInNodeSegment: true})
	n := &btree.Node[PersonKey, Person]{ID: sop.NewUUID(), Version: 1}
	nodes := []sop.Tuple[*sop.StoreInfo, []interface{}]{{First: si, Second: []interface{}{n}}}
	h := sop.NewHandle(n.ID)
	h.Version = 1
	reg.(*mocks.Mock_vid_registry).Lookup[n.ID] = h
	if ok, err := nr.areFetchedItemsIntact(ctx, nodes); err != nil || !ok {
		t.Fatalf("expected ok when versions match, got ok=%v err=%v", ok, err)
	}
	h.Version = 2
	reg.(*mocks.Mock_vid_registry).Lookup[n.ID] = h
	if ok, err := nr.areFetchedItemsIntact(ctx, nodes); err != nil || ok {
		t.Fatalf("expected not ok on version change, got ok=%v err=%v", ok, err)
	}
}

func Test_RefetchAndMerge_Paths(t *testing.T) {
	ctx := context.Background()
	type tc struct {
		name   string
		inNode bool
		action string
	}
	cases := []tc{{"add_out_of_node", false, "add"}, {"add_in_node", true, "add"}, {"get_existing", true, "get"}, {"remove_existing", true, "remove"}, {"update_in_node", true, "update"}, {"update_out_of_node", false, "update"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storeName := "rfm_" + c.name
			pk, p := newPerson("a", storeName, "x", "e@x", "p")
			if c.action != "add" {
				seedStoreWithOne(t, storeName, c.inNode, pk, p)
			}
			si := sop.StoreOptions{Name: storeName, SlotLength: 8, IsUnique: false, IsValueDataInNodeSegment: c.inNode, LeafLoadBalancing: false}
			trans, b3 := mustNewBtree[PersonKey, Person](t, si, Compare)
			switch c.action {
			case "add":
				ok, err := b3.Add(ctx, pk, p)
				if !ok || err != nil {
					t.Fatalf("Add failed: ok=%v err=%v", ok, err)
				}
			case "get":
				ok, err := b3.Find(ctx, pk, false)
				if !ok || err != nil {
					t.Fatalf("Find failed: ok=%v err=%v", ok, err)
				}
				if _, err := b3.GetCurrentValue(ctx); err != nil {
					t.Fatalf("GetCurrentValue failed: %v", err)
				}
			case "remove":
				if _, err := b3.Remove(ctx, pk); err != nil {
					t.Fatalf("Remove failed: %v", err)
				}
			case "update":
				p2 := p
				p2.Email = "changed@x"
				if _, err := b3.Update(ctx, pk, p2); err != nil {
					t.Fatalf("Update failed: %v", err)
				}
			}
			if err := trans.GetPhasedTransaction().(*Transaction).refetchAndMergeModifications(ctx); err != nil {
				t.Fatalf("refetchAndMergeModifications returned error: %v", err)
			}
			trans.Rollback(ctx)
		})
	}
}

func Test_RefetchAndMerge_Conflict_VersionMismatch(t *testing.T) {
	ctx := context.Background()
	storeName := "rfm_conflict"
	pk, p := newPerson("bob", "conflict", "m", "b@x", "1")
	seedStoreWithOne(t, storeName, true, pk, p)
	si := sop.StoreOptions{Name: storeName, SlotLength: 8, IsValueDataInNodeSegment: true}
	t1, b3 := mustNewBtree[PersonKey, Person](t, si, Compare)
	ok, err := b3.Find(ctx, pk, false)
	if !ok || err != nil {
		t.Fatalf("T1 Find failed: ok=%v err=%v", ok, err)
	}
	if _, err := b3.GetCurrentValue(ctx); err != nil {
		t.Fatalf("T1 GetCurrentValue failed: %v", err)
	}
	t2, b3w := mustNewBtree[PersonKey, Person](t, si, Compare)
	p2 := p
	p2.Email = "new@x"
	if _, err := b3w.Update(ctx, pk, p2); err != nil {
		t.Fatalf("T2 Update failed: %v", err)
	}
	t2.Commit(ctx)
	if err := t1.GetPhasedTransaction().(*Transaction).refetchAndMergeModifications(ctx); err == nil {
		t.Fatalf("expected version conflict error, got nil")
	}
	t1.Rollback(ctx)
}
