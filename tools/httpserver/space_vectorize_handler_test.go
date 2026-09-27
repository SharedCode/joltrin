package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	aidb "github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

type MockEmbedder struct{}

func (m *MockEmbedder) Name() string { return "mock_emb" }
func (m *MockEmbedder) Dim() int     { return 1536 }
func (m *MockEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	res := make([][]float32, len(texts))
	for i := range texts {
		res[i] = make([]float32, 1536)
	}
	return res, nil
}

func setMockConfig(dbName, dbPath string) {
	config = Config{
		SystemDB: &DatabaseConfig{Name: "system", Path: "/tmp/mock_sys_db", Mode: "standalone"},
		Databases: []DatabaseConfig{
			{Name: dbName, Path: dbPath, Mode: "standalone"},
		},
	}
}

func TestHandleVectorizeSpace_Methods(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/vectorize", nil)
	w := httptest.NewRecorder()

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected MethodNotAllowed, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte("{bad json}")))
	w := httptest.NewRecorder()

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected BadRequest, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_MissingCategory(t *testing.T) {
	body := `{"database": "testdb", "space": "testspace"}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()

	os.RemoveAll("/tmp/test_vectorize_db")
	defer os.RemoveAll("/tmp/test_vectorize_db")

	setMockConfig("testdb", "/tmp/test_vectorize_db")

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected OK, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_ItemIDsSuccess(t *testing.T) {
	uid := sop.NewUUID()
	uid2 := sop.NewUUID()
	body := `{"database": "testdb", "space": "testspace", "categoryId": "` + uid.String() + `", "itemIds": ["` + uid2.String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	req.Header.Set("X-Mock-Embedding", "true")
	req.Header.Set("X-Mock-Generator", "true")
	w := httptest.NewRecorder()

	dbPath := "/tmp/test_vectorize_success_db"

	db := aidb.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
	})
	trans, _ := db.BeginTransaction(context.Background(), sop.ForWriting)
	defer trans.Rollback(context.Background())
	llm := &MockGenerator{}
	emb := &MockEmbedder{}
	kb, _ := db.OpenKnowledgeBase(context.Background(), "testspace", trans, llm, emb, false)
	if kb != nil && kb.Store != nil {
		kb.Store.AddCategory(context.Background(), &memory.Category{ID: uid, Name: "Test Category"})
	}
	trans.Commit(context.Background())

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected OK, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_InvalidItemIDs(t *testing.T) {
	uid := sop.NewUUID()
	body := `{"database": "testdb", "space": "testspace", "categoryId": "` + uid.String() + `", "itemIds": ["bad-uuid"]}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()

	setMockConfig("testdb", "/tmp/test_vectorize_db")

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected BadRequest, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_InvalidDB(t *testing.T) {
	body := `{"database": "baddb", "space": "testspace", "categoryId": "bad"}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()

	setMockConfig("testdb", "/tmp/test_vectorize_db")

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected BadRequest, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_InvalidCategoryId(t *testing.T) {
	body := `{"database": "testdb", "space": "testspace", "categoryId": "bad"}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()

	setMockConfig("testdb", "/tmp/test_vectorize_db")

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected BadRequest, got %v", res.StatusCode)
	}
}

func TestHandleVectorizeSpace_Success(t *testing.T) {
	uid := sop.NewUUID()
	body := `{"database": "testdb", "space": "testspace", "categoryId": "` + uid.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	req.Header.Set("X-Mock-Embedding", "true")
	req.Header.Set("X-Mock-Generator", "true")
	w := httptest.NewRecorder()

	os.RemoveAll("/tmp/test_vectorize_success_db_2")
	defer os.RemoveAll("/tmp/test_vectorize_success_db_2")

	setMockConfig("testdb", "/tmp/test_vectorize_success_db_2")

	dbPath := "/tmp/test_vectorize_success_db_2"

	db := aidb.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
	})
	trans, _ := db.BeginTransaction(context.Background(), sop.ForWriting)
	defer trans.Rollback(context.Background())
	llm := &MockGenerator{}
	emb := &MockEmbedder{}
	kb, _ := db.OpenKnowledgeBase(context.Background(), "testspace", trans, llm, emb, false)
	if kb != nil && kb.Store != nil {
		kb.Store.AddCategory(context.Background(), &memory.Category{ID: uid, Name: "Test Category"})
	}
	trans.Commit(context.Background())

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		var bodyBytes []byte
		if res.Body != nil {
			bodyBytes, _ = io.ReadAll(res.Body)
		}
		t.Errorf("Expected OK, got %v: %s", res.StatusCode, string(bodyBytes))
	}

	// Check if task is created
	var resp map[string]interface{}
	json.NewDecoder(res.Body).Decode(&resp)

	if resp["task_id"] == nil || resp["task_id"] == "" {
		t.Errorf("Expected task_id, got empty")
	} else {
		// Wait a bit and check task status
		time.Sleep(200 * time.Millisecond)
		taskID := resp["task_id"].(string)

		task := GetTask(taskID)
		exists := task != nil

		if !exists {
			t.Errorf("Task %s should exist in registry", taskID)
		} else if task.Status != "completed" && task.Status != "failed" && task.Status != "in_progress" {
			t.Errorf("Task status is unexpected: %s, Message: %s, Error: %s", task.Status, task.Message, task.Error)
		}
	}
}
func TestHandleVectorizeSpace_GetDBOptionsFail(t *testing.T) {
	uid := sop.NewUUID()
	body := `{"database": "unconfigured_db_xyz", "space": "testspace", "categoryId": "` + uid.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected BadRequest for bad DB, got %v", res.StatusCode)
	}
}
