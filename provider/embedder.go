package provider

import "context"

// EmbeddingResult holds the output of an embedding request.
type EmbeddingResult struct {
	// Embedding is the vector representation of the input text.
	Embedding []float32
	// TokenCount is the number of tokens consumed (for billing/rate tracking).
	TokenCount int
}

// Embedder is implemented by providers that support text embedding.
// Not all Provider implementations support embedding — use SupportsEmbedding
// on ProviderCapabilities to check, or attempt a type assertion:
//
//	if e, ok := p.(provider.Embedder); ok { ... }
type Embedder interface {
	// Embed generates an embedding vector for a single text input.
	Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error)

	// EmbedBatch generates embedding vectors for multiple texts in a single API call.
	// Providers that don't support native batching should loop internally.
	EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error)

	// EmbeddingDimensions returns the output dimensions for the given model.
	// Returns 0 if the model is unknown.
	EmbeddingDimensions(model string) int
}
