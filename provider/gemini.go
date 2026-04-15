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

const geminiAPI = "https://generativelanguage.googleapis.com/v1beta/models"

var _ Embedder = (*Gemini)(nil)

// Gemini implements the Provider interface for the Google Gemini API.
type Gemini struct {
	apiKey string
	client *http.Client
}

// NewGemini creates a new Gemini provider. It reads GOOGLE_API_KEY from the environment.
func NewGemini() *Gemini {
	return &Gemini{
		apiKey: "",
		client: &http.Client{},
	}
}

// geminiRequest is the request body for the Gemini generateContent API.
type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	SystemInstruct   *geminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

// StreamChat implements Provider.StreamChat using Gemini's streaming SSE API.
func (g *Gemini) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	body := g.buildRequest(systemPrompt, messages)

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse&key=%s", geminiAPI, model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go g.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// buildRequest converts Nanite messages to Gemini API format.
func (g *Gemini) buildRequest(systemPrompt string, messages []ChatMessage) geminiRequest {
	req := geminiRequest{
		GenerationConfig: &geminiGenerationConfig{
			MaxOutputTokens: 8192,
		},
	}

	if systemPrompt != "" {
		req.SystemInstruct = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}

	for _, m := range messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		content := m.Content
		if content == "" && len(m.ContentBlocks) > 0 {
			var parts []string
			for _, b := range m.ContentBlocks {
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
				if b.Content != "" {
					parts = append(parts, b.Content)
				}
			}
			content = strings.Join(parts, "\n")
		}
		if content == "" {
			continue
		}
		req.Contents = append(req.Contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: content}},
		})
	}

	return req
}

// readSSE parses the SSE stream from Gemini and emits StreamEvents.
func (g *Gemini) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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

		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata *struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Candidates) > 0 {
			cand := chunk.Candidates[0]
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					ch <- StreamEvent{Type: "delta", Content: part.Text}
				}
			}

			if cand.FinishReason != "" && cand.FinishReason != "STOP" {
				ch <- StreamEvent{
					Type: "usage",
					Usage: &Usage{
						StopReason: strings.ToLower(cand.FinishReason),
					},
				}
			}
		}

		if chunk.UsageMetadata != nil {
			ch <- StreamEvent{
				Type: "usage",
				Usage: &Usage{
					InputTokens:  chunk.UsageMetadata.PromptTokenCount,
					OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
				},
			}
		}
	}

	// Emit done after stream ends.
	ch <- StreamEvent{Type: "done"}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: "error", Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// Complete makes a non-streaming completion call to Gemini.
func (g *Gemini) Complete(ctx context.Context, in ChatRequest) (string, error) {
	if g.apiKey == "" {
		return "", fmt.Errorf("GOOGLE_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}
	systemPrompt := in.EffectiveSystemPrompt()
	messages := in.Messages

	body := g.buildRequest(systemPrompt, messages)

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiAPI, model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini API error %d: %s", resp.StatusCode, string(errBody))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(result.Candidates) > 0 {
		var parts []string
		for _, p := range result.Candidates[0].Content.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "")), nil
	}
	return "", nil
}

// Capabilities returns the capabilities supported by the Gemini provider.
func (g *Gemini) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,
		SupportsToolCalling:         false, // Not yet implemented for Gemini HTTP
		SupportsImageInput:          true,
		SupportsSystemPromptCaching: true, // Gemini supports context caching
		SupportsEmbedding:           true,
		DefaultEmbeddingModel:       "text-embedding-004",
		MaxTokens:                   65536,   // Gemini 2.5 Pro max output tokens
		ContextWindowSize:           1048576, // Gemini 2.5 Pro context window
	}
}

// gemini embedding types (unexported)

type geminiBatchEmbedRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiEmbedRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

type geminiBatchEmbedResponse struct {
	Embeddings []geminiEmbeddingValue `json:"embeddings"`
}

type geminiEmbeddingValue struct {
	Values []float64 `json:"values"`
}

// Embed generates an embedding vector for a single text input.
func (g *Gemini) Embed(ctx context.Context, text string, model string) (*EmbeddingResult, error) {
	results, err := g.EmbedBatch(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	return &results[0], nil
}

// EmbedBatch generates embedding vectors for multiple texts in a single API call.
func (g *Gemini) EmbedBatch(ctx context.Context, texts []string, model string) ([]EmbeddingResult, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY not set")
	}

	if model == "" {
		model = "text-embedding-004"
	}

	// Build batch request.
	batchReq := geminiBatchEmbedRequest{
		Requests: make([]geminiEmbedRequest, len(texts)),
	}
	for i, text := range texts {
		batchReq.Requests[i] = geminiEmbedRequest{
			Model:   "models/" + model,
			Content: geminiContent{Parts: []geminiPart{{Text: text}}},
		}
	}

	payload, err := json.Marshal(batchReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s:batchEmbedContents?key=%s", geminiAPI, model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini embeddings error %d: %s", resp.StatusCode, string(errBody))
	}

	var batchResp geminiBatchEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]EmbeddingResult, len(batchResp.Embeddings))
	for i, emb := range batchResp.Embeddings {
		vec := make([]float32, len(emb.Values))
		for j, v := range emb.Values {
			vec[j] = float32(v)
		}
		results[i] = EmbeddingResult{
			Embedding: vec,
		}
	}

	return results, nil
}

// EmbeddingDimensions returns the output dimensions for the given model.
// Returns 0 if the model is unknown.
func (g *Gemini) EmbeddingDimensions(model string) int {
	switch model {
	case "text-embedding-004":
		return 768
	default:
		return 0
	}
}
