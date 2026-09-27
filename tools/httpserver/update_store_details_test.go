package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
)

func TestHandleUpdateStoreInfo_AllowsNonStructuralUpdateOnNonEmptyStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_update_details"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_details",
		"key_type":   "string",
		"value_type": "string",
		"data_size":  1,
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "store_details",
		"key":      "k1",
		"value":    "v1",
	}
	body, _ = json.Marshal(addReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewReader(body))
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add item failed: %d %s", w.Code, w.Body.String())
	}

	var target string
	err := filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Base(path) != "storeinfo.txt" || filepath.Base(filepath.Dir(path)) != "store_details" {
			return nil
		}
		target = path
		return nil
	})
	if err != nil {
		t.Fatalf("find storeinfo path: %v", err)
	}
	if target == "" {
		t.Fatal("storeinfo.txt not found")
	}

	ba, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read storeinfo: %v", err)
	}
	var info sop.StoreInfo
	if err := encoding.DefaultMarshaler.Unmarshal(ba, &info); err != nil {
		t.Fatalf("unmarshal storeinfo: %v", err)
	}
	info.IsValueDataGloballyCached = false
	ba, err = encoding.DefaultMarshaler.Marshal(info)
	if err != nil {
		t.Fatalf("marshal storeinfo: %v", err)
	}
	if err := os.WriteFile(target, ba, 0o644); err != nil {
		t.Fatalf("write storeinfo: %v", err)
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_details",
		"description": "updated description",
		"dataSize":    1,
		"slotLength":  1000,
		"isUnique":    true,
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected non-structural update to succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateStoreInfo_PersistsSchemaFieldMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_update_details_schema"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_schema_meta",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_schema_meta",
		"description": "schema metadata update",
		"schema":      map[string]any{"name": "string", "age": "number"},
		"keyFields":   []any{"name"},
		"valueFields": []any{"age"},
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected metadata update to succeed, got %d: %s", w.Code, w.Body.String())
	}

	var target string
	err := filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Base(path) != "storeinfo.txt" || filepath.Base(filepath.Dir(path)) != "store_schema_meta" {
			return nil
		}
		target = path
		return nil
	})
	if err != nil {
		t.Fatalf("find storeinfo path: %v", err)
	}
	if target == "" {
		t.Fatal("storeinfo.txt not found")
	}

	ba, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read storeinfo: %v", err)
	}
	var info sop.StoreInfo
	if err := encoding.DefaultMarshaler.Unmarshal(ba, &info); err != nil {
		t.Fatalf("unmarshal storeinfo: %v", err)
	}

	if got := info.Schema["name"]; got != "string" {
		t.Fatalf("expected schema name to be persisted, got %q", got)
	}
	if len(info.KeyFields) != 1 || info.KeyFields[0] != "name" {
		t.Fatalf("expected key fields to be persisted, got %+v", info.KeyFields)
	}
	if len(info.ValueFields) != 1 || info.ValueFields[0] != "age" {
		t.Fatalf("expected value fields to be persisted, got %+v", info.ValueFields)
	}
}

func TestHandleAddStore_IgnoresSchemaMetadataAtCreation(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_add_store_schema"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":     dbName,
		"store":        "store_create_schema",
		"key_type":     "map",
		"value_type":   "map",
		"description":  "created with schema metadata",
		"index_spec":   `{"index_fields":[{"field_name":"order_id","ascending_sort_order":true}]}`,
		"schema":       map[string]any{"order_id": "string", "amount": "number"},
		"key_fields":   []any{"order_id"},
		"value_fields": []any{"amount"},
		"relations": []any{map[string]any{
			"source_fields": []any{"order_id"},
			"target_store":  "orders",
			"target_fields": []any{"id"},
		}},
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	var target string
	err := filepath.WalkDir(tmpDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Base(path) != "storeinfo.txt" || filepath.Base(filepath.Dir(path)) != "store_create_schema" {
			return nil
		}
		target = path
		return nil
	})
	if err != nil {
		t.Fatalf("find storeinfo path: %v", err)
	}
	if target == "" {
		t.Fatal("storeinfo.txt not found")
	}

	ba, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read storeinfo: %v", err)
	}
	var info sop.StoreInfo
	if err := encoding.DefaultMarshaler.Unmarshal(ba, &info); err != nil {
		t.Fatalf("unmarshal storeinfo: %v", err)
	}

	if len(info.Schema) != 0 {
		t.Fatalf("expected schema metadata to be ignored at creation time, got %#v", info.Schema)
	}
	if len(info.KeyFields) != 0 {
		t.Fatalf("expected key fields to be ignored at creation time, got %+v", info.KeyFields)
	}
	if len(info.ValueFields) != 0 {
		t.Fatalf("expected value fields to be ignored at creation time, got %+v", info.ValueFields)
	}
	if len(info.Relations) != 1 || info.Relations[0].TargetStore != "orders" {
		t.Fatalf("expected relations to be persisted, got %+v", info.Relations)
	}
}

func TestHandleGetStoreInfo_PrioritizesSchemaForKeyStructType(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_store_info_key_type"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_key_struct",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_key_struct",
		"description": "schema metadata update",
		"schema":      map[string]any{"id": "string"},
		"keyFields":   []any{"id"},
		"keyType":     "string",
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected schema metadata update to succeed, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/store/info?database="+dbName+"&name=store_key_struct", nil)
	w = httptest.NewRecorder()
	handleGetStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected store info request to succeed, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got := response["keyType"]; got != "map" {
		t.Fatalf("expected keyType to be map from explicit schema/key fields, got %#v", got)
	}
}

func TestHandleGetStoreInfo_UsesDeclaredValueStructForMapValueType(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_store_info_value_type"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_value_struct",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "store_value_struct",
		"key":      "k1",
		"value":    "v1",
	}
	body, _ = json.Marshal(addReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewReader(body))
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add item failed: %d %s", w.Code, w.Body.String())
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_value_struct",
		"description": "struct value metadata",
		"schema":      map[string]any{"F1": "string", "F2": "string"},
		"valueFields": []any{"F1", "F2"},
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected metadata update to succeed, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/store/info?database="+dbName+"&name=store_value_struct", nil)
	w = httptest.NewRecorder()
	handleGetStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected store info request to succeed, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got := response["valueType"]; got != "map" {
		t.Fatalf("expected valueType to be map from declared value fields, got %#v", got)
	}
}

func TestHandleGetStoreInfo_PrioritizesDeclaredKeyStructForNonEmptyStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_store_info_key_type_nonempty"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_key_struct_nonempty",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "store_key_struct_nonempty",
		"key":      "k1",
		"value":    "v1",
	}
	body, _ = json.Marshal(addReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewReader(body))
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add item failed: %d %s", w.Code, w.Body.String())
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_key_struct_nonempty",
		"description": "schema metadata update",
		"schema":      map[string]any{"tenant": "string", "id": "string"},
		"keyFields":   []any{"tenant", "id"},
		"keyType":     "string",
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected schema metadata update to succeed, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/store/info?database="+dbName+"&name=store_key_struct_nonempty", nil)
	w = httptest.NewRecorder()
	handleGetStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected store info request to succeed, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got := response["keyType"]; got != "map" {
		t.Fatalf("expected keyType to remain map from explicit key metadata on non-empty store, got %#v", got)
	}
	keyFields, ok := response["keyFields"].([]any)
	if !ok || len(keyFields) != 2 || keyFields[0] != "tenant" || keyFields[1] != "id" {
		t.Fatalf("expected keyFields to be preserved, got %#v", response["keyFields"])
	}
}

func TestHandleGetStoreInfo_SkipsSamplesWhenSchemaMetadataExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_store_info_schema_no_samples"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_schema_backed_no_samples",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "store_schema_backed_no_samples",
		"key":      "k1",
		"value":    "v1",
	}
	body, _ = json.Marshal(addReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewReader(body))
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add item failed: %d %s", w.Code, w.Body.String())
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_schema_backed_no_samples",
		"description": "schema metadata update",
		"schema":      map[string]any{"tenant": "string", "id": "string", "value": "string"},
		"keyFields":   []any{"tenant", "id"},
		"valueFields": []any{"value"},
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected schema metadata update to succeed, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/store/info?database="+dbName+"&name=store_schema_backed_no_samples", nil)
	w = httptest.NewRecorder()
	handleGetStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected store info request to succeed, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got := response["keyType"]; got != "map" {
		t.Fatalf("expected keyType to be map from persisted schema metadata, got %#v", got)
	}
	if got := response["valueType"]; got != "map" {
		t.Fatalf("expected valueType to be map from persisted schema metadata, got %#v", got)
	}
	keyFields, ok := response["keyFields"].([]any)
	if !ok || len(keyFields) != 2 || keyFields[0] != "tenant" || keyFields[1] != "id" {
		t.Fatalf("expected schema-derived keyFields in response, got %#v", response["keyFields"])
	}
	if _, exists := response["sampleKey"]; exists {
		t.Fatalf("expected no sampleKey when schema metadata exists, got %#v", response["sampleKey"])
	}
	if _, exists := response["sampleValue"]; exists {
		t.Fatalf("expected no sampleValue when schema metadata exists, got %#v", response["sampleValue"])
	}
}

func TestHandleUpdateStoreInfo_IgnoresStaleStructuralDefaultsOnNonEmptyStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_update_details_stale"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "store_details_stale",
		"key_type":   "string",
		"value_type": "string",
		"data_size":  1,
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewReader(body))
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "store_details_stale",
		"key":      "k1",
		"value":    "v1",
	}
	body, _ = json.Marshal(addReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewReader(body))
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add item failed: %d %s", w.Code, w.Body.String())
	}

	updateReq := map[string]any{
		"database":    dbName,
		"storeName":   "store_details_stale",
		"description": "updated description",
		"dataSize":    0,
		"slotLength":  0,
		"isUnique":    false,
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/store/update", bytes.NewReader(body))
	handleUpdateStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected stale structural defaults to be ignored for non-structural update, got %d: %s", w.Code, w.Body.String())
	}
}
