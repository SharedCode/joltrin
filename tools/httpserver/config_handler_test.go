package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleUninstallSystem_RejectsSystemDBDeletion(t *testing.T) {
	defer func() { config = Config{} }()

	tmpDir := t.TempDir()
	systemDBPath := filepath.Join(tmpDir, "system-db")
	if err := os.MkdirAll(systemDBPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	config = Config{
		ConfigFile: filepath.Join(tmpDir, "config.json"),
		SystemDB:   &DatabaseConfig{Path: systemDBPath},
	}

	reqBody := map[string]any{"delete_system_db": true}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/system/uninstall", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handleUninstallSystem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleUninstallSystem() status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if _, err := os.Stat(systemDBPath); err != nil {
		t.Fatalf("system DB path was removed unexpectedly: %v", err)
	}
}

func TestHandleDeleteEnvironment_RejectsPathTraversal(t *testing.T) {
	defer func() { config = Config{} }()

	tmpDir := t.TempDir()
	victim := filepath.Join(tmpDir, "victim.json")
	if err := os.WriteFile(victim, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reqBody := map[string]any{"filename": "../" + filepath.Base(victim)}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Run from inside tmpDir so a naive relative traversal would actually reach victim.json.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	nested := filepath.Join(tmpDir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer os.Chdir(origWD)

	req := httptest.NewRequest(http.MethodPost, "/api/config/environments/delete", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handleDeleteEnvironment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleDeleteEnvironment() status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim file was removed by a path-traversal filename: %v", err)
	}
}

func TestHandleSwitchEnvironment_RejectsPathTraversal(t *testing.T) {
	defer func() { config = Config{} }()

	reqBody := map[string]any{"filename": "../../etc/passwd"}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/environments/switch", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handleSwitchEnvironment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleSwitchEnvironment() status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleCreateEnvironment_RejectsPathTraversal(t *testing.T) {
	defer func() { config = Config{} }()

	reqBody := map[string]any{"name": "../../tmp/evil"}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/environments/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handleCreateEnvironment(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("handleCreateEnvironment() status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
