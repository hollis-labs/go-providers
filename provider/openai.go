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

const openaiAPI = "https://api.openai.com/v1/chat/completions"
const openaiEmbeddingsAPI = "https://api.openai.com/v1/embeddings"

// Compile-time interface check: OpenAI must satisfy Embedder.
var _ Embedder = (*OpenAI)(nil)

// OpenAI implements the Provider interface for the OpenAI Chat Completions API.
type OpenAI struct {
	apiKey string
	client *http.Client
}

// NewOpenAI creates a new OpenAI provider. It reads OPENAI_API_KEY from the environment.
func NewOpenAI() *OpenAI {
	return &OpenAI{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		client: &http.Client{},
	}
}

// openaiRequest is the request body for the OpenAI Chat Completions API.
type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// StreamChat implements Provider.StreamChat using OpenAI's streaming SSE API.
func (o *OpenAI) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "gpt-4o"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	// Build messages array with system prompt first.
	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, openaiMessage{Role: m.Role, Content: m.Content})
	}

	body := openaiRequest{
		Model:    model,
		Messages: msgs,
		Stream:   true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiAPI, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go o.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the SSE stream from OpenAI and emits StreamEvents.
func (o *OpenAI) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			ch <- StreamEvent{Type: "done"}
			return
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			if delta != "" {
				ch <- StreamEvent{Type: "delta", Content: delta}
			}

			if chunk.Choices[0].FinishReason != nil {
				reason := *chunk.Choices[0].FinishReason
				ch <- StreamEvent{
					Type: "usage",
					Usage: &Usage{
						StopReason: reason,
					},
				}
			}
		}

		if chunk.Usage != nil {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
				},
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// Complete makes a non-streaming completion call to OpenAI.
func (o *OpenAI) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := o.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage makes a non-streaming completion call to OpenAI and preserves usage metadata.
func (o *OpenAI) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	if o.apiKey == "" {
		return CompleteResult{}, fmt.Errorf("OPENAI_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "gpt-4o"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range messages {
		msgs = append(msgs, openaiMessage{Role: m.Role, Content: m.Content})
	}

	body := openaiRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiAPI, bytes.NewReader(payload))
	if err != nil {
		return CompleteResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return CompleteResult{}, fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return CompleteResult{}, fmt.Errorf("decode response: %w", err)
	}

	out := CompleteResult{}
	if result.Usage != nil {
		out.Usage = &Usage{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
		}
	}
	if len(result.Choices) > 0 {
		out.Text = strings.TrimSpace(result.Choices[0].Message.Content)
	}
	return out, nil
}

// Capabilities returns the capabilities supported by the OpenAI provider.
func (o *OpenAI) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,  // OpenAI supports streaming responses
		SupportsPreToolHooks:        false, // No direct pre-tool hook support
		SupportsPostToolHooks:       false, // No direct post-tool hook support
		SupportsSystemPromptCaching: false, // No prompt caching support in current implementation
		SupportsToolCalling:         false, // Tool calling not implemented (StreamChatWithTools ignores tools)
		SupportsBatch:               false, // No batch API support in current implementation
		SupportsImageInput:          true,  // GPT-4o and GPT-4 Turbo support image inputs
		SupportsEmbedding:           true,
		DefaultEmbeddingModel:       "text-embedding-3-small",
		MaxTokens:                   16384,  // GPT-4o max output tokens
		ContextWindowSize:           128000, // GPT-4o context window
	}
}

// openaiEmbeddingRequest is the request body for the OpenAI Embeddings API.
type openaiEmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// openaiEmbeddingResponse is the response body from the OpenAI Embeddings API.
type openaiEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed generates an embedding vector for a single text input.
func (o *OpenAI) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := o.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (o *OpenAI) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	if model == "" {
		model = "text-embedding-3-small"
	}

	body := openaiEmbeddingRequest{
		Input: texts,
		Model: model,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openaiEmbeddingsAPI, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embeddings error %d: %s", resp.StatusCode, string(errBody))
	}

	var result openaiEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Build results ordered by index.
	results := make([]EmbeddingResult, len(result.Data))
	tokensPerText := 0
	if len(texts) > 0 {
		tokensPerText = result.Usage.PromptTokens / len(texts)
	}
	for _, d := range result.Data {
		results[d.Index] = EmbeddingResult{
			Embedding:  d.Embedding,
			TokenCount: tokensPerText,
		}
	}

	return results, nil
}

// EmbeddingDimensions returns the output dimensions for the given model.
// Returns 0 if the model is unknown.
func (o *OpenAI) EmbeddingDimensions(model string) int {
	switch model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 0
	}
}
