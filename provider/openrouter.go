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

const openrouterAPI = "https://openrouter.ai/api/v1/chat/completions"

// OpenRouter implements the Provider interface for the OpenRouter API.
// OpenRouter is an OpenAI-compatible gateway that routes to 200+ models
// from Anthropic, Google, Meta, Mistral, and others.
type OpenRouter struct {
	apiKey string
	client *http.Client
}

// NewOpenRouter creates a new OpenRouter provider. It reads OPENROUTER_API_KEY from the environment.
func NewOpenRouter() *OpenRouter {
	return &OpenRouter{
		apiKey: "",
		client: &http.Client{},
	}
}

// StreamChat implements Provider.StreamChat using OpenRouter's OpenAI-compatible streaming API.
func (o *OpenRouter) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	if o.apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "anthropic/claude-sonnet-4"
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

	req, err := http.NewRequestWithContext(ctx, "POST", openrouterAPI, bytes.NewReader(payload))
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
		return nil, fmt.Errorf("openrouter API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go o.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the OpenAI-compatible SSE stream from OpenRouter.
func (o *OpenRouter) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventError, Error: "context cancelled"}
			return
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			ch <- StreamEvent{Type: EventDone}
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
				ch <- StreamEvent{Type: EventDelta, Content: delta}
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
				Type: EventUsage,
				Usage: &Usage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
				},
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Sprintf("read stream: %v", err)}
	}
}

// Complete makes a non-streaming completion call to OpenRouter.
func (o *OpenRouter) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := o.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage makes a non-streaming completion call to OpenRouter and preserves usage metadata.
func (o *OpenRouter) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	if o.apiKey == "" {
		return CompleteResult{}, fmt.Errorf("OPENROUTER_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "anthropic/claude-sonnet-4"
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
		return CompleteResult{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openrouterAPI, bytes.NewReader(payload))
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
		return CompleteResult{}, fmt.Errorf("openrouter API error %d: %s", resp.StatusCode, string(errBody))
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

// Capabilities returns the capabilities supported by the OpenRouter provider.
func (o *OpenRouter) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:  true,
		SupportsToolCalling: false,
		SupportsImageInput:  true,   // Depends on underlying model, but most top models support it
		MaxTokens:           0,      // Variable — depends on routed model
		ContextWindowSize:   200000, // Upper bound for supported models
	}
}
