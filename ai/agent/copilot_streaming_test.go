package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
)

func TestCopilotAgent_DelegateToReasoningEngineStreamsToolEvents(t *testing.T) {
	a := &CopilotAgent{
		Config: Config{
			Verbose: true,
		},
		registry: NewRegistry(),
		databases: map[string]sop.DatabaseOptions{
			"dev": {},
		},
	}

	a.registry.Register("execute_script", "run a script", "{}", wrapStringTool(func(ctx context.Context, args map[string]any) (string, error) {
		return `[{"name":"John Doe"}]`, nil
	}))

	var events []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyEventStreamer, func(eventType string, data any) {
		events = append(events, eventType)
	})
	ctx = context.WithValue(ctx, ai.CtxKeyExecutor, a)
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev"})

	_, _, _, _, _, err := a.delegateToReasoningEngine(ctx, "show me users", &loopMockGenerator{}, "system prompt")
	if err != nil {
		t.Fatalf("delegateToReasoningEngine failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected streamed reasoning events, got none")
	}
}
