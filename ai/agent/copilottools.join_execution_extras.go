package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sharedcode/joltrin/jsondb"
)

func (jc *JoinRightCursor) GetIndexSpecs() map[string]*jsondb.IndexSpecification {
	specs := make(map[string]*jsondb.IndexSpecification)

	// Get spec from Left
	if provider, ok := jc.left.(SpecProvider); ok {
		leftSpecs := provider.GetIndexSpecs()
		for k, v := range leftSpecs {
			specs[k] = v
		}
	}

	// Get spec from Right
	info := jc.right.GetStoreInfo()
	if info.MapKeyIndexSpecification != "" {
		var spec jsondb.IndexSpecification
		if err := json.Unmarshal([]byte(info.MapKeyIndexSpecification), &spec); err == nil {
			specs["right_key"] = &spec
		}
	}
	return specs
}

// RightOuterJoinStoreCursor implements Right Outer Join where the Right side is a Store.
// It iterates the Right Store (Driver) and looks up matches in the Left List (Lookup).
type RightOuterJoinStoreCursor struct {
	rightStore jsondb.StoreAccessor
	leftCursor ScriptCursor   // Replaces leftList for streaming builds
	leftList   []any          // Retained for fallback or small lists
	on         map[string]any // {RightField: LeftField} (Inverted ON)
	ctx        context.Context
	engine     *ScriptEngine // Added to support Temp Store creation
	rightAlias string        // Alias for Right Store items
	leftAlias  string        // Alias for Left Store items

	// Option to suppress matches (producing Anti-Join behavior: only unmatched Right items)
	suppressMatches bool

	// Internal State
	rightIter      bool
	rightKey       any
	rightVal       any
	leftIndex      map[string][]any
	tempStore      jsondb.StoreAccessor // Helper store for spilled items
	spillThreshold int                  // Configurable usage limit
	indexed        bool

	// Current Matching State
	matches  []any
	matchIdx int

	// Closed state
	closed bool

	// Caching
	sortedKeys []string
}

func (c *RightOuterJoinStoreCursor) Close() error {
	c.closed = true
	return nil
}

func (c *RightOuterJoinStoreCursor) Next(ctx context.Context) (any, bool, error) {
	if c.closed {
		return nil, false, nil
	}

	// 1. Init Index & Keys
	if !c.indexed {
		c.sortedKeys = make([]string, 0, len(c.on))
		for k := range c.on {
			c.sortedKeys = append(c.sortedKeys, k)
		}
		sort.Strings(c.sortedKeys)

		c.leftIndex = make(map[string][]any)

		// Populate from Cursor if List is empty
		// Use configured spill threshold or default
		limit := c.spillThreshold
		if limit <= 0 {
			limit = 10000
		}

		count := 0
		var spillFailed bool

		if len(c.leftList) == 0 && c.leftCursor != nil {
			for {
				// Check context cancellation
				select {
				case <-ctx.Done():
					return nil, false, fmt.Errorf("context cancelled during right outer join indexing: %w", ctx.Err())
				default:
				}

				item, ok, err := c.leftCursor.Next(ctx)
				if err != nil {
					return nil, false, err
				}
				if !ok {
					break
				}

				count++
				key := c.generateKey(item, true)

				// Check for Spill
				if count > limit && c.tempStore == nil && c.engine != nil && !spillFailed {
					// Initialize Temp Store
					tsName := fmt.Sprintf("temp_join_%d", time.Now().UnixNano())
					// Attempt to open store with create flag
					ts, err := c.engine.OpenStore(c.ctx, map[string]any{"name": tsName, "create": true})
					if err == nil {
						c.tempStore = ts
					} else {
						spillFailed = true
					}
				}

				if c.tempStore != nil {
					// Add to Temp Store
					// We allow duplicates in the store, so we just add (key, item).
					if _, err := c.tempStore.Add(c.ctx, key, item); err != nil {
						return nil, false, err
					}
				} else {
					// Add to Memory
					c.leftIndex[key] = append(c.leftIndex[key], item)
				}
			}
		} else {
			// List provided directly
			for _, item := range c.leftList {
				key := c.generateKey(item, true)
				c.leftIndex[key] = append(c.leftIndex[key], item)
			}
		}
		c.indexed = true
	}

	// Loop until we return an item or finish
	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, false, fmt.Errorf("context cancelled during right outer join iteration: %w", ctx.Err())
		default:
		}

		// 1. If we have pending matches for the current Right item, emit them
		if c.matchIdx < len(c.matches) {
			lItem := c.matches[c.matchIdx]
			c.matchIdx++

			merged := c.merge(lItem, c.rightKey, c.rightVal)
			return merged, true, nil
		}

		// 2. Advance Right Store
		var err error
		if !c.rightIter {
			// First time
			var ok bool
			ok, err = c.rightStore.First(c.ctx)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil // Empty right store
			}
			c.rightIter = true
		} else {
			var ok bool
			ok, err = c.rightStore.Next(c.ctx)
			if err != nil {
				return nil, false, err
			}
			if !ok {
				return nil, false, nil // EOF
			}
		}

		// 3. Get Current Right Item
		k := c.rightStore.GetCurrentKey()
		v, err := c.rightStore.GetCurrentValue(c.ctx)
		if err != nil {
			return nil, false, err
		}

		c.rightKey = k
		c.rightVal = v

		// 4. Lookup in Left Index
		// Generate key from Right Item using RightFields
		// We flatten Right Item first to be consistent with getField extraction
		rFlat := renderItem(c.rightKey, c.rightVal, nil)
		lookupKey := c.generateKey(rFlat, false)

		memMatches := c.leftIndex[lookupKey]

		if c.tempStore == nil {
			c.matches = memMatches
		} else {
			// Hybrid: Memory + Spilled
			// Copy memory matches first to avoid mutating the map's slice
			c.matches = make([]any, len(memMatches))
			copy(c.matches, memMatches)

			// Scan Spilled Store
			// Find first match for the key
			ok, err := c.tempStore.FindOne(c.ctx, lookupKey, true)
			if err == nil && ok {
				for {
					// Check context cancellation
					select {
					case <-ctx.Done():
						return nil, false, fmt.Errorf("context cancelled during spilled store scan: %w", ctx.Err())
					default:
					}

					k := c.tempStore.GetCurrentKey()

					// Check if key still matches
					kStr, isStr := k.(string)
					if !isStr || kStr != lookupKey {
						// Moved past the key
						break
					}

					v, err := c.tempStore.GetCurrentValue(c.ctx)
					if err != nil {
						break
					}

					c.matches = append(c.matches, v)

					ok, err = c.tempStore.Next(c.ctx)
					if err != nil || !ok {
						break
					}
				}
			}
		}

		c.matchIdx = 0

		// Anti-Join / Full Join Support:
		// If we are suppressing matches (finding orphans only), and we found matches,
		// we skip this Right record entirely.
		if c.suppressMatches && len(c.matches) > 0 {
			c.matches = nil // Clear matches
			continue
		}

		// 5. If No Matches -> Emit Right Item (Left is Null)
		if len(c.matches) == 0 {
			merged := c.merge(nil, c.rightKey, c.rightVal)
			return merged, true, nil
		}

		// Loop continues to emit first match
	}
}

func (c *RightOuterJoinStoreCursor) generateKey(item any, isLeft bool) string {
	var sb strings.Builder
	for _, rKey := range c.sortedKeys {
		var val any
		if isLeft {
			lField := fmt.Sprintf("%v", c.on[rKey])
			val = getField(item, lField)
		} else {
			val = getField(item, rKey)
		}
		sb.WriteString(fmt.Sprintf("%v|", val))
	}
	return sb.String()
}

func (c *RightOuterJoinStoreCursor) merge(lItem any, rKey, rVal any) any {
	// Standard merge of two items (Left + Right)
	// Similar to JoinRightCursor.mergeResult but stripped down

	// Flatten Right
	rFlat := renderItem(rKey, rVal, nil)

	// Consolidate into Map
	newMap := make(map[string]any)
	var newKeys []string

	// 1. Add Left Fields (with Alias)
	if lItem != nil {
		var lMap map[string]any
		var lKeys []string

		if om, ok := lItem.(*OrderedMap); ok && om != nil {
			lMap = om.m
			lKeys = om.keys
		} else if om, ok := lItem.(OrderedMap); ok {
			lMap = om.m
			lKeys = om.keys
		} else if m, ok := lItem.(map[string]any); ok && m != nil {
			lMap = m
			for k := range m {
				lKeys = append(lKeys, k)
			}
			sort.Strings(lKeys)
		}

		for _, k := range lKeys {
			v := lMap[k]
			if c.leftAlias != "" {
				// Prevention of double prefixing:
				// If the key already starts with "alias.", assume it is already namespaced.
				// This happens because previous steps (like scan) might have auto-prefixed items.
				if strings.HasPrefix(k, c.leftAlias+".") {
					newMap[k] = v
					newKeys = append(newKeys, k)
				} else {
					key := c.leftAlias + "." + k
					newMap[key] = v
					newKeys = append(newKeys, key)
				}
			} else {
				// No alias -> Naked
				newMap[k] = v
				newKeys = append(newKeys, k)
			}
		}
	}

	// 2. Add Right Fields (Driver)
	var rMap map[string]any
	var rKeys []string

	if om, ok := rFlat.(*OrderedMap); ok {
		rMap = om.m
		rKeys = om.keys
	} else if m, ok := rFlat.(map[string]any); ok {
		rMap = m
		for k := range m {
			rKeys = append(rKeys, k)
		}
		sort.Strings(rKeys)
	}

	for _, k := range rKeys {
		v := rMap[k]
		if c.rightAlias != "" {
			key := c.rightAlias + "." + k
			newMap[key] = v
			newKeys = append(newKeys, key)
		} else {
			// No alias -> Naked
			newMap[k] = v
			newKeys = append(newKeys, k)
		}
	}

	return &OrderedMap{m: newMap, keys: newKeys, isImplicit: true}
}
