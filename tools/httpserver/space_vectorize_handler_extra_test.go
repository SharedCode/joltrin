package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

func TestHandleVectorizeSpace_BeginTransactionFail(t *testing.T) {
	uid := sop.NewUUID()
	body := `{"database": "bad_trans_db", "space": "testspace", "categoryId": "` + uid.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/vectorize", bytes.NewReader([]byte(body)))
	req.Header.Set("X-Mock-Embedding", "true")
	req.Header.Set("X-Mock-Generator", "true")
	w := httptest.NewRecorder()

	setMockConfig("bad_trans_db", "/tmp/bad/trans/db/some/path")

	handleVectorizeSpace(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected OK, got %v", res.StatusCode)
	}

	var resp map[string]interface{}
	json.NewDecoder(res.Body).Decode(&resp)
	if resp["task_id"] == nil {
		t.Fatalf("Expected task_id in response")
	}

	taskID := resp["task_id"].(string)
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if GetTask(taskID) != nil && (GetTask(taskID).Status == "error" || GetTask(taskID).Status == "completed") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	task := GetTask(taskID)
	if task == nil {
		t.Errorf("Task should exist")
	} else if task.Status != "completed" && task.Status != "error" {
		t.Errorf("Task status unexpected: %s message: %s error: %s", task.Status, task.Message, task.Error)
	}
}
