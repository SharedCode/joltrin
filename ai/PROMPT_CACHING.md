# Prompt Caching

## Overview

**Prompt Caching is now enabled** for both **Anthropic Claude** and **OpenAI GPT** generators, providing **50-90% cost savings** on repetitive context like system prompts, tools, and long context.

| Provider | Status | Savings | Method |
|----------|--------|---------|--------|
| **Anthropic Claude** | ✅ Enabled | 90% | Explicit `cache_control` markers |
| **OpenAI GPT** | ✅ Enabled | 50% | Automatic detection |
| **Google Gemini** | ⏳ Future | 75% | Explicit cache API |

---

## Anthropic Claude Prompt Caching

### How It Works

Anthropic's Prompt Caching allows you to mark specific content blocks with `cache_control` breakpoints. Cached content is:
- Written to cache on first request (25% premium: $3.75/1M tokens vs $3.00/1M)
- Read from cache on subsequent requests (90% discount: $0.30/1M tokens vs $3.00/1M)
- Valid for 5 minutes of inactivity

**What's Cached Automatically:**
1. ✅ **System prompts** - Your instructions, persona, guidelines (typically 1000s of tokens)
2. ✅ **Tool definitions** - Function signatures and schemas reused across requests
3. ⏳ **Long conversation context** - Coming in future updates

## Cost Model

### Example: 10K Token System Prompt + Tools, 100 Requests

**Without Caching:**
- 10,000 tokens × 100 requests × $0.000003 = **$3.00**

**With Caching:**
- Cache write (1st request): 10,000 × $0.0000037 = **$0.037**
- Cache reads (99 requests): 10,000 × 99 × $0.0000003 = **$0.297**
- **Total: $0.33 (89% savings!)**

### Real-World Scenarios

| Scenario | Cached Content | Requests | Without Caching | With Caching | Savings |
|----------|----------------|----------|-----------------|--------------|---------|
| SQL Agent | 5K tokens (system + 50 tools) | 1,000 | $15.00 | $1.52 | 90% |
| Code Copilot | 8K tokens (guidelines + context) | 500 | $12.00 | $1.23 | 90% |
| Document Q&A | 15K tokens (document context) | 200 | $9.00 | $0.96 | 89% |

## Implementation Details

### What Changed

**1. Type Definitions**
```go
// anthropicCacheControl marks content for caching
type anthropicCacheControl struct {
    Type string `json:"type"` // "ephemeral"
}

// anthropicContentBlock now supports cache_control
type anthropicContentBlock struct {
    Type         string                `json:"type"`
    Text         string                `json:"text,omitempty"`
    CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
    // ... other fields
}
```

**2. Request Structure**
```go
type anthropicRequest struct {
    Model       string                  `json:"model"`
    Messages    []anthropicMessage      `json:"messages"`
    MaxTokens   int                     `json:"max_tokens"`
    Temperature float32                 `json:"temperature,omitempty"`
    System      []anthropicContentBlock `json:"system,omitempty"` // Now array with cache_control
    Tools       []anthropicTool         `json:"tools,omitempty"`
}
```

**3. Response Usage Tracking**
```go
Usage struct {
    InputTokens             int `json:"input_tokens"`
    OutputTokens            int `json:"output_tokens"`
    CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"` // Tokens written
    CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`     // Tokens read (90% savings)
}
```

**4. Headers**
```go
req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31") // Enable caching
req.Header.Set("anthropic-version", "2023-06-01")
req.Header.Set("x-api-key", g.apiKey)
```

### Caching Strategy

**System Prompt Caching:**
```go
if opts.SystemPrompt != "" {
    reqBody.System = []anthropicContentBlock{
        {
            Type: "text",
            Text: opts.SystemPrompt,
            CacheControl: &anthropicCacheControl{Type: "ephemeral"}, // Cache!
        },
    }
}
```

**Tool Caching (Last Tool Marked):**
```go
// Anthropic caches everything up to and including the marked block
func (g *anthropic) convertToolsWithCaching(tools []ai.ToolDefinition) []anthropicTool {
    anthropicTools := g.convertTools(tools)
    
    // Mark LAST tool with cache_control to cache all tools
    if len(anthropicTools) > 0 {
        anthropicTools[len(anthropicTools)-1].CacheControl = &anthropicCacheControl{
            Type: "ephemeral",
        }
    }
    
    return anthropicTools
}
```

## Requirements

**Minimum Cached Content Size:**
- At least **1,024 tokens** required for caching to activate
- System prompts + tools typically exceed this threshold easily
- Shorter content won't be cached but won't error

**Cache Lifetime:**
- **5 minutes** of inactivity
- Each cache hit extends the lifetime by 5 minutes
- Active workloads stay cached indefinitely

## Usage

**No code changes required!** Prompt caching is automatically enabled for all Anthropic/Claude requests.

### Monitor Cache Performance

Check the response usage metrics:

```go
output, err := generator.Generate(ctx, prompt, opts)
if err != nil {
    return err
}

// Access raw response
if resp, ok := output.Raw.(anthropicResponse); ok {
    fmt.Printf("Input tokens: %d\n", resp.Usage.InputTokens)
    fmt.Printf("Cache write tokens: %d\n", resp.Usage.CacheCreationInputTokens)
    fmt.Printf("Cache read tokens: %d (90%% savings!)\n", resp.Usage.CacheReadInputTokens)
    fmt.Printf("Output tokens: %d\n", resp.Usage.OutputTokens)
}
```

### Example Output
```
First Request:
  Input tokens: 0
  Cache write tokens: 12,450 (system + tools written to cache)
  Cache read tokens: 0
  Output tokens: 523

Subsequent Requests (within 5 min):
  Input tokens: 0
  Cache write tokens: 0
  Cache read tokens: 12,450 (90% savings!)
  Output tokens: 487
```

## Best Practices

### ✅ DO

1. **Keep system prompts consistent** across requests to maximize cache hits
2. **Reuse tool definitions** - don't modify tool schemas unnecessarily
3. **Monitor cache metrics** in production to verify savings
4. **Use for high-frequency agents** - SQL copilot, code assistants, chatbots
5. **Batch similar requests** within 5-minute windows

### ❌ DON'T

1. **Don't cache dynamic content** - user messages, query results, timestamps
2. **Don't modify system prompts** frequently - breaks cache
3. **Don't worry about small prompts** - <1024 tokens won't cache but won't error
4. **Don't cache for one-off requests** - caching overhead not worth it
---

## OpenAI GPT Prompt Caching

### How It Works

OpenAI's Prompt Caching is **fully automatic** - no special markers needed. The API detects repeated message prefixes and caches them automatically.

**Caching behavior:**
- Automatically detects repeated content (system prompts, tool definitions)
- Minimum **1,024 tokens** required for caching
- **50% discount** on cached tokens ($1.25/1M vs $2.50/1M for GPT-4o)
- **5 minute TTL** (same as Anthropic)
- Content must match **exactly** for cache hits

### Cost Model

**Example: 5K Token System Prompt + Tools, 100 Requests**

**Without Caching:**
- 5,000 tokens × 100 requests × $0.0000025 = **$1.25**

**With Caching:**
- First request: 5,000 × $0.0000025 = **$0.0125**
- Remaining 99: 5,000 × 99 × $0.00000125 = **$0.619**
- **Total: $0.631 (49.5% savings!)**

### Real-World Scenarios

| Scenario | Cached Content | Requests | Without Caching | With Caching | Savings |
|----------|----------------|----------|-----------------|--------------|---------|
| SQL Agent | 5K tokens | 1,000 | $12.50 | $6.31 | 49.5% |
| Code Copilot | 8K tokens | 500 | $10.00 | $5.06 | 49.4% |
| Document Q&A | 12K tokens | 200 | $6.00 | $3.04 | 49.3% |

### Implementation

**Type Definitions:**
```go
type openAIResponse struct {
    Usage struct {
        PromptTokens     int `json:"prompt_tokens"`
        CompletionTokens int `json:"completion_tokens"`
        TotalTokens      int `json:"total_tokens"`
        PromptTokensDetails *struct {
            CachedTokens int `json:"cached_tokens,omitempty"` // Tokens from cache
        } `json:"prompt_tokens_details,omitempty"`
    } `json:"usage"`
}
```

**Response tracking:**
```go
output, err := generator.Generate(ctx, prompt, opts)
if err != nil {
    return err
}

// Access cache metrics from raw response
if resp, ok := output.Raw.(openAIResponse); ok {
    if details := resp.Usage.PromptTokensDetails; details != nil {
        cachedTokens := details.CachedTokens
        totalPrompt := resp.Usage.PromptTokens
        cacheHitRate := float64(cachedTokens) / float64(totalPrompt) * 100
        fmt.Printf("Cache hit: %d/%d tokens (%.1f%%)\n", 
            cachedTokens, totalPrompt, cacheHitRate)
    }
}
```

### Best Practices

✅ **DO:**
1. Keep system prompts consistent across requests
2. Use stable tool definitions
3. Monitor `cached_tokens` in production
4. Batch similar queries within 5-minute windows

❌ **DON'T:**
1. Modify system prompts unnecessarily
2. Change tool schemas between requests
3. Expect caching for <1024 token prompts

### Testing

```bash
cd ai
go test ./generator -v -run "TestChatGPTPromptCaching"
```

Tests verify:
- ✅ Chat Completions API tracks cached tokens
- ✅ Responses API tracks cached tokens
- ✅ Cost calculations with cache hits
- ✅ Behavior without cache hits

---
## Testing

Run the prompt caching test suite:

```bash
cd ai
go test ./generator -v -run "TestAnthropicPromptCaching"
```

Tests verify:
- ✅ System prompt marked with `cache_control`
- ✅ Last tool marked with `cache_control` (caches all tools)
- ✅ Beta header set correctly
- ✅ JSON marshaling includes cache_control fields

## References

**Anthropic Claude:**
- [Anthropic Prompt Caching Docs](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- Implementation: [ai/generator/anthropic.go](generator/anthropic.go)
- Tests: [ai/generator/anthropic_caching_test.go](generator/anthropic_caching_test.go)

**OpenAI GPT:**
- [OpenAI Prompt Caching Docs](https://platform.openai.com/docs/guides/prompt-caching)
- Implementation: [ai/generator/chatgpt.go](generator/chatgpt.go) + [chatgpt_types.go](generator/chatgpt_types.go)
- Tests: [ai/generator/chatgpt_caching_test.go](generator/chatgpt_caching_test.go)

## Version History

- **v1.0** (2026-06-03): 
  - ✅ Anthropic Claude: Explicit cache_control markers (90% savings)
  - ✅ OpenAI GPT: Automatic cache detection (50% savings)
- **Future (v1.1)**: 
  - ⏳ Gemini Context Caching API (75% savings, explicit cache management)
  - ⏳ Message-level caching for long conversation context
