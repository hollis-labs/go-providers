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

const openzenAPI = "https://api.open-zen.com/v1/chat/completions"

// OpenZen implements the Provider interface for the OpenZen API.
// OpenZen is an OpenAI-compatible inference gateway.
type OpenZen struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewOpenZen creates a new OpenZen provider. It reads OPENZEN_API_KEY from the environment.
// Optionally reads OPENZEN_BASE_URL to override the default endpoint.
func NewOpenZen() *OpenZen {
	base := os.Getenv("OPENZEN_BASE_URL")
	if base == "" {
		base = openzenAPI
	}
	return &OpenZen{
		apiKey:  "",
		baseURL: strings.TrimRight(base, "/"),
		client:  &http.Client{},
	}
}

// StreamChat implements Provider.StreamChat using OpenZen's OpenAI-compatible streaming API.
func (oz *OpenZen) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	if oz.apiKey == "" {
		return nil, fmt.Errorf("OPENZEN_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
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

	req, err := http.NewRequestWithContext(ctx, "POST", oz.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+oz.apiKey)

	resp, err := oz.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openzen API error %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go oz.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE parses the OpenAI-compatible SSE stream from OpenZen.
func (oz *OpenZen) readSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
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
					Type:  EventUsage,
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

// Complete makes a non-streaming completion call to OpenZen.
func (oz *OpenZen) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := oz.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage makes a non-streaming completion call to OpenZen and preserves usage metadata.
func (oz *OpenZen) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	if oz.apiKey == "" {
		return CompleteResult{}, fmt.Errorf("OPENZEN_API_KEY not set")
	}

	model := in.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
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

	req, err := http.NewRequestWithContext(ctx, "POST", oz.baseURL, bytes.NewReader(payload))
	if err != nil {
		return CompleteResult{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+oz.apiKey)

	resp, err := oz.client.Do(req)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return CompleteResult{}, fmt.Errorf("openzen API error %d: %s", resp.StatusCode, string(errBody))
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

// Capabilities returns the capabilities supported by the OpenZen provider.
func (oz *OpenZen) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:  true,
		SupportsToolCalling: false,
		SupportsImageInput:  true,
		MaxTokens:           0,      // Variable — depends on routed model
		ContextWindowSize:   200000, // Upper bound for supported models
	}
}
