package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/generator"
	"github.com/sharedcode/joltrin/v5"
)

type prewarmCountingGenerator struct {
	prewarmCalls int
}

func (g *prewarmCountingGenerator) Name() string { return "prewarm-test" }

func (g *prewarmCountingGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	return ai.GenOutput{}, nil
}

func (g *prewarmCountingGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	g.prewarmCalls++
	return nil
}

func (g *prewarmCountingGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0
}

func TestNewFromConfig_PrewarmCacheCalled(t *testing.T) {
	gen := &prewarmCountingGenerator{}
	generator.Register("prewarm-test", func(cfg map[string]any) (ai.Generator, error) {
		return gen, nil
	})

	cfg := Config{
		ID:           "prewarm-agent",
		Name:         "Prewarm Agent",
		SystemPrompt: "You are a test agent.",
		Generator: GeneratorConfig{
			Type: "prewarm-test",
		},
		StoragePath: t.TempDir(),
	}

	_, err := NewFromConfig(context.Background(), cfg, Dependencies{
		AgentRegistry: map[string]ai.Agent[map[string]any]{},
		SystemDB:      database.NewDatabase(sop.DatabaseOptions{StoresFolders: []string{t.TempDir()}, CacheType: sop.InMemory}),
		Databases:     map[string]sop.DatabaseOptions{},
	})
	if err != nil {
		t.Fatalf("NewFromConfig returned error: %v", err)
	}

	if gen.prewarmCalls != 1 {
		t.Fatalf("expected PrewarmCache to be called once, got %d", gen.prewarmCalls)
	}
}
