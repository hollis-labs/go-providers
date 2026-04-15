package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const mistralAPI = "https://api.mistral.ai/v1/chat/completions"
const mistralEmbeddingsAPI = "https://api.mistral.ai/v1/embeddings"

var _ Embedder = (*Mistral)(nil)

// Mistral implements the Provider interface for the Mistral API.
// Uses the OpenAI-compatible chat completions format.
type Mistral struct {
	apiKey string
	client *http.Client
}

// NewMistral creates a new Mistral provider. It reads MISTRAL_API_KEY from the environment.
func NewMistral() *Mistral {
	return &Mistral{
		apiKey: "",
		client: &http.Client{},
	}
}

// StreamChat implements Provider.StreamChat using Mistral's OpenAI-compatible streaming API.
func (m *Mistral) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("MISTRAL_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "mistral-large-latest"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, msg := range messages {
		msgs = append(msgs, openaiMessage{Role: msg.Role, Content: msg.Content})
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

	req, err := http.NewRequestWithContext(ctx, "POST", mistralAPI, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mistral API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go m.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the OpenAI-compatible SSE stream from Mistral.
func (m *Mistral) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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
				ch <- StreamEvent{
					Type:  "usage",
					Usage: &Usage{StopReason: *chunk.Choices[0].FinishReason},
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

// Complete makes a non-streaming completion call to Mistral.
func (m *Mistral) Complete(ctx context.Context, in ChatRequest) (string, error) {
	if m.apiKey == "" {
		return "", fmt.Errorf("MISTRAL_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "mistral-large-latest"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	msgs := make([]openaiMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, openaiMessage{Role: "system", Content: systemPrompt})
	}
	for _, msg := range messages {
		msgs = append(msgs, openaiMessage{Role: msg.Role, Content: msg.Content})
	}

	body := openaiRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", mistralAPI, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("mistral API error %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) > 0 {
		return strings.TrimSpace(result.Choices[0].Message.Content), nil
	}
	return "", nil
}

// Capabilities returns the capabilities supported by the Mistral provider.
func (m *Mistral) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:    true,
		SupportsToolCalling:   false, // Not yet implemented
		SupportsImageInput:    true,  // Pixtral models support vision
		SupportsEmbedding:     true,
		DefaultEmbeddingModel: "mistral-embed",
		MaxTokens:             16384,  // Mistral Large max output tokens
		ContextWindowSize:     131072, // Mistral Large context window
	}
}

// mistralEmbeddingRequest is the request body for the Mistral Embeddings API.
type mistralEmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// mistralEmbeddingResponse is the response body from the Mistral Embeddings API.
type mistralEmbeddingResponse struct {
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
func (m *Mistral) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := m.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (m *Mistral) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("MISTRAL_API_KEY not set")
	}

	if model == "" {
		model = "mistral-embed"
	}

	body := mistralEmbeddingRequest{
		Input: texts,
		Model: model,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", mistralEmbeddingsAPI, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mistral embeddings error %d: %s", resp.StatusCode, string(errBody))
	}

	var result mistralEmbeddingResponse
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
func (m *Mistral) EmbeddingDimensions(model string) int {
	switch model {
	case "mistral-embed":
		return 1024
	default:
		return 0
	}
}
