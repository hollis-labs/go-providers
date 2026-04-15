package provider

import (
	"context"
	"net/http"
	"testing"
)

func TestGeminiBuildRequest(t *testing.T) {
	g := &Gemini{}

	t.Run("system prompt becomes systemInstruction", func(t *testing.T) {
		req := g.buildRequest("You are helpful.", []ChatMessage{
			{Role: "user", Content: "Hello"},
		})
		if req.SystemInstruct == nil {
			t.Fatal("expected systemInstruction to be set")
		}
		if req.SystemInstruct.Parts[0].Text != "You are helpful." {
			t.Errorf("unexpected system text: %s", req.SystemInstruct.Parts[0].Text)
		}
	})

	t.Run("empty system prompt omits systemInstruction", func(t *testing.T) {
		req := g.buildRequest("", []ChatMessage{
			{Role: "user", Content: "Hello"},
		})
		if req.SystemInstruct != nil {
			t.Error("expected nil systemInstruction for empty prompt")
		}
	})

	t.Run("assistant role maps to model", func(t *testing.T) {
		req := g.buildRequest("", []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		})
		if len(req.Contents) != 2 {
			t.Fatalf("expected 2 contents, got %d", len(req.Contents))
		}
		if req.Contents[1].Role != "model" {
			t.Errorf("expected role=model, got %s", req.Contents[1].Role)
		}
	})

	t.Run("content blocks are joined", func(t *testing.T) {
		req := g.buildRequest("", []ChatMessage{
			{Role: "user", ContentBlocks: []ContentBlock{
				{Type: "text", Text: "Part 1"},
				{Type: "text", Text: "Part 2"},
			}},
		})
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(req.Contents))
		}
		if req.Contents[0].Parts[0].Text != "Part 1\nPart 2" {
			t.Errorf("unexpected joined text: %s", req.Contents[0].Parts[0].Text)
		}
	})

	t.Run("empty messages are skipped", func(t *testing.T) {
		req := g.buildRequest("", []ChatMessage{
			{Role: "user", Content: ""},
			{Role: "user", Content: "Real message"},
		})
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(req.Contents))
		}
	})
}

func TestGeminiStreamChat(t *testing.T) {
	t.Run("missing API key returns error", func(t *testing.T) {
		g := &Gemini{apiKey: "", client: &http.Client{}}
		_, err := g.StreamChat(context.Background(), ChatRequest{})
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})
}

func TestGeminiComplete(t *testing.T) {
	t.Run("missing API key returns error", func(t *testing.T) {
		g := &Gemini{apiKey: "", client: &http.Client{}}
		_, err := g.Complete(context.Background(), ChatRequest{})
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})
}

func TestGeminiCapabilities(t *testing.T) {
	g := NewGemini()
	caps := g.Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON to be true")
	}
	if !caps.SupportsImageInput {
		t.Error("expected SupportsImageInput to be true")
	}
	if caps.MaxTokens != 65536 {
		t.Errorf("expected MaxTokens=65536, got %d", caps.MaxTokens)
	}
	if caps.ContextWindowSize != 1048576 {
		t.Errorf("expected ContextWindowSize=1048576, got %d", caps.ContextWindowSize)
	}
}
