package provider

import (
	"context"
	"net/http"
	"testing"
)

func TestOpenZen_NoAPIKey(t *testing.T) {
	oz := &OpenZen{apiKey: "", baseURL: openzenAPI, client: http.DefaultClient}
	_, err := oz.StreamChat(context.Background(), "", nil, "")
	if err == nil {
		t.Error("expected error for missing API key")
	}
	_, err = oz.Complete(context.Background(), "", nil, "")
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestOpenZen_SetAPIKey(t *testing.T) {
	oz := NewOpenZen()
	oz.SetAPIKey("my-key")
	if oz.apiKey != "my-key" {
		t.Errorf("expected my-key, got %s", oz.apiKey)
	}
}

func TestOpenZen_Capabilities(t *testing.T) {
	oz := NewOpenZen()
	caps := oz.Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON")
	}
	if !caps.SupportsImageInput {
		t.Error("expected SupportsImageInput")
	}
	if caps.SupportsToolCalling {
		t.Error("expected no SupportsToolCalling")
	}
	if caps.MaxTokens != 0 {
		t.Errorf("expected MaxTokens 0, got %d", caps.MaxTokens)
	}
	if caps.ContextWindowSize != 200000 {
		t.Errorf("expected ContextWindowSize 200000, got %d", caps.ContextWindowSize)
	}
}

func TestOpenZen_BaseURLOverride(t *testing.T) {
	t.Setenv("OPENZEN_BASE_URL", "https://custom.example.com/v1/chat/completions")
	t.Setenv("OPENZEN_API_KEY", "test")
	oz := NewOpenZen()
	if oz.baseURL != "https://custom.example.com/v1/chat/completions" {
		t.Errorf("expected custom base URL, got %s", oz.baseURL)
	}
}

func TestOpenZen_DefaultBaseURL(t *testing.T) {
	oz := &OpenZen{apiKey: "test", baseURL: openzenAPI, client: http.DefaultClient}
	if oz.baseURL != openzenAPI {
		t.Errorf("expected default base URL, got %s", oz.baseURL)
	}
}
