package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	log "log/slog"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent/parser"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
	"github.com/sharedcode/joltrin/ai/obfuscation"
)

// NativeReActEngine implements ReasoningEngine using native LLM API tool calling.
type NativeReActEngine struct {
	EnableObfuscation bool
	MaxTokens         int
}

const nativeReActBaseToolIterations = 3
const nativeReActMaxToolIterations = 3
const nativeReActToolCallTemperature float32 = 0.1
const nativeReActRepairTemperature float32 = 0.0

type nativeToolResult struct {
	Name   string
	Result string
	Args   map[string]any
	Hint   *ai.ToolProgressHint
}

type askAnchoredMRUState []MRUItem

type pendingToolRepair struct {
	ToolName       string
	Strategy       nativeRepairStrategy
	ResearchTool   string
	ResearchReason string
}

type pendingGenerationRepair struct{}

type askLoopBudgetState struct {
	allowedIterations int
	maxIterations     int
}

type askLoopProgressDelta struct {
	Progressing bool
	NewFacts    []string
	NewRecipes  []string
	HintSignal  *ai.ToolProgressHint
}

type nativeRepairStrategy string

const (
	askMRUCategoryConfirmed                = "ASK_CONFIRMED"
	askMRUCategoryConfirmedStoreSchema     = "ASK_CONFIRMED_STORE_SCHEMA"
	askMRUCategoryConfirmedStoreRelation   = "ASK_CONFIRMED_STORE_RELATIONS"
	askMRUCategoryConfirmedJoinSelection   = "ASK_CONFIRMED_JOIN_SELECTION"
	askMRUCategoryConfirmedFilterSelection = "ASK_CONFIRMED_FILTER_SELECTION"

	nativeRepairStrategySameTool      nativeRepairStrategy = "same_tool"
	nativeRepairStrategyResearchFirst nativeRepairStrategy = "research_first"
)

type progressSink func(string)

// Run executes the orchestration loop relying on native tool calls.
func (e *NativeReActEngine) Run(ctx context.Context, req ai.ReasoningRequest) (ai.ReasoningResponse, error) {
	if resp, handled, err := handleDirectSlashReasoningRequest(ctx, req); handled {
		return resp, err
	}
	if loop := resolveOwnedReActLoop(req); loop != nil {
		return loop.Run(ctx, req)
	}
	return e.runDefaultLoop(ctx, req)
}

func handleDirectSlashReasoningRequest(ctx context.Context, req ai.ReasoningRequest) (ai.ReasoningResponse, bool, error) {
	trimmed := strings.TrimSpace(req.UserQuery)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") || req.Executor == nil {
		return ai.ReasoningResponse{}, false, nil
	}

	toolName, args, err := parser.ParseSlashCommand(trimmed[1:])
	if err != nil || strings.TrimSpace(toolName) == "" {
		return ai.ReasoningResponse{}, false, err
	}

	result, execErr := req.Executor.Execute(ctx, toolName, args)
	if execErr != nil {
		if strings.Contains(execErr.Error(), "unknown tool") {
			return ai.ReasoningResponse{}, false, nil
		}
		return ai.ReasoningResponse{}, true, execErr
	}

	return ai.ReasoningResponse{
		FinalText: result,
		ToolCalls: []ai.ToolCall{{Name: toolName, Args: args}},
	}, true, nil
}

func resolveOwnedReActLoop(req ai.ReasoningRequest) ai.ReActLoop {
	provider, ok := req.Generator.(ai.ReActLoopProvider)
	if !ok {
		return nil
	}
	return provider.ReActLoop()
}

func (e *NativeReActEngine) runDefaultLoop(ctx context.Context, req ai.ReasoningRequest) (ai.ReasoningResponse, error) {
	if req.Generator == nil {
		if e.EnableObfuscation {
			return ai.ReasoningResponse{FinalText: obfuscation.GlobalObfuscator.DeobfuscateText(req.ContextText)}, nil
		}
		return ai.ReasoningResponse{FinalText: req.ContextText}, nil
	}

	var tools []ai.ToolDefinition
	var err error
	if req.Executor != nil {
		tools, err = req.Executor.ListTools(ctx)
		if err != nil {
			return ai.ReasoningResponse{}, fmt.Errorf("failed to list tools: %w", err)
		}
	}
	log.Info("Native ReAct Engine Start",
		"generator", req.Generator.Name(),
		"default_format", logsafe.V(getRequestedOutputFormat(ctx)),
		"tool_count", len(tools),
		"tools", summarizeToolDefinitions(tools),
		"query_chars", len(req.UserQuery),
		"system_prompt_chars", len(req.SystemPrompt),
	)
	emitVerboseProgress(ctx, "Planning request with native multi-step loop.")

	executedToolCalls := make([]ai.ToolCall, 0)
	toolResults := make([]nativeToolResult, 0)
	budgetState := newAskLoopBudgetState()
	repairAttempts := 0
	var pendingRepair *pendingToolRepair
	var pendingMalformedCall *pendingGenerationRepair
	var pendingContinuation *ai.ToolCallContinuation
	strategy := resolveReActTurnStrategy(req)
	for iteration := 0; iteration < budgetState.maxIterations && iteration < budgetState.allowedIterations; iteration++ {
		// Check for context cancellation at the start of each iteration
		select {
		case <-ctx.Done():
			log.Warn("Native ReAct Engine: Request canceled by client", "iteration", iteration+1, "error", ctx.Err())
			return ai.ReasoningResponse{}, fmt.Errorf("request canceled during iteration %d: %w", iteration+1, ctx.Err())
		default:
		}

		emitVerboseProgress(ctx, "Reasoning iteration %d of %d.", iteration+1, budgetState.allowedIterations)
		temperature := nativeReActToolCallTemperature
		thinkingLevel := "low" // Default: strict schema adherence for tool calls
		if pendingRepair != nil || pendingMalformedCall != nil {
			temperature = nativeReActRepairTemperature
			thinkingLevel = "medium" // More reasoning for repair strategies
		}
		genOpts := ai.GenOptions{
			SystemPrompt:  req.SystemPrompt,
			MaxTokens:     e.MaxTokens,
			Temperature:   temperature,
			ThinkingLevel: thinkingLevel,
			Tools:         tools,
			ToolCallContinuations: func() []ai.ToolCallContinuation {
				if pendingContinuation == nil {
					return nil
				}
				return []ai.ToolCallContinuation{*pendingContinuation}
			}(),
		}
		turn := ai.ReActTurn{
			Iteration:       iteration + 1,
			UserQuery:       req.UserQuery,
			RepairDirective: latestNativeRepairDirective(toolResults),
			LoopState:       snapshotReActLoopState(iteration+1, budgetState, repairAttempts, pendingRepair, pendingMalformedCall != nil, toolResults, genOpts.ToolCallContinuations),
			Options:         genOpts,
			ToolResults:     cloneReActToolResults(toolResults),
			FinalTurn:       false,
		}
		mainPrompt := ""
		promptProfile := PromptBudgetProfile{}
		promptReport := PromptBudgetReport{}
		if !shouldBypassReActPrompt(strategy, turn) {
			promptProfile = nativeReActPromptBudgetProfile()
			mainPrompt, promptReport = buildNativeReActPromptWithReport(req, toolResults, buildAskAnchoredMRUState(toolResults), promptProfile)
		}
		effectivePrompt, effectiveOpts := prepareReActTurn(ctx, strategy, turn, mainPrompt)
		log.Info("Native ReAct Iteration Context",
			"iteration", iteration+1,
			"tool_results_count", len(toolResults),
			"prompt_chars", len(mainPrompt),
			"prompt", logsafe.V(formatLogSeparatedMessage("prompt", mainPrompt)),
			"effective_prompt_chars", len(effectivePrompt),
			"effective_prompt", logsafe.V(formatLogSeparatedMessage("effective_prompt", effectivePrompt)),
		)
		if firstTrimmed := firstTrimmedPromptComponent(promptProfile, promptReport); firstTrimmed != "" {
			emitVerboseProgress(ctx, "Prompt budget trimmed %s first; reduced components: %s.", firstTrimmed, summarizePromptBudgetTrim(promptReport))
			log.Info("Native ReAct Prompt Budget Applied", "phase", "iteration", "iteration", iteration+1, "trimmed_first", firstTrimmed, "trimmed", summarizePromptBudgetTrim(promptReport), "final_chars", len(mainPrompt))
		}
		emitVerboseProgress(ctx, "Waiting for model response.")
		output, err := req.Generator.Generate(ctx, effectivePrompt, effectiveOpts)
		pendingContinuation = nil
		if err != nil {
			if isRecoverableGenerationError(err) {
				emitVerboseProgress(ctx, "Model emitted a malformed native tool call; retrying.")
				pendingMalformedCall = &pendingGenerationRepair{}
				toolResults = append(toolResults, nativeToolResult{
					Name:   "native_tool_call",
					Result: formatRecoverableGenerationError(err),
				})
				log.Warn("Native ReAct Engine Recoverable Generation Failure", "error", err)
				continue
			}
			return ai.ReasoningResponse{}, fmt.Errorf("generation failed: %w", err)
		}
		pendingMalformedCall = nil
		rawOutputText := output.Text
		recoveredTextualToolCall := false
		if len(output.ToolCalls) == 0 {
			if recoveredCall, ok, recoverErr := extractEmbeddedNativeToolCall(output.Text); ok {
				if recoverErr != nil {
					emitVerboseProgress(ctx, "Model emitted a malformed native tool call; retrying.")
					pendingMalformedCall = &pendingGenerationRepair{}
					toolResults = append(toolResults, nativeToolResult{
						Name:   "native_tool_call",
						Result: formatRecoverableGenerationError(recoverErr),
					})
					log.Warn("Native ReAct Engine Recoverable Textual Tool Call Failure", "error", recoverErr)
					continue
				}
				recoveredTextualToolCall = true
				log.Info("Native ReAct Engine Recovered Textual Tool Call",
					"iteration", iteration+1,
					"tool", recoveredCall.Name,
					"recovered_textual_tool_call", true,
					"arg_keys", summarizeToolArgKeys(recoveredCall.Args),
					"llm_generated_script", formatLLMGeneratedScriptForLog(recoveredCall.Name, recoveredCall.Args),
				)
				output.ToolCalls = []ai.ToolCall{recoveredCall}
				output.Text = ""
			}
		}
		log.Info("Native ReAct Engine Output",
			"iteration", iteration+1,
			"text_chars", len(rawOutputText),
			"text", formatLogSeparatedMessage("model_output", rawOutputText),
			"tool_call_count", len(output.ToolCalls),
			"recovered_textual_tool_call", recoveredTextualToolCall,
		)

		if req.Executor == nil || len(output.ToolCalls) == 0 {
			emitVerboseProgress(ctx, "No further tools required; preparing final answer.")
			outcomeRecipes := summarizeOutcomeRecipes(toolResults)
			if shouldPreserveStructuredToolResult(ctx, toolResults) {
				return ai.ReasoningResponse{
					FinalText:      preserveStructuredToolResult(toolResults[len(toolResults)-1].Result, e.EnableObfuscation),
					ToolCalls:      executedToolCalls,
					OutcomeFacts:   summarizeOutcomeFacts(toolResults),
					OutcomeRecipes: outcomeRecipes,
				}, nil
			}

			finalText := output.Text
			if e.EnableObfuscation {
				finalText = obfuscation.GlobalObfuscator.DeobfuscateText(output.Text)
			}

			return ai.ReasoningResponse{
				FinalText:      finalText,
				ToolCalls:      executedToolCalls,
				OutcomeFacts:   summarizeOutcomeFacts(toolResults),
				OutcomeRecipes: outcomeRecipes,
			}, nil
		}

		toolCall := output.ToolCalls[0]
		if pendingRepair != nil && !pendingRepair.allowsTool(toolCall.Name) {
			emitVerboseProgress(ctx, "Tool `%s` must be corrected before other actions.", pendingRepair.ToolName)
			toolResults = append(toolResults, nativeToolResult{
				Name:   pendingRepair.ToolName,
				Result: formatPendingRepairReminder(*pendingRepair, toolCall.Name),
			})
			log.Warn("Native ReAct Engine Deferred Non-Repair Tool Call", "expected_tool", pendingRepair.ToolName, "received_tool", toolCall.Name)
			continue
		}
		// The model produced a native tool call such as "search_space".
		// This is the dispatch point: the engine hands that name + args to the executor.
		emitVerboseProgress(ctx, "Calling tool `%s`.", toolCall.Name)
		log.Info("Native React Engine Tool Call",
			"iteration", iteration+1,
			"tool", toolCall.Name,
			"arg_keys", summarizeToolArgKeys(toolCall.Args),
			"llm_generated_script", formatLLMGeneratedScriptForLog(toolCall.Name, toolCall.Args),
		)

		sanitizeToolCallArgs(toolCall.Args, e.EnableObfuscation)
		emitReasoningEvent(req, ai.ReasoningEventToolCall, ai.BuildToolCallEvent(toolCall.Name, cloneToolEventMap(toolCall.Args), iteration+1))

		execCtx := context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)
		priorRepair := pendingRepair
		// req.Executor is the registered runtime (CopilotAgent). Execute() resolves
		// the tool name from the registry and runs the matching handler.
		rawResult, err := req.Executor.Execute(execCtx, toolCall.Name, toolCall.Args)
		if err != nil {
			emitReasoningEvent(req, ai.ReasoningEventToolError, ai.BuildToolErrorEvent(toolCall.Name, cloneToolEventMap(toolCall.Args), err, iteration+1))
			if isRecoverableToolExecutionError(err) {
				repairPlan := classifyRecoverableToolRepair(toolCall.Name, err)
				if shouldEscalateRepairToClarification(repairAttempts) {
					emitVerboseProgress(ctx, "Repair remained unresolved after the first retry; switching to clarification.")
					pendingRepair = nil
					toolResults = appendOrReplaceRetriedToolResult(toolResults, nativeToolResult{
						Name:   toolCall.Name,
						Result: formatClarificationRequiredToolError(repairPlan, toolCall.Args, err),
						Args:   cloneToolEventMap(toolCall.Args),
					}, priorRepair, toolCall.Name)
					log.Warn("Native ReAct Engine Escalated Repair To Clarification", "tool", toolCall.Name, "error", logsafe.V(err))
					continue
				}
				repairAttempts++
				emitVerboseProgress(ctx, "Tool `%s` needs corrected arguments; retrying.", toolCall.Name)
				pendingRepair = &repairPlan
				toolResults = appendOrReplaceRetriedToolResult(toolResults, nativeToolResult{
					Name:   toolCall.Name,
					Result: formatRecoverableToolError(repairPlan, toolCall.Args, err),
					Args:   cloneToolEventMap(toolCall.Args),
				}, priorRepair, toolCall.Name)
				log.Warn("Native ReAct Engine Recoverable Tool Failure", "tool", toolCall.Name, "error", logsafe.V(err))
				continue
			}
			log.Error("Native ReAct Engine Tool Failure", "tool", toolCall.Name, "error", logsafe.V(err))
			return ai.ReasoningResponse{}, fmt.Errorf("tool execution failed: %w", err)
		}
		result, hint := unwrapToolResultEnvelope(rawResult)
		emitVerboseProgress(ctx, "Tool `%s` completed.", toolCall.Name)
		if shouldResetRepairAttempts(priorRepair, toolCall.Name) {
			repairAttempts = 0
		}
		pendingRepair = nil
		log.Info("Native ReAct Engine Tool Success", "iteration", iteration+1, "tool", toolCall.Name, "result_chars", len(result))
		emitReasoningEvent(req, ai.ReasoningEventToolResult, ai.BuildToolResultEvent(toolCall.Name, cloneToolEventMap(toolCall.Args), result, hint, iteration+1))

		executedToolCalls = append(executedToolCalls, toolCall)
		previousResults := append([]nativeToolResult(nil), toolResults...)
		toolResults = appendOrReplaceRetriedToolResult(toolResults, nativeToolResult{Name: toolCall.Name, Result: result, Args: cloneToolEventMap(toolCall.Args), Hint: cloneToolProgressHint(hint)}, priorRepair, toolCall.Name)
		pendingContinuation = &ai.ToolCallContinuation{
			ToolCall: toolCall,
			Response: coerceToolCallContinuationResponse(result),
		}
		if shouldShortCircuitAskLoopOnToolHint(hint) {
			emitVerboseProgress(ctx, "Tool `%s` reported a terminal hard error; short-circuiting the Ask loop.", toolCall.Name)
			outcomeRecipes := summarizeOutcomeRecipes(toolResults)
			return ai.ReasoningResponse{
				FinalText:      preserveStructuredToolResult(result, e.EnableObfuscation),
				ToolCalls:      executedToolCalls,
				OutcomeFacts:   summarizeOutcomeFacts(toolResults),
				OutcomeRecipes: outcomeRecipes,
			}, nil
		}
		_ = detectAskLoopProgress(previousResults, toolResults)
		if shouldPreserveStructuredToolResult(ctx, toolResults) {
			outcomeRecipes := summarizeOutcomeRecipes(toolResults)
			return ai.ReasoningResponse{
				FinalText:      preserveStructuredToolResult(result, e.EnableObfuscation),
				ToolCalls:      executedToolCalls,
				OutcomeFacts:   summarizeOutcomeFacts(toolResults),
				OutcomeRecipes: outcomeRecipes,
			}, nil
		}
	}

	emitVerboseProgress(ctx, "Reached retry cap; switching to clarification.")
	if shouldPreserveStructuredToolResult(ctx, toolResults) {
		outcomeRecipes := summarizeOutcomeRecipes(toolResults)
		return ai.ReasoningResponse{
			FinalText:      preserveStructuredToolResult(toolResults[len(toolResults)-1].Result, e.EnableObfuscation),
			ToolCalls:      executedToolCalls,
			OutcomeFacts:   summarizeOutcomeFacts(toolResults),
			OutcomeRecipes: outcomeRecipes,
		}, nil
	}

	log.Warn("Native ReAct Engine Reached Tool Iteration Limit", "limit", budgetState.allowedIterations, "hard_cap", budgetState.maxIterations)
	finalOpts := ai.GenOptions{
		SystemPrompt:  req.SystemPrompt,
		MaxTokens:     e.MaxTokens,
		Temperature:   0.7,
		ThinkingLevel: "high", // Higher reasoning for final synthesis and explanation
		ToolCallContinuations: func() []ai.ToolCallContinuation {
			if pendingContinuation == nil {
				return nil
			}
			return []ai.ToolCallContinuation{*pendingContinuation}
		}(),
	}
	finalTurn := ai.ReActTurn{
		Iteration:       budgetState.allowedIterations + 1,
		UserQuery:       req.UserQuery,
		RepairDirective: latestNativeRepairDirective(toolResults),
		LoopState:       snapshotReActLoopState(budgetState.allowedIterations+1, budgetState, repairAttempts, pendingRepair, pendingMalformedCall != nil, toolResults, finalOpts.ToolCallContinuations),
		Options:         finalOpts,
		ToolResults:     cloneReActToolResults(toolResults),
		FinalTurn:       true,
	}
	finalPrompt := ""
	promptProfile := PromptBudgetProfile{}
	promptReport := PromptBudgetReport{}
	if !shouldBypassReActPrompt(strategy, finalTurn) {
		promptProfile = nativeReActPromptBudgetProfile()
		finalPrompt, promptReport = buildNativeReActPromptWithReport(req, toolResults, buildAskAnchoredMRUState(toolResults), promptProfile)
		finalPrompt += "\n\nUser: We have hit the retry cap. Do not call any more tools. Briefly explain what is still blocking progress and ask one short, concrete clarification question that would unblock the task."
	}
	if firstTrimmed := firstTrimmedPromptComponent(promptProfile, promptReport); firstTrimmed != "" {
		emitVerboseProgress(ctx, "Prompt budget trimmed %s first; reduced components: %s.", firstTrimmed, summarizePromptBudgetTrim(promptReport))
		log.Info("Native ReAct Prompt Budget Applied", "phase", "final", "trimmed_first", firstTrimmed, "trimmed", summarizePromptBudgetTrim(promptReport), "final_chars", len(finalPrompt))
	}
	effectiveFinalPrompt, effectiveFinalOpts := prepareReActTurn(ctx, strategy, finalTurn, finalPrompt)
	emitVerboseProgress(ctx, "Waiting for model response.")
	output, err := req.Generator.Generate(ctx, effectiveFinalPrompt, effectiveFinalOpts)
	if err != nil {
		return ai.ReasoningResponse{}, fmt.Errorf("final synthesis generation failed: %w", err)
	}
	finalText := output.Text
	if e.EnableObfuscation {
		finalText = obfuscation.GlobalObfuscator.DeobfuscateText(output.Text)
	}
	return ai.ReasoningResponse{
		FinalText:      finalText,
		ToolCalls:      executedToolCalls,
		OutcomeFacts:   summarizeOutcomeFacts(toolResults),
		OutcomeRecipes: summarizeOutcomeRecipes(toolResults),
	}, nil
}

func resolveReActTurnStrategy(req ai.ReasoningRequest) ai.ReActTurnStrategy {
	strategy := ai.ReActTurnStrategy(ai.DefaultReActTurnStrategy{})
	if provider, ok := req.Generator.(ai.ReActTurnStrategyProvider); ok {
		if provided := provider.ReActTurnStrategy(); provided != nil {
			strategy = provided
		}
	}
	return strategy
}

func shouldBypassReActPrompt(strategy ai.ReActTurnStrategy, turn ai.ReActTurn) bool {
	bypass, ok := strategy.(ai.ReActPromptBypassStrategy)
	if !ok {
		return false
	}
	return bypass.ShouldBypassPrompt(turn)
}

func prepareReActTurn(ctx context.Context, strategy ai.ReActTurnStrategy, turn ai.ReActTurn, prompt string) (string, ai.GenOptions) {
	turn.Prompt = prompt
	turn = strategy.PrepareTurn(ctx, turn)
	if strings.TrimSpace(turn.Prompt) == "" {
		turn.Prompt = prompt
	}
	return turn.Prompt, turn.Options
}

func cloneReActToolResults(results []nativeToolResult) []ai.ReActToolResult {
	if len(results) == 0 {
		return nil
	}
	cloned := make([]ai.ReActToolResult, 0, len(results))
	for _, result := range results {
		cloned = append(cloned, ai.ReActToolResult{
			Name:   result.Name,
			Args:   cloneToolEventMap(result.Args),
			Result: result.Result,
			Hint:   cloneToolProgressHint(result.Hint),
		})
	}
	return cloned
}

func snapshotReActLoopState(iteration int, budgetState askLoopBudgetState, repairAttempts int, pendingRepair *pendingToolRepair, pendingMalformedCall bool, toolResults []nativeToolResult, continuations []ai.ToolCallContinuation) ai.ReActLoopState {
	phase, allowedNextActions := deriveReActLoopPhase(iteration, budgetState, pendingRepair, pendingMalformedCall)
	state := ai.ReActLoopState{
		Iteration:                iteration,
		AllowedIterations:        budgetState.allowedIterations,
		MaxIterations:            budgetState.maxIterations,
		RemainingToolCalls:       max(0, budgetState.allowedIterations-iteration+1),
		RepairAttempts:           repairAttempts,
		Phase:                    phase,
		AllowedNextActions:       allowedNextActions,
		PendingMalformedToolCall: pendingMalformedCall,
		ToolResults:              cloneReActToolResults(toolResults),
		ToolCallContinuations:    append([]ai.ToolCallContinuation(nil), continuations...),
	}
	if pendingRepair != nil {
		state.PendingRepair = &ai.ReActPendingRepair{
			ToolName:       pendingRepair.ToolName,
			Strategy:       string(pendingRepair.Strategy),
			ResearchTool:   pendingRepair.ResearchTool,
			ResearchReason: pendingRepair.ResearchReason,
		}
	}
	return state
}

func deriveReActLoopPhase(iteration int, budgetState askLoopBudgetState, pendingRepair *pendingToolRepair, pendingMalformedCall bool) (ai.ReActLoopPhase, []ai.ReActNextAction) {
	if iteration > budgetState.allowedIterations {
		return ai.ReActLoopPhaseClarification, []ai.ReActNextAction{ai.ReActNextActionAskClarification, ai.ReActNextActionAnswerUser}
	}
	if pendingMalformedCall {
		return ai.ReActLoopPhaseMalformedToolCall, []ai.ReActNextAction{ai.ReActNextActionCallTool, ai.ReActNextActionAskClarification}
	}
	if pendingRepair != nil {
		if pendingRepair.Strategy == nativeRepairStrategySameTool {
			return ai.ReActLoopPhaseRepair, []ai.ReActNextAction{ai.ReActNextActionRetrySameTool, ai.ReActNextActionAskClarification}
		}
		return ai.ReActLoopPhaseRepair, []ai.ReActNextAction{ai.ReActNextActionCallTool, ai.ReActNextActionAskClarification}
	}
	return ai.ReActLoopPhaseActive, []ai.ReActNextAction{ai.ReActNextActionCallTool, ai.ReActNextActionAnswerUser}
}

func latestNativeRepairDirective(results []nativeToolResult) string {
	if len(results) == 0 {
		return ""
	}
	return buildNativeRepairDirective(results[len(results)-1])
}

func coerceToolCallContinuationResponse(result string) any {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return map[string]any{"result": ""}
	}

	var decoded any
	if json.Unmarshal([]byte(trimmed), &decoded) == nil {
		return decoded
	}

	return map[string]any{"result": result}
}

func newAskLoopBudgetState() askLoopBudgetState {
	return askLoopBudgetState{
		allowedIterations: nativeReActBaseToolIterations,
		maxIterations:     nativeReActMaxToolIterations,
	}
}

func (s *askLoopBudgetState) extendIfProgressing(delta askLoopProgressDelta) bool {
	_ = delta
	return false
}

func emitReasoningEvent(req ai.ReasoningRequest, eventType string, data any) {
	if req.Streamer == nil {
		return
	}
	req.Streamer(eventType, data)
}

func cloneToolEventMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	clone := make(map[string]any, len(src))
	for key, value := range src {
		clone[key] = cloneToolEventValue(value)
	}
	return clone
}

func cloneToolEventValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneToolEventMap(typed)
	case []any:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = cloneToolEventValue(item)
		}
		return items
	default:
		return typed
	}
}

func isRecoverableToolExecutionError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	recoverableHints := []string{
		"missing",
		"required",
		"not a string",
		"invalid type",
		"must be",
		"expected",
		"validation failed",
		"grammar validation",
		"unnecessary nested joins",
	}
	for _, hint := range recoverableHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

func isRecoverableGenerationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	return strings.Contains(message, "malformed_function_call") || strings.Contains(message, "malformed function call")
}

func emitVerboseProgress(ctx context.Context, format string, args ...any) {
	if !isVerboseEnabled(ctx) {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if sink, ok := ctx.Value(ai.CtxKeyProgressSink).(func(string)); ok && sink != nil {
		sink(msg)
	}
}

func runnerSessionFromContext(ctx context.Context) *RunnerSession {
	if ctx == nil {
		return nil
	}
	if rs, ok := ctx.Value(RunnerSessionKey).(*RunnerSession); ok && rs != nil {
		return rs
	}
	return nil
}

func effectiveVerbose(ctx context.Context) bool {
	if rs := runnerSessionFromContext(ctx); rs != nil {
		return rs.IsVerbose()
	}
	return false
}

func isVerboseEnabled(ctx context.Context) bool {
	return effectiveVerbose(ctx)
}

const (
	componentNativeAskAnchoredMRU  PromptComponent = "ask_anchored_mru"
	componentNativeProgression     PromptComponent = "progression_history"
	componentNativeRepairDirective PromptComponent = "repair_directive"
	componentNativeToolResults     PromptComponent = "tool_results"
	nativeReActPromptTotalChars                    = 16000
)

func buildNativeReActPrompt(req ai.ReasoningRequest, toolResults []nativeToolResult, askState askAnchoredMRUState) string {
	rendered, _ := buildNativeReActPromptWithReport(req, toolResults, askState, nativeReActPromptBudgetProfile())
	return rendered
}

func buildNativeReActPromptWithReport(req ai.ReasoningRequest, toolResults []nativeToolResult, askState askAnchoredMRUState, profile PromptBudgetProfile) (string, PromptBudgetReport) {
	sections := buildNativeReActPromptSections(req, toolResults, askState)
	trimmed, report := budgetNativePromptSections(sections, profile)
	return renderNativePromptSections(trimmed), report
}

func buildNativeReActPromptSections(req ai.ReasoningRequest, toolResults []nativeToolResult, askState askAnchoredMRUState) []PromptElement {
	sections := make([]PromptElement, 0, 7)
	if contextText := compactNativePromptSectionText(req.ContextText, 6000); contextText != "" {
		sections = append(sections, PromptElement{Component: ComponentFocusedContext, Content: "Context:\n" + contextText})
	}
	if historyText := compactNativePromptSectionText(req.HistoryText, 2500); historyText != "" {
		sections = append(sections, PromptElement{Component: ComponentHistory, Content: "History:\n" + historyText})
	}
	sections = append(sections, PromptElement{Component: ComponentUserQuery, Content: fmt.Sprintf("User Query: %s", strings.TrimSpace(req.UserQuery))})
	if len(toolResults) == 0 {
		return sections
	}

	sections = append(sections,
		PromptElement{Component: componentNativeAskAnchoredMRU, Content: "Ask-anchored MRU:\n" + formatAskAnchoredMRUState(askState)},
		PromptElement{Component: componentNativeProgression, Content: "Progression history:\n" + strings.TrimSpace(formatNativeProgressionHistory(toolResults))},
	)
	if directive := buildNativeRepairDirective(toolResults[len(toolResults)-1]); directive != "" {
		sections = append(sections, PromptElement{Component: componentNativeRepairDirective, Content: directive})
	}
	sections = append(sections,
		PromptElement{Component: componentNativeToolResults, Content: buildNativeToolResultsSection(toolResults)},
		PromptElement{Component: ComponentUserQuery, Content: "User: Analyze the tool response and continue the task. If another tool is required, call it. Otherwise provide the final answer to the user."},
	)
	return sections
}

func nativeReActPromptBudgetProfile() PromptBudgetProfile {
	return PromptBudgetProfile{
		TotalChars: nativeReActPromptTotalChars,
		ComponentCharBudgets: map[PromptComponent]int{
			ComponentFocusedContext:        6000,
			ComponentHistory:               2500,
			ComponentUserQuery:             900,
			componentNativeAskAnchoredMRU:  1800,
			componentNativeProgression:     5200,
			componentNativeRepairDirective: 1100,
			componentNativeToolResults:     5200,
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
}

func budgetNativePromptSections(sections []PromptElement, profile PromptBudgetProfile) ([]PromptElement, PromptBudgetReport) {
	if len(sections) == 0 {
		return nil, PromptBudgetReport{}
	}

	working := make([]PromptElement, 0, len(sections))
	report := PromptBudgetReport{ComponentStats: make([]PromptComponentBudgetStat, 0, len(sections))}
	for _, section := range sections {
		original := strings.TrimSpace(section.Content)
		content := original
		if maxChars, ok := profile.ComponentCharBudgets[section.Component]; ok && maxChars > 0 {
			content = trimNativePromptSectionContent(section.Component, content, maxChars)
		}
		content = strings.TrimSpace(content)
		report.OriginalTotalChars += len(original)
		report.FinalTotalChars += len(content)
		working = append(working, PromptElement{Component: section.Component, Content: content})
		report.ComponentStats = append(report.ComponentStats, PromptComponentBudgetStat{
			Component:     section.Component,
			OriginalChars: len(original),
			FinalChars:    len(content),
		})
	}

	if profile.TotalChars <= 0 {
		return filterEmptyPromptElements(working), report
	}

	working, report = trimNativePromptSectionsToTotalBudget(working, profile, report, true)
	if renderedNativePromptChars(working) > profile.TotalChars {
		working, report = trimNativePromptSectionsToTotalBudget(working, profile, report, false)
	}
	return filterEmptyPromptElements(working), report
}

func trimNativePromptSectionsToTotalBudget(sections []PromptElement, profile PromptBudgetProfile, report PromptBudgetReport, useFloor bool) ([]PromptElement, PromptBudgetReport) {
	working := append([]PromptElement(nil), sections...)
	excess := renderedNativePromptChars(working) - profile.TotalChars
	if excess <= 0 {
		report.FinalTotalChars = renderedNativePromptChars(working)
		return filterEmptyPromptElements(working), report
	}

	for _, component := range profile.TrimPriorityLowToHigh {
		for i := range working {
			if working[i].Component != component || excess <= 0 {
				continue
			}
			floor := 0
			if useFloor {
				if budget, ok := profile.ComponentCharBudgets[component]; ok && budget > 0 {
					floor = minPromptChars(budget / 3)
				}
			}
			if len(working[i].Content) <= floor {
				continue
			}

			target := len(working[i].Content) - excess
			if target < floor {
				target = floor
			}
			trimmed := trimNativePromptSectionContent(component, working[i].Content, target)
			reducedBy := len(working[i].Content) - len(trimmed)
			working[i].Content = strings.TrimSpace(trimmed)
			for idx := range report.ComponentStats {
				if report.ComponentStats[idx].Component == component {
					report.ComponentStats[idx].FinalChars = len(working[i].Content)
					break
				}
			}
			excess -= reducedBy
		}
	}

	working = filterEmptyPromptElements(working)
	report.FinalTotalChars = renderedNativePromptChars(working)
	return working, report
}

func trimNativePromptSectionContent(component PromptComponent, content string, maxChars int) string {
	content = strings.TrimSpace(content)
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	if maxChars < 32 {
		return content[:maxChars]
	}

	switch component {
	case ComponentFocusedContext, componentNativeAskAnchoredMRU:
		return compactNativePromptSectionText(content, maxChars)
	case ComponentHistory:
		return trimPromptComponentContent(component, content, maxChars)
	case componentNativeProgression, componentNativeToolResults:
		return compactNativePromptText(content, maxChars)
	default:
		return trimPromptComponentContent(component, content, maxChars)
	}
}

func filterEmptyPromptElements(elements []PromptElement) []PromptElement {
	filtered := make([]PromptElement, 0, len(elements))
	for _, el := range elements {
		if strings.TrimSpace(el.Content) == "" {
			continue
		}
		filtered = append(filtered, el)
	}
	return filtered
}

func renderedNativePromptChars(sections []PromptElement) int {
	filtered := filterEmptyPromptElements(sections)
	if len(filtered) == 0 {
		return 0
	}
	total := 0
	for _, section := range filtered {
		total += len(section.Content)
	}
	total += (len(filtered) - 1) * len("\n\n")
	return total
}

func renderNativePromptSections(sections []PromptElement) string {
	filtered := filterEmptyPromptElements(sections)
	parts := make([]string, 0, len(filtered))
	for _, section := range filtered {
		parts = append(parts, strings.TrimSpace(section.Content))
	}
	return strings.Join(parts, "\n\n")
}

func buildNativeRepairDirective(last nativeToolResult) string {
	if strings.Contains(last.Result, "Clarification required:") {
		return "Clarification directive: The last tool failure still remains unresolved after the first repair attempt. Do not call another tool yet. Ask the user one short, concrete clarification question that names the blocker and wait for the answer."
	}
	if !strings.Contains(last.Result, "Retry instruction:") {
		return ""
	}
	if last.Name == "native_tool_call" {
		return "Repair directive: The last model output produced an invalid native tool call. Your next step should be to call exactly one valid tool using arguments that conform to the provided tool schema and repair guidance below. Do not summarize the error as a final answer unless correction is impossible."
	}
	if strings.Contains(last.Result, "Repair strategy: research_first") {
		return fmt.Sprintf("Repair directive: The last tool call to %s failed because grounded schema or relation facts are still missing. Your next step should be to call list_stores first, prefer scoped stores:[...] for likely targets, reuse its schema/relations output as the source of truth, and only then return to %s if needed. Do not summarize the error as a final answer unless correction is impossible.", last.Name, last.Name)
	}
	return fmt.Sprintf("Repair directive: The last tool call to %s failed because its arguments were invalid. Your next step should be to call the same tool again with corrected arguments using the repair guidance below. Do not summarize the error as a final answer unless correction is impossible.", last.Name)
}

func buildNativeToolResultsSection(toolResults []nativeToolResult) string {
	var sb strings.Builder
	sb.WriteString("Tool results:\n")
	for i, result := range toolResults {
		keepExpanded := shouldRetainNativePromptBlob(i, len(toolResults)) || len(toolResults) == 1
		sb.WriteString(fmt.Sprintf("Step %d Tool: %s\n", i+1, result.Name))
		if len(toolResults) > 2 && !keepExpanded {
			if summary := formatNativeToolResultSummary(result); summary != "" {
				sb.WriteString(fmt.Sprintf("[Tool Summary]:\n%s\n\n", summary))
				continue
			}
		}
		if formattedArgs := formatNativePromptArgs(result.Args, keepExpanded); formattedArgs != "" {
			sb.WriteString(fmt.Sprintf("[Tool Args]:\n%s\n", formattedArgs))
		}
		sb.WriteString(fmt.Sprintf("[System Tool Response]:\n%s\n\n", formatNativePromptToolResponse(result.Result, keepExpanded)))
	}
	return strings.TrimSpace(sb.String())
}

func firstTrimmedPromptComponent(profile PromptBudgetProfile, report PromptBudgetReport) PromptComponent {
	for _, component := range profile.TrimPriorityLowToHigh {
		for _, stat := range report.ComponentStats {
			if stat.Component == component && stat.Trimmed() {
				return component
			}
		}
	}
	return ""
}

func formatNativePromptArgs(args map[string]any, keepExpanded bool) string {
	if len(args) == 0 {
		return ""
	}
	payload := compactNativePromptArgs(args)
	if keepExpanded {
		payload = expandNativePromptArgs(args)
	}
	bytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}
	return string(bytes)
}

func formatNativeToolResultSummary(result nativeToolResult) string {
	summary := make(map[string]any, 3)
	if len(result.Args) > 0 {
		summary["args"] = compactNativePromptArgs(result.Args)
	}
	if response := formatNativePromptToolResponse(result.Result, false); response != "" {
		summary["response"] = response
	}
	if result.Hint != nil {
		if metadata := buildNativeProgressionMetadata(result); len(metadata) > 0 {
			summary["progression"] = metadata
		}
	}
	if len(summary) == 0 {
		return ""
	}
	bytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return ""
	}
	return string(bytes)
}

func formatNativeProgressionHistory(toolResults []nativeToolResult) string {
	entries := make([]map[string]any, 0, len(toolResults))
	anchorHash := ""
	for i, result := range toolResults {
		keepExpanded := shouldRetainNativePromptBlob(i, len(toolResults))
		ingredients, nextAnchorHash := buildNativeProgressionIngredients(result, i, len(toolResults), anchorHash)
		if anchorHash == "" && nextAnchorHash != "" {
			anchorHash = nextAnchorHash
		}
		entry := map[string]any{
			"step":           i + 1,
			"tool":           result.Name,
			"ingredients":    ingredients,
			"generated_call": compactNativePromptArgs(result.Args),
			"result":         formatNativePromptToolResponse(result.Result, keepExpanded),
			"progression":    buildNativeProgressionMetadata(result),
		}
		if keepExpanded {
			entry["generated_call"] = expandNativePromptArgs(result.Args)
		}
		entries = append(entries, entry)
	}
	bytes, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "- progression history unavailable\n"
	}
	return string(bytes) + "\n"
}

func shouldRetainNativePromptBlob(index int, total int) bool {
	if total <= 2 {
		return false
	}
	return index == 0 || index == total-1
}

func compactNativePromptArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	compacted := make(map[string]any, len(args))
	for key, value := range args {
		if key == "script" {
			compacted["script_summary"] = summarizeNativeScriptArg(value)
			continue
		}
		compacted[key] = compactNativePromptValue(value)
	}
	return compacted
}

func expandNativePromptArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	bytes, err := json.Marshal(args)
	if err != nil {
		return compactNativePromptArgs(args)
	}
	var expanded map[string]any
	if err := json.Unmarshal(bytes, &expanded); err != nil {
		return compactNativePromptArgs(args)
	}
	return expanded
}

func compactNativePromptValue(value any) any {
	switch typed := value.(type) {
	case string:
		return compactNativePromptText(typed, 240)
	case []string:
		if len(typed) <= 8 {
			return typed
		}
		items := append([]string(nil), typed[:8]...)
		items = append(items, fmt.Sprintf("... (+%d more)", len(typed)-8))
		return items
	case []any:
		if len(typed) <= 8 {
			items := make([]any, 0, len(typed))
			for _, item := range typed {
				items = append(items, compactNativePromptValue(item))
			}
			return items
		}
		items := make([]any, 0, 9)
		for _, item := range typed[:8] {
			items = append(items, compactNativePromptValue(item))
		}
		items = append(items, fmt.Sprintf("... (+%d more)", len(typed)-8))
		return items
	case map[string]any:
		mapped := make(map[string]any, len(typed))
		for key, item := range typed {
			mapped[key] = compactNativePromptValue(item)
		}
		return mapped
	default:
		return value
	}
}

func summarizeNativeScriptArg(raw any) map[string]any {
	steps := coerceNativeScriptSteps(raw)
	if len(steps) == 0 {
		return map[string]any{"step_count": 0}
	}
	ops := make([]string, 0, len(steps))
	digests := make([]string, 0, len(steps))
	for i, step := range steps {
		op := strings.TrimSpace(firstNativeString(step, "op", "command"))
		if op == "" {
			op = fmt.Sprintf("step_%d", i+1)
		}
		ops = append(ops, op)
		digests = append(digests, summarizeNativeScriptStep(i+1, step))
	}
	return map[string]any{
		"step_count": len(steps),
		"ops":        ops,
		"steps":      digests,
	}
}

func coerceNativeScriptSteps(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []any:
		steps := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if step, ok := item.(map[string]any); ok {
				steps = append(steps, step)
			}
		}
		return steps
	case []map[string]any:
		return typed
	case []ScriptInstruction:
		steps := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			step := map[string]any{"op": item.Op}
			if item.Args != nil {
				step["args"] = item.Args
			}
			if item.InputVar != "" {
				step["input_var"] = item.InputVar
			}
			if item.ResultVar != "" {
				step["result_var"] = item.ResultVar
			}
			steps = append(steps, step)
		}
		return steps
	default:
		var steps []map[string]any
		bytes, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(bytes, &steps); err != nil {
			return nil
		}
		return steps
	}
}

func summarizeNativeScriptStep(index int, step map[string]any) string {
	op := strings.TrimSpace(firstNativeString(step, "op", "command"))
	parts := []string{fmt.Sprintf("%d. %s", index, op)}
	if args, _ := step["args"].(map[string]any); len(args) > 0 {
		switch strings.ToLower(op) {
		case "begin_tx":
			parts = append(parts, summarizeBeginTxArgs(args))
		case "open_store":
			parts = append(parts, summarizeOpenStoreArgs(args))
		case "filter":
			parts = append(parts, summarizeFilterArgs(args))
		case "join", "join_right":
			parts = append(parts, summarizeJoinArgs(args))
		case "project":
			parts = append(parts, summarizeProjectArgs(args))
		case "limit":
			parts = append(parts, summarizeLimitArgs(args))
		case "return":
			parts = append(parts, summarizeReturnArgs(args))
		default:
			parts = append(parts, summarizeGenericArgs(args))
		}
	}
	if inputVar := strings.TrimSpace(firstNativeString(step, "input_var")); inputVar != "" {
		parts = append(parts, "input="+inputVar)
	}
	if resultVar := strings.TrimSpace(firstNativeString(step, "result_var")); resultVar != "" {
		parts = append(parts, "-> "+resultVar)
	}
	return compactNativePromptText(strings.Join(filterEmptyStrings(parts), " | "), 180)
}

func summarizeBeginTxArgs(args map[string]any) string {
	parts := make([]string, 0, 2)
	if mode := strings.TrimSpace(firstNativeString(args, "mode")); mode != "" {
		parts = append(parts, "mode="+mode)
	}
	if database := strings.TrimSpace(firstNativeString(args, "database")); database != "" {
		parts = append(parts, "db="+database)
	}
	return strings.Join(parts, ", ")
}

func summarizeOpenStoreArgs(args map[string]any) string {
	parts := make([]string, 0, 2)
	if name := strings.TrimSpace(firstNativeString(args, "name", "store", "store_name")); name != "" {
		parts = append(parts, "store="+name)
	}
	if tx := strings.TrimSpace(firstNativeString(args, "transaction")); tx != "" {
		parts = append(parts, "tx="+tx)
	}
	return strings.Join(parts, ", ")
}

func summarizeFilterArgs(args map[string]any) string {
	if condition, ok := args["condition"].(map[string]any); ok && len(condition) > 0 {
		return "condition=" + summarizeConditionMap(condition)
	}
	return summarizeGenericArgs(args)
}

func summarizeJoinArgs(args map[string]any) string {
	parts := make([]string, 0, 3)
	if store := strings.TrimSpace(firstNativeString(args, "store", "with", "right_store")); store != "" {
		parts = append(parts, "store="+store)
	}
	if on, ok := args["on"].(map[string]any); ok && len(on) > 0 {
		parts = append(parts, "on="+summarizeJoinOnMap(on))
	}
	if joinType := strings.TrimSpace(firstNativeString(args, "type")); joinType != "" {
		parts = append(parts, "type="+joinType)
	}
	return strings.Join(parts, ", ")
}

func summarizeProjectArgs(args map[string]any) string {
	fields, ok := args["fields"]
	if !ok {
		return summarizeGenericArgs(args)
	}
	return "fields=" + compactNativePromptText(fmt.Sprintf("%v", fields), 80)
}

func summarizeLimitArgs(args map[string]any) string {
	if limit, ok := args["count"]; ok {
		return fmt.Sprintf("count=%v", limit)
	}
	if limit, ok := args["limit"]; ok {
		return fmt.Sprintf("limit=%v", limit)
	}
	return summarizeGenericArgs(args)
}

func summarizeReturnArgs(args map[string]any) string {
	if value := strings.TrimSpace(firstNativeString(args, "value")); value != "" {
		return "value=" + value
	}
	return summarizeGenericArgs(args)
}

func summarizeGenericArgs(args map[string]any) string {
	parts := make([]string, 0, len(args))
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, compactNativePromptValue(args[key])))
	}
	return compactNativePromptText(strings.Join(parts, ", "), 120)
}

func summarizeConditionMap(condition map[string]any) string {
	keys := make([]string, 0, len(condition))
	for key := range condition {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%s", key, summarizeConditionValue(condition[key])))
	}
	return compactNativePromptText(strings.Join(parts, "; "), 120)
}

func summarizeConditionValue(value any) string {
	if mapped, ok := value.(map[string]any); ok && len(mapped) > 0 {
		keys := make([]string, 0, len(mapped))
		for key := range mapped {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", key, mapped[key]))
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprintf("%v", value)
}

func summarizeJoinOnMap(on map[string]any) string {
	keys := make([]string, 0, len(on))
	for key := range on {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s->%v", key, on[key]))
	}
	return compactNativePromptText(strings.Join(parts, "; "), 100)
}

func formatNativePromptToolResponse(result string, keepExpanded bool) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return ""
	}
	if keepExpanded {
		return trimmed
	}
	if strings.Contains(trimmed, "Retry instruction:") {
		lines := strings.Split(trimmed, "\n")
		kept := make([]string, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len(kept) == 0 || strings.HasPrefix(line, "Tool:") || strings.HasPrefix(line, "Repair ") || strings.HasPrefix(line, "Research ") || strings.HasPrefix(line, "Retry instruction:") || strings.HasPrefix(line, "Suggested fix example:") {
				kept = append(kept, compactNativePromptText(line, 260))
			}
		}
		return strings.Join(kept, "\n")
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= 6 && len(trimmed) <= 1200 {
		return trimmed
	}
	kept := make([]string, 0, 7)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kept = append(kept, compactNativePromptText(line, 220))
		if len(kept) == 6 {
			break
		}
	}
	remaining := len(lines) - len(kept)
	if remaining > 0 {
		kept = append(kept, fmt.Sprintf("... (+%d more lines)", remaining))
	}
	return strings.Join(kept, "\n")
}

func compactNativePromptText(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if maxChars <= 0 || len(trimmed) <= maxChars {
		return trimmed
	}
	head := (maxChars * 2) / 3
	tail := maxChars - head - len("\n... [truncated] ...\n")
	if tail < 32 {
		tail = 32
		head = maxChars - tail - len("\n... [truncated] ...\n")
		if head < 32 {
			head = maxChars / 2
			tail = maxChars / 2
		}
	}
	return trimmed[:head] + "\n... [truncated] ...\n" + trimmed[len(trimmed)-tail:]
}

func compactNativePromptSectionText(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if maxChars <= 0 || len(trimmed) <= maxChars {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	selected := make([]string, 0, len(lines))
	contentInSection := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		plain := strings.TrimSpace(line)
		if plain == "" {
			continue
		}
		if isNativePromptSectionHeader(plain) {
			selected = append(selected, plain)
			contentInSection = 0
			continue
		}
		if contentInSection < 4 {
			selected = append(selected, plain)
			contentInSection++
		}
	}
	if len(selected) == 0 {
		return compactNativePromptText(trimmed, maxChars)
	}
	for len(selected) > 0 {
		joined := strings.Join(selected, "\n")
		if len(joined) <= maxChars {
			return joined
		}
		removeIdx := lastRemovableNativePromptLine(selected)
		if removeIdx == -1 {
			break
		}
		selected = append(selected[:removeIdx], selected[removeIdx+1:]...)
	}
	return compactNativePromptText(strings.Join(selected, "\n"), maxChars)
}

func isNativePromptSectionHeader(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
		return true
	}
	if strings.HasSuffix(line, ":") {
		return true
	}
	if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
		return false
	}
	return strings.ToUpper(line) == line && len(line) <= 80
}

func lastRemovableNativePromptLine(lines []string) int {
	protected := make(map[int]bool, len(lines))
	awaitingFirstContent := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isNativePromptSectionHeader(trimmed) {
			protected[i] = true
			awaitingFirstContent = true
			continue
		}
		if awaitingFirstContent {
			protected[i] = true
			awaitingFirstContent = false
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if !protected[i] {
			return i
		}
	}
	return -1
}

func firstNativeString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func filterEmptyStrings(values []string) []string {
	filtered := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func buildNativeProgressionIngredients(result nativeToolResult, index int, total int, anchorHash string) (map[string]any, string) {
	envelope := buildNativeIngredientEnvelope(result)
	envelopeHash := hashNativeIngredientEnvelope(envelope)
	keepAnchorOutsideHistory := index == 0 && shouldRetainNativePromptBlob(index, total)
	if keepAnchorOutsideHistory {
		payload := map[string]any{
			"anchor_hash":          envelopeHash,
			"anchor_rendered":      false,
			"anchor_render_reason": "covered_by_retained_context",
		}
		return payload, envelopeHash
	}

	ingredients := map[string]any{
		"tool_info":         result.Name,
		"envelope_hash":     envelopeHash,
		"envelope_rendered": true,
	}
	if anchorHash != "" && index > 0 {
		ingredients["anchor_ref"] = anchorHash
	}
	if anchorHash != "" && index < total-1 && envelopeHash == anchorHash {
		ingredients["envelope_rendered"] = false
		ingredients["render_suppressed_reason"] = "duplicate_of_anchor"
		return ingredients, anchorHash
	}
	if len(envelope) > 0 {
		ingredients["envelope"] = envelope
	}
	return ingredients, anchorHash
}

func buildNativeIngredientEnvelope(result nativeToolResult) map[string]any {
	envelope := map[string]any{
		"tool_info": result.Name,
	}
	if !strings.Contains(result.Result, "Retry instruction:") {
		if facts := summarizeSuccessfulToolResult(result); len(facts) > 0 {
			envelope["confirmed_facts"] = facts
		}
		if result.Name == "list_stores" {
			if schemaSnapshot := extractListStoresSchemaSnapshot(result.Result); len(schemaSnapshot) > 0 {
				envelope["schema_snapshot"] = schemaSnapshot
			}
		}
	}
	if repairStrategy := extractRepairStrategy(result.Result); repairStrategy != "" {
		envelope["repair_strategy"] = repairStrategy
	}
	return envelope
}

func hashNativeIngredientEnvelope(envelope map[string]any) string {
	if len(envelope) == 0 {
		return ""
	}
	bytes, err := json.Marshal(envelope)
	if err != nil {
		return ""
	}
	return "ing_env_" + shortNativeIngredientHash(string(bytes))
}

func shortNativeIngredientHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:6])
}

func buildNativeProgressionMetadata(result nativeToolResult) map[string]any {
	metadata := make(map[string]any)
	if retryInstruction := extractRetryInstruction(result.Result); retryInstruction != "" {
		metadata["retry_instruction"] = retryInstruction
	}
	if result.Hint != nil {
		if status := strings.TrimSpace(result.Hint.Status); status != "" {
			metadata["status"] = status
		}
		if result.Hint.CompletionDelta > 0 {
			metadata["completion_delta"] = result.Hint.CompletionDelta
		}
		if len(result.Hint.Tips) > 0 {
			metadata["tips"] = append([]string(nil), result.Hint.Tips...)
		}
		if len(result.Hint.Clues) > 0 {
			metadata["clues"] = append([]string(nil), result.Hint.Clues...)
		}
		if len(result.Hint.Missing) > 0 {
			metadata["missing"] = append([]string(nil), result.Hint.Missing...)
		}
		if len(result.Hint.SuggestedNextTools) > 0 {
			metadata["suggested_next_tools"] = append([]string(nil), result.Hint.SuggestedNextTools...)
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func extractRetryInstruction(resultText string) string {
	const marker = "Retry instruction:"
	idx := strings.Index(resultText, marker)
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(resultText[idx+len(marker):])
}

func extractRepairStrategy(resultText string) string {
	const marker = "Repair strategy:"
	idx := strings.Index(resultText, marker)
	if idx == -1 {
		return ""
	}
	remaining := resultText[idx+len(marker):]
	if newline := strings.IndexByte(remaining, '\n'); newline >= 0 {
		remaining = remaining[:newline]
	}
	return strings.TrimSpace(remaining)
}

func buildAskAnchoredMRUState(toolResults []nativeToolResult) askAnchoredMRUState {
	state := make(askAnchoredMRUState, 0, 8)
	if len(toolResults) == 0 {
		return state
	}

	state = appendAskMRUItem(state, "ASK_ITERATION", fmt.Sprintf("%d", len(toolResults)))
	last := toolResults[len(toolResults)-1]
	state = appendAskMRUItem(state, "ASK_LAST_TOOL", last.Name)

	if strings.Contains(last.Result, "Retry instruction:") {
		state = appendAskMRUItem(state, "ASK_LAST_OUTCOME", "repair_required")
		state = appendAskMRUItem(state, "ASK_CURRENT_FOCUS", "Preserve valid work, classify the failure, and change only the broken slice in the next call.")
		state = appendAskMRUItem(state, "ASK_NEXT_DELTA", summarizeNextRepairNeed(last))
		if attempted := summarizeAttemptedArgs(last.Result); attempted != "" {
			state = appendAskMRUItem(state, "ASK_PRESERVE", attempted)
		}
	} else {
		state = appendAskMRUItem(state, "ASK_LAST_OUTCOME", "tool_completed")
		state = appendAskMRUItem(state, "ASK_CURRENT_FOCUS", "Use confirmed tool output to narrow the next step instead of replaying the full task.")
		for _, fact := range summarizeSuccessfulToolResult(last) {
			state = appendAskConfirmedMRUItem(state, fact)
		}
		state = appendAskProgressHintMRUItems(state, last.Hint)
	}

	for i := len(toolResults) - 2; i >= 0; i-- {
		result := toolResults[i]
		if strings.Contains(result.Result, "Retry instruction:") {
			continue
		}
		for _, fact := range summarizeSuccessfulToolResult(result) {
			state = appendAskConfirmedMRUItem(state, fact)
		}
		state = appendAskProgressHintMRUItems(state, result.Hint)
		state = appendAskMRUItem(state, "ASK_PRESERVE", fmt.Sprintf("Previous successful tool preserved: %s", result.Name))
		break
	}

	if !askMRUHasCategoryPrefix(state, askMRUCategoryConfirmed) && !askMRUHasCategory(state, "ASK_PRESERVE") {
		state = appendAskMRUItem(state, "ASK_PRESERVE", "No grounded facts yet; keep the next action narrow and schema-conformant.")
	}

	return state
}

func formatAskAnchoredMRUState(state askAnchoredMRUState) string {
	if len(state) == 0 {
		return "- No Ask-anchored MRU state yet.\n"
	}

	var sb strings.Builder
	for _, item := range state {
		switch {
		case item.Category == "ASK_ITERATION":
			sb.WriteString(fmt.Sprintf("- Iteration: %s\n", item.Context))
		case item.Category == "ASK_LAST_TOOL":
			sb.WriteString(fmt.Sprintf("- Last tool: %s\n", item.Context))
		case item.Category == "ASK_LAST_OUTCOME":
			sb.WriteString(fmt.Sprintf("- Last outcome: %s\n", item.Context))
		case item.Category == "ASK_CURRENT_FOCUS":
			sb.WriteString(fmt.Sprintf("- Current focus: %s\n", item.Context))
		case strings.HasPrefix(item.Category, askMRUCategoryConfirmed):
			sb.WriteString(fmt.Sprintf("- Confirmed: %s\n", item.Context))
		case item.Category == "ASK_PRESERVE":
			sb.WriteString(fmt.Sprintf("- Preserve: %s\n", item.Context))
		case item.Category == "ASK_NEXT_DELTA":
			sb.WriteString(fmt.Sprintf("- Next delta: %s\n", item.Context))
		case item.Category == "ASK_PROGRESS":
			sb.WriteString(fmt.Sprintf("- Progress: %s\n", item.Context))
		case item.Category == "ASK_TIP":
			sb.WriteString(fmt.Sprintf("- Tip: %s\n", item.Context))
		case item.Category == "ASK_CLUE":
			sb.WriteString(fmt.Sprintf("- Clue: %s\n", item.Context))
		case item.Category == "ASK_SUGGESTED_TOOL":
			sb.WriteString(fmt.Sprintf("- Suggested tool: %s\n", item.Context))
		case item.Category == "ASK_MISSING":
			sb.WriteString(fmt.Sprintf("- Missing: %s\n", item.Context))
		}
	}
	return sb.String()
}

func appendAskProgressHintMRUItems(state askAnchoredMRUState, hint *ai.ToolProgressHint) askAnchoredMRUState {
	if hint == nil {
		return state
	}
	if status := strings.TrimSpace(hint.Status); status != "" || hint.CompletionDelta > 0 {
		progressText := status
		if hint.CompletionDelta > 0 {
			if progressText != "" {
				progressText += fmt.Sprintf(" (+%.2f)", hint.CompletionDelta)
			} else {
				progressText = fmt.Sprintf("+%.2f", hint.CompletionDelta)
			}
		}
		state = appendAskMRUItem(state, "ASK_PROGRESS", progressText)
	}
	for _, tip := range hint.Tips {
		state = appendAskMRUItem(state, "ASK_TIP", tip)
	}
	for _, clue := range hint.Clues {
		state = appendAskMRUItem(state, "ASK_CLUE", clue)
	}
	for _, toolName := range hint.SuggestedNextTools {
		state = appendAskMRUItem(state, "ASK_SUGGESTED_TOOL", toolName)
	}
	for _, missing := range hint.Missing {
		state = appendAskMRUItem(state, "ASK_MISSING", missing)
	}
	return state
}

func appendAskConfirmedMRUItem(state askAnchoredMRUState, fact string) askAnchoredMRUState {
	category := askMRUCategoryConfirmed
	if joinSelectionKey, ok := classifyExecuteScriptJoinFactKey(fact); ok {
		category = fmt.Sprintf("%s_%s", askMRUCategoryConfirmedJoinSelection, normalizeMRUFactKey(joinSelectionKey))
	} else if filterSelectionKey, ok := classifyExecuteScriptFilterFactKey(fact); ok {
		category = fmt.Sprintf("%s_%s", askMRUCategoryConfirmedFilterSelection, normalizeMRUFactKey(filterSelectionKey))
	} else if details, ok := classifyConfirmedStoreFact(fact); ok && details.FactType == confirmedStoreFactTypeSchema {
		category = fmt.Sprintf("%s_%s", askMRUCategoryConfirmedStoreSchema, normalizeMRUFactKey(details.StoreName))
	} else if details, ok := classifyConfirmedStoreFact(fact); ok && details.FactType == confirmedStoreFactTypeRelations {
		category = fmt.Sprintf("%s_%s", askMRUCategoryConfirmedStoreRelation, normalizeMRUFactKey(details.CategoryKey()))
	}
	return appendAskMRUItem(state, category, fact)
}

func appendAskMRUItem(state askAnchoredMRUState, category string, context string) askAnchoredMRUState {
	if strings.TrimSpace(context) == "" {
		return state
	}
	return append(state, MRUItem{Category: category, Context: context, Scope: MRUScopeAsk})
}

func askMRUHasCategory(state askAnchoredMRUState, category string) bool {
	for _, item := range state {
		if item.Category == category {
			return true
		}
	}
	return false
}

func askMRUHasCategoryPrefix(state askAnchoredMRUState, prefix string) bool {
	for _, item := range state {
		if strings.HasPrefix(item.Category, prefix) {
			return true
		}
	}
	return false
}

func summarizeNextRepairNeed(result nativeToolResult) string {
	if result.Name == "native_tool_call" {
		return "Return exactly one valid tool call and avoid malformed function-call output."
	}
	if strings.Contains(result.Result, "Repair strategy: research_first") {
		return "Research missing schema or relation facts with scoped list_stores calls before retrying execute_script."
	}
	return fmt.Sprintf("Repair %s without restarting the whole plan or broadening scope.", result.Name)
}

func inferLikelyResearchStores(args map[string]any) []string {
	if len(args) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	addStore := func(raw any) {
		name, ok := raw.(string)
		if !ok {
			return
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "@") || strings.HasPrefix(name, "$") {
			return
		}
		seen[name] = struct{}{}
	}
	if stores, ok := args["stores"].([]any); ok {
		for _, store := range stores {
			addStore(store)
		}
	}
	addStore(args["store"])
	if script, ok := args["script"].([]any); ok {
		for _, stepRaw := range script {
			step, ok := stepRaw.(map[string]any)
			if !ok {
				continue
			}
			stepArgs, _ := step["args"].(map[string]any)
			if stepArgs == nil {
				continue
			}
			addStore(stepArgs["store"])
			addStore(stepArgs["name"])
			if stores, ok := stepArgs["stores"].([]any); ok {
				for _, store := range stores {
					addStore(store)
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	stores := make([]string, 0, len(seen))
	for store := range seen {
		stores = append(stores, store)
	}
	sort.Strings(stores)
	return stores
}

func formatScopedListStoresHint(args map[string]any) string {
	stores := inferLikelyResearchStores(args)
	if len(stores) == 0 {
		return " and prefer scoped args like stores:[\"users\",\"orders\"] when likely targets are already known"
	}
	quoted := make([]string, 0, len(stores))
	for _, store := range stores {
		quoted = append(quoted, fmt.Sprintf("%q", store))
	}
	return fmt.Sprintf(" using stores:[%s]", strings.Join(quoted, ","))
}

func summarizeAttemptedArgs(resultText string) string {
	const marker = "Attempted args:\n"
	start := strings.Index(resultText, marker)
	if start == -1 {
		return ""
	}
	start += len(marker)
	end := strings.Index(resultText[start:], "\nRetry instruction:")
	if end == -1 {
		end = len(resultText) - start
	}
	args := strings.TrimSpace(resultText[start : start+end])
	if args == "" || args == "{}" {
		return "Previous call shape existed but carried no reusable arguments."
	}
	return fmt.Sprintf("Reuse the valid structure from attempted args before changing invalid fields:\n%s", args)
}

func summarizeSuccessfulToolResult(result nativeToolResult) []string {
	return ai.SummarizeSuccessfulToolResult(nativeToolResultAsShared(result))
}

func summarizeOutcomeFacts(toolResults []nativeToolResult) []string {
	return ai.SummarizeOutcomeFacts(nativeToolResultsAsShared(toolResults))
}

func summarizeOutcomeRecipes(toolResults []nativeToolResult) []ai.LearnedRecipe {
	return ai.SummarizeOutcomeRecipes(nativeToolResultsAsShared(toolResults))
}

func nativeToolResultAsShared(result nativeToolResult) ai.ReActToolResult {
	return ai.ReActToolResult{
		Name:   result.Name,
		Result: result.Result,
		Args:   result.Args,
		Hint:   result.Hint,
	}
}

func nativeToolResultsAsShared(results []nativeToolResult) []ai.ReActToolResult {
	if len(results) == 0 {
		return nil
	}
	shared := make([]ai.ReActToolResult, 0, len(results))
	for _, result := range results {
		shared = append(shared, nativeToolResultAsShared(result))
	}
	return shared
}

func isRecoverableRepairResult(result nativeToolResult, toolName string, strategy nativeRepairStrategy) bool {
	if result.Name != toolName || !strings.Contains(result.Result, "Retry instruction:") {
		return false
	}
	if strategy == "" {
		return true
	}
	return strings.Contains(result.Result, fmt.Sprintf("Repair strategy: %s", strategy))
}

func hasSuccessfulTool(results []nativeToolResult, toolName string) bool {
	for _, result := range results {
		if result.Name == toolName && !strings.Contains(result.Result, "Retry instruction:") {
			return true
		}
	}
	return false
}

func hasSuccessfulToolSequence(results []nativeToolResult, names ...string) bool {
	if len(names) == 0 {
		return false
	}
	nameIdx := 0
	for _, result := range results {
		if strings.Contains(result.Result, "Retry instruction:") {
			continue
		}
		if result.Name != names[nameIdx] {
			continue
		}
		nameIdx++
		if nameIdx == len(names) {
			return true
		}
	}
	return false
}

func detectAskLoopProgress(previousResults []nativeToolResult, currentResults []nativeToolResult) askLoopProgressDelta {
	if len(currentResults) == 0 {
		return askLoopProgressDelta{}
	}
	latest := currentResults[len(currentResults)-1]
	if strings.Contains(latest.Result, "Retry instruction:") {
		return askLoopProgressDelta{}
	}
	delta := askLoopProgressDelta{
		NewFacts:   diffStringsAsSet(summarizeOutcomeFacts(currentResults), summarizeOutcomeFacts(previousResults)),
		NewRecipes: diffRecipeIDs(summarizeOutcomeRecipes(currentResults), summarizeOutcomeRecipes(previousResults)),
		HintSignal: cloneToolProgressHint(latest.Hint),
	}
	delta.Progressing = len(delta.NewFacts) > 0 || len(delta.NewRecipes) > 0 || toolProgressHintSignalsProgress(latest.Hint)
	return delta
}

func diffStringsAsSet(current []string, previous []string) []string {
	if len(current) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(previous))
	for _, item := range previous {
		seen[strings.TrimSpace(item)] = true
	}
	delta := make([]string, 0, len(current))
	for _, item := range current {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		delta = append(delta, trimmed)
	}
	return delta
}

func diffRecipeIDs(current []ai.LearnedRecipe, previous []ai.LearnedRecipe) []string {
	if len(current) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(previous))
	for _, item := range previous {
		seen[strings.TrimSpace(item.ID)] = true
	}
	delta := make([]string, 0, len(current))
	for _, item := range current {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		delta = append(delta, id)
	}
	return delta
}

func toolProgressHintSignalsProgress(hint *ai.ToolProgressHint) bool {
	if hint == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(hint.Status))
	if status == "progressing" || status == "complete" || status == "closer" {
		return true
	}
	return hint.CompletionDelta > 0 || len(hint.Tips) > 0 || len(hint.Clues) > 0 || len(hint.SuggestedNextTools) > 0
}

func shouldShortCircuitAskLoopOnToolHint(hint *ai.ToolProgressHint) bool {
	if hint == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(hint.Status)) {
	case "anti_success", "blocked", "error", "failed", "fatal", "hard_error", "terminal_error":
		return true
	default:
		return false
	}
}

func unwrapToolResultEnvelope(rawResult string) (string, *ai.ToolProgressHint) {
	trimmed := strings.TrimSpace(rawResult)
	if trimmed == "" {
		return rawResult, nil
	}
	var envelope ai.ToolResultEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return rawResult, nil
	}
	if len(envelope.ToolResult) == 0 && envelope.ProgressHint == nil {
		return rawResult, nil
	}
	result := formatEnvelopeToolResult(envelope.ToolResult)
	if strings.TrimSpace(result) == "" {
		result = rawResult
	}
	return result, cloneToolProgressHint(envelope.ProgressHint)
}

func formatEnvelopeToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		if bytes, marshalErr := json.Marshal(value); marshalErr == nil {
			return string(bytes)
		}
	}
	return string(raw)
}

func cloneToolProgressHint(hint *ai.ToolProgressHint) *ai.ToolProgressHint {
	if hint == nil {
		return nil
	}
	cloned := *hint
	cloned.Missing = append([]string(nil), hint.Missing...)
	cloned.Tips = append([]string(nil), hint.Tips...)
	cloned.Clues = append([]string(nil), hint.Clues...)
	cloned.SuggestedNextTools = append([]string(nil), hint.SuggestedNextTools...)
	return &cloned
}

func extractListStoresSchemaSnapshot(resultText string) []map[string]any {
	trimmed := strings.TrimSpace(resultText)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	snapshot := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "Stores:") {
			continue
		}
		entry := extractListStoresSchemaLineSnapshot(line)
		if len(entry) == 0 {
			continue
		}
		snapshot = append(snapshot, entry)
	}
	return snapshot
}

func extractListStoresSchemaLineSnapshot(line string) map[string]any {
	idx := strings.Index(line, " schema=")
	if idx <= 0 {
		return nil
	}
	storeName := strings.TrimSpace(line[:idx])
	remainder := strings.TrimSpace(line[idx+len(" schema="):])
	if storeName == "" || remainder == "" {
		return nil
	}
	entry := map[string]any{"store": storeName}
	relationsIdx := strings.Index(remainder, " relations=")
	schemaPart := remainder
	if relationsIdx >= 0 {
		schemaPart = strings.TrimSpace(remainder[:relationsIdx])
		relationsPart := strings.TrimSpace(remainder[relationsIdx+len(" relations="):])
		if relations := parseListStoresRelations(relationsPart); len(relations) > 0 {
			entry["relations"] = relations
		}
	}
	schemaPart = trimListStoresSchemaMetadata(schemaPart)
	if fields := parseListStoresFields(schemaPart); len(fields) > 0 {
		entry["fields"] = fields
	}
	return entry
}

func parseListStoresFields(schemaPart string) []map[string]string {
	schemaPart = strings.TrimSpace(schemaPart)
	schemaPart = strings.TrimPrefix(schemaPart, "{")
	schemaPart = strings.TrimSuffix(schemaPart, "}")
	fieldsRaw := splitTopLevelDelimited(schemaPart, ',')
	fields := make([]map[string]string, 0, len(fieldsRaw))
	for _, rawField := range fieldsRaw {
		rawField = strings.TrimSpace(rawField)
		if rawField == "" {
			continue
		}
		name, fieldType, ok := strings.Cut(rawField, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		fieldType = strings.TrimSpace(fieldType)
		if name == "" || fieldType == "" {
			continue
		}
		fields = append(fields, map[string]string{"name": name, "type": fieldType})
	}
	return fields
}

func parseListStoresRelations(relationsPart string) []string {
	relationsPart = strings.TrimSpace(relationsPart)
	relationsPart = strings.TrimPrefix(relationsPart, "[")
	relationsPart = strings.TrimSuffix(relationsPart, "]")
	if relationsPart == "" {
		return nil
	}
	relationItems := splitTopLevelDelimited(relationsPart, ',')
	relations := make([]string, 0, len(relationItems))
	for _, relation := range relationItems {
		relation = strings.TrimSpace(relation)
		if relation == "" {
			continue
		}
		relations = append(relations, relation)
	}
	return relations
}

func trimListStoresSchemaMetadata(schemaPart string) string {
	schemaPart = strings.TrimSpace(schemaPart)
	if schemaPart == "" {
		return ""
	}
	if strings.HasPrefix(schemaPart, "{") {
		if end := findBalancedSegmentEnd(schemaPart, '{', '}'); end > 0 {
			return strings.TrimSpace(schemaPart[:end])
		}
	}
	if descIdx := strings.Index(schemaPart, " description="); descIdx >= 0 {
		return strings.TrimSpace(schemaPart[:descIdx])
	}
	return schemaPart
}

func splitTopLevelDelimited(text string, delimiter rune) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	parts := make([]string, 0, 8)
	start := 0
	parenDepth := 0
	braceDepth := 0
	bracketDepth := 0
	inString := false
	escaped := false
	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		default:
			if r == delimiter && parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				parts = append(parts, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(text[start:]))
	return parts
}

func findBalancedSegmentEnd(text string, open, close rune) int {
	depth := 0
	inString := false
	escaped := false
	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func formatRecoverableToolError(repair pendingToolRepair, args map[string]any, err error) string {
	argsJSON := "{}"
	if len(args) > 0 {
		if b, marshalErr := json.MarshalIndent(args, "", "  "); marshalErr == nil {
			argsJSON = string(b)
		}
	}

	categoryLine := ""
	exampleLine := ""
	strategyLine := ""
	guidanceLine := ""
	validationIssues := collectExecuteScriptValidationIssues(err)
	if len(validationIssues) > 0 {
		categories := make([]string, 0, len(validationIssues))
		examples := make([]string, 0, len(validationIssues))
		hasJoinIssue := false
		seenCategories := map[string]struct{}{}
		seenExamples := map[string]struct{}{}
		for _, validationErr := range validationIssues {
			if validationErr.Category != "" {
				if _, ok := seenCategories[validationErr.Category]; !ok {
					seenCategories[validationErr.Category] = struct{}{}
					categories = append(categories, validationErr.Category)
				}
			}
			if validationErr.Example != "" {
				if _, ok := seenExamples[validationErr.Example]; !ok {
					seenExamples[validationErr.Example] = struct{}{}
					examples = append(examples, validationErr.Example)
				}
			}
			switch validationErr.Category {
			case "invalid_join_on_placeholder", "invalid_join_on_field_placeholder":
				hasJoinIssue = true
			}
		}
		if len(categories) > 0 {
			label := "Repair category"
			if len(categories) > 1 {
				label = "Repair categories"
			}
			categoryLine = fmt.Sprintf("\n%s: %s", label, strings.Join(categories, ", "))
		}
		if len(examples) == 1 {
			exampleLine = fmt.Sprintf("\nSuggested fix example:\n%s", examples[0])
		} else if len(examples) > 1 {
			exampleLine = fmt.Sprintf("\nSuggested fix examples:\n- %s", strings.Join(examples, "\n- "))
		}
		if hasJoinIssue {
			guidanceLine = "\nJoin repair note: After list_stores confirms a relation path, prefer relation+target for relation-driven joins. If you keep join.on, rewrite only the invalid join slice and use concrete field strings from the confirmed relation mapping."
		}
	}
	if repair.Strategy != "" {
		strategyLine = fmt.Sprintf("\nRepair strategy: %s", repair.Strategy)
		if repair.Strategy == nativeRepairStrategyResearchFirst && repair.ResearchReason != "" {
			strategyLine += fmt.Sprintf("\nResearch reason: %s", repair.ResearchReason)
		}
	}

	retryInstruction := "Retry instruction: Return a corrected call for the same tool. Preserve valid arguments, fix invalid or missing arguments, and do not repeat the same malformed shape."
	if repair.Strategy == nativeRepairStrategyResearchFirst {
		researchTool := repair.ResearchTool
		if researchTool == "" {
			researchTool = "list_stores"
		}
		retryInstruction = fmt.Sprintf("Retry instruction: Call %s first%s to research the missing schema or relation facts, then return to %s with corrected grounded arguments. Preserve valid arguments and do not restart the whole plan.", researchTool, formatScopedListStoresHint(args), repair.ToolName)
	}

	return fmt.Sprintf(
		"Tool execution error: %v\nTool: %s%s%s%s%s\nAttempted args:\n%s\n%s",
		err,
		repair.ToolName,
		categoryLine,
		exampleLine,
		strategyLine,
		guidanceLine,
		argsJSON,
		retryInstruction,
	)
}

func formatClarificationRequiredToolError(repair pendingToolRepair, args map[string]any, err error) string {
	argsJSON := "{}"
	if len(args) > 0 {
		if b, marshalErr := json.MarshalIndent(args, "", "  "); marshalErr == nil {
			argsJSON = string(b)
		}
	}

	reason := "The ask still failed after one repair attempt and now needs user clarification before more tool calls."
	if repair.ResearchReason != "" {
		reason = repair.ResearchReason
	}

	categoryLine := ""
	exampleLine := ""
	guidanceLine := ""
	validationIssues := collectExecuteScriptValidationIssues(err)
	if len(validationIssues) > 0 {
		categories := make([]string, 0, len(validationIssues))
		examples := make([]string, 0, len(validationIssues))
		hasJoinIssue := false
		seenCategories := map[string]struct{}{}
		seenExamples := map[string]struct{}{}
		for _, validationErr := range validationIssues {
			if validationErr.Category != "" {
				if _, ok := seenCategories[validationErr.Category]; !ok {
					seenCategories[validationErr.Category] = struct{}{}
					categories = append(categories, validationErr.Category)
				}
			}
			if validationErr.Example != "" {
				if _, ok := seenExamples[validationErr.Example]; !ok {
					seenExamples[validationErr.Example] = struct{}{}
					examples = append(examples, validationErr.Example)
				}
			}
			switch validationErr.Category {
			case "invalid_join_on_placeholder", "invalid_join_on_field_placeholder":
				hasJoinIssue = true
			}
		}
		if len(categories) > 0 {
			label := "Repair category"
			if len(categories) > 1 {
				label = "Repair categories"
			}
			categoryLine = fmt.Sprintf("\n%s: %s", label, strings.Join(categories, ", "))
		}
		if len(examples) == 1 {
			exampleLine = fmt.Sprintf("\nSuggested fix example:\n%s", examples[0])
		} else if len(examples) > 1 {
			exampleLine = fmt.Sprintf("\nSuggested fix examples:\n- %s", strings.Join(examples, "\n- "))
		}
		if hasJoinIssue {
			guidanceLine = "\nJoin repair note: The researched relation still does not fully resolve this join. Ask for the missing join mapping instead of inventing a new one."
		}
	}

	return fmt.Sprintf(
		"Clarification required: %s\nTool execution error: %v\nTool: %s%s%s%s\nAttempted args:\n%s\nUser-facing next step: Ask one short clarification question that resolves the ambiguity blocking this tool call. Do not call more tools until the user answers.",
		reason,
		err,
		repair.ToolName,
		categoryLine,
		exampleLine,
		guidanceLine,
		argsJSON,
	)
}

func formatRecoverableGenerationError(err error) string {
	return fmt.Sprintf(
		"Model generation error: %v\nRetry instruction: Return exactly one valid native tool call that conforms to the provided tool schema. Do not emit malformed function calls, partial arguments, or placeholder-only argument shapes.",
		err,
	)
}

func formatLLMGeneratedScriptForLog(toolName string, args map[string]any) string {
	if toolName != "execute_script" || len(args) == 0 {
		return ""
	}
	script, ok := args["script"]
	if !ok {
		return ""
	}
	bytes, err := json.Marshal(script)
	if err != nil {
		return ""
	}
	return formatLogSeparatedMessage("execute_script", string(bytes))
}

func formatLogSeparatedMessage(label, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	separator := strings.Repeat("=", 24)
	return fmt.Sprintf("\n%s BEGIN %s %s\n%s\n%s END %s %s", separator, label, separator, trimmed, separator, label, separator)
}

func extractEmbeddedNativeToolCall(text string) (ai.ToolCall, bool, error) {
	trimmed := strings.TrimSpace(text)
	idx := strings.Index(trimmed, "call:")
	if idx < 0 {
		return ai.ToolCall{}, false, nil
	}
	callText := strings.TrimSpace(trimmed[idx:])
	braceIdx := strings.Index(callText, "{")
	if braceIdx < 0 {
		return ai.ToolCall{}, true, fmt.Errorf("printed native tool call is missing JSON args object")
	}
	toolSpec := strings.TrimSpace(callText[len("call:"):braceIdx])
	if toolSpec == "" {
		return ai.ToolCall{}, true, fmt.Errorf("printed native tool call is missing tool name")
	}
	segments := strings.Split(toolSpec, ":")
	toolName := strings.TrimSpace(segments[len(segments)-1])
	if toolName == "" {
		return ai.ToolCall{}, true, fmt.Errorf("printed native tool call is missing tool name")
	}
	jsonPayload, err := extractBalancedJSONObject(callText[braceIdx:])
	if err != nil {
		return ai.ToolCall{}, true, err
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(jsonPayload), &args); err != nil {
		return ai.ToolCall{}, true, fmt.Errorf("printed native tool call has invalid JSON args: %w", err)
	}
	return ai.ToolCall{Name: toolName, Args: args}, true, nil
}

func extractBalancedJSONObject(text string) (string, error) {
	depth := 0
	inString := false
	escaped := false
	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if inString {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[:i+1], nil
			}
		}
	}
	return "", fmt.Errorf("printed native tool call contains truncated JSON args")
}

func formatPendingRepairReminder(repair pendingToolRepair, attemptedToolName string) string {
	if repair.Strategy == nativeRepairStrategyResearchFirst {
		allowedTool := repair.ResearchTool
		if allowedTool == "" {
			allowedTool = "list_stores"
		}
		return fmt.Sprintf(
			"Repair required before continuing.\nTool: %s\nRepair note: The model attempted %s instead.\nRetry instruction: Call %s next, preferably with scoped stores:[...] for the likely targets, use its schema/relations output as source of truth, then return to %s. Do not switch to unrelated tools or provide a final answer until the research attempt is made.",
			repair.ToolName,
			attemptedToolName,
			allowedTool,
			repair.ToolName,
		)
	}
	return fmt.Sprintf(
		"Repair required before continuing.\nTool: %s\nRepair note: The model attempted %s instead.\nRetry instruction: Call %s next with corrected arguments. Do not switch tools or provide a final answer until the repair attempt is made.",
		repair.ToolName,
		attemptedToolName,
		repair.ToolName,
	)
}

func shouldEscalateRepairToClarification(repairAttempts int) bool {
	return repairAttempts >= 1
}

func isRoutedAskContext(ctx context.Context) bool {
	p := ai.GetSessionPayload(ctx)
	if p == nil || p.Variables == nil {
		return false
	}
	routingState, ok := p.Variables["RoutingState"].(*TaskContextClassification)
	if !ok || routingState == nil {
		return false
	}
	return strings.TrimSpace(routingState.RoutingGate) != ""
}

func shouldResetRepairAttempts(priorRepair *pendingToolRepair, toolName string) bool {
	if priorRepair == nil {
		return true
	}
	if priorRepair.Strategy == nativeRepairStrategyResearchFirst {
		researchTool := priorRepair.ResearchTool
		if researchTool == "" {
			researchTool = "list_stores"
		}
		if toolName == researchTool {
			return false
		}
	}
	return true
}

func appendOrReplaceRetriedToolResult(results []nativeToolResult, next nativeToolResult, priorRepair *pendingToolRepair, toolName string) []nativeToolResult {
	if priorRepair == nil || strings.TrimSpace(priorRepair.ToolName) != strings.TrimSpace(toolName) {
		return append(results, next)
	}
	if strings.EqualFold(strings.TrimSpace(toolName), "execute_script") {
		collapsed := make([]nativeToolResult, 0, len(results))
		for _, result := range results {
			if strings.EqualFold(strings.TrimSpace(result.Name), "execute_script") {
				continue
			}
			collapsed = append(collapsed, result)
		}
		return append(collapsed, next)
	}
	for index := len(results) - 1; index >= 0; index-- {
		if strings.TrimSpace(results[index].Name) != strings.TrimSpace(toolName) {
			continue
		}
		replaced := append([]nativeToolResult(nil), results...)
		replaced[index] = next
		return replaced
	}
	return append(results, next)
}

func (r pendingToolRepair) allowsTool(toolName string) bool {
	if r.Strategy == nativeRepairStrategyResearchFirst {
		researchTool := r.ResearchTool
		if researchTool == "" {
			researchTool = "list_stores"
		}
		return toolName == researchTool
	}
	return toolName == r.ToolName
}

func classifyRecoverableToolRepair(toolName string, err error) pendingToolRepair {
	repair := pendingToolRepair{ToolName: toolName, Strategy: nativeRepairStrategySameTool}
	if toolName != "execute_script" || err == nil {
		return repair
	}

	for _, validationErr := range collectExecuteScriptValidationIssues(err) {
		switch validationErr.Category {
		case "invalid_join_on_placeholder", "invalid_join_on_field_placeholder", "invalid_filter_field_placeholder":
			repair.Strategy = nativeRepairStrategyResearchFirst
			repair.ResearchTool = "list_stores"
			repair.ResearchReason = "The failure indicates missing grounded schema or relation mapping facts."
			return repair
		case "invalid_filter_input_shape", "invalid_filter_query_mismatch", "invalid_filter_placeholder", "invalid_filter_operator_placeholder":
			repair.Strategy = nativeRepairStrategySameTool
		}
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "relation") || strings.Contains(message, "schema") || strings.Contains(message, "join mapping") {
		repair.Strategy = nativeRepairStrategyResearchFirst
		repair.ResearchTool = "list_stores"
		repair.ResearchReason = "The failure text points to unresolved schema or relation ambiguity."
	}
	return repair
}

func collectExecuteScriptValidationIssues(err error) []*executeScriptValidationError {
	if err == nil {
		return nil
	}
	if aggregate := new(executeScriptValidationErrors); errors.As(err, &aggregate) && aggregate != nil {
		issues := make([]*executeScriptValidationError, 0, len(aggregate.Errors))
		for _, item := range aggregate.Errors {
			if item != nil {
				issues = append(issues, item)
			}
		}
		if len(issues) > 0 {
			return issues
		}
	}
	if single := new(executeScriptValidationError); errors.As(err, &single) && single != nil {
		return []*executeScriptValidationError{single}
	}
	return nil
}

func sanitizeToolCallArgs(args map[string]any, enableObfuscation bool) {
	cleanToken := func(s string) string {
		s = strings.Trim(s, "*_`")
		s = strings.ReplaceAll(s, "\u00a0", " ")
		s = strings.TrimSpace(s)
		s = strings.Trim(s, "\"'")
		return strings.TrimSpace(s)
	}

	var sanitize func(any) any
	sanitize = func(v any) any {
		switch val := v.(type) {
		case string:
			val = cleanToken(val)
			if enableObfuscation {
				val = obfuscation.GlobalObfuscator.DeobfuscateText(val)
			}
			return val
		case []any:
			for i, item := range val {
				val[i] = sanitize(item)
			}
			return val
		case map[string]any:
			cleaned := make(map[string]any, len(val))
			for k, item := range val {
				cleaned[cleanToken(k)] = sanitize(item)
			}
			return cleaned
		default:
			return val
		}
	}

	for k, v := range args {
		args[k] = sanitize(v)
	}
}

func summarizeToolDefinitions(tools []ai.ToolDefinition) string {
	if len(tools) == 0 {
		return ""
	}
	names := make([]string, 0, len(tools))
	for i, tool := range tools {
		if i >= 12 {
			names = append(names, "...")
			break
		}
		names = append(names, tool.Name)
	}
	return strings.Join(names, ",")
}

func summarizeToolArgKeys(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	return strings.Join(keys, ",")
}

func getRequestedOutputFormat(ctx context.Context) string {
	format, _ := ctx.Value(ai.CtxKeyDefaultFormat).(string)
	return strings.ToLower(strings.TrimSpace(format))
}

func shouldPreserveStructuredToolResult(ctx context.Context, toolResults []nativeToolResult) bool {
	if len(toolResults) == 0 {
		return false
	}
	lastTool := toolResults[len(toolResults)-1]
	if !shouldPreserveStructuredToolName(lastTool.Name) {
		return false
	}
	switch getRequestedOutputFormat(ctx) {
	case "csv", "json", "ndjson", "tsv":
		return true
	default:
		return false
	}
}

func shouldPreserveStructuredToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "execute_script":
		return true
	default:
		return false
	}
}

func preserveStructuredToolResult(result string, enableObfuscation bool) string {
	if enableObfuscation {
		return obfuscation.GlobalObfuscator.DeobfuscateText(result)
	}
	return result
}
