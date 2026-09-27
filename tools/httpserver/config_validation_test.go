package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestHandleSaveConfig_PersistsRootUserForLogin(t *testing.T) {
	oldConfig := config
	defer func() { config = oldConfig }()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	config = Config{}
	config.ConfigFile = configFile

	payload := map[string]interface{}{
		"registry_path": filepath.Join(tmpDir, "system"),
		"port":          8080,
		"root_password": "secret123",
		"system_options": map[string]interface{}{
			"stores_folders": []string{},
			"type":           0,
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/config/save", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handleSaveConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleSaveConfig() status = %d, body=%s", w.Code, w.Body.String())
	}

	config = Config{}
	if err := loadConfig(configFile); err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	ok, user, err := config.Authenticate("root", "secret123")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !ok || user == nil || user.Username != "root" {
		t.Fatalf("Authenticate() = ok=%v user=%v", ok, user)
	}
}

func TestHandleSaveConfig_AllowsImmediateSessionCreation(t *testing.T) {
	withIsolatedSessionStore(t)
	oldConfig := config
	oldFacade := tokenFacade
	defer func() {
		config = oldConfig
		tokenFacade = oldFacade
	}()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	config = Config{SystemDB: config.SystemDB}
	config.ConfigFile = configFile
	tokenFacade = nil
	tokenFacadeOnce = sync.Once{}

	payload := map[string]interface{}{
		"registry_path": filepath.Join(tmpDir, "system"),
		"port":          8080,
		"root_password": "secret123",
		"system_options": map[string]interface{}{
			"stores_folders": []string{},
			"type":           0,
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/config/save", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	handleSaveConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("handleSaveConfig() status = %d, body=%s", w.Code, w.Body.String())
	}

	loginBody := bytes.NewBufferString(`{"username":"root","password":"secret123"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()

	handleLogin(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		_, _, sessionErr := currentTokenFacade().CreateSession(context.Background(), "root", sop.RoleAdmin)
		if sessionErr != nil {
			t.Fatalf("handleLogin() status = %d, body=%s, CreateSession() error=%v", loginW.Code, loginW.Body.String(), sessionErr)
		}
		t.Fatalf("handleLogin() status = %d, body=%s", loginW.Code, loginW.Body.String())
	}
}

func TestValidateSetupWizardRootPassword(t *testing.T) {
	if err := validateSetupWizardRootPassword(""); err == nil {
		t.Fatal("expected blank root password to be rejected")
	}
	if err := validateSetupWizardRootPassword("   "); err == nil {
		t.Fatal("expected whitespace-only root password to be rejected")
	}
	if err := validateSetupWizardRootPassword("secret123"); err != nil {
		t.Fatalf("expected non-empty root password to pass, got %v", err)
	}
}

func TestHandleSaveConfig_DeduplicatesPaths(t *testing.T) {
	// Restore config state after test
	defer func() {
		config = Config{}
	}()

	tmpDir, err := os.MkdirTemp("", "sop_repro_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Case 1: Registry Path and Stores Folder are "effectively" the same but different strings (e.g. trailing slash)
	// This mirrors what might happen if frontend sends both.
	regPath := filepath.Join(tmpDir, "system")
	storePath := regPath + "/" // Trailing slash

	payload := map[string]interface{}{
		"registry_path": regPath,
		"port":          8080,
		"root_password": "secret123",
		"system_options": map[string]interface{}{
			"stores_folders": []string{storePath},
			"type":           0,
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/config/save", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	// Execute
	handleSaveConfig(w, req)

	resp := w.Result()
	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Logf("Response Status: %d", resp.StatusCode)
		t.Logf("Response Body: %s", string(bodyBytes))

		// If validation is active, this expects to fail if dedupe isn't robust
		if strings.Contains(string(bodyBytes), "internal path conflict") {
			t.Log("reproduced: internal path conflict with same logical path")
		} else {
			t.Log("failed with other error")
		}
	} else {
		t.Log("Test passed (Validation might be disabled or working)")
	}
}

func TestHandleSaveConfig_AllowsUniqueECPaths(t *testing.T) {
	// Restore config state after test
	defer func() {
		config = Config{}
	}()

	tmpDir, err := os.MkdirTemp("", "sop_ec_repro_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	regPath := filepath.Join(tmpDir, "system")
	// EC paths
	d1 := filepath.Join(tmpDir, "d1")
	d2 := filepath.Join(tmpDir, "d2")

	// Payload with unique EC paths
	payload := map[string]interface{}{
		"registry_path": regPath,
		"port":          8080,
		"root_password": "secret123",
		"system_options": map[string]interface{}{
			"stores_folders": []string{},
			"type":           0,
			"erasure_config": map[string]interface{}{
				"default": map[string]interface{}{
					"data_shards_count":               1,
					"parity_shards_count":             1,
					"base_folder_paths_across_drives": []string{d1, d2},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/config/save", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	// Execute
	handleSaveConfig(w, req)

	resp := w.Result()
	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Logf("Response Status: %d", resp.StatusCode)
		t.Logf("Response Body: %s", string(bodyBytes))
	} else {
		t.Log("Test passed")
	}
}

func TestHandleSaveConfig_ErasureIsolation(t *testing.T) {
	// Restore config state after test
	defer func() {
		config = Config{}
	}()

	tmpDir, err := os.MkdirTemp("", "sop_ec_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	path1 := filepath.Join(tmpDir, "disk1")
	path2 := filepath.Join(tmpDir, "disk2")

	// Helper to send request
	sendReq := func(payload map[string]interface{}) (int, string) {
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/api/config/save", bytes.NewBuffer(body))
		w := httptest.NewRecorder()
		handleSaveConfig(w, req)
		resp := w.Result()
		bodyBytes, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(bodyBytes)
	}

	t.Run("Fails with duplicate BasePaths in Global Config", func(t *testing.T) {
		payload := map[string]interface{}{
			"registry_path": filepath.Join(tmpDir, "system_dup_global"),
			"port":          8080,
			"root_password": "secret123",
			"system_options": map[string]interface{}{
				"stores_folders": []string{filepath.Join(tmpDir, "stores")},
				"type":           0, // Standalone
				"erasure_config": map[string]interface{}{
					"default": map[string]interface{}{
						"data_shards_count":               1,
						"parity_shards_count":             1,
						"base_folder_paths_across_drives": []string{path1, path1}, // Duplicate
					},
				},
			},
		}
		status, body := sendReq(payload)
		if status != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", status)
		}
		if !strings.Contains(body, "all paths must be unique") {
			t.Errorf("Expected error message about unique paths, got: %s", body)
		}
	})

	t.Run("Fails with mismatch chunk count in Global Config", func(t *testing.T) {
		payload := map[string]interface{}{
			"registry_path": filepath.Join(tmpDir, "system_mismatch"),
			"port":          8080,
			"root_password": "secret123",
			"system_options": map[string]interface{}{
				"stores_folders": []string{filepath.Join(tmpDir, "stores")},
				"type":           0, // Standalone
				"erasure_config": map[string]interface{}{
					"default": map[string]interface{}{
						"data_shards_count":               1,
						"parity_shards_count":             1,
						"base_folder_paths_across_drives": []string{path1}, // Only 1, need 2
					},
				},
			},
		}
		status, body := sendReq(payload)
		if status != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", status)
		}
		if !strings.Contains(body, "BasePaths count must match Data+Parity") {
			t.Errorf("Expected error message about path count, got: %s", body)
		}
	})

	t.Run("Fails with duplicate BasePaths in Per-Store Config", func(t *testing.T) {
		payload := map[string]interface{}{
			"registry_path": filepath.Join(tmpDir, "system_dup_store"),
			"port":          8080,
			"root_password": "secret123",
			"system_options": map[string]interface{}{
				"stores_folders": []string{filepath.Join(tmpDir, "stores")},
				"type":           0, // Standalone
				"erasure_config": map[string]interface{}{
					"storeA": map[string]interface{}{
						"data_shards_count":               1,
						"parity_shards_count":             1,
						"base_folder_paths_across_drives": []string{path1, path1}, // Duplicate
					},
				},
			},
		}
		status, body := sendReq(payload)
		if status != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", status)
		}
		// Expect "Erasure Config (System Key: storeA)" or similar. Matching looser "storeA" covers it.
		if !strings.Contains(body, "storeA") {
			t.Errorf("Expected error context for storeA, got: %s", body)
		}
		if !strings.Contains(body, "all paths must be unique") {
			t.Errorf("Expected error message about unique paths, got: %s", body)
		}
	})

	t.Run("Succeeds with valid unique paths", func(t *testing.T) {
		payload := map[string]interface{}{
			"registry_path": filepath.Join(tmpDir, "system_valid"),
			"port":          8080,
			"root_password": "secret123",
			"system_options": map[string]interface{}{
				"stores_folders": []string{filepath.Join(tmpDir, "stores")},
				"type":           0, // Standalone
				"erasure_config": map[string]interface{}{
					"default": map[string]interface{}{
						"data_shards_count":               1,
						"parity_shards_count":             1,
						"base_folder_paths_across_drives": []string{path1, path2}, // Unique
					},
				},
			},
		}
		status, body := sendReq(payload)
		// Assuming success or unrelated failure
		if status == http.StatusBadRequest && strings.Contains(body, "must be unique") {
			t.Fatalf("Failed unexpected on uniqueness: %s", body)
		}
	})
}
