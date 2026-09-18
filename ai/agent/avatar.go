package agent

import (
	"context"
	"fmt"
	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

func (a *CopilotAgent) executeAvatarSubAgent(ctx context.Context, avatarName, taskContext string) (string, error) {
	var customPersona string
	var allowedTools []string

	if a.systemDB != nil {
		if tx, err := a.systemDB.BeginTransaction(ctx, sop.ForReading); err == nil {
			if kb, err := a.systemDB.OpenKnowledgeBase(ctx, avatarName, tx, nil, nil, false); err == nil {
				if cfg, err := kb.GetConfig(ctx); err == nil && cfg != nil {
					if cfg.SystemPrompt != "" {
						customPersona = cfg.SystemPrompt
					}
					allowedTools = cfg.AllowedTools
				}
			}
			tx.Rollback(ctx)
		}
	}

	avatarPrompt := a.buildAvatarPrompt(ctx, avatarName, taskContext, customPersona, allowedTools)

	engine := &NativeReActEngine{
		EnableObfuscation: false, // Inherit or check ctx if needed
	}

	// Wrapper to restrict tools based on the KB config.
	var executor ai.ToolExecutor = a
	if allowedTools != nil {
		executor = &restrictedExecutor{
			base:         a,
			allowedTools: allowedTools,
		}
	}

	req := ai.ReasoningRequest{
		SystemPrompt: avatarPrompt,
		UserQuery:    "Execute the task outlined in your context.",
		Executor:     executor,
		Generator:    a.resolveGenerator(ctx),
	}

	resp, err := engine.Run(ctx, req)
	if err != nil {
		return "", fmt.Errorf("avatar execution failed: %w", err)
	}

	return fmt.Sprintf("Avatar %s completed task: \n%s", avatarName, resp.FinalText), nil
}

func (a *CopilotAgent) buildAvatarPrompt(ctx context.Context, avatarName, taskContext, customPersona string, allowedTools []string) string {
	persona := customPersona
	if persona == "" {
		persona = fmt.Sprintf("You are the %s Avatar. Your task is strictly limited to your domain.", avatarName)
	}

	persona += fmt.Sprintf("\n\nTask Context from Omni Supervisor:\n%s\n\n", taskContext)

	// Instruct the Avatar on its specific allowed tools to restore Omni-like fluency.
	if len(allowedTools) > 0 {
		persona += "You have access to the following tools via the native tools platform to help answer the user's question. ONLY use these allowed tools:\n"
		allTools := a.registry.List()
		for _, t := range allTools {
			for _, allowed := range allowedTools {
				if t.Name == allowed && !t.Hidden {
					schemaStr := t.ArgsSchema
					if len(schemaStr) > 0 && schemaStr[0] == '{' {
						schemaStr = "(args: " + schemaStr + ")"
					}
					persona += fmt.Sprintf("- %s%s: %s\n", t.Name, schemaStr, t.Description)
					break
				}
			}
		}
		persona += "\n"
	}

	// Inject LTM & Semantic working Memory exactly like Omni
	persona += a.getLTMSemanticContext(ctx, taskContext)

	// Always inject System Tools loaded into LTM
	persona += a.getSystemToolsContext(ctx)

	// Inject Domain-specific playbooks exactly like Omni but target its own KB only
	persona += a.getPlaybooksContext(ctx, taskContext, []string{avatarName})

	// Inject the active session conversation text
	convHistory := a.getSessionMemoryContext()

	fullPrompt := persona + "\n" + convHistory + "\nUser: " + taskContext
	log.Info("LLM Context (AVATAR)", "AvatarName", logsafe.V(avatarName), "SystemPrompt", logsafe.V(fullPrompt))

	return fullPrompt
}

type restrictedExecutor struct {
	base         ai.ToolExecutor
	allowedTools []string
}

func (re *restrictedExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	allowed := false
	for _, t := range re.allowedTools {
		if t == toolName {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("access denied: tool '%s' is not in the allowed list for this Avatar", toolName)
	}
	return re.base.Execute(ctx, toolName, args)
}

func (re *restrictedExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	all, err := re.base.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	var filtered []ai.ToolDefinition
	for _, def := range all {
		allowed := false
		for _, t := range re.allowedTools {
			if t == def.Name {
				allowed = true
				break
			}
		}
		if allowed {
			filtered = append(filtered, def)
		}
	}
	return filtered, nil
}
