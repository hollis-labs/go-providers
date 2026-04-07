package provider

import (
	"testing"
)

func TestProviderCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
	}{
		{
			name:     "Anthropic provider",
			provider: NewAnthropic(),
		},
		{
			name:     "OpenAI provider",
			provider: NewOpenAI(),
		},
		{
			name:     "Ollama provider",
			provider: NewOllama(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := tt.provider.Capabilities()

			// Verify that capabilities struct is properly returned
			// All providers should have some capability information
			if caps.MaxTokens < 0 {
				t.Errorf("MaxTokens should not be negative, got %d", caps.MaxTokens)
			}

			// Test that at least streaming is supported by all providers
			if !caps.SupportsStreamJSON {
				t.Errorf("Expected SupportsStreamJSON to be true for %s", tt.name)
			}
		})
	}
}

func TestAnthropicCapabilities(t *testing.T) {
	provider := NewAnthropic()
	caps := provider.Capabilities()

	// Test Anthropic-specific capabilities
	if !caps.SupportsToolCalling {
		t.Error("Anthropic should support tool calling")
	}
	if !caps.SupportsSystemPromptCaching {
		t.Error("Anthropic should support system prompt caching")
	}
	if !caps.SupportsImageInput {
		t.Error("Anthropic should support image input")
	}
	if caps.MaxTokens != 16384 {
		t.Errorf("Anthropic MaxTokens should be 16384, got %d", caps.MaxTokens)
	}
	if caps.ContextWindowSize != 200000 {
		t.Errorf("Anthropic ContextWindowSize should be 200000, got %d", caps.ContextWindowSize)
	}
}

func TestOpenAICapabilities(t *testing.T) {
	provider := NewOpenAI()
	caps := provider.Capabilities()

	// Test OpenAI-specific capabilities
	if caps.SupportsToolCalling {
		t.Error("OpenAI provider should not support tool calling in current implementation")
	}
	if caps.SupportsSystemPromptCaching {
		t.Error("OpenAI should not support system prompt caching in current implementation")
	}
	if !caps.SupportsImageInput {
		t.Error("OpenAI should support image input")
	}
	if caps.MaxTokens != 16384 {
		t.Errorf("OpenAI MaxTokens should be 16384, got %d", caps.MaxTokens)
	}
	if caps.ContextWindowSize != 128000 {
		t.Errorf("OpenAI ContextWindowSize should be 128000, got %d", caps.ContextWindowSize)
	}
}

func TestOllamaCapabilities(t *testing.T) {
	provider := NewOllama()
	caps := provider.Capabilities()

	// Test Ollama-specific capabilities
	if caps.SupportsToolCalling {
		t.Error("Ollama provider should not support tool calling in current implementation")
	}
	if caps.SupportsSystemPromptCaching {
		t.Error("Ollama should not support system prompt caching in current implementation")
	}
	if caps.SupportsImageInput {
		t.Error("Most Ollama models don't support image input")
	}
	if caps.MaxTokens != 0 {
		t.Errorf("Ollama MaxTokens should be 0 (variable), got %d", caps.MaxTokens)
	}
}