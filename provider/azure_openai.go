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

var _ Embedder = (*AzureOpenAI)(nil)

// AzureOpenAI implements the Provider interface for Azure-hosted OpenAI models.
// Uses the same protocol as OpenAI but with different auth (api-key header)
// and URL structure (resource endpoint + deployment + api-version).
type AzureOpenAI struct {
	apiKey     string
	endpoint   string // e.g. https://<resource>.openai.azure.com
	deployment string // deployment name
	apiVersion string // e.g. 2024-06-01
	client     *http.Client
}

// NewAzureOpenAI creates a new Azure OpenAI provider. Configuration from env:
//   - AZURE_OPENAI_API_KEY — API key
//   - AZURE_OPENAI_ENDPOINT — resource endpoint URL
//   - AZURE_OPENAI_DEPLOYMENT — deployment name
//   - AZURE_OPENAI_API_VERSION — API version (default: 2024-06-01)
func NewAzureOpenAI() *AzureOpenAI {
	apiVersion := os.Getenv("AZURE_OPENAI_API_VERSION")
	if apiVersion == "" {
		apiVersion = "2024-06-01"
	}
	return &AzureOpenAI{
		apiKey:     "",
		endpoint:   strings.TrimRight(os.Getenv("AZURE_OPENAI_ENDPOINT"), "/"),
		deployment: os.Getenv("AZURE_OPENAI_DEPLOYMENT"),
		apiVersion: apiVersion,
		client:     &http.Client{},
	}
}

// url constructs the Azure OpenAI chat completions endpoint.
func (az *AzureOpenAI) url() string {
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		az.endpoint, az.deployment, az.apiVersion)
}

// StreamChat implements Provider.StreamChat using Azure OpenAI's streaming API.
func (az *AzureOpenAI) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	if az.apiKey == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_API_KEY not set")
	}
	if az.endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT not set")
	}

	// in.Model is ignored for Azure — the deployment determines the model.
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
		Messages: msgs,
		Stream:   true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", az.url(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", az.apiKey)

	resp, err := az.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure openai API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go az.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the OpenAI-compatible SSE stream from Azure.
func (az *AzureOpenAI) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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

// Complete makes a non-streaming completion call to Azure OpenAI.
func (az *AzureOpenAI) Complete(ctx context.Context, in ChatRequest) (string, error) {
	if az.apiKey == "" {
		return "", fmt.Errorf("AZURE_OPENAI_API_KEY not set")
	}
	if az.endpoint == "" {
		return "", fmt.Errorf("AZURE_OPENAI_ENDPOINT not set")
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
		Messages: msgs,
		Stream:   false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", az.url(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", az.apiKey)

	resp, err := az.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("azure openai API error %d: %s", resp.StatusCode, string(errBody))
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

// Capabilities returns the capabilities supported by the Azure OpenAI provider.
func (az *AzureOpenAI) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:    true,
		SupportsToolCalling:   false, // Not yet implemented
		SupportsImageInput:    true,  // GPT-4o/Turbo on Azure support vision
		SupportsEmbedding:     true,
		DefaultEmbeddingModel: "text-embedding-3-small",
		MaxTokens:             16384,  // Azure OpenAI max output tokens
		ContextWindowSize:     128000, // Azure OpenAI context window
	}
}

// embeddingsURL constructs the Azure OpenAI embeddings endpoint for the given deployment.
func (az *AzureOpenAI) embeddingsURL(deployment string) string {
	return fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
		az.endpoint, deployment, az.apiVersion)
}

// azureEmbeddingRequest is the request body for the Azure OpenAI Embeddings API.
type azureEmbeddingRequest struct {
	Input []string `json:"input"`
}

// azureEmbeddingResponse is the response body from the Azure OpenAI Embeddings API.
type azureEmbeddingResponse struct {
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
func (az *AzureOpenAI) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := az.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (az *AzureOpenAI) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if az.apiKey == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_API_KEY not set")
	}
	if az.endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT not set")
	}

	deployment := model
	if deployment == "" {
		deployment = az.deployment
	}

	body := azureEmbeddingRequest{
		Input: texts,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", az.embeddingsURL(deployment), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", az.apiKey)

	resp, err := az.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure openai embeddings error %d: %s", resp.StatusCode, string(errBody))
	}

	var result azureEmbeddingResponse
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
func (az *AzureOpenAI) EmbeddingDimensions(model string) int {
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
