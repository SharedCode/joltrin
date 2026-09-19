package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai"
)

type loopMockGenerator struct {
	calls int
}

func (m *loopMockGenerator) Name() string { return "loop_mock" }

func (m *loopMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *loopMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *loopMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		if strings.Contains(prompt, "Tool results:") {
			return ai.GenOutput{Text: "unexpected tool results on first pass"}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}

	checks := []string{
		"Ask-anchored MRU:",
		"- Last tool: execute_script",
		"- Last outcome: tool_completed",
		"- Confirmed: execute_script returned:",
		"Tool results:",
		"[System Tool Response]:",
		"Analyze the tool response",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			return ai.GenOutput{Text: "missing synthesis prompt context: " + check}, nil
		}
	}
	return ai.GenOutput{Text: "Final answer: Found John Doe in the database"}, nil
}

type toolTemperatureMockGenerator struct {
	temperatures []float32
	calls        int
}

func (m *toolTemperatureMockGenerator) Name() string { return "tool_temperature_mock" }

func (m *toolTemperatureMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *toolTemperatureMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *toolTemperatureMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.temperatures = append(m.temperatures, opts.Temperature)
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	return ai.GenOutput{Text: "Final answer"}, nil
}

type reActTurnStrategyMockGenerator struct {
	prompts []string
	options []ai.GenOptions
	calls   int
}

func (m *reActTurnStrategyMockGenerator) Name() string { return "react_turn_strategy_mock" }

func (m *reActTurnStrategyMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *reActTurnStrategyMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

type reActTurnStrategyMock struct{}

func (reActTurnStrategyMock) PrepareTurn(ctx context.Context, turn ai.ReActTurn) ai.ReActTurn {
	_ = ctx
	if turn.Iteration > 1 && len(turn.Options.ToolCallContinuations) > 0 {
		if turn.LoopState.Iteration != turn.Iteration {
			turn.Prompt = fmt.Sprintf("loop state iteration mismatch: %d vs %d", turn.LoopState.Iteration, turn.Iteration)
			return turn
		}
		if len(turn.LoopState.ToolCallContinuations) != len(turn.Options.ToolCallContinuations) {
			turn.Prompt = fmt.Sprintf("loop state continuation mismatch: %#v", turn.LoopState.ToolCallContinuations)
			return turn
		}
		turn.Prompt = "adapter prompt: continue with native tool state"
	}
	return turn
}

func (m *reActTurnStrategyMockGenerator) ReActTurnStrategy() ai.ReActTurnStrategy {
	return reActTurnStrategyMock{}
}

func (m *reActTurnStrategyMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.prompts = append(m.prompts, prompt)
	m.options = append(m.options, opts)
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	return ai.GenOutput{Text: "Final answer: adapter used"}, nil
}

type reActTurnStrategyMockExecutor struct{}

func (e *reActTurnStrategyMockExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	return `["ok"]`, nil
}

func (e *reActTurnStrategyMockExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

type ownedReActLoopMock struct{}

func (ownedReActLoopMock) Run(ctx context.Context, req ai.ReasoningRequest) (ai.ReasoningResponse, error) {
	_ = ctx
	_ = req
	return ai.ReasoningResponse{FinalText: "Final answer: provider owned loop used"}, nil
}

type ownedReActLoopMockGenerator struct{}

func (m *ownedReActLoopMockGenerator) Name() string { return "owned_react_loop_mock" }

func (m *ownedReActLoopMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *ownedReActLoopMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *ownedReActLoopMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	_ = ctx
	_ = prompt
	_ = opts
	return ai.GenOutput{Text: "unexpected default loop execution"}, nil
}

func (m *ownedReActLoopMockGenerator) ReActLoop() ai.ReActLoop {
	return ownedReActLoopMock{}
}

type directSlashExecutor struct {
	calledTool string
	calledArgs map[string]any
}

func (e *directSlashExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	_ = ctx
	e.calledTool = tool
	e.calledArgs = args
	if tool == "list_databases" {
		return "[\"dev_db\"]", nil
	}
	return "", fmt.Errorf("unknown tool: %s", tool)
}

func (e *directSlashExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	_ = ctx
	return []ai.ToolDefinition{{Name: "list_databases"}}, nil
}

type bypassReActPromptMockGenerator struct {
	calls int
}

func (m *bypassReActPromptMockGenerator) Name() string { return "bypass_react_prompt_mock" }

func (m *bypassReActPromptMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *bypassReActPromptMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

type bypassReActPromptMock struct{}

func (bypassReActPromptMock) ShouldBypassPrompt(turn ai.ReActTurn) bool {
	return turn.Iteration > 1 && len(turn.Options.ToolCallContinuations) > 0
}

func (bypassReActPromptMock) PrepareTurn(ctx context.Context, turn ai.ReActTurn) ai.ReActTurn {
	_ = ctx
	if turn.Iteration > 1 && turn.Prompt != "" {
		turn.Prompt = "unexpected synthetic prompt was built"
		return turn
	}
	if turn.Iteration > 1 {
		turn.Prompt = "bypassed prompt: continue with carried tool state"
	}
	return turn
}

func (m *bypassReActPromptMockGenerator) ReActTurnStrategy() ai.ReActTurnStrategy {
	return bypassReActPromptMock{}
}

func (m *bypassReActPromptMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	if prompt != "bypassed prompt: continue with carried tool state" {
		return ai.GenOutput{Text: "unexpected continuation prompt: " + prompt}, nil
	}
	return ai.GenOutput{Text: "Final answer: prompt build bypassed"}, nil
}

type recoverableTempMockGenerator struct {
	temperatures []float32
	calls        int
}

func (m *recoverableTempMockGenerator) Name() string { return "recoverable_temp_mock" }

func (m *recoverableTempMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *recoverableTempMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *recoverableTempMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.temperatures = append(m.temperatures, opts.Temperature)
	m.calls++
	if m.calls <= 2 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	return ai.GenOutput{Text: "Final answer"}, nil
}

type malformedFunctionCallRecoveryGenerator struct {
	calls int
}

func (m *malformedFunctionCallRecoveryGenerator) Name() string { return "malformed_function_call_mock" }

func (m *malformedFunctionCallRecoveryGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0
}

func (m *malformedFunctionCallRecoveryGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *malformedFunctionCallRecoveryGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{}, fmt.Errorf("no candidates returned from gemini; finish_reason=MALFORMED_FUNCTION_CALL")
	case 2:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last outcome: repair_required",
			"- Current focus: Preserve valid work, classify the failure, and change only the broken slice in the next call.",
			"- Next delta: Return exactly one valid tool call and avoid malformed function-call output.",
			"Model generation error:",
			"Retry instruction:",
			"Repair directive: The last model output produced an invalid native tool call.",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing malformed function call retry context: " + check}, nil
			}
		}
		if opts.Temperature != nativeReActRepairTemperature {
			return ai.GenOutput{Text: fmt.Sprintf("wrong retry temperature: %v", opts.Temperature)}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Found John Doe in the database"}, nil
	}
}

type loopMockExecutor struct{}

func (e *loopMockExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	return `[{"name":"John Doe"}]`, nil
}

func (e *loopMockExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

type textualToolCallRecoveryGenerator struct {
	calls int
}

func (m *textualToolCallRecoveryGenerator) Name() string { return "textual_tool_call_mock" }

func (m *textualToolCallRecoveryGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *textualToolCallRecoveryGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *textualToolCallRecoveryGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{Text: "Would you like me to rewrite and re-run the query with the correct scan operations to find John's orders over $500?\n\ncall:default_api:execute_script{\"script\":[{\"op\":\"scan\"}]}"}, nil
	}
	if !strings.Contains(prompt, "Tool results:") {
		return ai.GenOutput{Text: "missing tool result follow-up"}, nil
	}
	return ai.GenOutput{Text: "Final answer: Found John Doe in the database"}, nil
}

type truncatedTextualToolCallRecoveryGenerator struct {
	calls int
}

func (m *truncatedTextualToolCallRecoveryGenerator) Name() string {
	return "truncated_textual_tool_call_mock"
}

func (m *truncatedTextualToolCallRecoveryGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0
}

func (m *truncatedTextualToolCallRecoveryGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *truncatedTextualToolCallRecoveryGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{Text: "call:default_api:execute_script{\"script\":[{\"op\":\"scan\"}]"}, nil
	case 2:
		checks := []string{
			"Model generation error:",
			"Retry instruction: Return exactly one valid native tool call",
			"Repair directive: The last model output produced an invalid native tool call.",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing truncated textual call retry context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Found John Doe in the database"}, nil
	}
}

type recoverableLoopMockExecutor struct{ calls int }

func (e *recoverableLoopMockExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.calls++
	if e.calls == 1 {
		return "", fmt.Errorf("invalid type for filter condition field \"first_name\": got boolean placeholder true")
	}
	return `[{"name":"John Doe"}]`, nil
}

func (e *recoverableLoopMockExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

type nativeHintContextExecutor struct{ sawNativeHints bool }

func (e *nativeHintContextExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.sawNativeHints = ctx.Value(ai.CtxKeyNativeToolHints) == true
	return `[1]`, nil
}

func (e *nativeHintContextExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

func TestNativeReActEngine_SynthesizesAfterToolResult(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &loopMockGenerator{}
	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Found John Doe in the database" {
		t.Fatalf("expected synthesized final answer, got %q", resp.FinalText)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "execute_script" {
		t.Fatalf("expected recorded tool call, got %#v", resp.ToolCalls)
	}
	if gen.calls != 2 {
		t.Fatalf("expected two generator calls, got %d", gen.calls)
	}
}

func TestNativeReActEngine_UsesLowTemperatureForToolCalls(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &toolTemperatureMockGenerator{}
	_, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(gen.temperatures) == 0 || gen.temperatures[0] != nativeReActToolCallTemperature {
		t.Fatalf("expected first tool-call temperature %v, got %#v", nativeReActToolCallTemperature, gen.temperatures)
	}
}

func TestNativeReActEngine_UsesProviderReActTurnStrategyForContinuationTurns(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &reActTurnStrategyMockGenerator{}
	executor := &reActTurnStrategyMockExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Find users",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: adapter used" {
		t.Fatalf("expected ReAct-turn-strategy-backed final answer, got %q", resp.FinalText)
	}
	if len(gen.prompts) != 2 {
		t.Fatalf("expected two generator calls, got %#v", gen.prompts)
	}
	if gen.prompts[1] != "adapter prompt: continue with native tool state" {
		t.Fatalf("expected adapter prompt on continuation turn, got %q", gen.prompts[1])
	}
	if len(gen.options[1].ToolCallContinuations) != 1 {
		t.Fatalf("expected tool-call continuation to be preserved for strategy turn, got %#v", gen.options[1].ToolCallContinuations)
	}
}

func TestNativeReActEngine_DelegatesToOwnedReActLoopWhenProvided(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &ownedReActLoopMockGenerator{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Find users",
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: provider owned loop used" {
		t.Fatalf("expected provider-owned loop response, got %q", resp.FinalText)
	}
}

func TestNativeReActEngine_HandlesDirectSlashCommandBeforeDefaultLoop(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &loopMockGenerator{}
	executor := &directSlashExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "/list_databases",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if gen.calls != 0 {
		t.Fatalf("expected slash command to bypass generator, got %d calls", gen.calls)
	}
	if executor.calledTool != "list_databases" {
		t.Fatalf("expected slash command tool execution, got %q", executor.calledTool)
	}
	if resp.FinalText != "[\"dev_db\"]" {
		t.Fatalf("expected direct tool result, got %q", resp.FinalText)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_databases" {
		t.Fatalf("expected direct tool call to be recorded, got %#v", resp.ToolCalls)
	}
}

func TestNativeReActEngine_HandlesDirectSlashCommandBeforeOwnedLoop(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &ownedReActLoopMockGenerator{}
	executor := &directSlashExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "/list_databases",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if executor.calledTool != "list_databases" {
		t.Fatalf("expected slash command tool execution before owned loop, got %q", executor.calledTool)
	}
	if resp.FinalText != "[\"dev_db\"]" {
		t.Fatalf("expected direct tool result, got %q", resp.FinalText)
	}
}

func TestNativeReActEngine_BypassesSyntheticPromptWhenStrategyRequestsIt(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &bypassReActPromptMockGenerator{}
	executor := &reActTurnStrategyMockExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Find users",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: prompt build bypassed" {
		t.Fatalf("expected bypass-backed final answer, got %q", resp.FinalText)
	}
}

func TestNativeReActEngine_UsesZeroTemperatureForRepairRetry(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &recoverableTempMockGenerator{}
	_, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &recoverableLoopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(gen.temperatures) < 2 {
		t.Fatalf("expected at least two generation calls, got %#v", gen.temperatures)
	}
	if gen.temperatures[0] != nativeReActToolCallTemperature {
		t.Fatalf("expected initial tool-call temperature %v, got %#v", nativeReActToolCallTemperature, gen.temperatures)
	}
	if gen.temperatures[1] != nativeReActRepairTemperature {
		t.Fatalf("expected repair temperature %v, got %#v", nativeReActRepairTemperature, gen.temperatures)
	}
}

func TestFormatLLMGeneratedScriptForLog_ExecuteScript(t *testing.T) {
	got := formatLLMGeneratedScriptForLog("execute_script", map[string]any{
		"script": []any{map[string]any{"op": "scan"}, map[string]any{"op": "return"}},
	})
	if !strings.Contains(got, `{"op":"scan"}`) || !strings.Contains(got, `{"op":"return"}`) {
		t.Fatalf("expected execute_script log payload to include serialized script steps, got %q", got)
	}
}

func TestFormatLLMGeneratedScriptForLog_NonExecuteScript(t *testing.T) {
	if got := formatLLMGeneratedScriptForLog("list_stores", map[string]any{"stores": []any{"users"}}); got != "" {
		t.Fatalf("expected non-execute_script tool logs to omit script payload, got %q", got)
	}
}

func TestNativeReActEngine_MarksToolExecutionForNativeHints(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &toolTemperatureMockGenerator{}
	executor := &nativeHintContextExecutor{}

	_, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !executor.sawNativeHints {
		t.Fatal("expected native tool execution to carry the native hint context flag")
	}
}

func TestNativeReActEngine_RetriesRecoverableMalformedFunctionCall(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &malformedFunctionCallRecoveryGenerator{}
	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Found John Doe in the database" {
		t.Fatalf("expected recovered final answer, got %q", resp.FinalText)
	}
	if gen.calls != 3 {
		t.Fatalf("expected three generator calls after malformed function call recovery, got %d", gen.calls)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "execute_script" {
		t.Fatalf("expected recovered execute_script tool call to be recorded, got %#v", resp.ToolCalls)
	}
}

type csvLoopMockGenerator struct {
	calls int
}

func (m *csvLoopMockGenerator) Name() string { return "csv_loop_mock" }

func (m *csvLoopMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *csvLoopMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *csvLoopMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	return ai.GenOutput{Text: "| name |\n| John Doe |"}, nil
}

type csvLoopMockExecutor struct{}

func (e *csvLoopMockExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	return "name\nJohn Doe\n", nil
}

func (e *csvLoopMockExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

type csvMetaToolMockGenerator struct {
	calls int
}

func (m *csvMetaToolMockGenerator) Name() string { return "csv_meta_tool_mock" }

func (m *csvMetaToolMockGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *csvMetaToolMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *csvMetaToolMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "list_tools",
			Args: map[string]any{},
		}}}, nil
	}
	return ai.GenOutput{Text: "Final answer: Use execute_script for store queries."}, nil
}

type csvMetaToolMockExecutor struct{}

func (e *csvMetaToolMockExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	return "/list_tools\n/execute_script\n", nil
}

func (e *csvMetaToolMockExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "list_tools"}}, nil
}

func TestNativeReActEngine_PreservesCSVToolResult(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &csvLoopMockGenerator{}
	ctx := context.WithValue(context.Background(), ai.CtxKeyDefaultFormat, "csv")
	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &csvLoopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "name\nJohn Doe\n" {
		t.Fatalf("expected raw CSV tool result, got %q", resp.FinalText)
	}
	if gen.calls != 1 {
		t.Fatalf("expected one generator call for structured output fast path, got %d", gen.calls)
	}
}

func TestNativeReActEngine_DoesNotPreserveMetaToolResultForCSV(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &csvMetaToolMockGenerator{}
	ctx := context.WithValue(context.Background(), ai.CtxKeyDefaultFormat, "csv")
	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "How should I query the users store?",
		Executor:     &csvMetaToolMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Use execute_script for store queries." {
		t.Fatalf("expected synthesized answer, got %q", resp.FinalText)
	}
	if gen.calls != 2 {
		t.Fatalf("expected two generator calls when meta tool output should not be preserved, got %d", gen.calls)
	}
}

func TestNativeReActEngine_UsesSessionPayloadVerboseForRuntimeProgress(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &loopMockGenerator{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	_, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	want := []string{
		"Planning request with native multi-step loop.",
		"Reasoning iteration 1 of 3.",
		"Waiting for model response.",
		"Calling tool `execute_script`.",
		"Tool `execute_script` completed.",
		"Reasoning iteration 2 of 3.",
		"Waiting for model response.",
		"No further tools required; preparing final answer.",
	}
	if !reflect.DeepEqual(progress, want) {
		t.Fatalf("unexpected progress messages:\nwant=%#v\ngot=%#v", want, progress)
	}
}

func TestNativeReActEngine_RecoversPrintedTextualToolCall(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &textualToolCallRecoveryGenerator{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Find John orders over 500",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Found John Doe in the database" {
		t.Fatalf("expected recovered final answer, got %q", resp.FinalText)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "execute_script" {
		t.Fatalf("expected printed textual tool call to be recovered and executed, got %#v", resp.ToolCalls)
	}
}

func TestNativeReActEngine_RepairsTruncatedPrintedTextualToolCall(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &truncatedTextualToolCallRecoveryGenerator{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Find John orders over 500",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Found John Doe in the database" {
		t.Fatalf("expected repaired final answer, got %q", resp.FinalText)
	}
	if gen.calls != 3 {
		t.Fatalf("expected truncated printed call to trigger repair retry before final answer, got %d calls", gen.calls)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "execute_script" {
		t.Fatalf("expected repaired tool call to execute after truncated printed call, got %#v", resp.ToolCalls)
	}
}

func TestNativeReActEngine_RespectsVerboseFalse(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &loopMockGenerator{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: false})

	_, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(progress) != 0 {
		t.Fatalf("expected no progress messages when verbose=false, got %#v", progress)
	}
}

func TestEmitReasoningEvent_AlwaysStreamsWhenStreamerPresent(t *testing.T) {
	var events int
	emitReasoningEvent(ai.ReasoningRequest{
		Streamer: func(eventType string, data any) {
			events++
		},
		Verbose: false,
	}, ai.ReasoningEventToolResult, map[string]any{"tool": "execute_script"})

	if events != 1 {
		t.Fatalf("expected tool result events to stream even when verbose=false, got %d", events)
	}

	events = 0
	emitReasoningEvent(ai.ReasoningRequest{
		Streamer: func(eventType string, data any) {
			events++
		},
		Verbose: true,
	}, ai.ReasoningEventToolResult, map[string]any{"tool": "execute_script"})

	if events != 1 {
		t.Fatalf("expected tool result events to stream when verbose=true, got %d", events)
	}
}

func TestNativeReActEngine_StreamsStructuredToolEvents(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &loopMockGenerator{}

	type streamedEvent struct {
		eventType string
		payload   map[string]any
	}
	var events []streamedEvent

	_, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Show me users",
		Executor:     &loopMockExecutor{},
		Generator:    gen,
		Verbose:      true,
		Streamer: func(eventType string, data any) {
			payload, _ := data.(map[string]any)
			events = append(events, streamedEvent{eventType: eventType, payload: payload})
		},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 streamed tool lifecycle events, got %#v", events)
	}
	if events[0].eventType != "tool_call" {
		t.Fatalf("expected first event to be tool_call, got %#v", events[0])
	}
	if events[0].payload["tool"] != "execute_script" {
		t.Fatalf("expected execute_script tool call, got %#v", events[0].payload)
	}
	if events[1].eventType != "tool_result" {
		t.Fatalf("expected second event to be tool_result, got %#v", events[1])
	}
	if events[1].payload["tool"] != "execute_script" {
		t.Fatalf("expected execute_script tool result, got %#v", events[1].payload)
	}
	if events[1].payload["result"] != `[{"name":"John Doe"}]` {
		t.Fatalf("expected raw tool result in streamed payload, got %#v", events[1].payload)
	}
}

type recoverableArgErrorGenerator struct {
	calls int
}

func (m *recoverableArgErrorGenerator) Name() string { return "recoverable_arg_error_mock" }

func (m *recoverableArgErrorGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *recoverableArgErrorGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *recoverableArgErrorGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "mint_to_space",
			Args: map[string]any{},
		}}}, nil
	case 2:
		if !strings.Contains(prompt, "Tool execution error: argument 'content' is missing or not a string") {
			return ai.GenOutput{Text: "missing repair context"}, nil
		}
		if !strings.Contains(prompt, "Tool: mint_to_space") || !strings.Contains(prompt, "Retry instruction:") {
			return ai.GenOutput{Text: "missing structured retry context"}, nil
		}
		if !strings.Contains(prompt, "Repair directive: The last tool call to mint_to_space failed because its arguments were invalid.") {
			return ai.GenOutput{Text: "missing repair directive"}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "mint_to_space",
			Args: map[string]any{
				"kb_name": "Tasks",
				"content": "Task 1: Define scope\nTask 2: Assign owners",
			},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Added sample tasks to Tasks."}, nil
	}
}

type recoverableArgErrorExecutor struct {
	callCount int
}

func (e *recoverableArgErrorExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	content, _ := args["content"].(string)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("argument 'content' is missing or not a string")
	}
	return "Successfully minted content to Knowledge Base 'Tasks'.", nil
}

func (e *recoverableArgErrorExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "mint_to_space"}}, nil
}

func TestNativeReActEngine_RetriesRecoverableToolArgumentErrors(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &recoverableArgErrorGenerator{}
	executor := &recoverableArgErrorExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Generate sample tasks and add them to my Tasks space",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Added sample tasks to Tasks." {
		t.Fatalf("expected repaired final answer, got %q", resp.FinalText)
	}
	if gen.calls != 3 {
		t.Fatalf("expected three generator calls after recoverable tool error, got %d", gen.calls)
	}
	if executor.callCount != 2 {
		t.Fatalf("expected two tool execution attempts, got %d", executor.callCount)
	}
	if !containsProgressMessage(progress, "Tool `mint_to_space` needs corrected arguments; retrying.") {
		t.Fatalf("expected recoverable retry progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "mint_to_space" {
		t.Fatalf("expected only the successful repaired tool call to be recorded, got %#v", resp.ToolCalls)
	}
}

type delayedRepairGenerator struct {
	calls int
}

func (m *delayedRepairGenerator) Name() string { return "delayed_repair_mock" }

func (m *delayedRepairGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *delayedRepairGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *delayedRepairGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "mint_to_space",
			Args: map[string]any{},
		}}}, nil
	case 2:
		if !strings.Contains(prompt, "Retry instruction:") {
			return ai.GenOutput{Text: "missing initial repair guidance"}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "list_tools",
			Args: map[string]any{},
		}}}, nil
	case 3:
		if !strings.Contains(prompt, "Repair required before continuing.") {
			return ai.GenOutput{Text: "missing enforcement reminder"}, nil
		}
		if !strings.Contains(prompt, "The model attempted list_tools instead.") {
			return ai.GenOutput{Text: "missing attempted tool reminder"}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "mint_to_space",
			Args: map[string]any{
				"kb_name": "Tasks",
				"content": "Task 1: Define scope",
			},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Added sample task to Tasks."}, nil
	}
}

type delayedRepairExecutor struct {
	callCount int
	tools     []string
}

func (e *delayedRepairExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	e.tools = append(e.tools, tool)
	if tool == "list_tools" {
		return "mint_to_space, list_tools", nil
	}
	content, _ := args["content"].(string)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("argument 'content' is missing or not a string")
	}
	return "Successfully minted content to Knowledge Base 'Tasks'.", nil
}

func (e *delayedRepairExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "mint_to_space"}, {Name: "list_tools"}}, nil
}

func TestNativeReActEngine_RequiresRepairBeforeSwitchingTools(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &delayedRepairGenerator{}
	executor := &delayedRepairExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Generate sample tasks and add them to my Tasks space",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Added sample task to Tasks." {
		t.Fatalf("expected repaired final answer, got %q", resp.FinalText)
	}
	if gen.calls != 4 {
		t.Fatalf("expected four generator calls after enforced repair, got %d", gen.calls)
	}
	if executor.callCount != 2 {
		t.Fatalf("expected only failing and repaired tool executions, got %d", executor.callCount)
	}
	if !reflect.DeepEqual(executor.tools, []string{"mint_to_space", "mint_to_space"}) {
		t.Fatalf("expected only mint_to_space to execute, got %#v", executor.tools)
	}
	if !containsProgressMessage(progress, "Tool `mint_to_space` must be corrected before other actions.") {
		t.Fatalf("expected repair enforcement progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "mint_to_space" {
		t.Fatalf("expected only the successful repaired tool call to be recorded, got %#v", resp.ToolCalls)
	}
}

func TestFormatRecoverableToolError_IncludesValidationCategoryAndExample(t *testing.T) {
	err := newExecuteScriptValidationError(
		"invalid_filter_placeholder",
		"invalid type for filter condition field \"first_name\": got boolean placeholder true; expected an operator/value predicate",
		`{"op":"filter","args":{"condition":{"first_name":{"$eq":"<value>"}}}}`,
	)

	formatted := formatRecoverableToolError(pendingToolRepair{ToolName: "execute_script", Strategy: nativeRepairStrategySameTool}, map[string]any{
		"script": []any{map[string]any{"op": "filter"}},
	}, err)

	if !strings.Contains(formatted, "Repair category: invalid_filter_placeholder") {
		t.Fatalf("expected repair category in formatted error, got %q", formatted)
	}
	if !strings.Contains(formatted, "Suggested fix example:") {
		t.Fatalf("expected suggested fix example in formatted error, got %q", formatted)
	}
	if !strings.Contains(formatted, `"first_name":{"$eq":"<value>"}`) {
		t.Fatalf("expected example predicate in formatted error, got %q", formatted)
	}
}

func TestFormatRecoverableToolError_IncludesMultipleValidationIssues(t *testing.T) {
	err := collapseExecuteScriptValidationErrors([]*executeScriptValidationError{
		newExecuteScriptValidationError(
			"invalid_filter_input_shape",
			"filter input_var \"users_store\" resolves to an open_store handle",
			`{"op":"scan","args":{"store":"users_store"},"result_var":"users_cursor"}`,
		),
		newExecuteScriptValidationError(
			"invalid_filter_query_mismatch",
			"filter condition field \"total_amount\" uses scalar value 500 but the current query implies $gt 500",
			`{"op":"filter","args":{"condition":{"total_amount":{"$gt":500}}}}`,
		),
	})

	formatted := formatRecoverableToolError(pendingToolRepair{ToolName: "execute_script", Strategy: nativeRepairStrategySameTool}, map[string]any{
		"script": []any{map[string]any{"op": "filter"}},
	}, err)

	if !strings.Contains(formatted, "Repair categories: invalid_filter_input_shape, invalid_filter_query_mismatch") {
		t.Fatalf("expected aggregated repair categories in formatted error, got %q", formatted)
	}
	if !strings.Contains(formatted, "Suggested fix examples:") {
		t.Fatalf("expected aggregated suggested fix examples in formatted error, got %q", formatted)
	}
	if !strings.Contains(formatted, `{"op":"scan","args":{"store":"users_store"},"result_var":"users_cursor"}`) {
		t.Fatalf("expected scan repair example in formatted error, got %q", formatted)
	}
	if !strings.Contains(formatted, `"total_amount":{"$gt":500}`) {
		t.Fatalf("expected predicate repair example in formatted error, got %q", formatted)
	}
}

func TestAppendOrReplaceRetriedToolResult_ReplacesLatestSameTool(t *testing.T) {
	results := []nativeToolResult{
		{Name: "list_stores", Result: "grounded"},
		{Name: "execute_script", Result: "Retry instruction: fix first attempt"},
	}
	updated := appendOrReplaceRetriedToolResult(
		results,
		nativeToolResult{Name: "execute_script", Result: "ok"},
		&pendingToolRepair{ToolName: "execute_script", Strategy: nativeRepairStrategySameTool},
		"execute_script",
	)
	if len(updated) != 2 {
		t.Fatalf("expected replacement to preserve slice length, got %#v", updated)
	}
	if updated[0].Name != "list_stores" || updated[0].Result != "grounded" {
		t.Fatalf("expected unrelated tool result to remain intact, got %#v", updated)
	}
	if updated[1].Name != "execute_script" || updated[1].Result != "ok" {
		t.Fatalf("expected latest execute_script retry result to replace prior entry, got %#v", updated)
	}
}

func TestAppendOrReplaceRetriedToolResult_ExecuteScriptRetryDropsEarlierExecuteScriptEntries(t *testing.T) {
	results := []nativeToolResult{
		{Name: "list_stores", Result: "grounded users/orders"},
		{Name: "execute_script", Result: "first bad script"},
		{Name: "list_stores", Result: "grounded bridge relation"},
		{Name: "execute_script", Result: "second bad script"},
	}
	updated := appendOrReplaceRetriedToolResult(
		results,
		nativeToolResult{Name: "execute_script", Result: "final repaired script"},
		&pendingToolRepair{ToolName: "execute_script", Strategy: nativeRepairStrategySameTool},
		"execute_script",
	)
	if len(updated) != 3 {
		t.Fatalf("expected only non-execute_script entries plus the latest execute_script, got %#v", updated)
	}
	if updated[0].Name != "list_stores" || updated[1].Name != "list_stores" {
		t.Fatalf("expected research tool entries to remain, got %#v", updated)
	}
	if updated[2].Name != "execute_script" || updated[2].Result != "final repaired script" {
		t.Fatalf("expected only the latest execute_script entry to remain, got %#v", updated)
	}
}

type executeScriptRepairGenerator struct {
	calls int
}

func (m *executeScriptRepairGenerator) Name() string { return "execute_script_repair_mock" }

func (m *executeScriptRepairGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *executeScriptRepairGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *executeScriptRepairGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "filter",
						"args": map[string]any{
							"condition": map[string]any{"first_name": true},
						},
					},
				},
			},
		}}}, nil
	case 2:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last outcome: repair_required",
			"- Preserve: Reuse the valid structure from attempted args before changing invalid fields:",
			"- Next delta: Repair execute_script without restarting the whole plan or broadening scope.",
			"Tool: execute_script",
			"Repair category: invalid_filter_placeholder",
			"Suggested fix example:",
			`"first_name":{"$eq":"<value>"}`,
			"Repair directive: The last tool call to execute_script failed because its arguments were invalid.",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing execute_script retry context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "filter",
						"args": map[string]any{
							"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}},
						},
					},
				},
			},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Found matching records for John."}, nil
	}
}

type executeScriptRepairExecutor struct {
	callCount int
}

func (e *executeScriptRepairExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	if e.callCount == 1 {
		return "", newExecuteScriptValidationError(
			"invalid_filter_placeholder",
			"invalid type for filter condition field \"first_name\": got boolean placeholder true; expected an operator/value predicate",
			`{"op":"filter","args":{"condition":{"first_name":{"$eq":"<value>"}}}}`,
		)
	}
	return `[{"first_name":"John"}]`, nil
}

func (e *executeScriptRepairExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

func TestNativeReActEngine_RetriesExecuteScriptWithValidationGuidance(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &executeScriptRepairGenerator{}
	executor := &executeScriptRepairExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Find users named John",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Found matching records for John." {
		t.Fatalf("expected repaired final answer, got %q", resp.FinalText)
	}
	if gen.calls != 3 {
		t.Fatalf("expected three generator calls after execute_script repair, got %d", gen.calls)
	}
	if executor.callCount != 2 {
		t.Fatalf("expected two execute_script attempts, got %d", executor.callCount)
	}
	if !containsProgressMessage(progress, "Tool `execute_script` needs corrected arguments; retrying.") {
		t.Fatalf("expected execute_script retry progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "execute_script" {
		t.Fatalf("expected only the successful repaired execute_script call to be recorded, got %#v", resp.ToolCalls)
	}
}

type executeScriptJoinRepairGenerator struct {
	calls int
}

func (m *executeScriptJoinRepairGenerator) Name() string { return "execute_script_join_repair_mock" }

func (m *executeScriptJoinRepairGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *executeScriptJoinRepairGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *executeScriptJoinRepairGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "join",
						"args": map[string]any{
							"store": "users_orders",
							"on":    map[string]any{"users.key": true},
						},
					},
				},
			},
		}}}, nil
	case 2:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last outcome: repair_required",
			"- Next delta: Research missing schema or relation facts with scoped list_stores calls before retrying execute_script.",
			"Repair strategy: research_first",
			"Research reason:",
			"Join repair note: After list_stores confirms a relation path, prefer relation+target for relation-driven joins.",
			"- Preserve: Reuse the valid structure from attempted args before changing invalid fields:",
			"Tool: execute_script",
			"Repair category: invalid_join_on_placeholder",
			"Suggested fix example:",
			`"relation":"users_orders","target":"orders_store"`,
			"Repair directive: The last tool call to execute_script failed because grounded schema or relation facts are still missing.",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing execute_script join retry context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "list_stores",
			Args: map[string]any{
				"stores": []any{"users", "users_orders"},
			},
		}}}, nil
	case 3:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last tool: list_stores",
			"- Last outcome: tool_completed",
			"- Confirmed: list_stores confirmed users schema=key:string, first_name:string",
			"- Confirmed: list_stores confirmed users relations=[users_orders(key->users.key)]",
			"- Confirmed: list_stores confirmed users_orders schema=key:string, user_id:string",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing post-research context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "join",
						"args": map[string]any{
							"relation": "users_orders",
							"target":   "orders_store",
						},
					},
				},
			},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Joined users with users_orders successfully."}, nil
	}
}

type executeScriptJoinRepairExecutor struct {
	callCount int
}

func (e *executeScriptJoinRepairExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	if tool == "list_stores" {
		return "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]\nusers_orders schema=key:string, user_id:string", nil
	}
	if e.callCount == 1 {
		return "", newExecuteScriptValidationError(
			"invalid_join_on_placeholder",
			"invalid type for join.on[\"users.key\"]: got boolean placeholder true; expected a field path string such as \"key\"",
			`{"op":"join","args":{"relation":"users_orders","target":"orders_store"}}`,
		)
	}
	return `[{"users.key":"u1","users_orders.value":"o1"}]`, nil
}

func (e *executeScriptJoinRepairExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}, {Name: "list_stores"}}, nil
}

type discoveryClarificationGenerator struct {
	calls int
}

func (m *discoveryClarificationGenerator) Name() string { return "discovery_clarification_mock" }

func (m *discoveryClarificationGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *discoveryClarificationGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *discoveryClarificationGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "join",
						"args": map[string]any{
							"store": "users_orders",
							"on":    map[string]any{"users.key": true},
						},
					},
				},
			},
		}}}, nil
	case 2:
		if !strings.Contains(prompt, "Repair directive: The last tool call to execute_script failed because grounded schema or relation facts are still missing.") {
			return ai.GenOutput{Text: "missing research repair directive"}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "list_stores",
			Args: map[string]any{"stores": []any{"users", "users_orders"}},
		}}}, nil
	case 3:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "join",
						"args": map[string]any{
							"store": "users_orders",
							"on":    map[string]any{"users.key": "user_id"},
						},
					},
				},
			},
		}}}, nil
	default:
		if !strings.Contains(prompt, "Clarification directive: The last tool failure still remains unresolved after the first repair attempt.") {
			return ai.GenOutput{Text: "missing clarification directive"}, nil
		}
		if !strings.Contains(prompt, "Clarification required:") {
			return ai.GenOutput{Text: "missing clarification result context"}, nil
		}
		checks := []string{
			"Repair category: invalid_join_on_placeholder",
			"Suggested fix example:",
			`"relation":"users_orders","target":"orders_store"`,
			"Join repair note: The researched relation still does not fully resolve this join. Ask for the missing join mapping instead of inventing a new one.",
			`"on": {`,
			`"users.key": "user_id"`,
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing clarification detail: " + check}, nil
			}
		}
		return ai.GenOutput{Text: "Which join mapping should I use between users and users_orders: users.key -> users_orders.user_id, or a different relation?"}, nil
	}
}

type discoveryClarificationExecutor struct {
	callCount int
	tools     []string
}

func (e *discoveryClarificationExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	e.tools = append(e.tools, tool)
	if tool == "list_stores" {
		return "users schema=key:string, first_name:string relations=[users_orders(user_id->users.key)]\nusers_orders schema=key:string, user_id:string", nil
	}
	return "", newExecuteScriptValidationError(
		"invalid_join_on_placeholder",
		"invalid join mapping: users.key -> users_orders.user_id is still ambiguous for this ask; expected a confirmed relation mapping before proceeding",
		`{"op":"join","args":{"relation":"users_orders","target":"orders_store"}}`,
	)
}

func (e *discoveryClarificationExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}, {Name: "list_stores"}}, nil
}

type routedSameToolClarificationGenerator struct {
	calls int
}

func (m *routedSameToolClarificationGenerator) Name() string {
	return "routed_same_tool_clarification_mock"
}

func (m *routedSameToolClarificationGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0
}

func (m *routedSameToolClarificationGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *routedSameToolClarificationGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "mint_to_space",
			Args: map[string]any{},
		}}}, nil
	case 2:
		if !strings.Contains(prompt, "Repair directive: The last tool call to mint_to_space failed because its arguments were invalid.") {
			return ai.GenOutput{Text: "missing same-tool repair directive"}, nil
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "mint_to_space",
			Args: map[string]any{"kb_name": "Tasks"},
		}}}, nil
	default:
		if !strings.Contains(prompt, "Clarification directive: The last tool failure still remains unresolved after the first repair attempt.") {
			return ai.GenOutput{Text: "missing clarification directive"}, nil
		}
		if !strings.Contains(prompt, "Clarification required:") {
			return ai.GenOutput{Text: "missing clarification result context"}, nil
		}
		return ai.GenOutput{Text: "What content should I mint into the Tasks space?"}, nil
	}
}

func TestNativeReActEngine_RoutedSameToolRepairEscalatesToClarificationAfterFirstRetry(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &routedSameToolClarificationGenerator{}
	executor := &recoverableArgErrorExecutor{}
	var progress []string
	payload := &ai.SessionPayload{Variables: map[string]any{
		"RoutingState": &TaskContextClassification{RoutingGate: RoutingGateFocused, Domain: SpacesDomain},
	}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})
	ctx = context.WithValue(ctx, ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Add content to my Tasks space",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "What content should I mint into the Tasks space?" {
		t.Fatalf("expected clarification question, got %q", resp.FinalText)
	}
	if gen.calls != 3 {
		t.Fatalf("expected three generator calls before clarification handoff, got %d", gen.calls)
	}
	if executor.callCount != 2 {
		t.Fatalf("expected two mint_to_space attempts before clarification, got %d", executor.callCount)
	}
	if !containsProgressMessage(progress, "Repair remained unresolved after the first retry; switching to clarification.") {
		t.Fatalf("expected routed same-tool clarification escalation progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("expected no successful tool calls to be recorded, got %#v", resp.ToolCalls)
	}
}

func TestNativeReActEngine_RetriesExecuteScriptJoinWithValidationGuidance(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &executeScriptJoinRepairGenerator{}
	executor := &executeScriptJoinRepairExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Join users with users_orders",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Joined users with users_orders successfully." {
		t.Fatalf("expected repaired final answer, got %q", resp.FinalText)
	}
	if gen.calls != 4 {
		t.Fatalf("expected four generator calls after execute_script join research+repair, got %d", gen.calls)
	}
	if executor.callCount != 3 {
		t.Fatalf("expected execute_script failure, list_stores research, and execute_script retry, got %d calls", executor.callCount)
	}
	if !containsProgressMessage(progress, "Tool `execute_script` needs corrected arguments; retrying.") {
		t.Fatalf("expected execute_script retry progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Name != "list_stores" || resp.ToolCalls[1].Name != "execute_script" {
		t.Fatalf("expected list_stores research followed by repaired execute_script, got %#v", resp.ToolCalls)
	}
	if len(resp.OutcomeFacts) == 0 {
		t.Fatalf("expected grounded outcome facts to be returned, got %#v", resp.OutcomeFacts)
	}
	factsText := strings.Join(resp.OutcomeFacts, "\n")
	if !strings.Contains(factsText, "list_stores confirmed users schema=key:string, first_name:string") {
		t.Fatalf("expected outcome facts to carry confirmed store schema, got %#v", resp.OutcomeFacts)
	}
	if !strings.Contains(factsText, "list_stores confirmed users relations=[users_orders(key->users.key)]") {
		t.Fatalf("expected outcome facts to carry confirmed relation mapping, got %#v", resp.OutcomeFacts)
	}
}

func TestNativeReActEngine_RoutedAmbiguityEscalatesToClarificationAfterFirstRepairAttempt(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &discoveryClarificationGenerator{}
	executor := &discoveryClarificationExecutor{}
	var progress []string
	payload := &ai.SessionPayload{Variables: map[string]any{
		"RoutingState": &TaskContextClassification{RoutingGate: RoutingGateFocused, Domain: StoresDomain},
	}}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})
	ctx = context.WithValue(ctx, ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Join users with users_orders",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Which join mapping should I use between users and users_orders: users.key -> users_orders.user_id, or a different relation?" {
		t.Fatalf("expected clarification question, got %q", resp.FinalText)
	}
	if gen.calls != 4 {
		t.Fatalf("expected four generator calls before clarification handoff, got %d", gen.calls)
	}
	if executor.callCount != 3 {
		t.Fatalf("expected execute_script, list_stores, execute_script before clarification, got %d", executor.callCount)
	}
	if !containsProgressMessage(progress, "Repair remained unresolved after the first retry; switching to clarification.") {
		t.Fatalf("expected clarification escalation progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_stores" {
		t.Fatalf("expected only the successful research tool call to be recorded, got %#v", resp.ToolCalls)
	}
	if len(executor.tools) != 3 || executor.tools[0] != "execute_script" || executor.tools[1] != "list_stores" || executor.tools[2] != "execute_script" {
		t.Fatalf("expected routed clarification flow to attempt execute_script, research, then execute_script, got %#v", executor.tools)
	}
}

func TestNativeReActEngine_NonRoutedAmbiguityEscalatesToClarificationAfterFirstRepairAttempt(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &discoveryClarificationGenerator{}
	executor := &discoveryClarificationExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) {
		progress = append(progress, msg)
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Join users with users_orders",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Which join mapping should I use between users and users_orders: users.key -> users_orders.user_id, or a different relation?" {
		t.Fatalf("expected clarification question, got %q", resp.FinalText)
	}
	if gen.calls != 4 {
		t.Fatalf("expected four generator calls before clarification handoff, got %d", gen.calls)
	}
	if executor.callCount != 3 {
		t.Fatalf("expected execute_script, list_stores, execute_script before clarification, got %d", executor.callCount)
	}
	if !containsProgressMessage(progress, "Repair remained unresolved after the first retry; switching to clarification.") {
		t.Fatalf("expected clarification escalation progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_stores" {
		t.Fatalf("expected only the successful research tool call to be recorded, got %#v", resp.ToolCalls)
	}
	if len(executor.tools) != 3 || executor.tools[0] != "execute_script" || executor.tools[1] != "list_stores" || executor.tools[2] != "execute_script" {
		t.Fatalf("expected clarification flow to attempt execute_script, research, then execute_script, got %#v", executor.tools)
	}
}

type progressionHistoryJoinRepairGenerator struct {
	calls int
}

func (m *progressionHistoryJoinRepairGenerator) Name() string {
	return "progression_history_join_repair_mock"
}

func (m *progressionHistoryJoinRepairGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0
}

func (m *progressionHistoryJoinRepairGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *progressionHistoryJoinRepairGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "join",
						"args": map[string]any{
							"store": "users_orders",
							"on":    map[string]any{"users.key": true},
						},
					},
				},
			},
		}}}, nil
	case 2:
		checks := []string{
			"Progression history:",
			"\"tool\": \"execute_script\"",
			"\"ingredients\"",
			"\"repair_strategy\": \"research_first\"",
			"\"generated_call\"",
			"\"script_summary\"",
			"users.key-\\u003etrue",
			"\"progression\"",
			"\"retry_instruction\": \"Call list_stores first using stores:[\\\"users_orders\\\"] to research the missing schema or relation facts, then return to execute_script with corrected grounded arguments. Preserve valid arguments and do not restart the whole plan.\"",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing progression-history repair context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "list_stores",
			Args: map[string]any{"stores": []any{"users", "users_orders"}},
		}}}, nil
	case 3:
		checks := []string{
			"Progression history:",
			"\"tool\": \"list_stores\"",
			"\"tool_info\": \"list_stores\"",
			"\"envelope_hash\"",
			"\"generated_call\"",
			"\"stores\"",
			"users_orders",
			"\"status\": \"progressing\"",
			"\"suggested_next_tools\"",
			"\"execute_script\"",
			"users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
			"users_orders schema=key:string, user_id:string",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing progression-history grounded context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{
				"script": []any{
					map[string]any{
						"op": "join",
						"args": map[string]any{
							"store": "users_orders",
							"on":    map[string]any{"users.key": "key"},
						},
					},
				},
			},
		}}}, nil
	default:
		return ai.GenOutput{Text: "Final answer: Joined users with users_orders successfully from progression history."}, nil
	}
}

type progressionHistoryJoinRepairExecutor struct {
	callCount int
}

func (e *progressionHistoryJoinRepairExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	if tool == "list_stores" {
		result := "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]\nusers_orders schema=key:string, user_id:string"
		return wrapToolResultWithListStoresHint(result, []string{
			"users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
			"users_orders schema=key:string, user_id:string",
		}), nil
	}
	if e.callCount == 1 {
		return "", newExecuteScriptValidationError(
			"invalid_join_on_placeholder",
			"invalid type for join.on[\"users.key\"]: got boolean placeholder true; expected a field path string such as \"key\"",
			`{"op":"join","args":{"relation":"users_orders","target":"orders_store"}}`,
		)
	}
	return `[{"users.key":"u1","users_orders.value":"o1"}]`, nil
}

func (e *progressionHistoryJoinRepairExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}, {Name: "list_stores"}}, nil
}

func TestNativeReActEngine_UsesProgressionHistoryToRepairExecuteScriptJoin(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &progressionHistoryJoinRepairGenerator{}
	executor := &progressionHistoryJoinRepairExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Join users with users_orders",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: Joined users with users_orders successfully from progression history." {
		t.Fatalf("expected final answer from progression-history repair, got %q", resp.FinalText)
	}
	if gen.calls != 4 {
		t.Fatalf("expected four generator calls for repair, research, corrected execution, and synthesis; got %d", gen.calls)
	}
	if executor.callCount != 3 {
		t.Fatalf("expected execute_script failure, list_stores research, and execute_script retry, got %d calls", executor.callCount)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Name != "list_stores" || resp.ToolCalls[1].Name != "execute_script" {
		t.Fatalf("expected list_stores research followed by repaired execute_script, got %#v", resp.ToolCalls)
	}
	seenRecipe := false
	for _, recipe := range resp.OutcomeRecipes {
		if recipe.ID == "implicit.execute_script.research_then_retry" {
			seenRecipe = true
			break
		}
	}
	if !seenRecipe {
		t.Fatalf("expected research-then-retry recipe to be learned, got %#v", resp.OutcomeRecipes)
	}
}

func TestBuildAskAnchoredMRUState_TypesConfirmedStoreFacts(t *testing.T) {
	state := buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "list_stores",
		Result: "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]\nusers_orders schema=key:string, user_id:string",
	}})

	seenCategories := map[string]bool{}
	for _, item := range state {
		seenCategories[item.Category] = true
	}

	if !seenCategories[askMRUCategoryConfirmedStoreSchema+"_USERS"] {
		t.Fatalf("expected typed ask-scoped schema category, got %+v", state)
	}
	if !seenCategories[askMRUCategoryConfirmedStoreRelation+"_USERS__USERS_ORDERS__USERS_ORDERS_KEY__USERS_KEY"] {
		t.Fatalf("expected typed ask-scoped relations category, got %+v", state)
	}
	if !seenCategories[askMRUCategoryConfirmedStoreSchema+"_USERS_ORDERS"] {
		t.Fatalf("expected typed ask-scoped schema category for joined store, got %+v", state)
	}

	formatted := formatAskAnchoredMRUState(state)
	if !strings.Contains(formatted, "- Confirmed: list_stores confirmed users schema=key:string, first_name:string") {
		t.Fatalf("expected typed ask-scoped facts to preserve prompt rendering, got %s", formatted)
	}
	if !strings.Contains(formatted, "- Confirmed: list_stores confirmed users relations=[users_orders(key->users.key)]") {
		t.Fatalf("expected typed ask-scoped relation fact to preserve prompt rendering, got %s", formatted)
	}
	if !strings.Contains(formatted, "- Confirmed: list_stores confirmed users_orders schema=key:string, user_id:string") {
		t.Fatalf("expected typed ask-scoped joined-store schema fact to preserve prompt rendering, got %s", formatted)
	}
}

func TestBuildAskAnchoredMRUState_TypesExecuteScriptJoinSelection(t *testing.T) {
	state := buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "execute_script",
		Result: `[{"users.key":"u1","users_orders.value":"o1"}]`,
		Args: map[string]any{"script": []any{
			map[string]any{
				"op": "join",
				"args": map[string]any{
					"store": "users_orders",
					"on":    map[string]any{"users.key": "key"},
				},
			},
		}},
	}})

	seenCategories := map[string]bool{}
	for _, item := range state {
		seenCategories[item.Category] = true
	}

	if !seenCategories[askMRUCategoryConfirmedJoinSelection+"_JOIN__USERS_ORDERS__USERS_KEY__KEY"] {
		t.Fatalf("expected typed ask-scoped join-selection category, got %+v", state)
	}

	formatted := formatAskAnchoredMRUState(state)
	if !strings.Contains(formatted, "- Confirmed: execute_script confirmed join store=users_orders on=users.key->key") {
		t.Fatalf("expected ask-scoped join-selection fact to preserve prompt rendering, got %s", formatted)
	}
	if !strings.Contains(formatted, "- Confirmed: execute_script returned:") {
		t.Fatalf("expected generic execute_script result summary to remain present, got %s", formatted)
	}
}

func TestBuildAskAnchoredMRUState_TypesExecuteScriptFilterSelection(t *testing.T) {
	state := buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "execute_script",
		Result: `[{"first_name":"John"}]`,
		Args: map[string]any{"script": []any{
			map[string]any{
				"op": "filter",
				"args": map[string]any{
					"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}},
				},
			},
		}},
	}})

	seenCategories := map[string]bool{}
	for _, item := range state {
		seenCategories[item.Category] = true
	}

	if !seenCategories[askMRUCategoryConfirmedFilterSelection+"_FIRST_NAME___EQ"] {
		t.Fatalf("expected typed ask-scoped filter-selection category, got %+v", state)
	}

	formatted := formatAskAnchoredMRUState(state)
	if !strings.Contains(formatted, "- Confirmed: execute_script confirmed filter field=first_name op=$eq") {
		t.Fatalf("expected ask-scoped filter-selection fact to preserve prompt rendering, got %s", formatted)
	}
	if !strings.Contains(formatted, "- Confirmed: execute_script returned:") {
		t.Fatalf("expected generic execute_script result summary to remain present, got %s", formatted)
	}
}

func TestSummarizeOutcomeRecipes_ExtractsExecuteScriptRepairPatterns(t *testing.T) {
	recipes := summarizeOutcomeRecipes([]nativeToolResult{
		{
			Name:   "execute_script",
			Result: "Tool execution error: missing schema\nTool: execute_script\nRepair strategy: research_first\nAttempted args:\n{}\nRetry instruction: Call list_stores first and prefer scoped args like stores:[\"users\",\"orders\"] when likely targets are already known to research the missing schema or relation facts, then return to execute_script with corrected grounded arguments. Preserve valid arguments and do not restart the whole plan.",
		},
		{
			Name:   "list_stores",
			Result: "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "filter", "args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}},
			}},
			Result: `[ {"first_name":"John"} ]`,
		},
		{
			Name:   "execute_script",
			Result: "Tool execution error: invalid join\nTool: execute_script\nRepair strategy: same_tool\nAttempted args:\n{}\nRetry instruction: Return a corrected call for the same tool. Preserve valid arguments, fix invalid or missing arguments, and do not repeat the same malformed shape.",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: `[ {"users.key":"u1"} ]`,
		},
	})

	seen := map[string]bool{}
	for _, recipe := range recipes {
		seen[recipe.ID] = true
	}

	if !seen["implicit.execute_script.research_then_retry"] {
		t.Fatalf("expected research-first repair recipe, got %+v", recipes)
	}
	if !seen["implicit.execute_script.repair_in_place"] {
		t.Fatalf("expected in-place repair recipe, got %+v", recipes)
	}
	if len(recipes) != 2 {
		t.Fatalf("expected exactly 2 distinct recipes, got %+v", recipes)
	}
}

func TestDetectAskLoopProgress_TreatsNewRecipeAsProgressWithoutFreshHint(t *testing.T) {
	previousResults := []nativeToolResult{
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: `[ {"users.key":"u1"} ]`,
		},
	}

	currentResults := []nativeToolResult{
		{
			Name:   "execute_script",
			Result: "Tool execution error: invalid join\nTool: execute_script\nRepair strategy: same_tool\nAttempted args:\n{}\nRetry instruction: Return a corrected call for the same tool. Preserve valid arguments, fix invalid or missing arguments, and do not repeat the same malformed shape.",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: `[ {"users.key":"u1"} ]`,
		},
	}

	delta := detectAskLoopProgress(previousResults, currentResults)
	if !delta.Progressing {
		t.Fatalf("expected new learned recipe to count as progress, got %+v", delta)
	}
	if len(delta.NewFacts) != 0 {
		t.Fatalf("expected no new facts in this control case, got %+v", delta.NewFacts)
	}
	if len(delta.NewRecipes) != 1 || delta.NewRecipes[0] != "implicit.execute_script.repair_in_place" {
		t.Fatalf("expected in-place repair recipe delta, got %+v", delta.NewRecipes)
	}
	if delta.HintSignal != nil {
		t.Fatalf("expected no fresh hint signal in this control case, got %+v", delta.HintSignal)
	}

	budget := newAskLoopBudgetState()
	if budget.extendIfProgressing(delta) {
		t.Fatal("did not expect retry budget to extend from progress")
	}
	if budget.allowedIterations != nativeReActBaseToolIterations {
		t.Fatalf("expected budget to remain at %d, got %d", nativeReActBaseToolIterations, budget.allowedIterations)
	}
}

func TestEngineNative_HelperClassifiers(t *testing.T) {
	if !isRecoverableToolExecutionError(fmt.Errorf("missing required field")) {
		t.Fatal("expected missing/required message to be recoverable")
	}
	if isRecoverableToolExecutionError(fmt.Errorf("permission denied")) {
		t.Fatal("did not expect unrelated tool error to be recoverable")
	}
	if !isRecoverableGenerationError(fmt.Errorf("finish_reason=MALFORMED_FUNCTION_CALL")) {
		t.Fatal("expected malformed function call error to be recoverable")
	}
	if isRecoverableGenerationError(fmt.Errorf("rate limit exceeded")) {
		t.Fatal("did not expect unrelated generation error to be recoverable")
	}
}

func TestBuildNativeReActPrompt_IncludesRepairDirectiveVariants(t *testing.T) {
	baseReq := ai.ReasoningRequest{ContextText: "ctx", UserQuery: "query"}

	nativeCallPrompt := buildNativeReActPrompt(baseReq, []nativeToolResult{{
		Name:   "native_tool_call",
		Result: "Model generation error: bad call\nRetry instruction: Return exactly one valid native tool call.",
	}}, buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "native_tool_call",
		Result: "Model generation error: bad call\nRetry instruction: Return exactly one valid native tool call.",
	}}))
	if !strings.Contains(nativeCallPrompt, "Repair directive: The last model output produced an invalid native tool call.") {
		t.Fatalf("expected native tool call repair directive, got %s", nativeCallPrompt)
	}

	researchPrompt := buildNativeReActPrompt(baseReq, []nativeToolResult{{
		Name:   "execute_script",
		Result: "Tool execution error\nRepair strategy: research_first\nRetry instruction: Call list_stores first and prefer scoped args like stores:[\"users\",\"orders\"] when likely targets are already known",
	}}, buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "execute_script",
		Result: "Tool execution error\nRepair strategy: research_first\nRetry instruction: Call list_stores first and prefer scoped args like stores:[\"users\",\"orders\"] when likely targets are already known",
	}}))
	if !strings.Contains(researchPrompt, "call list_stores first") {
		t.Fatalf("expected research-first repair directive, got %s", researchPrompt)
	}

	sameToolPrompt := buildNativeReActPrompt(baseReq, []nativeToolResult{{
		Name:   "execute_script",
		Result: "Tool execution error\nRetry instruction: Return a corrected call for the same tool.",
	}}, buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "execute_script",
		Result: "Tool execution error\nRetry instruction: Return a corrected call for the same tool.",
	}}))
	if !strings.Contains(sameToolPrompt, "call the same tool again with corrected arguments") {
		t.Fatalf("expected same-tool repair directive, got %s", sameToolPrompt)
	}
}

func TestBuildNativeReActPrompt_ExposesToolArgsWithRetryContext(t *testing.T) {
	baseReq := ai.ReasoningRequest{ContextText: "ctx", UserQuery: "query"}
	prompt := buildNativeReActPrompt(baseReq, []nativeToolResult{
		{
			Name: "list_stores",
			Args: map[string]any{"database": "system", "store": "users"},
			Hint: &ai.ToolProgressHint{
				Status:             "progressing",
				CompletionDelta:    0.25,
				SuggestedNextTools: []string{"execute_script"},
				Missing:            []string{"join key mapping"},
			},
			Result: "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: "Tool execution error: invalid filter\nTool: execute_script\nAttempted args:\n{}\nRetry instruction: Return a corrected call for the same tool.",
		},
	}, buildAskAnchoredMRUState([]nativeToolResult{
		{
			Name: "list_stores",
			Args: map[string]any{"database": "system", "store": "users"},
			Hint: &ai.ToolProgressHint{
				Status:             "progressing",
				CompletionDelta:    0.25,
				SuggestedNextTools: []string{"execute_script"},
				Missing:            []string{"join key mapping"},
			},
			Result: "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: "Tool execution error: invalid filter\nTool: execute_script\nAttempted args:\n{}\nRetry instruction: Return a corrected call for the same tool.",
		},
	}))

	checks := []string{
		"Ask-anchored MRU:",
		"Progression history:",
		"\"ingredients\"",
		"\"progression\"",
		"\"tool_info\": \"list_stores\"",
		"\"envelope_hash\"",
		"\"generated_call\"",
		"\"result\"",
		"\"completion_delta\": 0.25",
		"- Suggested tool: execute_script",
		"- Missing: join key mapping",
		"Step 1 Tool: list_stores",
		"[Tool Args]:",
		"\"database\": \"system\"",
		"\"store\": \"users\"",
		"Step 2 Tool: execute_script",
		"\"script_summary\"",
		"\"step_count\": 1",
		"store=users_orders",
		"Retry instruction: Return a corrected call for the same tool.",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to include %q, got %s", check, prompt)
		}
	}
}

func TestFormatNativeProgressionHistory_UsesStructuredIngredientsCallAndResult(t *testing.T) {
	history := formatNativeProgressionHistory([]nativeToolResult{
		{
			Name: "list_stores",
			Args: map[string]any{"database": "system", "store": "users"},
			Hint: &ai.ToolProgressHint{
				Status:             "progressing",
				CompletionDelta:    0.25,
				Clues:              []string{"users_orders relation confirmed"},
				Missing:            []string{"join key mapping"},
				SuggestedNextTools: []string{"execute_script"},
			},
			Result: "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
		},
		{
			Name:   "execute_script",
			Args:   map[string]any{"script": []any{map[string]any{"op": "join"}}},
			Result: "Tool execution error: invalid join\nRepair strategy: research_first\nRetry instruction: Call list_stores first and prefer scoped args like stores:[\"users\",\"orders\"] when likely targets are already known",
		},
	})

	checks := []string{
		"\"step\": 1",
		"\"tool\": \"list_stores\"",
		"\"tool_info\": \"list_stores\"",
		"\"envelope_hash\"",
		"\"schema_snapshot\"",
		"\"name\": \"first_name\"",
		"\"type\": \"string\"",
		"\"progression\"",
		"\"status\": \"progressing\"",
		"\"completion_delta\": 0.25",
		"\"clues\"",
		"\"missing\"",
		"\"suggested_next_tools\"",
		"\"generated_call\"",
		"\"database\": \"system\"",
		"\"script_summary\"",
		"\"step_count\": 1",
		"\"result\": \"users schema=key:string, first_name:string relations=[users_orders(key-\\u003eusers.key)]",
		"\"repair_strategy\": \"research_first\"",
		"\"retry_instruction\": \"Call list_stores first and prefer scoped args like stores:[\\\"users\\\",\\\"orders\\\"] when likely targets are already known\"",
	}
	for _, check := range checks {
		if !strings.Contains(history, check) {
			t.Fatalf("expected progression history to include %q, got %s", check, history)
		}
	}
}

func TestExtractListStoresSchemaSnapshot_PreservesFieldTypes(t *testing.T) {
	snapshot := extractListStoresSchemaSnapshot("users schema=key:string, first_name:string, age:int relations=[users_orders(key->users.key)]\norders schema=key:string, total_amount:float64")
	if len(snapshot) != 2 {
		t.Fatalf("expected two store snapshots, got %#v", snapshot)
	}
	if snapshot[0]["store"] != "users" {
		t.Fatalf("expected first snapshot to be users, got %#v", snapshot[0])
	}
	fields, ok := snapshot[0]["fields"].([]map[string]string)
	if !ok {
		t.Fatalf("expected typed fields slice, got %#v", snapshot[0]["fields"])
	}
	if len(fields) != 3 {
		t.Fatalf("expected three parsed user fields, got %#v", fields)
	}
	if fields[1]["name"] != "first_name" || fields[1]["type"] != "string" {
		t.Fatalf("expected first_name:string, got %#v", fields[1])
	}
	relations, ok := snapshot[0]["relations"].([]string)
	if !ok || len(relations) != 1 || relations[0] != "users_orders(key->users.key)" {
		t.Fatalf("expected parsed relation, got %#v", snapshot[0]["relations"])
	}
}

func TestExtractListStoresSchemaSnapshot_PreservesBracedSchemaAndJSONRelations(t *testing.T) {
	result := "Stores:\n" +
		"orders schema={items: list, key: uuid, order_date: string, status: string, total_amount: number} relations=[{\"source_fields\":[\"key\"],\"target_store\":\"users_orders\",\"target_fields\":[\"value\"]}]\n" +
		"users schema={age: number, country: string, email: string, first_name: string, gender: string, key: uuid, last_name: string} relations=[{\"source_fields\":[\"age\"],\"target_store\":\"users_by_age\",\"target_fields\":[\"key\"]},{\"source_fields\":[\"key\"],\"target_store\":\"users_orders\",\"target_fields\":[\"key\"]}]\n" +
		"users_orders schema={key: uuid, value: uuid} description=\"Link table: UserID -> OrderID\" relations=[{\"source_fields\":[\"key\"],\"target_store\":\"users\",\"target_fields\":[\"key\"]},{\"source_fields\":[\"value\"],\"target_store\":\"orders\",\"target_fields\":[\"key\"]}]"

	snapshot := extractListStoresSchemaSnapshot(result)
	if len(snapshot) != 3 {
		t.Fatalf("expected three store snapshots, got %#v", snapshot)
	}
	ordersFields, ok := snapshot[0]["fields"].([]map[string]string)
	if !ok || len(ordersFields) != 5 {
		t.Fatalf("expected five parsed order fields, got %#v", snapshot[0]["fields"])
	}
	if ordersFields[0]["name"] != "items" || ordersFields[0]["type"] != "list" {
		t.Fatalf("expected items:list, got %#v", ordersFields[0])
	}
	usersRelations, ok := snapshot[1]["relations"].([]string)
	if !ok || len(usersRelations) != 2 {
		t.Fatalf("expected two parsed user relations, got %#v", snapshot[1]["relations"])
	}
	if usersRelations[0] != `{"source_fields":["age"],"target_store":"users_by_age","target_fields":["key"]}` {
		t.Fatalf("expected intact JSON relation object, got %#v", usersRelations[0])
	}
	bridgeFields, ok := snapshot[2]["fields"].([]map[string]string)
	if !ok || len(bridgeFields) != 2 {
		t.Fatalf("expected two parsed bridge fields, got %#v", snapshot[2]["fields"])
	}
	if bridgeFields[1]["name"] != "value" || bridgeFields[1]["type"] != "uuid" {
		t.Fatalf("expected value:uuid, got %#v", bridgeFields[1])
	}
}

func TestFormatNativePromptArgs_CompactsExecuteScriptPayload(t *testing.T) {
	formatted := formatNativePromptArgs(map[string]any{"script": []any{
		map[string]any{"op": "begin_tx", "args": map[string]any{"mode": "read"}, "result_var": "tx"},
		map[string]any{"op": "open_store", "args": map[string]any{"name": "users", "transaction": "tx"}, "result_var": "users_store"},
		map[string]any{"op": "filter", "args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "input_var": "users_store", "result_var": "john_users"},
	}}, false)

	checks := []string{
		"\"script_summary\"",
		"\"step_count\": 3",
		"\"ops\"",
		"begin_tx",
		"open_store",
		"filter",
		"1. begin_tx | mode=read | -\\u003e tx",
		"2. open_store | store=users, tx=tx | -\\u003e users_store",
		"3. filter | condition=first_name:$eq=John | input=users_store | -\\u003e john_users",
	}
	for _, check := range checks {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected compact args to include %q, got %s", check, formatted)
		}
	}
	if strings.Contains(formatted, "\"script\": [") {
		t.Fatalf("expected raw script array to be removed from prompt args, got %s", formatted)
	}
}

func TestFormatNativeProgressionHistory_KeepsAnchorAndLatestCompactsMiddle(t *testing.T) {
	history := formatNativeProgressionHistory([]nativeToolResult{
		{
			Name:   "list_stores",
			Args:   map[string]any{"stores": []any{"users", "users_orders"}},
			Result: "users schema=key:string, first_name:string relations=[users_orders(key->users.key)]",
		},
		{
			Name:   "execute_script",
			Args:   map[string]any{"script": []any{map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": true}}}}},
			Result: "Tool execution error: invalid join\nRetry instruction: Return a corrected call for the same tool.",
		},
		{
			Name:   "execute_script",
			Args:   map[string]any{"script": []any{map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}}}},
			Result: `[ {"users.key":"u1","users_orders.value":"o1"} ]`,
		},
	})

	checks := []string{
		"\"stores\"",
		"\"anchor_hash\"",
		"\"anchor_rendered\": false",
		"\"script_summary\"",
		"\"anchor_ref\"",
		"\"envelope_hash\"",
		"\"envelope_rendered\": true",
		"\"envelope\"",
		"\"tool_info\": \"execute_script\"",
		"users.key-\\u003etrue",
		"\"script\"",
		"\"users.key\": \"key\"",
	}
	for _, check := range checks {
		if !strings.Contains(history, check) {
			t.Fatalf("expected history to include %q, got %s", check, history)
		}
	}
}

func TestBuildNativeReActPrompt_CollapsesMiddleToolResults(t *testing.T) {
	prompt := buildNativeReActPrompt(ai.ReasoningRequest{ContextText: "ctx", UserQuery: "query"}, []nativeToolResult{
		{
			Name:   "list_stores",
			Args:   map[string]any{"stores": []any{"users"}},
			Result: "users schema=key:string, first_name:string",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": true}}},
			}},
			Result: "Tool execution error: invalid join\nRetry instruction: Return a corrected call for the same tool.",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: `[ {"users.key":"u1"} ]`,
		},
	}, buildAskAnchoredMRUState([]nativeToolResult{
		{
			Name:   "list_stores",
			Args:   map[string]any{"stores": []any{"users"}},
			Result: "users schema=key:string, first_name:string",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": true}}},
			}},
			Result: "Tool execution error: invalid join\nRetry instruction: Return a corrected call for the same tool.",
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: `[ {"users.key":"u1"} ]`,
		},
	}))

	checks := []string{
		"Step 1 Tool: list_stores",
		"[Tool Args]:",
		"Step 2 Tool: execute_script",
		"[Tool Summary]:",
		"\"response\": \"Tool execution error: invalid join",
		"\"script_summary\"",
		"Step 3 Tool: execute_script",
		"\"script\"",
		"\"users.key\": \"key\"",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to include %q, got %s", check, prompt)
		}
	}
	if strings.Contains(prompt, "Step 2 Tool: execute_script\n[Tool Args]:") {
		t.Fatalf("expected middle tool result to avoid detailed Tool Args block, got %s", prompt)
	}
}

func TestBuildNativeReActPrompt_EnforcesHardBudgetAndReportsFirstTrim(t *testing.T) {
	profile := PromptBudgetProfile{
		TotalChars: 1200,
		ComponentCharBudgets: map[PromptComponent]int{
			ComponentFocusedContext:        250,
			ComponentHistory:               180,
			ComponentUserQuery:             120,
			componentNativeAskAnchoredMRU:  180,
			componentNativeProgression:     320,
			componentNativeRepairDirective: 220,
			componentNativeToolResults:     320,
		},
		TrimPriorityLowToHigh: []PromptComponent{
			componentNativeAskAnchoredMRU,
			ComponentHistory,
			componentNativeProgression,
			ComponentFocusedContext,
			componentNativeToolResults,
			componentNativeRepairDirective,
			ComponentUserQuery,
		},
	}
	prompt, report := buildNativeReActPromptWithReport(ai.ReasoningRequest{
		ContextText: strings.Join([]string{
			"Focused execution context:",
			"- keep joins grounded in list_stores",
			"- preserve valid transaction flow",
			strings.Repeat("context detail ", 80),
		}, "\n"),
		HistoryText: strings.Repeat("history detail ", 120),
		UserQuery:   "Find orders for John over 500",
	}, []nativeToolResult{
		{
			Name:   "list_stores",
			Args:   map[string]any{"database": "system", "store": "users"},
			Result: strings.Repeat("users schema=key:string, first_name:string relations=[users_orders(key->users.key)]\n", 12),
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": true}}},
			}},
			Result: strings.Repeat("Tool execution error: invalid join\nRetry instruction: Return a corrected call for the same tool.\n", 15),
		},
		{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}},
			}},
			Result: strings.Repeat(`[ {"users.key":"u1","users_orders.value":"o1"} ]`, 20),
		},
	}, buildAskAnchoredMRUState([]nativeToolResult{
		{Name: "list_stores", Args: map[string]any{"database": "system", "store": "users"}, Result: strings.Repeat("users schema=key:string, first_name:string\n", 8)},
		{Name: "execute_script", Args: map[string]any{"script": []any{map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": true}}}}}, Result: strings.Repeat("Tool execution error: invalid join\nRetry instruction: Return a corrected call for the same tool.\n", 10)},
		{Name: "execute_script", Args: map[string]any{"script": []any{map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": "key"}}}}}, Result: strings.Repeat(`[ {"users.key":"u1"} ]`, 10)},
	}), profile)

	if len(prompt) > profile.TotalChars {
		t.Fatalf("expected prompt to honor hard budget, got %d chars: %s", len(prompt), prompt)
	}
	if report.OriginalTotalChars <= report.FinalTotalChars {
		t.Fatalf("expected budgeting to reduce prompt, got original=%d final=%d", report.OriginalTotalChars, report.FinalTotalChars)
	}
	if firstTrimmed := firstTrimmedPromptComponent(profile, report); firstTrimmed != componentNativeAskAnchoredMRU {
		t.Fatalf("expected ask-anchored MRU to trim first, got %q with report %+v", firstTrimmed, report)
	}
	checks := []string{
		"User Query: Find orders for John over 500",
		"Tool results:",
		"Progression history:",
		"[truncated]",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected budgeted prompt to retain %q, got %s", check, prompt)
		}
	}
}

func TestCompactNativePromptSectionText_PreservesSectionShape(t *testing.T) {
	text := strings.Join([]string{
		"Focused execution context:",
		"- stores: users, orders, users_orders",
		"- keep joins grounded in list_stores",
		"- avoid placeholder predicates",
		"- preserve valid transaction flow",
		"- extra detail that can be compacted",
		"Recipe notes:",
		"- list_stores before execute_script when relations are ambiguous",
		"- reuse confirmed join mappings",
		"- preserve valid args on repair",
		"- another detail that can be compacted",
	}, "\n")

	formatted := compactNativePromptSectionText(text, 220)
	checks := []string{
		"Focused execution context:",
		"- stores: users, orders, users_orders",
		"- keep joins grounded in list_stores",
		"Recipe notes:",
		"- list_stores before execute_script when relations are ambiguous",
	}
	for _, check := range checks {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected structured compact context to preserve %q, got %s", check, formatted)
		}
	}
	if len(formatted) > 220 {
		t.Fatalf("expected structured compact context to stay within budget, got %d chars: %s", len(formatted), formatted)
	}
	if strings.Contains(formatted, "- another detail that can be compacted") {
		t.Fatalf("expected lowest-priority lines to be removed before truncating structured context, got %s", formatted)
	}
}

func TestEngineNative_RepairHelpers(t *testing.T) {
	if got := summarizeNextRepairNeed(nativeToolResult{Name: "native_tool_call"}); !strings.Contains(got, "valid tool call") {
		t.Fatalf("unexpected native tool next-repair summary: %q", got)
	}
	if got := summarizeNextRepairNeed(nativeToolResult{Name: "execute_script", Result: "Repair strategy: research_first"}); !strings.Contains(got, "list_stores") {
		t.Fatalf("unexpected research-first next-repair summary: %q", got)
	}
	if got := summarizeAttemptedArgs("Tool execution error\nAttempted args:\n{\n  \"script\": []\n}\nRetry instruction: fix it"); !strings.Contains(got, "Reuse the valid structure") {
		t.Fatalf("expected attempted args reuse summary, got %q", got)
	}
	if got := summarizeAttemptedArgs("Tool execution error\nAttempted args:\n{}\nRetry instruction: fix it"); !strings.Contains(got, "no reusable arguments") {
		t.Fatalf("expected empty attempted args summary, got %q", got)
	}
	if got := summarizeAttemptedArgs("no attempted args marker"); got != "" {
		t.Fatalf("expected no attempted args summary, got %q", got)
	}

	repairResult := nativeToolResult{Name: "execute_script", Result: "Repair strategy: same_tool\nRetry instruction: fix"}
	if !isRecoverableRepairResult(repairResult, "execute_script", nativeRepairStrategySameTool) {
		t.Fatal("expected recoverable same-tool result to match")
	}
	if isRecoverableRepairResult(repairResult, "list_stores", nativeRepairStrategySameTool) {
		t.Fatal("did not expect tool-name mismatch to match")
	}
	if !hasSuccessfulTool([]nativeToolResult{{Name: "execute_script", Result: "ok"}}, "execute_script") {
		t.Fatal("expected successful tool detection")
	}
	if hasSuccessfulTool([]nativeToolResult{{Name: "execute_script", Result: "Retry instruction: fix"}}, "execute_script") {
		t.Fatal("did not expect repair-only result to count as successful")
	}
	if !hasSuccessfulToolSequence([]nativeToolResult{{Name: "execute_script", Result: "Retry instruction: fix"}, {Name: "list_stores", Result: "ok"}, {Name: "execute_script", Result: "ok"}}, "list_stores", "execute_script") {
		t.Fatal("expected successful tool sequence to ignore repair-only entries")
	}
	if hasSuccessfulToolSequence(nil, "list_stores") {
		t.Fatal("did not expect empty tool sequence to match")
	}
}

func TestEngineNative_RepairFormattingAndClassification(t *testing.T) {
	validationErr := newExecuteScriptValidationError(
		"invalid_join_on_placeholder",
		"join mapping placeholder",
		`{"relation":"users_orders","target":"orders_store"}`,
	)
	researchRepair := classifyRecoverableToolRepair("execute_script", validationErr)
	if researchRepair.Strategy != nativeRepairStrategyResearchFirst || researchRepair.ResearchTool != "list_stores" {
		t.Fatalf("expected validation error to trigger research-first repair, got %+v", researchRepair)
	}
	sameToolRepair := classifyRecoverableToolRepair("execute_script", fmt.Errorf("missing required args"))
	if sameToolRepair.Strategy != nativeRepairStrategySameTool {
		t.Fatalf("expected generic arg error to stay same-tool, got %+v", sameToolRepair)
	}
	if !researchRepair.allowsTool("list_stores") || researchRepair.allowsTool("execute_script") {
		t.Fatalf("expected research-first repair to allow only the research tool, got %+v", researchRepair)
	}
	if !sameToolRepair.allowsTool("execute_script") || sameToolRepair.allowsTool("list_stores") {
		t.Fatalf("expected same-tool repair to allow only the original tool, got %+v", sameToolRepair)
	}

	formatted := formatRecoverableToolError(researchRepair, map[string]any{"script": []any{"scan"}}, validationErr)
	if !strings.Contains(formatted, "Repair category: invalid_join_on_placeholder") || !strings.Contains(formatted, "Suggested fix example") || !strings.Contains(formatted, "Research reason:") {
		t.Fatalf("expected formatted recoverable tool error to include validation and research guidance, got %s", formatted)
	}
	if !strings.Contains(formatted, "Join repair note: After list_stores confirms a relation path, prefer relation+target for relation-driven joins.") {
		t.Fatalf("expected formatted recoverable tool error to include join-specific repair guidance, got %s", formatted)
	}
	if !strings.Contains(formatted, `stores:["users","orders"]`) {
		t.Fatalf("expected generic scoped list_stores guidance, got %s", formatted)
	}
	if !strings.Contains(formatRecoverableGenerationError(fmt.Errorf("bad tool call")), "Retry instruction: Return exactly one valid native tool call") {
		t.Fatal("expected recoverable generation error to include retry guidance")
	}
	if !strings.Contains(formatPendingRepairReminder(researchRepair, "search_space"), "Call list_stores next") || !strings.Contains(formatPendingRepairReminder(researchRepair, "search_space"), "scoped stores:[...]") {
		t.Fatal("expected research-first reminder to point to list_stores")
	}
	if !strings.Contains(formatPendingRepairReminder(sameToolRepair, "list_stores"), "Call execute_script next with corrected arguments") {
		t.Fatal("expected same-tool reminder to point back to execute_script")
	}
}

func TestFormatRecoverableToolError_ResearchFirstInfersScopedStoresFromAttemptedArgs(t *testing.T) {
	repair := pendingToolRepair{ToolName: "execute_script", Strategy: nativeRepairStrategyResearchFirst, ResearchTool: "list_stores"}
	err := newExecuteScriptValidationError(
		"invalid_join_on_placeholder",
		"invalid type for join.on[\"users.key\"]: got boolean placeholder true",
		`{"op":"join","args":{"relation":"users_orders","target":"orders_store"}}`,
	)
	formatted := formatRecoverableToolError(repair, map[string]any{"script": []any{
		map[string]any{"op": "open_store", "args": map[string]any{"name": "users"}},
		map[string]any{"op": "join", "args": map[string]any{"store": "users_orders", "on": map[string]any{"users.key": true}}},
		map[string]any{"op": "join", "args": map[string]any{"store": "orders", "on": map[string]any{"value": "key"}}},
	}}, err)
	if !strings.Contains(formatted, `Call list_stores first using stores:["orders","users","users_orders"]`) {
		t.Fatalf("expected inferred scoped store hint, got %s", formatted)
	}
}

func TestUnwrapToolResultEnvelope_ExtractsProgressHint(t *testing.T) {
	result, hint := unwrapToolResultEnvelope(`{"tool_result":{"rows":[{"name":"John"}]},"progress_hint":{"status":"progressing","completion_delta":0.25,"tips":["Use execute_script next"],"clues":["users_orders relation confirmed"],"suggested_next_tools":["execute_script"]}}`)
	if !strings.Contains(result, `"rows":[{"name":"John"}]`) {
		t.Fatalf("expected unwrapped tool result payload, got %q", result)
	}
	if hint == nil || hint.Status != "progressing" || len(hint.SuggestedNextTools) != 1 || hint.SuggestedNextTools[0] != "execute_script" {
		t.Fatalf("expected progress hint to be extracted, got %+v", hint)
	}

	raw, noHint := unwrapToolResultEnvelope(`[1,2,3]`)
	if raw != `[1,2,3]` || noHint != nil {
		t.Fatalf("expected raw non-envelope result to pass through unchanged, got %q %+v", raw, noHint)
	}
}

func TestBuildAskAnchoredMRUState_IncludesProgressHints(t *testing.T) {
	state := buildAskAnchoredMRUState([]nativeToolResult{{
		Name:   "list_stores",
		Result: "users schema=key:string, first_name:string",
		Hint: &ai.ToolProgressHint{
			Status:             "progressing",
			CompletionDelta:    0.25,
			Tips:               []string{"Retry execute_script with grounded fields."},
			Clues:              []string{"users_orders relation is available."},
			SuggestedNextTools: []string{"execute_script"},
			Missing:            []string{"join key mapping"},
		},
	}})

	formatted := formatAskAnchoredMRUState(state)
	if !strings.Contains(formatted, "- Progress: progressing (+0.25)") {
		t.Fatalf("expected progress hint in ask MRU state, got %s", formatted)
	}
	if !strings.Contains(formatted, "- Tip: Retry execute_script with grounded fields.") || !strings.Contains(formatted, "- Clue: users_orders relation is available.") || !strings.Contains(formatted, "- Suggested tool: execute_script") || !strings.Contains(formatted, "- Missing: join key mapping") {
		t.Fatalf("expected tool hint details in ask MRU state, got %s", formatted)
	}
}

type progressBudgetGenerator struct{ calls int }

func (m *progressBudgetGenerator) Name() string                                 { return "progress_budget_mock" }
func (m *progressBudgetGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *progressBudgetGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *progressBudgetGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if strings.Contains(prompt, "We have hit the retry cap.") {
		return ai.GenOutput{Text: "Which specific store or field should I focus on next to unblock this query?"}, nil
	}
	if m.calls <= 3 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	return ai.GenOutput{Text: "unexpected follow-up after retry cap"}, nil
}

type progressBudgetExecutor struct{ callCount int }

func (e *progressBudgetExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	return fmt.Sprintf(`{"tool_result":[{"step":%d}],"progress_hint":{"status":"progressing","completion_delta":0.20,"clues":["step %d narrowed the path"],"suggested_next_tools":["execute_script"]}}`, e.callCount, e.callCount), nil
}

func (e *progressBudgetExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

func TestNativeReActEngine_FixedLoopBudgetCapsEvenWhenProgressing(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &progressBudgetGenerator{}
	executor := &progressBudgetExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) { progress = append(progress, msg) })
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Keep narrowing until you can answer.",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Which specific store or field should I focus on next to unblock this query?" {
		t.Fatalf("expected clarification after retry cap, got %q", resp.FinalText)
	}
	if executor.callCount != nativeReActBaseToolIterations {
		t.Fatalf("expected fixed loop budget to stop at %d tool calls, got %d", nativeReActBaseToolIterations, executor.callCount)
	}
	if containsProgressMessage(progress, "Loop budget extended to") {
		t.Fatalf("did not expect loop budget extension progress message, got %#v", progress)
	}
	if !containsProgressMessage(progress, "Reached retry cap; switching to clarification.") {
		t.Fatalf("expected retry-cap clarification progress message, got %#v", progress)
	}
	if len(resp.ToolCalls) != nativeReActBaseToolIterations {
		t.Fatalf("expected capped progressing tool calls to be recorded, got %#v", resp.ToolCalls)
	}
}

type stalledBudgetGenerator struct{ calls int }

func (m *stalledBudgetGenerator) Name() string                                 { return "stalled_budget_mock" }
func (m *stalledBudgetGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *stalledBudgetGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *stalledBudgetGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if strings.Contains(prompt, "We have hit the retry cap.") {
		return ai.GenOutput{Text: "What field or join should I correct to continue this query?"}, nil
	}
	return ai.GenOutput{ToolCalls: []ai.ToolCall{{
		Name: "execute_script",
		Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
	}}}, nil
}

type stalledBudgetExecutor struct{ callCount int }

func (e *stalledBudgetExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	return "", fmt.Errorf("missing required field")
}

func (e *stalledBudgetExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

func TestNativeReActEngine_DoesNotExtendLoopBudgetWithoutProgress(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &stalledBudgetGenerator{}
	executor := &stalledBudgetExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Try until you hit the retry cap.",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "What field or join should I correct to continue this query?" {
		t.Fatalf("expected clarification after capped retries, got %q", resp.FinalText)
	}
	if executor.callCount != nativeReActBaseToolIterations {
		t.Fatalf("expected no progress to stop at base loop budget %d, got %d", nativeReActBaseToolIterations, executor.callCount)
	}
	if gen.calls != nativeReActBaseToolIterations+1 {
		t.Fatalf("expected capped retries plus one clarification call, got %d", gen.calls)
	}
}

type cappedBudgetGenerator struct{ calls int }

func (m *cappedBudgetGenerator) Name() string                                 { return "capped_budget_mock" }
func (m *cappedBudgetGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *cappedBudgetGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *cappedBudgetGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if strings.Contains(prompt, "We have hit the retry cap.") {
		return ai.GenOutput{Text: "What concrete next constraint should I use to continue narrowing the result?"}, nil
	}
	return ai.GenOutput{ToolCalls: []ai.ToolCall{{
		Name: "execute_script",
		Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
	}}}, nil
}

type cappedBudgetExecutor struct{ callCount int }

func (e *cappedBudgetExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	return fmt.Sprintf(`{"tool_result":[{"step":%d}],"progress_hint":{"status":"progressing","completion_delta":0.10,"tips":["continue narrowing"],"suggested_next_tools":["execute_script"]}}`, e.callCount), nil
}

func (e *cappedBudgetExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

type antiSuccessGenerator struct{ calls int }

func (m *antiSuccessGenerator) Name() string                                 { return "anti_success_mock" }
func (m *antiSuccessGenerator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *antiSuccessGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *antiSuccessGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	if m.calls == 1 {
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{map[string]any{"op": "scan"}}},
		}}}, nil
	}
	return ai.GenOutput{Text: "unexpected follow-up generation after hard error"}, nil
}

type antiSuccessExecutor struct{ callCount int }

func (e *antiSuccessExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	e.callCount++
	return `{"tool_result":"Permission denied: write access is blocked for this operation.","progress_hint":{"status":"hard_error","tips":["Stop retrying this tool until permissions change."]}}`, nil
}

func (e *antiSuccessExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return []ai.ToolDefinition{{Name: "execute_script"}}, nil
}

func TestNativeReActEngine_ProgressAwareBudgetStillCapsAtFixedLimit(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &cappedBudgetGenerator{}
	executor := &cappedBudgetExecutor{}

	resp, err := engine.Run(context.Background(), ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Keep progressing until the hard cap.",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "What concrete next constraint should I use to continue narrowing the result?" {
		t.Fatalf("expected clarification after hard cap, got %q", resp.FinalText)
	}
	if executor.callCount != nativeReActMaxToolIterations {
		t.Fatalf("expected progressing loop to stop at fixed cap %d, got %d", nativeReActMaxToolIterations, executor.callCount)
	}
	if gen.calls != nativeReActMaxToolIterations+1 {
		t.Fatalf("expected fixed cap plus one clarification call, got %d", gen.calls)
	}
}

func TestNativeReActEngine_ShortCircuitsOnAntiSuccessHardErrorHint(t *testing.T) {
	engine := &NativeReActEngine{}
	gen := &antiSuccessGenerator{}
	executor := &antiSuccessExecutor{}
	var progress []string
	ctx := context.WithValue(context.Background(), ai.CtxKeyProgressSink, func(msg string) { progress = append(progress, msg) })
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	resp, err := engine.Run(ctx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    "Try the blocked operation.",
		Executor:     executor,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Permission denied: write access is blocked for this operation." {
		t.Fatalf("expected terminal tool result to be returned immediately, got %q", resp.FinalText)
	}
	if gen.calls != 1 {
		t.Fatalf("expected hard error hint to stop after the first generation call, got %d", gen.calls)
	}
	if executor.callCount != 1 {
		t.Fatalf("expected only one tool execution before short-circuit, got %d", executor.callCount)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "execute_script" {
		t.Fatalf("expected the terminal tool call to be recorded, got %#v", resp.ToolCalls)
	}
	if !containsProgressMessage(progress, "Tool `execute_script` reported a terminal hard error; short-circuiting the Ask loop.") {
		t.Fatalf("expected terminal hard-error progress message, got %#v", progress)
	}
}

func TestEngineNative_HelperClassifiers_RecognizeTerminalHintStatuses(t *testing.T) {
	if !shouldShortCircuitAskLoopOnToolHint(&ai.ToolProgressHint{Status: "hard_error"}) {
		t.Fatal("expected hard_error to short-circuit the Ask loop")
	}
	if !shouldShortCircuitAskLoopOnToolHint(&ai.ToolProgressHint{Status: "anti_success"}) {
		t.Fatal("expected anti_success to short-circuit the Ask loop")
	}
	if !shouldShortCircuitAskLoopOnToolHint(&ai.ToolProgressHint{Status: "terminal_error"}) {
		t.Fatal("expected terminal_error to short-circuit the Ask loop")
	}
	if shouldShortCircuitAskLoopOnToolHint(&ai.ToolProgressHint{Status: "progressing"}) {
		t.Fatal("did not expect progressing to short-circuit the Ask loop")
	}
}

func containsProgressMessage(progress []string, want string) bool {
	for _, message := range progress {
		if message == want {
			return true
		}
	}
	return false
}
