package agent

import (
	"context"
	"fmt"
	log "log/slog"
	"strings"

	"github.com/sharedcode/joltrin/jsondb"
)

// MaxConsecutiveRowsWithoutMatch is the safety limit for consecutive rows scanned
// without finding a match in filter or join operations. This prevents runaway scans
// from malformed queries while still allowing unlimited streaming of matching data.
const MaxConsecutiveRowsWithoutMatch = 100000

// ScriptCursor represents a streaming iterator for script operations.
type ScriptCursor interface {
	Next(ctx context.Context) (any, bool, error)
	Close() error
}

// OrderedFieldsProvider allows cursors to expose the list of fields in order.
type OrderedFieldsProvider interface {
	GetOrderedFields() []string
}

// SpecProvider allows cursors to expose IndexSpecifications for field ordering.
type SpecProvider interface {
	GetIndexSpecs() map[string]*jsondb.IndexSpecification
}

// DeferredCleanupCursor wraps a ScriptCursor and executes deferred functions on Close.
type DeferredCleanupCursor struct {
	source  ScriptCursor
	cleanup []func(context.Context, *ScriptEngine) error
	ctx     context.Context
	engine  *ScriptEngine
	closed  bool
}

func (d *DeferredCleanupCursor) Next(ctx context.Context) (any, bool, error) {
	return d.source.Next(ctx)
}

func (d *DeferredCleanupCursor) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true

	err := d.source.Close()

	for i := len(d.cleanup) - 1; i >= 0; i-- {
		log.Debug("Executing deferred operation (from Cursor Close)", "index", i)
		if ferr := d.cleanup[i](d.ctx, d.engine); ferr != nil {
			log.Error("Deferred execution failed (from Cursor Close)", "error", ferr)

			if err == nil {
				err = ferr
			}
		}
	}
	return err
}

func (d *DeferredCleanupCursor) GetOrderedFields() []string {
	if p, ok := d.source.(OrderedFieldsProvider); ok {
		return p.GetOrderedFields()
	}
	return nil
}

func (d *DeferredCleanupCursor) GetIndexSpecs() map[string]*jsondb.IndexSpecification {
	if p, ok := d.source.(SpecProvider); ok {
		return p.GetIndexSpecs()
	}
	return nil
}

// StoreCursor wraps a StoreAccessor to provide a ScriptCursor.
type StoreCursor struct {
	store     jsondb.StoreAccessor
	storeName string // Add StoreName to support prefixing
	indexSpec *jsondb.IndexSpecification
	ctx       context.Context
	limit     int
	count     int
	filter    map[string]any
	engine    *ScriptEngine
	isDesc    bool
	prefix    any
	started   bool
	closed    bool
}

func (sc *StoreCursor) GetIndexSpecs() map[string]*jsondb.IndexSpecification {
	if sc.indexSpec != nil {
		return map[string]*jsondb.IndexSpecification{
			"key": sc.indexSpec,
		}
	}
	return nil
}

func (sc *StoreCursor) Next(ctx context.Context) (any, bool, error) {
	if sc.closed {
		return nil, false, nil
	}
	if sc.limit > 0 && sc.count >= sc.limit {
		return nil, false, nil
	}

	var ok bool
	var err error

	if !sc.started {

		sc.started = true

		k := sc.store.GetCurrentKey()
		if k == nil {
			return nil, false, nil
		}
		ok = true
	} else {
		if sc.isDesc {
			ok, err = sc.store.Previous(ctx)
		} else {
			ok, err = sc.store.Next(ctx)
		}
		if err != nil {
			return nil, false, err
		}
	}

	for ok {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, false, fmt.Errorf("context cancelled during cursor iteration: %w", ctx.Err())
		default:
		}

		k := sc.store.GetCurrentKey()
		// Use GetCurrentValueNoLock to avoid tracking items during scan
		v, err := sc.store.GetCurrentValueNoLock(ctx)
		if err != nil {
			return nil, false, err
		}

		if sc.prefix != nil {
			if kStr, isStr := k.(string); isStr {
				pStr := fmt.Sprintf("%v", sc.prefix)
				if !strings.HasPrefix(kStr, pStr) {
					return nil, false, nil
				}
			}
		}

		item := renderItem(k, v, nil)

		if sc.filter != nil {
			match, err := sc.engine.evaluateCondition(item, sc.filter)
			if err != nil {
				return nil, false, err
			}
			if !match {
				if sc.isDesc {
					ok, err = sc.store.Previous(ctx)
				} else {
					ok, err = sc.store.Next(ctx)
				}
				if err != nil {
					return nil, false, err
				}
				continue
			}
		}

		// Only register read lock for items we're actually emitting
		if err := sc.store.RLockCurrentItem(ctx); err != nil {
			return nil, false, fmt.Errorf("failed to lock emitted item: %w", err)
		}

		if m, ok := item.(map[string]any); ok && sc.storeName != "" {
			prefixed := make(map[string]any, len(m))
			for k, val := range m {

				prefixed[sc.storeName+"."+k] = val
			}
			item = prefixed
		}

		sc.count++
		return item, true, nil
	}

	return nil, false, nil
}

func (sc *StoreCursor) Close() error {
	sc.closed = true
	return nil
}

type JoinPlan struct {
	Strategy     int
	IndexFields  []string // Ordered list of fields in the Index
	PrefixFields []string // Fields from ON clause that match the Index Prefix
	IsComposite  bool     // True if the Store uses a Map Key (Composite)
	Ascending    bool     // True if the first prefix field is Ascending
}

// JoinRightCursor performs a streaming join with probing and scanning support.
// It replaces both JoinCursor (Lookup) and NestedLoopJoinCursor (Scan).
type JoinRightCursor struct {
	left      ScriptCursor
	right     jsondb.StoreAccessor
	joinType  string
	on        map[string]any
	ctx       context.Context
	engine    *ScriptEngine
	currentL  any
	matched   bool
	rightIter bool

	// Execution Plan
	plan      JoinPlan
	planReady bool

	// Dataset info for prefixing
	rightStoreName string
	leftStoreName  string

	// Legacy / Runtime State
	useFallback  bool  // optimization: materialization fallback
	fallbackList []any // fallback: in-memory list
	fallbackIdx  int
	closed       bool
	bloomFilter  *BloomFilter // Optimization: Bloom Filter for Right Store Keys

	// Safety limits
	rowsSinceLastMatch  int
	maxRowsWithoutMatch int // Safety: max consecutive rows without match before failing
	totalRowsScanned    int // For progress logging only

	// Index Seek State - tracks mid-scan continuation
	scanning bool // true when we're mid-scan of right store for current left row
}

func (jc *JoinRightCursor) Next(ctx context.Context) (any, bool, error) {
	if jc.closed {
		return nil, false, nil
	}
	return jc.NextOptimized(ctx)
}

func (jc *JoinRightCursor) Close() error {
	jc.closed = true
	return jc.left.Close()
}

// FilterCursor filters a stream.
type FilterCursor struct {
	source              ScriptCursor
	filter              any
	engine              *ScriptEngine
	closed              bool
	rowsSinceLastMatch  int
	maxRowsWithoutMatch int // Safety: max consecutive rows without a match before failing
	totalRowsScanned    int // For progress logging only
}

func (fc *FilterCursor) Next(ctx context.Context) (any, bool, error) {
	if fc.closed {
		return nil, false, nil
	}

	// Set default max consecutive rows without match if not configured
	if fc.maxRowsWithoutMatch == 0 {
		fc.maxRowsWithoutMatch = MaxConsecutiveRowsWithoutMatch
	}

	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, false, fmt.Errorf("context cancelled during filter iteration: %w", ctx.Err())
		default:
		}

		item, ok, err := fc.source.Next(ctx)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}

		fc.totalRowsScanned++
		fc.rowsSinceLastMatch++

		// Safety: Check if we've scanned too many consecutive rows without any matches
		if fc.rowsSinceLastMatch > fc.maxRowsWithoutMatch {
			return nil, false, fmt.Errorf("filter scanned %d consecutive rows with no matches (total %d rows) - possible incorrect filter or cartesian product", fc.rowsSinceLastMatch, fc.totalRowsScanned)
		}

		// Log progress every 10k consecutive non-matches to help diagnose issues
		if fc.rowsSinceLastMatch%10000 == 0 {
			log.Info("FilterCursor: Scanning large dataset without matches",
				"consecutive_non_matches", fc.rowsSinceLastMatch,
				"total_scanned", fc.totalRowsScanned,
				"filter", fc.filter)
		}

		match, err := fc.engine.evaluateCondition(item, fc.filter)
		if err != nil {
			return nil, false, fmt.Errorf("filter evaluation error after scanning %d rows: %w", fc.totalRowsScanned, err)
		}
		if match {
			// Reset counter on match - filter is working, allow unlimited streaming
			fc.rowsSinceLastMatch = 0
			log.Debug("FilterCursor match", "item", item, "total_rows_scanned", fc.totalRowsScanned)
			return item, true, nil
		}
		log.Debug("FilterCursor mismatch", "filter", fc.filter, "item", item)
	}
}

func (fc *FilterCursor) Close() error {
	fc.closed = true
	return fc.source.Close()
}

func (fc *FilterCursor) GetOrderedFields() []string {
	if provider, ok := fc.source.(OrderedFieldsProvider); ok {
		return provider.GetOrderedFields()
	}
	return nil
}

// ProjectCursor projects fields from a stream.
type ProjectCursor struct {
	source ScriptCursor
	fields []ProjectionField
	closed bool
}

func (pc *ProjectCursor) Next(ctx context.Context) (any, bool, error) {
	if pc.closed {
		return nil, false, nil
	}
	item, ok, err := pc.source.Next(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	result := renderItem(nil, item, pc.fields)
	log.Debug("ProjectCursor.Next", "item_in", item, "fields", pc.fields, "item_out", result)
	return result, true, nil
}

func (pc *ProjectCursor) Close() error {
	pc.closed = true
	return pc.source.Close()
}

func (pc *ProjectCursor) GetOrderedFields() []string {
	fields := make([]string, len(pc.fields))
	for i, f := range pc.fields {
		fields[i] = f.Dst
	}
	return fields
}

// LimitCursor limits a stream.
type LimitCursor struct {
	source ScriptCursor
	limit  int
	count  int
	closed bool
}

func (lc *LimitCursor) GetIndexSpecs() map[string]*jsondb.IndexSpecification {
	if provider, ok := lc.source.(SpecProvider); ok {
		return provider.GetIndexSpecs()
	}
	return nil
}

func (lc *LimitCursor) GetOrderedFields() []string {
	if provider, ok := lc.source.(OrderedFieldsProvider); ok {
		return provider.GetOrderedFields()
	}
	return nil
}

func (lc *LimitCursor) Next(ctx context.Context) (any, bool, error) {
	if lc.closed {
		return nil, false, nil
	}
	if lc.count >= lc.limit {
		return nil, false, nil
	}
	item, ok, err := lc.source.Next(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	lc.count++
	return item, true, nil
}

func (lc *LimitCursor) Close() error {
	lc.closed = true
	return lc.source.Close()
}

// ListCursor wraps a slice of maps.
type ListCursor struct {
	items []any
	index int
}

func (lc *ListCursor) Next(ctx context.Context) (any, bool, error) {
	if lc.index >= len(lc.items) {
		return nil, false, nil
	}
	item := lc.items[lc.index]
	lc.index++
	return item, true, nil
}

func (lc *ListCursor) Close() error {
	return nil
}

// MultiCursor chains multiple cursors.
type MultiCursor struct {
	cursors []ScriptCursor
	current int
}

func (mc *MultiCursor) Next(ctx context.Context) (any, bool, error) {
	for mc.current < len(mc.cursors) {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, false, fmt.Errorf("context cancelled during multi-cursor iteration: %w", ctx.Err())
		default:
		}

		item, ok, err := mc.cursors[mc.current].Next(ctx)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return item, true, nil
		}

		mc.current++
	}
	return nil, false, nil
}

func (mc *MultiCursor) Close() error {
	var firstErr error
	for _, c := range mc.cursors {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
