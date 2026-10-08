package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	coredb "github.com/sharedcode/joltrin/v5/database"
)

// fakeOllama serves /api/chat from a script and /api/embeddings from a keyword
// embedding, so the tests exercise the real HTTP client and the real Joltrin
// store without a model.
type fakeOllama struct {
	srv     *httptest.Server
	mu      sync.Mutex
	replies []string // chat replies, consumed in order; the last one repeats
	chats   int
}

var keywordAxes = []string{"backup", "deploy", "rollback", "release", "publish", "note"}

func keywordVec(text string) []float64 {
	t := strings.ToLower(text)
	v := make([]float64, len(keywordAxes))
	var norm float64
	for i, k := range keywordAxes {
		if strings.Contains(t, k) {
			v[i] = 1
			norm++
		}
	}
	if norm == 0 {
		v[0] = 1
		norm = 1
	}
	for i := range v {
		v[i] /= math.Sqrt(norm)
	}
	return v
}

func newFakeOllama(t *testing.T, replies ...string) *fakeOllama {
	t.Helper()
	f := &fakeOllama{replies: replies}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		i := f.chats
		if i >= len(f.replies) {
			i = len(f.replies) - 1
		}
		f.chats++
		reply := f.replies[i]
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": reply}})
	})
	mux.HandleFunc("/api/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": keywordVec(req.Prompt)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func newTestAgent(t *testing.T, baseURL string) *agent {
	t.Helper()
	db := database.NewDatabase(coredb.DatabaseOptions{StoresFolders: []string{t.TempDir()}})
	a, err := newAgent(db, baseURL, "test-chat", "test-embed", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseAction(t *testing.T) {
	wf, err := newWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"draft_note": true, "publish_note": true}
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"valid", `{"step":"draft_note","note":"hello"}`, ""},
		{"not json", `draft a note please`, "malformed"},
		{"unknown field", `{"step":"draft_note","note":"x","extra":1}`, "malformed"},
		{"trailing data", `{"step":"draft_note","note":"x"} {"step":"publish_note","note":"y"}`, "trailing"},
		{"unknown step", `{"step":"drop_prod_db","note":"x"}`, "unknown step"},
		{"step not offered to model", `{"step":"human_approve","note":"x"}`, "not available"},
		{"empty note", `{"step":"draft_note","note":""}`, "note must be"},
		{"oversized note", `{"step":"draft_note","note":"` + strings.Repeat("a", maxNoteBytes+1) + `"}`, "note must be"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAction(tc.raw, wf, allowed)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestRecallRanksRelevantFact(t *testing.T) {
	f := newFakeOllama(t, `{}`)
	a := newTestAgent(t, f.srv.URL)
	ctx := context.Background()
	if err := a.ingest(ctx, releaseFacts); err != nil {
		t.Fatal(err)
	}
	hits, err := a.recall(ctx, "How do I rollback a bad release?", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != "f3" {
		t.Fatalf("top hit = %+v, want f3", hits)
	}
}

func TestSchemaRejectionThenRecovery(t *testing.T) {
	f := newFakeOllama(t,
		`not json at all`,
		`{"step":"drop_prod_db","note":"x"}`,
		`{"step":"draft_note","note":"Re-point the release symlink to roll back."}`,
	)
	a := newTestAgent(t, f.srv.URL)
	ctx := context.Background()

	res, err := a.run(ctx, "r", "write a note", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || res.Step != "draft_note" || res.Attempts != 3 {
		t.Fatalf("result = %+v, want draft_note committed on attempt 3", res)
	}
	if n, _ := a.logCount(ctx); n != 1 {
		t.Fatalf("log rows = %d, want 1", n)
	}
}

func TestBarrierBlocksPublishUntilHumanApproves(t *testing.T) {
	f := newFakeOllama(t, `{"step":"publish_note","note":"Release notes go out now."}`)
	a := newTestAgent(t, f.srv.URL)
	ctx := context.Background()

	// A draft exists, but nobody approved it: every attempt is blocked and
	// nothing is written.
	if _, err := a.wf.CheckAndCommitIdempotent(a.trace, "draft_note", "seed"); err != nil {
		t.Fatal(err)
	}
	res, err := a.run(ctx, "blocked", "publish", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed || res.Attempts != a.maxRetries+1 {
		t.Fatalf("result = %+v, want blocked after %d attempts", res, a.maxRetries+1)
	}
	if !strings.Contains(res.Reason, "approved") {
		t.Fatalf("reason %q should name the missing state", res.Reason)
	}
	if n, _ := a.logCount(ctx); n != 0 {
		t.Fatalf("blocked run wrote %d rows, want 0", n)
	}

	if err := a.approve(); err != nil {
		t.Fatal(err)
	}
	res, err = a.run(ctx, "approved", "publish", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || res.Step != "publish_note" {
		t.Fatalf("result = %+v, want publish_note committed", res)
	}
	if n, _ := a.logCount(ctx); n != 1 {
		t.Fatalf("log rows = %d, want 1", n)
	}
}

func TestModelCannotApproveItself(t *testing.T) {
	f := newFakeOllama(t, `{"step":"human_approve","note":"I approve my own work."}`)
	a := newTestAgent(t, f.srv.URL)
	if _, err := a.wf.CheckAndCommitIdempotent(a.trace, "draft_note", "seed"); err != nil {
		t.Fatal(err)
	}
	res, err := a.run(context.Background(), "self", "approve", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed {
		t.Fatalf("model approved its own work: %+v", res)
	}
	if err := a.wf.CheckSafety(a.trace, "publish_note"); err == nil {
		t.Fatal("publish_note became allowed without a human approval")
	}
}

func TestDemoWithScriptedModel(t *testing.T) {
	f := newFakeOllama(t,
		`{"step":"draft_note","note":"Rollback by re-pointing the release symlink."}`,
		`{"step":"publish_note","note":"publish"}`,
		`{"step":"publish_note","note":"publish"}`,
		`{"step":"publish_note","note":"publish"}`,
		`{"step":"publish_note","note":"Release note published."}`,
	)
	var out strings.Builder
	if err := demo(context.Background(), f.srv.URL, "test-chat", "test-embed", &out); err != nil {
		t.Fatalf("demo: %v\n%s", err, out.String())
	}
	for _, want := range []string{"recall f3", "blocked by barrier", "committed=false rows before=1 after=1", `committed step "publish_note"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("demo output missing %q:\n%s", want, out.String())
		}
	}
}

// TestLiveGemma runs the full demo against a real Ollama with gemma3:4b and
// embeddinggemma. It is skipped unless JOLTRIN_OLLAMA_LIVE=1.
//
//	JOLTRIN_OLLAMA_LIVE=1 go test ./examples/gemma_ollama -run TestLiveGemma -v
func TestLiveGemma(t *testing.T) {
	if os.Getenv("JOLTRIN_OLLAMA_LIVE") != "1" {
		t.Skip("set JOLTRIN_OLLAMA_LIVE=1 to run against a local Ollama")
	}
	baseURL := os.Getenv("OLLAMA_HOST_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	var out strings.Builder
	if err := demo(context.Background(), baseURL, "gemma3:4b", "embeddinggemma", &out); err != nil {
		t.Fatalf("demo: %v\n%s", err, out.String())
	}
	t.Log("\n" + out.String())
	if !strings.Contains(out.String(), "recall f3") {
		t.Errorf("rollback fact should be retrieved:\n%s", out.String())
	}
}
