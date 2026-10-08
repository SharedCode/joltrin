// Package main runs a fully local agent loop: Gemma (through Ollama) proposes
// actions, and Joltrin retrieves context, enforces workflow ordering, and
// commits state, all in one Go process.
//
// The model only proposes. Every action goes through three gates before it can
// change state:
//
//  1. App schema check: valid JSON, known fields, an allowed step, bounded payload.
//  2. verify barrier: the step's preconditions and the safety rules must hold.
//  3. ACID transaction: the write and its embedding commit together or not at all.
//
// The model is never offered human_approve. Only the application calls it, so a
// model cannot approve its own work.
//
// Prerequisites and run:
//
//	ollama pull gemma3:4b && ollama pull embeddinggemma
//	go run ./examples/gemma_ollama
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/vector"
	sop "github.com/sharedcode/joltrin/v5"
	coredb "github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/v5/verify"
)

const (
	factsStore = "facts"
	logStore   = "agent_log"

	maxNoteBytes = 2000
	systemPrompt = `You are a release assistant. Reply with JSON only, in this exact shape:
{"step": "<draft_note|publish_note>", "note": "<text>"}
Use draft_note to write a note. Use publish_note only when the task says to publish.`
)

// ollamaChat is a minimal client for Ollama's /api/chat endpoint.
type ollamaChat struct {
	baseURL string
	model   string
	http    *http.Client
}

func (c *ollamaChat) chat(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":   c.model,
		"stream":  false,
		"format":  "json", // Ollama constrains the reply to valid JSON
		"options": map[string]any{"temperature": 0, "num_ctx": 4096},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return "", fmt.Errorf("ollama chat: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ollama chat: decode: %w", err)
	}
	return out.Message.Content, nil
}

// action is what the model is asked to produce.
type action struct {
	Step string `json:"step"`
	Note string `json:"note"`
}

// parseAction is the app-level schema check. It runs before the barrier and
// rejects anything the barrier should never have to reason about.
func parseAction(raw string, wf *verify.Workflow, allowed map[string]bool) (action, error) {
	var a action
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("malformed action: %w", err)
	}
	if dec.More() {
		return a, errors.New("malformed action: trailing data after JSON object")
	}
	if _, ok := wf.Steps[verify.StepID(a.Step)]; !ok {
		return a, fmt.Errorf("unknown step %q", a.Step)
	}
	if !allowed[a.Step] {
		return a, fmt.Errorf("step %q is not available to you", a.Step)
	}
	if n := len(a.Note); n == 0 || n > maxNoteBytes {
		return a, fmt.Errorf("note must be 1..%d bytes", maxNoteBytes)
	}
	return a, nil
}

// newWorkflow declares ordering invariants only. Payload validation lives in
// parseAction, not here.
func newWorkflow() (*verify.Workflow, error) {
	return verify.NewWorkflow(
		[]verify.Step{
			{ID: "draft_note", Establishes: []verify.State{"drafted"}},
			{ID: "human_approve", Requires: []verify.State{"drafted"}, Establishes: []verify.State{"approved"}},
			{ID: "publish_note", Requires: []verify.State{"approved"}, Establishes: []verify.State{"published"}},
		},
		[]verify.SafetyRule{
			{Name: "no-publish-without-approval", Forbidden: "published", Requires: "approved"},
		},
		[]verify.ReachabilityRule{
			{Name: "publish-stays-reachable", Target: "published"},
		},
	)
}

type agent struct {
	db         *database.Database
	embedder   *embed.OllamaEmbedder
	llm        *ollamaChat
	wf         *verify.Workflow
	trace      *verify.Trace
	allowed    map[string]bool // steps the model may propose
	maxRetries int
	out        io.Writer
}

// outcome reports how a run ended. Committed is false when every attempt was
// rejected, in which case nothing was written.
type outcome struct {
	Committed bool
	Step      string
	Attempts  int
	Reason    string // last rejection, empty when committed
}

func (a *agent) logf(format string, args ...any) { fmt.Fprintf(a.out, format+"\n", args...) }

// ingest embeds the facts first (slow, outside any lock), then writes them in a
// single transaction.
func (a *agent) ingest(ctx context.Context, facts map[string]string) error {
	ids := make([]string, 0, len(facts))
	texts := make([]string, 0, len(facts))
	for id, t := range facts {
		ids, texts = append(ids, id), append(texts, t)
	}
	vecs, err := a.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed facts: %w", err)
	}
	items := make([]ai.Item[map[string]any], len(ids))
	for i := range ids {
		items[i] = ai.Item[map[string]any]{ID: ids[i], Vector: vecs[i], Payload: map[string]any{"text": texts[i]}}
	}
	tx, err := a.db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	store, err := a.db.OpenVectorStore(ctx, factsStore, tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := store.UpsertBatch(ctx, items); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// recall returns the k facts closest to the query.
func (a *agent) recall(ctx context.Context, query string, k int) ([]ai.Hit[map[string]any], error) {
	vecs, err := a.embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	tx, err := a.db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, err
	}
	store, err := a.db.OpenVectorStore(ctx, factsStore, tx, vector.Config{})
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	hits, err := store.Query(ctx, vecs[0], k, nil)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return hits, tx.Commit(ctx)
}

// logCount reports how many actions have been committed.
func (a *agent) logCount(ctx context.Context) (int64, error) {
	tx, err := a.db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return 0, err
	}
	store, err := a.db.OpenVectorStore(ctx, logStore, tx, vector.Config{})
	if err != nil {
		_ = tx.Rollback(ctx)
		return 0, err
	}
	n, err := store.Count(ctx)
	if err != nil {
		_ = tx.Rollback(ctx)
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// approve is the human gate. Only the application calls it.
func (a *agent) approve() error {
	return a.wf.CheckAndCommit(a.trace, "human_approve")
}

// run asks the model for an action and retries on rejection. Nothing is written
// unless an action passes the schema check and the barrier.
func (a *agent) run(ctx context.Context, runID, task, knowledge string) (outcome, error) {
	user := "Context:\n" + knowledge + "\nTask: " + task
	var res outcome
	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		res.Attempts = attempt + 1
		raw, err := a.llm.chat(ctx, systemPrompt, user)
		if err != nil {
			return res, err
		}
		act, err := parseAction(raw, a.wf, a.allowed)
		if err != nil {
			res.Reason = err.Error()
			a.logf("  attempt %d rejected by schema check: %s", attempt+1, res.Reason)
			user += "\nYour last reply was rejected: " + res.Reason + ". Reply with valid JSON."
			continue
		}
		err = a.commit(ctx, act, fmt.Sprintf("%s-%d", runID, attempt))
		if err == nil {
			res.Committed, res.Step, res.Reason = true, act.Step, ""
			a.logf("  attempt %d committed step %q", attempt+1, act.Step)
			return res, nil
		}
		var v *verify.Violation
		if !errors.As(err, &v) {
			return res, err
		}
		res.Reason = a.wf.WhyBlocked(v)
		a.logf("  attempt %d blocked by barrier: %s", attempt+1, res.Reason)
		user += "\nBlocked: " + res.Reason + ". " + a.wf.NextAction(v)
	}
	return res, nil
}

// commit is the only path that changes state. Cheap rejections come first: the
// read-only barrier check runs before any embedding or transaction work.
func (a *agent) commit(ctx context.Context, act action, idemKey string) error {
	step := verify.StepID(act.Step)
	if err := a.wf.CheckSafety(a.trace, step); err != nil {
		return err
	}
	vecs, err := a.embedder.EmbedTexts(ctx, []string{act.Note})
	if err != nil {
		return fmt.Errorf("embed note: %w", err)
	}
	tx, err := a.db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	store, err := a.db.OpenVectorStore(ctx, logStore, tx, vector.Config{UsageMode: ai.Dynamic})
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := store.Upsert(ctx, ai.Item[map[string]any]{
		ID:      fmt.Sprintf("act-%d", time.Now().UnixNano()),
		Vector:  vecs[0],
		Payload: map[string]any{"step": act.Step, "note": act.Note},
	}); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// The data is durable; now record the step. The key makes a replayed commit
	// of the same attempt a no-op. If the process dies between the two commits
	// the row exists but the trace has no entry, which fails closed: later steps
	// stay blocked until the step is recorded again.
	_, err = a.wf.CheckAndCommitIdempotent(a.trace, step, idemKey)
	return err
}

func newAgent(db *database.Database, baseURL, chatModel, embedModel string, out io.Writer) (*agent, error) {
	wf, err := newWorkflow()
	if err != nil {
		return nil, err
	}
	return &agent{
		db:         db,
		embedder:   embed.NewOllama(baseURL, embedModel),
		llm:        &ollamaChat{baseURL: baseURL, model: chatModel, http: &http.Client{Timeout: 3 * time.Minute}},
		wf:         wf,
		trace:      verify.NewTrace(),
		allowed:    map[string]bool{"draft_note": true, "publish_note": true},
		maxRetries: 2,
		out:        out,
	}, nil
}

var releaseFacts = map[string]string{
	"f1": "Backups run nightly at 02:00 UTC and are retained for 30 days.",
	"f2": "Production deploys require two approvals and a green canary.",
	"f3": "Rollbacks are done by re-pointing the release symlink to the previous build.",
}

func main() {
	baseURL := flag.String("ollama", "http://localhost:11434", "Ollama base URL")
	chatModel := flag.String("chat-model", "gemma3:4b", "Gemma chat model")
	embedModel := flag.String("embed-model", "embeddinggemma", "embedding model (768 dims)")
	flag.Parse()

	if err := demo(context.Background(), *baseURL, *chatModel, *embedModel, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// demo runs three acts against a throwaway database:
//  1. Gemma drafts a note from retrieved facts: committed.
//  2. Gemma is told to publish before approval: the barrier blocks every attempt.
//  3. A human approves, then publishing succeeds.
func demo(ctx context.Context, baseURL, chatModel, embedModel string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	dir, err := os.MkdirTemp("", "joltrin-gemma-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db := database.NewDatabase(coredb.DatabaseOptions{StoresFolders: []string{dir}})

	a, err := newAgent(db, baseURL, chatModel, embedModel, out)
	if err != nil {
		return err
	}
	if err := a.ingest(ctx, releaseFacts); err != nil {
		return err
	}
	a.logf("ingested %d facts with %s", len(releaseFacts), embedModel)

	question := "How do I roll back a bad release?"
	hits, err := a.recall(ctx, question, 2)
	if err != nil {
		return err
	}
	var knowledge strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&knowledge, "- %v\n", h.Payload["text"])
		a.logf("recall %s score=%.3f", h.ID, h.Score)
	}

	a.logf("\nact 1: draft a note")
	r1, err := a.run(ctx, "act1", question+" Write the answer as a note.", knowledge.String())
	if err != nil {
		return err
	}
	if !r1.Committed {
		return fmt.Errorf("act 1: no valid action after %d attempts: %s", r1.Attempts, r1.Reason)
	}

	a.logf("\nact 2: publish before approval")
	before, err := a.logCount(ctx)
	if err != nil {
		return err
	}
	r2, err := a.run(ctx, "act2", "Publish the release note now.", knowledge.String())
	if err != nil {
		return err
	}
	after, err := a.logCount(ctx)
	if err != nil {
		return err
	}
	a.logf("  committed=%v rows before=%d after=%d", r2.Committed, before, after)
	if r2.Committed || after != before {
		return errors.New("act 2: barrier let an unapproved publish through")
	}

	a.logf("\nact 3: human approves, then publish")
	if err := a.approve(); err != nil {
		return err
	}
	r3, err := a.run(ctx, "act3", "Publish the release note now.", knowledge.String())
	if err != nil {
		return err
	}
	if !r3.Committed || r3.Step != "publish_note" {
		return fmt.Errorf("act 3: expected publish_note to commit, got %+v", r3)
	}
	n, err := a.logCount(ctx)
	if err != nil {
		return err
	}
	a.logf("  log rows: %d", n)
	return nil
}
