package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

func TestHandleAddStore_PreservesMapValueSchemaWithoutSeedKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_schema_seed"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "schema_seed_store",
		"key_type":   "string",
		"value_type": "map",
		"seed_value": map[string]any{
			"name": "alice",
		},
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/store/info?database="+dbName+"&name=schema_seed_store", nil)
	w = httptest.NewRecorder()
	handleGetStoreInfo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get store info failed: %d %s", w.Code, w.Body.String())
	}

	var info map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal store info failed: %v", err)
	}
	if got := info["valueType"]; got != "map" {
		t.Fatalf("expected valueType=map, got %v", got)
	}
	if got := info["count"]; got != float64(1) {
		t.Fatalf("expected count=1, got %v", got)
	}
}

func TestHandleDeleteItem_AllowsDeletingOnlyRemainingRow(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_delete_last_row"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "single_row_store",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "single_row_store",
		"key":      "only-row",
		"value":    "data",
	}
	body, _ = json.Marshal(addReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add row failed: %d %s", w.Code, w.Body.String())
	}

	deleteReq := map[string]any{
		"database": dbName,
		"store":    "single_row_store",
		"key":      "only-row",
	}
	body, _ = json.Marshal(deleteReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/delete", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleDeleteItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected delete to succeed for the only row, got %d: %s", w.Code, w.Body.String())
	}

	dbOpts, err := getDBOptions(context.Background(), dbName)
	if err != nil {
		t.Fatalf("getDBOptions failed: %v", err)
	}
	trans, err := database.BeginTransaction(context.Background(), dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer trans.Rollback(context.Background())

	store, err := database.OpenBtree[string, any](context.Background(), dbOpts, "single_row_store", trans, nil)
	if err != nil {
		t.Fatalf("OpenBtree failed: %v", err)
	}
	if got := store.Count(); got != 0 {
		t.Fatalf("expected the only row to be deleted, got %d", got)
	}
}

func TestHandleListItems_ReturnsEmptyAfterDeletingLastRow(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_list_empty_after_delete"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "empty_after_delete",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "empty_after_delete",
		"key":      "only-row",
		"value":    "data",
	}
	body, _ = json.Marshal(addReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add row failed: %d %s", w.Code, w.Body.String())
	}

	deleteReq := map[string]any{
		"database": dbName,
		"store":    "empty_after_delete",
		"key":      "only-row",
	}
	body, _ = json.Marshal(deleteReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/delete", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleDeleteItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected delete to succeed for the only row, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/store/items?database="+dbName+"&name=empty_after_delete&action=current&key=only-row", nil)
	w = httptest.NewRecorder()
	handleListItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list items failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal list response failed: %v", err)
	}
	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatalf("expected data[] in response, got %T", resp["data"])
	}
	if len(data) != 0 {
		t.Fatalf("expected no items after deleting the last row, got %d items: %+v", len(data), data)
	}
}

func TestHandleDeleteItems_PreservesAnchorRow(t *testing.T) {
	tmpDir := t.TempDir()
	dbName := "testdb_delete_anchor"
	config = Config{
		Databases: []DatabaseConfig{{
			Name: dbName,
			Path: tmpDir,
			Mode: "standalone",
		}},
	}

	createReq := map[string]any{
		"database":   dbName,
		"store":      "anchor_store",
		"key_type":   "string",
		"value_type": "string",
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/store/add", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleAddStore(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create store failed: %d %s", w.Code, w.Body.String())
	}

	addReq := map[string]any{
		"database": dbName,
		"store":    "anchor_store",
		"key":      "root_anchor",
		"value":    "anchor-value",
	}
	body, _ = json.Marshal(addReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add anchor failed: %d %s", w.Code, w.Body.String())
	}

	addReq = map[string]any{
		"database": dbName,
		"store":    "anchor_store",
		"key":      "user-row",
		"value":    "data-value",
	}
	body, _ = json.Marshal(addReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/item/add", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleAddItem(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add user row failed: %d %s", w.Code, w.Body.String())
	}

	bulkReq := map[string]any{
		"database": dbName,
		"store":    "anchor_store",
		"keys":     []any{"root_anchor", "user-row"},
	}
	body, _ = json.Marshal(bulkReq)
	req = httptest.NewRequest(http.MethodPost, "/api/store/items/delete", bytes.NewBuffer(body))
	w = httptest.NewRecorder()
	handleDeleteItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk delete failed: %d %s", w.Code, w.Body.String())
	}

	dbOpts, err := getDBOptions(context.Background(), dbName)
	if err != nil {
		t.Fatalf("getDBOptions failed: %v", err)
	}

	trans, err := database.BeginTransaction(context.Background(), dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	defer trans.Rollback(context.Background())

	store, err := database.OpenBtree[string, any](context.Background(), dbOpts, "anchor_store", trans, nil)
	if err != nil {
		t.Fatalf("OpenBtree failed: %v", err)
	}

	if got := store.Count(); got != 1 {
		t.Fatalf("expected one remaining row, got %d", got)
	}

	found, err := store.Find(context.Background(), "root_anchor", true)
	if err != nil {
		t.Fatalf("Find root_anchor failed: %v", err)
	}
	if !found {
		t.Fatal("expected anchor row to remain")
	}
}
