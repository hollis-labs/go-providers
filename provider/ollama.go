package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Compile-time check: Ollama implements the Embedder interface.
var _ Embedder = (*Ollama)(nil)

// Ollama implements the Provider interface for the local Ollama REST API.
type Ollama struct {
	host   string
	client *http.Client
}

// NewOllama creates a new Ollama provider. It reads OLLAMA_HOST from the environment,
// defaulting to http://localhost:11434.
func NewOllama() *Ollama {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	return &Ollama{
		host:   strings.TrimRight(host, "/"),
		client: &http.Client{},
	}
}

// ollamaRequest is the request body for the Ollama /api/chat endpoint.
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamChat implements Provider.StreamChat using Ollama's streaming API.
// Ollama streams newline-delimited JSON with message.content fields.
func (o *Ollama) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	model := in.Model
	if model == "" {
		model = "llama3.1"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	// Build messages array with system prompt first.
	msgs := make([]ollamaMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	body := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Stream:   true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := o.host + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go o.readStream(ctx, resp.Body, ch)
	return ch, nil
}

// readStream parses the newline-delimited JSON stream from Ollama.
func (o *Ollama) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: "error", Error: "context cancelled"}
			return
		default:
		}

		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done            bool `json:"done"`
			PromptEvalCount int  `json:"prompt_eval_count"`
			EvalCount       int  `json:"eval_count"`
		}

		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		if chunk.Message.Content != "" {
			ch <- StreamEvent{Type: "delta", Content: chunk.Message.Content}
		}

		if chunk.Done {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  chunk.PromptEvalCount,
					OutputTokens: chunk.EvalCount,
					StopReason:   "end_turn",
				},
			}
			ch <- StreamEvent{Type: "done"}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// Complete makes a non-streaming completion call to Ollama.
func (o *Ollama) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := o.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage makes a non-streaming completion call to Ollama and preserves usage metadata.
func (o *Ollama) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	model := in.Model
	if model == "" {
		model = "llama3.1"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	msgs := make([]ollamaMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	body := ollamaRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("marshal request: %w", err)
	}

	url := o.host + "/api/chat"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return CompleteResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return CompleteResult{}, fmt.Errorf("ollama API error %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Done            bool `json:"done"`
		PromptEvalCount int  `json:"prompt_eval_count"`
		EvalCount       int  `json:"eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return CompleteResult{}, fmt.Errorf("decode response: %w", err)
	}

	return CompleteResult{
		Text: strings.TrimSpace(result.Message.Content),
		Usage: &Usage{
			InputTokens:  result.PromptEvalCount,
			OutputTokens: result.EvalCount,
			StopReason:   "end_turn",
		},
	}, nil
}

// Capabilities returns the capabilities supported by the Ollama provider.
func (o *Ollama) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,  // Ollama supports streaming responses
		SupportsPreToolHooks:        false, // No direct pre-tool hook support
		SupportsPostToolHooks:       false, // No direct post-tool hook support
		SupportsSystemPromptCaching: false, // No prompt caching support in current implementation
		SupportsToolCalling:         false, // Tool calling not implemented (StreamChatWithTools ignores tools)
		SupportsBatch:               false, // No batch API support in current implementation
		SupportsImageInput:          false, // Most Ollama models don't support image inputs (depends on model)
		MaxTokens:                   0,     // Variable depending on specific model loaded in Ollama
		SupportsEmbedding:           true,
		DefaultEmbeddingModel:       "nomic-embed-text",
	}
}

// ollamaEmbedRequest is the request body for the Ollama /api/embed endpoint.
type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// ollamaEmbedResponse is the response from the Ollama /api/embed endpoint.
type ollamaEmbedResponse struct {
	Model           string      `json:"model"`
	Embeddings      [][]float64 `json:"embeddings"`
	TotalDuration   int64       `json:"total_duration"`
	PromptEvalCount int         `json:"prompt_eval_count"`
}

// Embed generates an embedding vector for a single text input.
func (o *Ollama) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := o.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (o *Ollama) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if model == "" {
		model = "nomic-embed-text"
	}

	body := ollamaEmbedRequest{
		Model: model,
		Input: texts,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := o.host + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed API error %d: %s", resp.StatusCode, string(errBody))
	}

	var embedResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(embedResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embedResp.Embeddings))
	}

	// Compute per-text token estimate (Ollama only returns total prompt_eval_count).
	perText := 0
	if len(texts) > 0 {
		perText = embedResp.PromptEvalCount / len(texts)
	}

	results := make([]EmbeddingResult, len(embedResp.Embeddings))
	for i, emb := range embedResp.Embeddings {
		f32 := make([]float32, len(emb))
		for j, v := range emb {
			f32[j] = float32(v)
		}
		results[i] = EmbeddingResult{
			Embedding:  f32,
			TokenCount: perText,
		}
	}

	return results, nil
}

// EmbeddingDimensions returns the output dimensions for the given embedding model.
// Returns 0 if the model is unknown.
func (o *Ollama) EmbeddingDimensions(model string) int {
	switch model {
	case "nomic-embed-text":
		return 768
	case "all-minilm":
		return 384
	case "mxbai-embed-large":
		return 1024
	default:
		return 0
	}
}
