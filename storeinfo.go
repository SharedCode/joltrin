package sop

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// MaxSlotLength is the maximum number of items a node can accommodate.
// Enforced both when a StoreInfo is first created (NewStoreInfo) and again
// at the point a node is allocated (btree.getSlotLength), since a StoreInfo
// loaded from persisted storage doesn't go through NewStoreInfo and could
// otherwise carry an unbounded or corrupted value straight into a make().
const MaxSlotLength = 20000

// StoreInfo describes a B-Tree store configuration and runtime state persisted in the backend.
type StoreInfo struct {
	// Name is the short store name.
	Name string `json:"name" minLength:"1" maxLength:"128"`
	// SlotLength is the number of items per node.
	SlotLength int `json:"slot_length" min:"2" max:"20000"`
	// IsUnique enforces uniqueness on the key of key/value items.
	IsUnique bool `json:"is_unique"`
	// Description optionally describes the store.
	Description string `json:"description" maxLength:"1000"`
	// RegistryTable is the registry table name.
	RegistryTable string `json:"registry_table" minLength:"1" maxLength:"140"`
	// BlobTable defines the target Erasure Coding (EC) configuration to use.
	// The Database Options contain an EC configuration map keyed by a name, and
	// this field's value is used to look up a match. If no matching entry is found,
	// it falls back to the default EC config (which uses an empty string key "").
	BlobTable string `json:"blob_table" minLength:"1" maxLength:"300"`
	// RootNodeID is the root B-Tree node identifier.
	RootNodeID UUID `json:"root_node_id"`
	// Count is the total number of items persisted.
	Count int64 `json:"count"`
	// CountDelta is used internally to reconcile Count updates; it should not be persisted.
	CountDelta int64 `json:"-"`
	// Timestamp is the add/update time in milliseconds.
	Timestamp int64 `json:"timestamp"`
	// IsValueDataInNodeSegment stores the Value within the node segment when true.
	IsValueDataInNodeSegment bool `json:"is_value_data_in_node_segment"`
	// IsValueDataActivelyPersisted persists Value separately on Add/Update when true.
	IsValueDataActivelyPersisted bool `json:"is_value_data_actively_persisted"`
	// IsValueDataGloballyCached enables Redis caching of Value data when true.
	IsValueDataGloballyCached bool `json:"is_value_data_globally_cached"`
	// LeafLoadBalancing enables distribution to sibling nodes when capacity allows.
	LeafLoadBalancing bool `json:"leaf_load_balancing"`
	// CacheConfig overrides global cache settings per store.
	CacheConfig StoreCacheConfig `json:"cache_config"`

	// MapKeyIndexSpecification contains a CEL or index specification used by the comparer.
	MapKeyIndexSpecification string `json:"mapkey_index_spec"`

	// CELexpression specifies the CEL expression used as comparer for keys.
	CELexpression string `json:"cel_expression,omitempty"`

	// IsPrimitiveKey hints the Python binding which JSON B-Tree to instantiate on open.
	// This is an internal feature and only needed to be managed by code when using (dynamic typed) languages like Python.
	IsPrimitiveKey bool `json:"is_primitive_key"`

	// Relations describes foreign key relationships to other stores.
	Relations []Relation `json:"relations,omitempty"`

	// Schema stores field types without prefixes for LLM correlation with Relations.
	// Format: {"key": "string", "first_name": "string", "age": "number"}
	Schema map[string]string `json:"schema,omitempty"`

	// KeyFields lists the field names that are part of the Key.
	// Example: ["key"] for primitive keys, ["id", "timestamp"] for composite keys
	KeyFields []string `json:"key_fields,omitempty"`

	// ValueFields lists the field names that are part of the Value.
	// Example: ["first_name", "age", "email"] for the value object
	ValueFields []string `json:"value_fields,omitempty"`

	// CustomData stores optional arbitrary configuration for the store.
	// It is intentionally flexible for integration-specific extensions.
	CustomData map[string]any `json:"custom_data,omitempty"`

	// For internal use only. Code can use this as hint.
	NeedsMetaDataSave bool `json:"-"`

	// Version allows versioning of the store info payload for future upgrades.
	Version string `json:"version,omitempty"`
}

// Interpolates back into ValueDataSize
func (si *StoreInfo) ValueDataSize() ValueDataSize {
	dataSize := MediumData
	if si.IsValueDataActivelyPersisted {
		dataSize = BigData
	} else if si.IsValueDataInNodeSegment {
		dataSize = SmallData
	}
	return dataSize
}

// GetCustomData returns the value for the given key from CustomData.
func (si *StoreInfo) GetCustomData(key string) (any, bool) {
	if si == nil || si.CustomData == nil {
		return nil, false
	}
	val, ok := si.CustomData[key]
	return val, ok
}

// SetCustomData stores a value under the given key in CustomData.
func (si *StoreInfo) SetCustomData(key string, value any) {
	if si == nil {
		return
	}
	if si.CustomData == nil {
		si.CustomData = make(map[string]any)
	}
	si.CustomData[key] = value
}

// DeleteCustomData removes a value for the given key from CustomData.
func (si *StoreInfo) DeleteCustomData(key string) bool {
	if si == nil || si.CustomData == nil {
		return false
	}
	_, ok := si.CustomData[key]
	if !ok {
		return false
	}
	delete(si.CustomData, key)
	return true
}

// GetCustomDataMap returns a copy of the CustomData map.
func (si *StoreInfo) GetCustomDataMap() map[string]any {
	if si == nil || si.CustomData == nil {
		return nil
	}
	out := make(map[string]any, len(si.CustomData))
	for k, v := range si.CustomData {
		out[k] = v
	}
	return out
}

// SetCustomDataMap replaces the full CustomData map with a copy.
func (si *StoreInfo) SetCustomDataMap(data map[string]any) {
	if si == nil {
		return
	}
	if len(data) == 0 {
		si.CustomData = nil
		return
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	si.CustomData = out
}

// Relation describes a foreign key relationship to another store.
type Relation struct {
	SourceFields []string `json:"source_fields"`
	TargetStore  string   `json:"target_store"`
	TargetFields []string `json:"target_fields"`
}

// StoreCacheConfig declares cache durations and TTL flags for store artifacts.
type StoreCacheConfig struct {
	// RegistryCacheDuration controls caching for registry objects.
	RegistryCacheDuration time.Duration `json:"registry_cache_duration"`
	// IsRegistryCacheTTL enables sliding TTL for registry cache.
	IsRegistryCacheTTL bool `json:"is_registry_cache_ttl"`
	// NodeCacheDuration controls caching for nodes.
	NodeCacheDuration time.Duration `json:"node_cache_duration"`
	// IsNodeCacheTTL enables sliding TTL for node cache.
	IsNodeCacheTTL bool `json:"is_node_cache_ttl"`
	// ValueDataCacheDuration controls caching for the item Value part when globally cached.
	ValueDataCacheDuration time.Duration `json:"value_data_cache_duration"`
	// IsValueDataCacheTTL enables sliding TTL for value data cache.
	IsValueDataCacheTTL bool `json:"is_value_data_cache_ttl"`
	// StoreInfoCacheDuration controls caching for StoreInfo records.
	StoreInfoCacheDuration time.Duration `json:"store_info_cache_duration"`
	// IsStoreInfoCacheTTL enables sliding TTL for store info cache.
	IsStoreInfoCacheTTL bool `json:"is_store_info_cache_ttl"`
}

const minCacheDuration = time.Duration(5 * time.Minute)

// NewStoreCacheConfig returns a StoreCacheConfig with uniform cache durations and TTL settings applied.
// If cacheDuration is between 1ns and 5 minutes, it will be clamped to 5 minutes. TTL is disabled when duration is zero.
func NewStoreCacheConfig(cacheDuration time.Duration, isCacheTTL bool) *StoreCacheConfig {
	if cacheDuration > 0 && cacheDuration < minCacheDuration {
		cacheDuration = minCacheDuration
	}
	if cacheDuration == 0 && isCacheTTL {
		isCacheTTL = false
	}
	return &StoreCacheConfig{
		RegistryCacheDuration:  cacheDuration,
		IsRegistryCacheTTL:     isCacheTTL,
		NodeCacheDuration:      cacheDuration,
		IsNodeCacheTTL:         isCacheTTL,
		StoreInfoCacheDuration: cacheDuration,
		IsStoreInfoCacheTTL:    isCacheTTL,
		ValueDataCacheDuration: cacheDuration,
		IsValueDataCacheTTL:    isCacheTTL,
	}
}

// enforceMinimumRule applies SOP minimum caching policy to ensure orchestrations remain effective.
func (scc *StoreCacheConfig) enforceMinimumRule() {
	if scc.NodeCacheDuration > 0 && scc.NodeCacheDuration < minCacheDuration {
		scc.NodeCacheDuration = minCacheDuration
	}
	if scc.NodeCacheDuration == 0 && scc.IsNodeCacheTTL {
		scc.IsNodeCacheTTL = false
	}
	// scc.NodeCacheDuration defaults to -1, meaning no caching.
	if scc.NodeCacheDuration == 0 {
		scc.NodeCacheDuration = -1
	}

	if scc.RegistryCacheDuration > 0 && scc.RegistryCacheDuration < minCacheDuration {
		scc.RegistryCacheDuration = time.Duration(10 * time.Minute)
	}
	if scc.RegistryCacheDuration == 0 && scc.IsRegistryCacheTTL {
		scc.IsRegistryCacheTTL = false
	}
	if scc.RegistryCacheDuration == 0 {
		// Registry cache duration needs to be decent, 15 mins or 10 mins. It has to minimally last
		// entire transaction commit period as Registry entries are used for implementing core engine
		// functionalities like conflict detection & btree contents' auto-merge.
		scc.RegistryCacheDuration = time.Duration(10 * time.Minute)
	}

	if scc.StoreInfoCacheDuration > 0 && scc.StoreInfoCacheDuration < minCacheDuration {
		scc.StoreInfoCacheDuration = minCacheDuration
	}
	if scc.StoreInfoCacheDuration == 0 && scc.IsStoreInfoCacheTTL {
		scc.IsStoreInfoCacheTTL = false
	}
	if scc.StoreInfoCacheDuration == 0 {
		scc.StoreInfoCacheDuration = minCacheDuration
	}

	// Value Data can be set to minimum.
	if scc.ValueDataCacheDuration <= 0 && scc.IsValueDataCacheTTL {
		scc.IsValueDataCacheTTL = false
	}
	if scc.ValueDataCacheDuration == 0 {
		scc.ValueDataCacheDuration = minCacheDuration
	}
}

// NewStoreInfo creates and normalizes a StoreInfo based on StoreOptions, applying default naming and cache policy.
func NewStoreInfo(si StoreOptions) *StoreInfo {
	// Default Slot length to 2000, if not specified.
	if si.SlotLength <= 0 {
		si.SlotLength = 2000
	}

	// Only even numbered slot lengths are allowed as we reduced scenarios to simplify logic.
	if si.SlotLength%2 != 0 {
		si.SlotLength--
	}
	// Minimum slot length is 2.
	if si.SlotLength < 2 {
		si.SlotLength = 2
	}

	registryTableName := si.Name
	blobTableName := si.Name

	if !si.DisableRegistryStoreFormatting {
		// auto generate table names based off of store name.
		registryTableName = FormatRegistryTable(si.Name)
	}
	if !si.DisableBlobStoreFormatting {
		blobTableName = fmt.Sprintf("%s_b", si.Name)
		if si.BlobStoreBaseFolderPath != "" {
			// Append the store name as suffix so blob folders will be separated from one another, if not yet.
			if !strings.HasSuffix(si.BlobStoreBaseFolderPath, si.Name) {
				si.BlobStoreBaseFolderPath = fmt.Sprintf("%s%c%s", si.BlobStoreBaseFolderPath, os.PathSeparator, si.Name)
			}
			blobTableName = si.BlobStoreBaseFolderPath
		}
	}

	// Maximum slot length is 20,000. It may be ridiculously huge blob if too big.
	// Even 20,000 may be too much, depending on key & value data size you'll store.
	if si.SlotLength > MaxSlotLength {
		si.SlotLength = MaxSlotLength
	}

	// Enforce some basic rule not to create conflicting setup.
	if si.IsValueDataInNodeSegment {
		si.IsValueDataGloballyCached = false
		si.IsValueDataActivelyPersisted = false
	}

	// Use the SOP default cache config if the parameter received is not set.
	if si.CacheConfig == nil {
		cc := GetDefaultCacheConfig()
		si.CacheConfig = &cc
	}
	// Apply SOP minimum caching rule if needed.
	si.CacheConfig.enforceMinimumRule()

	// Turn off global caching flag if user specifies "no caching" (-1) on value data cache duration.
	if si.CacheConfig.ValueDataCacheDuration < 0 {
		si.IsValueDataGloballyCached = false
	}

	return &StoreInfo{
		Name:                         si.Name,
		Version:                      Version,
		SlotLength:                   si.SlotLength,
		IsUnique:                     si.IsUnique,
		Description:                  si.Description,
		RegistryTable:                registryTableName,
		BlobTable:                    blobTableName,
		IsValueDataInNodeSegment:     si.IsValueDataInNodeSegment,
		IsValueDataActivelyPersisted: si.IsValueDataActivelyPersisted,
		IsValueDataGloballyCached:    si.IsValueDataGloballyCached,
		LeafLoadBalancing:            si.LeafLoadBalancing,
		CacheConfig:                  *si.CacheConfig,
		MapKeyIndexSpecification:     si.MapKeyIndexSpecification,
		CELexpression:                si.CELexpression,
		IsPrimitiveKey:               si.IsPrimitiveKey,
		Relations:                    si.Relations,
		CustomData:                   si.CustomData,
	}
}

// IsEmpty reports whether the StoreInfo has zero values; an empty StoreInfo means the B-Tree does not yet exist.
func (s StoreInfo) IsEmpty() bool {
	return s.Name == "" && s.SlotLength == 0
}

// IsCompatible reports whether two StoreInfo configurations are compatible for merge/attach semantics.
func (s StoreInfo) IsCompatible(b StoreInfo) bool {
	return s.SlotLength == b.SlotLength &&
		s.IsUnique == b.IsUnique &&
		s.BlobTable == b.BlobTable &&
		s.RegistryTable == b.RegistryTable &&
		s.IsValueDataInNodeSegment == b.IsValueDataInNodeSegment &&
		s.IsValueDataActivelyPersisted == b.IsValueDataActivelyPersisted &&
		s.IsValueDataGloballyCached == b.IsValueDataGloballyCached &&
		s.LeafLoadBalancing == b.LeafLoadBalancing &&
		//s.IsPrimitiveKey == b.IsPrimitiveKey &&
		s.MapKeyIndexSpecification == b.MapKeyIndexSpecification &&
		s.CELexpression == b.CELexpression
}

// FormatRegistryTable formats a store name into a registry table name by adding an _r suffix.
func FormatRegistryTable(name string) string {
	return fmt.Sprintf("%s_r", name)
}
