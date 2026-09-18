package main

import (
	"fmt"
	log "log/slog"
	"net/http"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/internal/logsafe"
)

type embedderSettings struct {
	Provider string
	APIKey   string
	URL      string
	Model    string
}

func resolveEmbedderSettings(r *http.Request) embedderSettings {
	settings := embedderSettings{
		Provider: "local",
		Model:    "kelindar",
	}

	if r != nil {
		if h := r.Header.Get("X-Embedder-Provider"); h != "" {
			settings.Provider = h
		}
		if h := r.Header.Get("X-Embedder-API-Key"); h != "" {
			settings.APIKey = h
		}
		if h := r.Header.Get("X-Embedder-URL"); h != "" {
			settings.URL = h
		}
		if h := r.Header.Get("X-Embedder-Model"); h != "" {
			settings.Model = h
		}
	}

	settings.Provider, settings.Model = normalizeProviderAndModel(settings.Provider, settings.Model)

	return settings
}

func isInternalKnowledgeBase(spaceName, preloadPath string) bool {
	normalized := strings.ToLower(strings.TrimSpace(spaceName))
	normalized = strings.ReplaceAll(normalized, "_", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.Join(strings.Fields(normalized), " ")

	lowerPath := strings.ToLower(strings.TrimSpace(preloadPath))

	switch normalized {
	case "sop", "sop kb", "sop knowledge base":
		return strings.TrimSpace(preloadPath) == "" || strings.Contains(lowerPath, "sop_base_knowledge.json")
	case "medical", "medical kb", "medical knowledge base":
		return strings.TrimSpace(preloadPath) == "" || strings.Contains(lowerPath, "medical.json")
	default:
		return false
	}
}

func shouldForceLocalBuiltinEmbedder(spaceName, preloadPath string) bool {
	return isInternalKnowledgeBase(spaceName, preloadPath)
}

func pinnedInternalKBSettings(spaceName string) (string, bool) {
	if !isInternalKnowledgeBase(spaceName, "") {
		return "", false
	}
	return "kelindar:nomic-embed-text-v1.5-q8_0", true
}

func newConfiguredEmbedder(settings embedderSettings) (ai.Embeddings, error) {
	switch settings.Provider {
	case "gemini":
		apiKey := settings.APIKey
		model := settings.Model
		if model == "" {
			model = "gemini-embedding-2"
		}
		return embed.NewGemini(apiKey, model), nil
	case "openai":
		apiKey := settings.APIKey
		model := settings.Model
		if model == "" {
			model = "text-embedding-3-small"
		}
		return embed.NewOpenAI(apiKey, model, settings.URL), nil
	case "ollama":
		url := settings.URL
		model := settings.Model
		if model == "" {
			model = "nomic-embed-text"
		}
		return embed.NewOllama(url, model), nil
	case "local":
		modelPath := strings.TrimSpace(settings.URL)
		if modelPath == "" {
			modelPath = strings.TrimSpace(settings.Model)
		}
		if modelPath == "" {
			modelPath = "kelindar"
		}
		return embed.NewLocalWithProvider("kelindar", modelPath, 0)
	case "kelindar":
		modelPath := strings.TrimSpace(settings.Model)
		if modelPath == "" {
			modelPath = strings.TrimSpace(settings.URL)
		}
		if modelPath == "" {
			modelPath = "kelindar"
		}
		return embed.NewLocalWithProvider("kelindar", modelPath, 0)
	case "simple":
		return embed.NewSimple("mock_embedder", 384, nil), nil
	default:
		return nil, fmt.Errorf("unsupported embedder provider %q", settings.Provider)
	}
}

// isInternalKnowledgeBase reports whether a space is one of the built-in
// internal KBs that should always use the local Kelindar/Nomic embedder.

// warmupBuiltinLocalEmbedder preloads the local Kelindar model for internal KBs.
func warmupBuiltinLocalEmbedder(spaceName, preloadPath string) {
	if !isInternalKnowledgeBase(spaceName, preloadPath) {
		return
	}

	localEmbedder, err := embed.NewLocalWithProvider("kelindar", "kelindar", 0)
	if err != nil {
		return
	}
	_ = localEmbedder.Close()
}

func GetConfiguredEmbedderForSpace(r *http.Request, spaceName, preloadPath string) ai.Embeddings {
	if isInternalKnowledgeBase(spaceName, preloadPath) {
		warmupBuiltinLocalEmbedder(spaceName, preloadPath)
		embedder, err := newConfiguredEmbedder(embedderSettings{Provider: "local", Model: "kelindar", URL: "kelindar"})
		if err != nil {
			log.Error("pinned internal kelindar embedder initialization failed", "space", logsafe.V(spaceName), "err", err)
			return nil
		}
		log.Debug("embedder selection using pinned internal local kelindar", "space", logsafe.V(spaceName))
		return embedder
	}

	log.Debug("embedder selection", "space", logsafe.V(spaceName), "preload_path", logsafe.V(preloadPath), "forced_local", false, "production_mode", config.ProductionMode)
	return GetConfiguredEmbedder(r)
}

// GetConfiguredEmbedder returns an AI embedder based on the server configuration.
func GetConfiguredEmbedder(r *http.Request) ai.Embeddings {
	settings := resolveEmbedderSettings(r)
	log.Debug("embedder selection details", "production_mode", config.ProductionMode, "provider", logsafe.V(settings.Provider), "model", logsafe.V(settings.Model), "url", logsafe.V(settings.URL))

	if !config.ProductionMode {
		log.Warn("embedder selection using mock embedder", "reason", "production_mode=false", "provider", logsafe.V(settings.Provider), "model", logsafe.V(settings.Model))
		return embed.NewSimple("mock_embedder", 384, nil)
	}

	embedder, err := newConfiguredEmbedder(settings)
	if err != nil {
		log.Error("embedder selection failed, using mock embedder", "err", logsafe.V(err), "provider", logsafe.V(settings.Provider), "model", logsafe.V(settings.Model))
		return embed.NewSimple("mock_embedder", 384, nil)
	}
	log.Debug("embedder selection using configured real embedder", "provider", logsafe.V(settings.Provider), "model", logsafe.V(settings.Model))
	return embedder
}
