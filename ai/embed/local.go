package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
	"io"
	log "log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type localVectorizer interface {
	EmbedText(text string) ([]float32, error)
	Close() error
}

type Local struct {
	modelPath        string
	gpuLayers        int
	name             string
	dim              int
	profile          EmbeddingProfile
	model            localVectorizer
	newModel         func(modelPath string, gpuLayers int) (localVectorizer, error)
	gate             chan struct{}
	concurrencyLimit int
}

type LocalEmbedderFactory func(modelPath string, gpuLayers int) (localVectorizer, error)

const defaultLocalModelDownloadURL = "https://huggingface.co/magicunicorn/nomic-embed-text-v1.5-Q8_0-GGUF/resolve/main/nomic-embed-text-v1.5-q8_0.gguf"
const defaultLocalModelFileName = "nomic-embed-text-v1.5-q8_0.gguf"

func defaultEmbeddingProfile() EmbeddingProfile {
	return EmbeddingProfile{
		RoutingDim:     256,
		DocumentDim:    768,
		RoutingPrefix:  "classification: ",
		DocStorePrefix: "search_document: ",
		DocQueryPrefix: "search_query: ",
	}
}

func modelRegistryKey(modelPath string) string {
	base := filepath.Base(strings.TrimSpace(modelPath))
	if dot := strings.LastIndex(base, "."); dot > 0 {
		ext := strings.ToLower(base[dot+1:])
		if ext == "gguf" || ext == "bin" || ext == "onnx" || ext == "json" {
			base = base[:dot]
		}
	}
	return base
}

func loadEmbeddingProfile(modelPath string) EmbeddingProfile {
	profile := defaultEmbeddingProfile()

	requestedModel := strings.TrimSpace(modelPath)
	if parts := strings.SplitN(requestedModel, ":", 2); len(parts) == 2 && strings.EqualFold(parts[0], "kelindar") {
		requestedModel = parts[1]
	}

	manifestCandidates := []string{"embedder_profiles.json"}
	for _, dir := range []string{
		".",
		"models",
		filepath.Join("ai", "models"),
		"..",
		filepath.Join("..", "..", "models"),
		filepath.Join("..", "..", "ai", "models"),
		filepath.Dir(os.Args[0]),
		filepath.Join(filepath.Dir(os.Args[0]), "models"),
		filepath.Join(filepath.Dir(os.Args[0]), "ai", "models"),
	} {
		for _, candidate := range manifestCandidates {
			candidatePath := filepath.Join(dir, candidate)
			data, err := os.ReadFile(candidatePath)
			if err != nil {
				continue
			}
			if parsed, ok := loadProfileFromJSON(data, modelPath); ok {
				return parsed
			}
		}
	}

	candidateNames := []string{modelRegistryKey(requestedModel) + ".json"}
	candidateNames = append(candidateNames, modelRegistryKey(defaultLocalModelFileName)+".json")
	for _, candidate := range candidateNames {
		for _, dir := range []string{
			".",
			"models",
			filepath.Join("ai", "models"),
			"..",
			filepath.Join("..", "..", "models"),
			filepath.Join("..", "..", "ai", "models"),
			filepath.Dir(os.Args[0]),
			filepath.Join(filepath.Dir(os.Args[0]), "models"),
			filepath.Join(filepath.Dir(os.Args[0]), "ai", "models"),
		} {
			candidatePath := filepath.Join(dir, candidate)
			data, err := os.ReadFile(candidatePath)
			if err != nil {
				continue
			}
			if parsed, ok := loadProfileFromJSON(data, modelPath); ok {
				return parsed
			}
		}
	}

	return profile
}

func loadProfileFromJSON(data []byte, modelPath string) (EmbeddingProfile, bool) {
	profile := defaultEmbeddingProfile()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil && len(raw) > 0 {
		if raw["model_name"] == nil && raw["dimensions"] == nil && raw["prefixes"] == nil {
			modelKey := modelRegistryKey(modelPath)
			if entry, ok := raw[modelKey]; ok {
				var profileEntry struct {
					ModelName          string `json:"model_name"`
					ModelType          string `json:"model_type"`
					DisplayName        string `json:"display_name"`
					MaxContextTokens   int    `json:"max_context_tokens"`
					SupportsMatryoshka bool   `json:"supports_matryoshka"`
					Dimensions         struct {
						Routing  int `json:"routing"`
						Document int `json:"document"`
					} `json:"dimensions"`
					Prefixes struct {
						RoutingSearch string `json:"routing_search"`
						DocStorage    string `json:"doc_storage"`
						DocSearch     string `json:"doc_search"`
					} `json:"prefixes"`
				}
				if err := json.Unmarshal(entry, &profileEntry); err == nil {
					if profileEntry.ModelName != "" {
						profile.ModelName = profileEntry.ModelName
					} else {
						profile.ModelName = profileEntry.ModelType
					}
					profile.DisplayName = profileEntry.DisplayName
					profile.MaxContextTokens = profileEntry.MaxContextTokens
					profile.SupportsMatryoshka = profileEntry.SupportsMatryoshka
					if profileEntry.Dimensions.Routing > 0 {
						profile.RoutingDim = profileEntry.Dimensions.Routing
					}
					if profileEntry.Dimensions.Document > 0 {
						profile.DocumentDim = profileEntry.Dimensions.Document
					}
					if profileEntry.Prefixes.RoutingSearch != "" {
						profile.RoutingPrefix = profileEntry.Prefixes.RoutingSearch
					}
					if profileEntry.Prefixes.DocStorage != "" {
						profile.DocStorePrefix = profileEntry.Prefixes.DocStorage
					}
					if profileEntry.Prefixes.DocSearch != "" {
						profile.DocQueryPrefix = profileEntry.Prefixes.DocSearch
					}
					return profile, true
				}
			}
			return profile, false
		}
	}

	var rawProfile struct {
		ModelName          string `json:"model_name"`
		ModelType          string `json:"model_type"`
		DisplayName        string `json:"display_name"`
		MaxContextTokens   int    `json:"max_context_tokens"`
		SupportsMatryoshka bool   `json:"supports_matryoshka"`
		Dimensions         struct {
			Routing  int `json:"routing"`
			Document int `json:"document"`
		} `json:"dimensions"`
		Prefixes struct {
			RoutingSearch string `json:"routing_search"`
			DocStorage    string `json:"doc_storage"`
			DocSearch     string `json:"doc_search"`
		} `json:"prefixes"`
	}
	if err := json.Unmarshal(data, &rawProfile); err != nil {
		return EmbeddingProfile{}, false
	}

	if rawProfile.ModelName != "" {
		profile.ModelName = rawProfile.ModelName
	} else {
		profile.ModelName = rawProfile.ModelType
	}
	profile.DisplayName = rawProfile.DisplayName
	profile.MaxContextTokens = rawProfile.MaxContextTokens
	profile.SupportsMatryoshka = rawProfile.SupportsMatryoshka
	if rawProfile.Dimensions.Routing > 0 {
		profile.RoutingDim = rawProfile.Dimensions.Routing
	}
	if rawProfile.Dimensions.Document > 0 {
		profile.DocumentDim = rawProfile.Dimensions.Document
	}
	if rawProfile.Prefixes.RoutingSearch != "" {
		profile.RoutingPrefix = rawProfile.Prefixes.RoutingSearch
	}
	if rawProfile.Prefixes.DocStorage != "" {
		profile.DocStorePrefix = rawProfile.Prefixes.DocStorage
	}
	if rawProfile.Prefixes.DocSearch != "" {
		profile.DocQueryPrefix = rawProfile.Prefixes.DocSearch
	}
	return profile, true
}

func computeLocalConcurrencyLimit() int {
	numCores := runtime.NumCPU()
	targetThreads := numCores
	if numCores <= 4 {
		targetThreads = numCores / 2
		if targetThreads < 1 {
			targetThreads = 1
		}
	} else {
		targetThreads = (numCores * 3) / 4
	}
	if targetThreads < 1 {
		targetThreads = 1
	}
	return targetThreads
}

func setupLocalHardwareThreads() (int, func()) {
	targetThreads := computeLocalConcurrencyLimit()

	oldThreads := os.Getenv("OMP_NUM_THREADS")
	_ = os.Setenv("OMP_NUM_THREADS", strconv.Itoa(targetThreads))

	return targetThreads, func() {
		if oldThreads == "" {
			_ = os.Unsetenv("OMP_NUM_THREADS")
			return
		}
		_ = os.Setenv("OMP_NUM_THREADS", oldThreads)
	}
}

var localEmbedderFactories = map[string]LocalEmbedderFactory{}

var downloadLocalModelFile = func(dst, modelURL string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(modelURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %s", resp.Status)
	}

	_, err = io.Copy(out, resp.Body)
	return err
}

func RegisterLocalEmbedder(name string, factory LocalEmbedderFactory) {
	if strings.TrimSpace(name) == "" {
		return
	}
	if factory == nil {
		return
	}
	localEmbedderFactories[strings.ToLower(strings.TrimSpace(name))] = factory
}

func AvailableLocalEmbedders() []string {
	names := make([]string, 0, len(localEmbedderFactories))
	for name := range localEmbedderFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func NewLocal(modelPath string, gpuLayers int) (*Local, error) {
	return NewLocalWithProvider("", modelPath, gpuLayers)
}

func NewLocalWithProvider(providerName, modelPath string, gpuLayers int) (*Local, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if providerName == "" {
		providerName = "kelindar"
	}
	log.Info("local embedder provider selected", "provider", providerName, "model_path", logsafe.V(modelPath), "gpu_layers", gpuLayers)
	factory, ok := localEmbedderFactories[providerName]
	if !ok {
		available := strings.Join(AvailableLocalEmbedders(), ", ")
		if available == "" {
			available = "(none registered)"
		}
		return nil, fmt.Errorf("unknown local embedder provider %q (available: %s)", providerName, available)
	}
	return newLocal(modelPath, gpuLayers, factory)
}

func ensureLocalModelPath(modelPath string) (string, error) {
	modelPath = strings.TrimSpace(modelPath)
	if modelPath == "" {
		return "", fmt.Errorf("local embedder model path is required")
	}

	if _, err := os.Stat(modelPath); err == nil {
		return modelPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	candidateNames := []string{filepath.Base(modelPath)}
	if strings.EqualFold(modelPath, "kelindar") {
		candidateNames = append(candidateNames, defaultLocalModelFileName)
	}

	for _, candidate := range candidateNames {
		for _, dir := range []string{
			".",
			"models",
			filepath.Join("ai", "models"),
			"..",
			filepath.Join("..", "..", "models"),
			filepath.Join("..", "..", "ai", "models"),
			filepath.Dir(os.Args[0]),
			filepath.Join(filepath.Dir(os.Args[0]), "models"),
			filepath.Join(filepath.Dir(os.Args[0]), "ai", "models"),
		} {
			candidatePath := filepath.Join(dir, candidate)
			if _, err := os.Stat(candidatePath); err == nil {
				return candidatePath, nil
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	cacheDir := filepath.Join(home, ".cache", "sop", "models")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	cachedPath := filepath.Join(cacheDir, defaultLocalModelFileName)
	if _, err := os.Stat(cachedPath); err == nil {
		return cachedPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := downloadLocalModelFile(cachedPath, defaultLocalModelDownloadURL); err != nil {
		return "", fmt.Errorf("failed to download local embedder model: %w", err)
	}
	return cachedPath, nil
}

func newLocal(modelPath string, gpuLayers int, factory LocalEmbedderFactory) (*Local, error) {
	modelPath = strings.TrimSpace(modelPath)
	if modelPath == "" {
		return nil, fmt.Errorf("local embedder model path is required")
	}
	if factory == nil {
		return nil, fmt.Errorf("local embedder factory is not registered")
	}

	model, err := factory(modelPath, gpuLayers)
	if err != nil {
		log.Error("local embedder model load failed", "model_path", logsafe.V(modelPath), "gpu_layers", gpuLayers, "error", err)
		return nil, fmt.Errorf("failed to load local embedder model: %w", err)
	}

	concurrencyLimit := computeLocalConcurrencyLimit()
	local := &Local{
		modelPath:        modelPath,
		gpuLayers:        gpuLayers,
		name:             "local-" + strings.ToLower(filepath.Base(modelPath)),
		profile:          loadEmbeddingProfile(modelPath),
		model:            model,
		newModel:         factory,
		gate:             make(chan struct{}, concurrencyLimit),
		concurrencyLimit: concurrencyLimit,
	}
	log.Debug("local embedder initialized", "name", logsafe.V(local.Name()), "model_path", logsafe.V(local.modelPath), "dim", local.Dim(), "routing_dim", local.profile.RoutingDim, "document_dim", local.profile.DocumentDim, "concurrency_limit", concurrencyLimit)
	return local, nil
}

func (e *Local) Name() string { return e.name }

func (e *Local) Dim() int {
	if e.profile.DocumentDim > 0 {
		return e.profile.DocumentDim
	}
	return e.dim
}

func (e *Local) EmbedCategoryTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embedWithProfile(ctx, texts, e.profile.RoutingPrefix, e.profile.RoutingDim)
}

func (e *Local) EmbedDocumentTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embedWithProfile(ctx, texts, e.profile.DocStorePrefix, e.profile.DocumentDim)
}

func (e *Local) EmbedQueryTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embedWithProfile(ctx, texts, e.profile.DocQueryPrefix, e.profile.DocumentDim)
}

func (e *Local) embedWithProfile(ctx context.Context, texts []string, prefix string, targetDim int) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	log.Debug("local embedder prefix path", "name", e.Name(), "prefix", prefix, "target_dim", targetDim, "text_count", len(texts), "model_path", e.modelPath)

	prefixed := make([]string, 0, len(texts))
	for _, text := range texts {
		prefixed = append(prefixed, prefix+text)
	}

	vectors, err := e.EmbedTexts(ctx, prefixed)
	if err != nil {
		return nil, err
	}

	out := make([][]float32, 0, len(vectors))
	for _, vec := range vectors {
		candidate := vec
		if targetDim > 0 && len(vec) > targetDim {
			candidate = make([]float32, targetDim)
			copy(candidate, vec[:targetDim])
		}
		out = append(out, candidate)
	}
	return out, nil
}

func (e *Local) Close() error {
	if e.model == nil {
		return nil
	}
	return e.model.Close()
}

func (e *Local) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	log.Debug("local embedder embedding batch", "name", e.Name(), "model_path", e.modelPath, "text_count", len(texts), "dim", e.Dim())
	if len(texts) == 0 {
		return nil, nil
	}
	if e.model == nil {
		return nil, fmt.Errorf("local embedder model is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("local embedding aborted: %w", err)
	}

	if e.gate != nil {
		select {
		case e.gate <- struct{}{}:
		case <-ctx.Done():
			return nil, fmt.Errorf("local embedding cancelled while waiting for a slot: %w", ctx.Err())
		}
		defer func() { <-e.gate }()
	}

	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("local embedding aborted: %w", err)
		}

		vector, err := e.model.EmbedText(text)
		if err != nil {
			return nil, fmt.Errorf("local embedding failed: %w", err)
		}

		// Currently, these are the constraints of what requires Vector normalization, as per Kelindra
		// library feature requirements.
		if e.profile.SupportsMatryoshka && strings.HasPrefix(text, e.profile.RoutingPrefix) {
			if e.profile.RoutingDim > 0 && len(vector) > e.profile.RoutingDim {
				trimmed := make([]float32, e.profile.RoutingDim)
				copy(trimmed, vector[:e.profile.RoutingDim])
				vector = NormalizeVector(trimmed)
			} else if len(vector) > 0 {
				vector = NormalizeVector(vector)
			}
		}
		if e.dim == 0 && len(vector) > 0 {
			e.dim = len(vector)
		}
		out = append(out, vector)
	}

	log.Debug("local embedder vectors generated", "name", e.Name(), "model_path", e.modelPath, "input_text_count", len(texts), "output_vector_count", len(out), "vector_dims", func() []int {
		dims := make([]int, 0, len(out))
		for _, vec := range out {
			dims = append(dims, len(vec))
		}
		return dims
	}())
	return out, nil
}
