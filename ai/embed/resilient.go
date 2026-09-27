package embed

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-retry"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

// ResilientEmbedder wraps an existing Embeddings model automatically
// backing off and retrying in case of network or API limit errors.
type ResilientEmbedder struct {
	base ai.Embeddings
}

// NewResilientEmbedder creates a ResilientEmbedder wrapping base Embeddings.
func NewResilientEmbedder(base ai.Embeddings) *ResilientEmbedder {
	return &ResilientEmbedder{base: base}
}

func (r *ResilientEmbedder) Name() string { return fmt.Sprintf("resilient-%s", r.base.Name()) }
func (r *ResilientEmbedder) Dim() int     { return r.base.Dim() }

func (r *ResilientEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return r.retryCall(ctx, func(rctx context.Context) ([][]float32, error) {
		return r.base.EmbedTexts(rctx, texts)
	})
}

func (r *ResilientEmbedder) EmbedCategoryTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if modeAware, ok := r.base.(ai.EmbeddingModeSupport); ok {
		return r.retryCall(ctx, func(rctx context.Context) ([][]float32, error) {
			return modeAware.EmbedCategoryTexts(rctx, texts)
		})
	}
	return r.EmbedTexts(ctx, texts)
}

func (r *ResilientEmbedder) EmbedDocumentTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if modeAware, ok := r.base.(ai.EmbeddingModeSupport); ok {
		return r.retryCall(ctx, func(rctx context.Context) ([][]float32, error) {
			return modeAware.EmbedDocumentTexts(rctx, texts)
		})
	}
	return r.EmbedTexts(ctx, texts)
}

func (r *ResilientEmbedder) EmbedQueryTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if modeAware, ok := r.base.(ai.EmbeddingModeSupport); ok {
		return r.retryCall(ctx, func(rctx context.Context) ([][]float32, error) {
			return modeAware.EmbedQueryTexts(rctx, texts)
		})
	}
	return r.EmbedTexts(ctx, texts)
}

func (r *ResilientEmbedder) retryCall(ctx context.Context, call func(context.Context) ([][]float32, error)) ([][]float32, error) {
	var result [][]float32
	err := sop.Retry(ctx, func(rctx context.Context) error {
		res, rerr := call(rctx)
		if rerr != nil {
			if sop.ShouldRetry(rerr) {
				return retry.RetryableError(rerr)
			}
			return rerr
		}
		result = res
		return nil
	}, nil)
	return result, err
}
