package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ModelCatalog struct {
	LLM      []ModelCatalogGroup `json:"llm,omitempty"`
	Embedder []ModelCatalogGroup `json:"embedder,omitempty"`
}

type ModelCatalogGroup struct {
	Label   string               `json:"label"`
	Options []ModelCatalogOption `json:"options,omitempty"`
}

type ModelCatalogOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func defaultLocalEmbedderOptions() []ModelCatalogOption {
	candidates := []string{
		filepath.Join("ai", "models"),
		filepath.Join("..", "ai", "models"),
		filepath.Join("..", "..", "ai", "models"),
	}

	seen := map[string]struct{}{}
	var options []ModelCatalogOption

	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				continue
			}
			for modelKey, rawEntry := range raw {
				if _, ok := seen[modelKey]; ok {
					continue
				}
				seen[modelKey] = struct{}{}

				var entryInfo struct {
					DisplayName string `json:"display_name"`
				}
				if err := json.Unmarshal(rawEntry, &entryInfo); err != nil {
					continue
				}
				value := "kelindar:" + modelKey
				label := "Kelindar / Gemini embedder"
				if strings.Contains(modelKey, "gemini") {
					value = "gemini:" + modelKey
					label = "Google embedding model"
				} else {
					label = "Kelindar embedded library"
				}
				if entryInfo.DisplayName != "" {
					label += " (" + entryInfo.DisplayName + ")"
				} else {
					label += " (" + modelKey + ")"
				}

				options = append(options, ModelCatalogOption{
					Value: value,
					Label: label,
				})
			}
		}
	}

	sort.Slice(options, func(i, j int) bool {
		return options[i].Label < options[j].Label
	})
	return options
}

func defaultModelCatalog() ModelCatalog {
	localOptions := defaultLocalEmbedderOptions()
	return ModelCatalog{
		LLM: []ModelCatalogGroup{
			{
				Label: "Google",
				Options: []ModelCatalogOption{
					{Value: "gemini:gemini-3.1-pro-preview", Label: "Gemini 3.1 Pro Preview"},
				},
			},
			{
				Label: "Anthropic",
				Options: []ModelCatalogOption{
					{Value: "anthropic:claude-4.6-sonnet", Label: "Claude Sonnet 4.6"},
				},
			},
			{
				Label: "OpenAI",
				Options: []ModelCatalogOption{
					{Value: "openai:gpt-5.4", Label: "GPT-5.4"},
				},
			},
			{
				Label: "Local",
				Options: []ModelCatalogOption{
					{Value: "ollama", Label: "Ollama (Local Default)"},
					{Value: "ollama:gemma-4", Label: "Ollama (Gemma 4)"},
				},
			},
		},
		Embedder: []ModelCatalogGroup{
			{
				Label:   "Local",
				Options: localOptions,
			},
		},
	}
}

const modelCatalogFilename = "model_catalog.json"

func ensureModelCatalogDefaults(catalog *ModelCatalog) bool {
	defaults := defaultModelCatalog()
	changed := false

	if len(catalog.LLM) == 0 {
		catalog.LLM = defaults.LLM
		changed = true
	}
	if len(catalog.Embedder) == 0 {
		catalog.Embedder = defaults.Embedder
		changed = true
	}

	return changed
}

func modelCatalogCandidatePaths(configPath string) []string {
	seen := map[string]struct{}{}
	var paths []string

	add := func(path string) {
		if path == "" {
			return
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}

	if configPath != "" {
		add(filepath.Join(filepath.Dir(configPath), modelCatalogFilename))
		if exePath, err := os.Executable(); err == nil {
			add(filepath.Join(filepath.Dir(exePath), modelCatalogFilename))
		}
		return paths
	}

	if cwd, err := os.Getwd(); err == nil {
		add(filepath.Join(cwd, modelCatalogFilename))
		add(filepath.Join(filepath.Dir(cwd), modelCatalogFilename))
	}

	add(modelCatalogFilename)

	return paths
}

func loadModelCatalog(configPath string) (bool, error) {
	modelCatalog = defaultModelCatalog()

	for _, path := range modelCatalogCandidatePaths(configPath) {
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}

		var loaded ModelCatalog
		decodeErr := json.NewDecoder(f).Decode(&loaded)
		f.Close()
		if decodeErr != nil {
			return false, decodeErr
		}

		seededDefaults := ensureModelCatalogDefaults(&loaded)
		modelCatalog = loaded
		return seededDefaults, nil
	}

	return false, nil
}
