package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/stretchr/testify/assert"
)

// TestDeferSilentOutput verifies that internal script compilation/execution
// stays silent. Streaming must happen only at the final public Execute boundary.
func TestDeferSilentOutput(t *testing.T) {
	// 1. Setup Streamer
	buf := &bytes.Buffer{}

	js := NewJSONStreamer(buf)
	js.SetSuppressStepStart(false)

	ctx := context.Background()
	ctx = context.WithValue(ctx, CtxKeyJSONStreamer, js)
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	// 2. Setup Engine
	engine := NewScriptEngine(NewScriptContext(), func(name string) (Database, error) {
		return nil, fmt.Errorf("db not supported")
	})

	// 3. Define Script with Defer
	script := []ScriptInstruction{
		{
			Op:   "defer",
			Args: map[string]any{"op": "assign", "value": "cleanup"},
		},
		{
			Op:   "assign",
			Args: map[string]any{"value": "real_work"},
		},
	}

	compiled, err := CompileScript(script)
	assert.NoError(t, err)

	// 4. Exec
	err = compiled(ctx, engine)
	assert.NoError(t, err)

	// 5. Analyze Output
	output := buf.String()
	t.Logf("Stream Output:\n%s", output)

	assert.Empty(t, output, "CompileScript should not emit stream output during internal execution")
	assert.NotContains(t, output, "\"Deferred\"", "Output should not contain 'Deferred' string")
	assert.NotContains(t, output, "D,e,f,e,r", "Output should not contain char-split Deferred")
}

// TestScriptReturnNilIsHandled verifies that if a script returns nil (e.g. by ending with defer),
// the toolExecuteScript wrapper returns a success message instead of "null".
func TestScriptReturnNilIsHandled(t *testing.T) {
	// Setup a minimal agent
	agent := &CopilotAgent{
		Config: Config{StubMode: false},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			return nil, nil // Return nil store for now, script doesn't use it
		},
	}

	ctx := context.Background()

	// Script: just defer. LastResult will be nil.
	// We use the JSON string representation of the script as that's what the tool accepts
	// Using a nested defer which returns nil (and adds another deferred op that won't run until outer finishes?)
	// Actually, if we just run a command that triggers my "Silent" fix, e.g. a script with just 'defer'.
	// In the previous run, the validation 'defer' -> 'assign clean' set LastResult='clean'.
	// So we need a command that returns nil. 'commit_tx' does that.
	// But commit_tx might fail if no tx.

	// Let's use 'defer' inside 'defer'.
	// The inner defer will return nil and schedule its task (which might not run immediately, but that's fine).
	// We just want the return value of execution to be nil.
	// Using 'return' without value, which returns nil inside the deferred task.
	script := []map[string]any{
		{
			"op": "defer",
			"args": map[string]any{
				"command": map[string]any{
					"op": "return",
				},
			},
		},
	}
	scriptBytes, _ := json.Marshal(script)
	scriptJSON := string(scriptBytes)

	args := map[string]any{
		"script": scriptJSON,
	}

	// Exec
	resp, err := agent.toolExecuteScript(ctx, args)
	assert.NoError(t, err)

	t.Logf("Response: %s", resp)

	// It should NOT be "null" which causes the "n,u,l,l" output in UI
	assert.NotEqual(t, "null", resp)

	// It should be the fallback success message
	assert.Contains(t, resp, "Script executed successfully")
}

// TestSerializeResultHandlesNil checks lower level serialization behavior
// to confirm that "null" string is indeed what happens when we serialize nil.
func TestSerializeResultHandlesNil(t *testing.T) {
	res, err := serializeResult(context.Background(), nil)
	assert.NoError(t, err)
	// json.Marshal(nil) -> "null"
	// This confirms WHY we needed the fix in toolExecuteScript
	assert.Equal(t, "null", res, "serializeResult(nil) produces 'null', creating the need for the upper-level fix")
}

// TestExplicitReturnNil checks if a top-level return op returning nil produces "null"
func TestExplicitReturnNil(t *testing.T) {
	agent := &CopilotAgent{
		Config: Config{StubMode: false},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			return nil, nil
		},
	}

	ctx := context.Background()

	script := []map[string]any{
		{
			"op": "return",
		},
	}
	scriptBytes, _ := json.Marshal(script)
	args := map[string]any{"script": string(scriptBytes)}

	resp, err := agent.toolExecuteScript(ctx, args)
	assert.NoError(t, err)

	t.Logf("Response: %s", resp)

	if resp == "null" {
		t.Fatal("Reproduced 'null' output for explicit return op")
	}
}

// MockCursor implements ScriptCursor for testing
type MockCursor struct {
	Items []any
	Index int
}

func (m *MockCursor) Next(ctx context.Context) (any, bool, error) {
	if m.Index >= len(m.Items) {
		return nil, false, nil
	}
	item := m.Items[m.Index]
	m.Index++
	return item, true, nil
}
func (m *MockCursor) Close() error { return nil }

// TestEmptyCursorReturnsEmptyList verifies that returning an empty cursor
// produces "[]" (empty list) instead of "null" (which breaks the UI).
func TestEmptyCursorReturnsEmptyList(t *testing.T) {
	emptyCursor := &MockCursor{Items: []any{}}
	res, err := serializeResult(context.Background(), emptyCursor)
	assert.NoError(t, err)

	t.Logf("Result: %s", res)

	if res == "null" {
		t.Fatal("Empty cursor serialized to 'null' instead of '[]'")
	}
	assert.Equal(t, "[]", res)
}

func TestResultReducerAppendAndWrite(t *testing.T) {
	reducer := newResultReducer()
	reducer.Append(map[string]any{"users.id": 7, "name": "alpha"})

	written := reducer.Write()
	assert.IsType(t, []any{}, written)
	assert.Len(t, written, 1)
}

func TestResultReducerCutoffAddsNotice(t *testing.T) {
	reducer := newResultReducer()
	for i := 0; i < 10; i++ {
		reducer.Append(map[string]any{"index": i})
	}

	written := reducer.Write()
	items, ok := written.([]any)
	assert.True(t, ok)
	assert.Len(t, items, defaultResultReducerCutoff+1)
	assert.Equal(t, fmt.Sprintf("Results were 10 and were cutoff after first %d rows. The UI already displayed these sample rows; do not repeat them in your answer.", defaultResultReducerCutoff), items[defaultResultReducerCutoff].(map[string]any)["_result_reducer_notice"])
}

func TestResultEmitterUsesReducerOnBufferedPath(t *testing.T) {
	emitter := NewResultEmitter(context.Background())
	emitter.Start()
	for i := 0; i < 10; i++ {
		emitter.Emit(map[string]any{"index": i})
	}

	out := emitter.Finalize()
	var items []any
	assert.NoError(t, json.Unmarshal([]byte(out), &items))
	assert.Len(t, items, defaultResultReducerCutoff+1)

	notice, ok := items[defaultResultReducerCutoff].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, fmt.Sprintf("Results were 10 and were cutoff after first %d rows. The UI already displayed these sample rows; do not repeat them in your answer.", defaultResultReducerCutoff), notice["_result_reducer_notice"])
}

type recordingStreamer struct {
	items []any
}

func (r *recordingStreamer) BeginArray()                     {}
func (r *recordingStreamer) SetMetadata(meta map[string]any) {}
func (r *recordingStreamer) WriteItem(item any)              { r.items = append(r.items, item) }
func (r *recordingStreamer) EndArray()                       {}

func TestSerializeResultUsesResultStreamerForCursorRows(t *testing.T) {
	streamer := &recordingStreamer{}
	ctx := context.WithValue(context.Background(), ai.CtxKeyResultStreamer, streamer)

	res, err := serializeResult(ctx, &MockCursor{Items: []any{map[string]any{"id": 1}, map[string]any{"id": 2}}})
	assert.NoError(t, err)
	assert.JSONEq(t, `[{"id":1},{"id":2}]`, res)
	assert.Len(t, streamer.items, 2)
}

func TestSerializeResultStreamsListResultsToResultStreamer(t *testing.T) {
	streamer := &recordingStreamer{}
	ctx := context.WithValue(context.Background(), ai.CtxKeyResultStreamer, streamer)

	res, err := serializeResult(ctx, []any{map[string]any{"id": 1}, map[string]any{"id": 2}})
	assert.NoError(t, err)
	assert.JSONEq(t, `[{"id":1},{"id":2}]`, res)
	assert.Len(t, streamer.items, 2)
}

func TestStreamReturnCursorResult_UsesLiveStreamer(t *testing.T) {
	var eventTypes []string
	var eventPayloads []any

	ctx := context.WithValue(context.Background(), ai.CtxKeyEventStreamer, func(eventType string, payload any) {
		eventTypes = append(eventTypes, eventType)
		eventPayloads = append(eventPayloads, payload)
	})

	streamed, err := streamReturnCursorResult(ctx, &MockCursor{Items: []any{map[string]any{"id": 1}}})
	assert.NoError(t, err)
	assert.True(t, streamed)
	assert.Contains(t, eventTypes, "result_stream")
	assert.Contains(t, eventTypes, "record")
	assert.NotEmpty(t, eventPayloads)
}

func TestResolveResultStreamerUsesEventStreamer(t *testing.T) {
	var eventTypes []string
	var eventPayloads []any

	ctx := context.WithValue(context.Background(), ai.CtxKeyEventStreamer, func(eventType string, payload any) {
		eventTypes = append(eventTypes, eventType)
		eventPayloads = append(eventPayloads, payload)
	})

	streamer, closeable := resolveResultStreamer(ctx)
	if assert.NotNil(t, streamer) {
		streamer.BeginArray()
		streamer.WriteItem(map[string]any{"id": 1})
		streamer.EndArray()
	}
	if closeable != nil {
		closeable.Close()
	}

	assert.Contains(t, eventTypes, "record")

	for _, payload := range eventPayloads {
		if item, ok := payload.(map[string]any); ok && item["id"] == 1 {
			assert.Equal(t, map[string]any{"id": 1}, item)
			return
		}
	}

	t.Fatalf("expected record payload containing id=1, got %#v", eventPayloads)
}
