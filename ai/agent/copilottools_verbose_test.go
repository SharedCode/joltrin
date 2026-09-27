package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
)

func TestToolSetVerboseUpdatesRunnerSession(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	rs := NewRunnerSession()
	ctx := context.WithValue(context.Background(), RunnerSessionKey, rs)

	msg, err := ag.toolSetVerbose(ctx, map[string]any{"verbose": true})
	if err != nil {
		t.Fatalf("toolSetVerbose() error = %v", err)
	}
	if !rs.Verbose {
		t.Fatalf("expected runner session verbosity to be enabled, got %q", msg)
	}
}

func TestHandleSlashCommandVerboseReportsActualState(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	rs := NewRunnerSession()
	ctx := context.WithValue(context.Background(), RunnerSessionKey, rs)

	handled, msg, err := ag.handleSlashCommand(ctx, "/verbose", nil)
	if err != nil {
		t.Fatalf("handleSlashCommand() error = %v", err)
	}
	if !handled {
		t.Fatal("expected /verbose to be handled locally")
	}
	if rs.Verbose {
		t.Fatalf("expected /verbose to leave runner session verbosity untouched, got response %q", msg)
	}
	if !strings.Contains(msg, "OFF") {
		t.Fatalf("expected verbose status to report OFF, got %q", msg)
	}
}

func TestHandleSlashCommandVerboseUsesEffectiveStateFromRunnerSession(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	rs := NewRunnerSession()
	rs.Verbose = true
	ctx := context.WithValue(context.Background(), RunnerSessionKey, rs)

	handled, msg, err := ag.handleSlashCommand(ctx, "/verbose", nil)
	if err != nil {
		t.Fatalf("handleSlashCommand() error = %v", err)
	}
	if !handled {
		t.Fatal("expected /verbose to be handled locally")
	}
	if !rs.Verbose {
		t.Fatalf("expected /verbose to preserve the runner session verbosity state, got response %q", msg)
	}
	if !strings.Contains(msg, "ON") {
		t.Fatalf("expected verbose status to report ON for the current runner session state, got %q", msg)
	}
}

func TestHandleSlashCommandVerboseIsGetterOnly(t *testing.T) {
	ag := NewCopilotAgent(Config{Verbose: true}, nil, nil)
	rs := NewRunnerSession()
	rs.Verbose = true
	ctx := context.WithValue(context.Background(), RunnerSessionKey, rs)

	before := rs.Verbose
	handled, msg, err := ag.handleSlashCommand(ctx, "/verbose", nil)
	if err != nil {
		t.Fatalf("handleSlashCommand() error = %v", err)
	}
	if !handled {
		t.Fatal("expected /verbose to be handled locally")
	}
	if rs.Verbose != before {
		t.Fatalf("expected /verbose to be getter-only and not mutate runner session verbosity, before=%t after=%t response=%q", before, rs.Verbose, msg)
	}
	if !strings.Contains(msg, "ON") {
		t.Fatalf("expected verbose status to report ON, got %q", msg)
	}
}
func TestCopilotAgentExecute_UsesRunnerSessionVerboseForToolRuntime(t *testing.T) {
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{"system": {}}, nil)
	rs := NewRunnerSession()
	rs.Verbose = true
	ctx := context.WithValue(context.Background(), RunnerSessionKey, rs)
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "system"})

	ag.registry.Register("echo_verbose", "", `{"type":"object"}`, wrapStringTool(func(ctx context.Context, args map[string]any) (string, error) {
		return strconv.FormatBool(effectiveVerbose(ctx)), nil
	}))

	out, err := ag.Execute(ctx, "echo_verbose", map[string]any{"database": "system"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out != "true" {
		t.Fatalf("expected runtime tool execution to see verbose=true, got %q", out)
	}
}

func TestHandleSlashCommandUnknownToolEscapesQuotesInMessage(t *testing.T) {
	ag := NewCopilotAgent(Config{}, nil, nil)
	ctx := context.Background()

	// No generator, so an unknown tool falls straight to the "no AI Copilot
	// available" branch instead of an LLM fallback. The tool name embeds a
	// single quote to prove it can't break out of the message's quoting.
	handled, msg, err := ag.handleSlashCommand(ctx, `/weird'tool arg1`, nil)
	if err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
	if !handled {
		t.Fatal("expected the unknown tool to be handled locally (no generator to fall back to)")
	}
	if !strings.Contains(msg, `"weird'tool"`) {
		t.Fatalf("expected the tool name to be safely quoted with escaping intact, got %q", msg)
	}
}
