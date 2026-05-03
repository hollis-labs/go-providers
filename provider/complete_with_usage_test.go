package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHTTPClientForServer(srv *httptest.Server) *http.Client {
	return &http.Client{
		Transport: &rewriteTransport{
			base:      srv.Client().Transport,
			targetURL: srv.URL,
		},
	}
}

func writeExecutableScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

func TestOpenAICompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("expected bearer auth, got %q", r.Header.Get("Authorization"))
		}
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"Hello from OpenAI"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
	}))
	defer srv.Close()

	o := &OpenAI{apiKey: "test-key", client: testHTTPClientForServer(srv)}
	result, err := o.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from OpenAI" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
}

func TestAzureOpenAICompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "test-key" {
			t.Fatalf("expected api-key auth, got %q", r.Header.Get("api-key"))
		}
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"Hello from Azure"}}],"usage":{"prompt_tokens":13,"completion_tokens":9}}`)
	}))
	defer srv.Close()

	az := &AzureOpenAI{
		apiKey:     "test-key",
		endpoint:   "https://azure.example.com",
		deployment: "demo",
		apiVersion: "2024-06-01",
		client:     testHTTPClientForServer(srv),
	}
	result, err := az.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from Azure" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 13 || result.Usage.OutputTokens != 9 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
}

func TestAnthropicCompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("expected x-api-key auth, got %q", r.Header.Get("x-api-key"))
		}
		fmt.Fprintln(w, `{"content":[{"type":"text","text":"Hello from Anthropic"}],"usage":{"input_tokens":17,"output_tokens":12,"cache_creation_input_tokens":5,"cache_read_input_tokens":3},"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	a := NewAnthropic()
	a.apiKey = "test-key"
	a.client = testHTTPClientForServer(srv)
	a.Retry.MaxRetries = 0

	result, err := a.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from Anthropic" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil {
		t.Fatal("expected usage")
	}
	if result.Usage.InputTokens != 17 || result.Usage.OutputTokens != 12 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.Usage.CacheCreationTokens != 5 || result.Usage.CacheReadTokens != 3 {
		t.Fatalf("unexpected cache usage: %+v", result.Usage)
	}
	if result.Usage.StopReason != "end_turn" {
		t.Fatalf("unexpected stop reason: %+v", result.Usage)
	}
}

// TestAnthropicCompleteWithUsage_DefaultMaxTokens pins the no-cap default the
// f6a0a8e commit established (16384, replacing the historical 128 that
// silently truncated longer completions). Regression-prone because the
// constant only appears in two places (StreamChat, CompleteWithUsage) and
// drifting one without the other re-introduces the truncation bug.
func TestAnthropicCompleteWithUsage_DefaultMaxTokens(t *testing.T) {
	var captured struct {
		MaxTokens int `json:"max_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		fmt.Fprintln(w, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	a := NewAnthropic()
	a.apiKey = "test-key"
	a.client = testHTTPClientForServer(srv)
	a.Retry.MaxRetries = 0

	if _, err := a.CompleteWithUsage(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if captured.MaxTokens != 16384 {
		t.Errorf("expected default max_tokens=16384, got %d", captured.MaxTokens)
	}
}

// TestAnthropicCompleteWithUsage_HonorsCallerMaxTokens verifies that a
// caller-supplied ChatRequest.MaxTokens is forwarded to the provider rather
// than overridden by the adapter default.
func TestAnthropicCompleteWithUsage_HonorsCallerMaxTokens(t *testing.T) {
	var captured struct {
		MaxTokens int `json:"max_tokens"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		fmt.Fprintln(w, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1},"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	a := NewAnthropic()
	a.apiKey = "test-key"
	a.client = testHTTPClientForServer(srv)
	a.Retry.MaxRetries = 0

	if _, err := a.CompleteWithUsage(context.Background(), ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 256,
	}); err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if captured.MaxTokens != 256 {
		t.Errorf("expected caller-supplied max_tokens=256, got %d", captured.MaxTokens)
	}
}

func TestGeminiCompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"candidates":[{"content":{"parts":[{"text":"Hello from Gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":19,"candidatesTokenCount":8}}`)
	}))
	defer srv.Close()

	g := &Gemini{apiKey: "test-key", client: testHTTPClientForServer(srv)}
	result, err := g.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from Gemini" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 19 || result.Usage.OutputTokens != 8 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if result.Usage.StopReason != "stop" {
		t.Fatalf("unexpected stop reason: %+v", result.Usage)
	}
}

func TestMistralCompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"Hello from Mistral"}}],"usage":{"prompt_tokens":23,"completion_tokens":10}}`)
	}))
	defer srv.Close()

	m := &Mistral{apiKey: "test-key", client: testHTTPClientForServer(srv)}
	result, err := m.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from Mistral" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 23 || result.Usage.OutputTokens != 10 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
}

func TestOpenRouterCompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"Hello from OpenRouter"}}],"usage":{"prompt_tokens":29,"completion_tokens":14}}`)
	}))
	defer srv.Close()

	o := &OpenRouter{apiKey: "test-key", client: testHTTPClientForServer(srv)}
	result, err := o.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from OpenRouter" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 29 || result.Usage.OutputTokens != 14 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
}

func TestOpenZenCompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"Hello from OpenZen"}}],"usage":{"prompt_tokens":31,"completion_tokens":15}}`)
	}))
	defer srv.Close()

	oz := &OpenZen{apiKey: "test-key", baseURL: "https://openzen.example.com/v1/chat/completions", client: testHTTPClientForServer(srv)}
	result, err := oz.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from OpenZen" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 31 || result.Usage.OutputTokens != 15 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
}

func TestOllamaCompleteWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"message":{"content":"Hello from Ollama"},"done":true,"prompt_eval_count":37,"eval_count":16}`)
	}))
	defer srv.Close()

	o := &Ollama{host: srv.URL, client: srv.Client()}
	result, err := o.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if result.Text != "Hello from Ollama" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.InputTokens != 37 || result.Usage.OutputTokens != 16 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
}

func TestSubprocessBridgeCompleteWithUsageNilWhenCLIHasNoUsage(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	writeExecutableScript(t, script, `#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from subprocess without usage"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}'
`)

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	result, err := bridge.CompleteWithUsage(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test prompt"},
	}})
	if err != nil {
		t.Fatalf("CompleteWithUsage failed: %v", err)
	}
	if !strings.Contains(result.Text, "Hello from subprocess without usage") {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage != nil {
		t.Fatalf("expected nil usage, got %+v", result.Usage)
	}
}
