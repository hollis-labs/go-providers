package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAzureOpenAI_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := azureEmbeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2, 0.3}, Index: 0},
			},
			Usage: struct {
				PromptTokens int `json:"prompt_tokens"`
				TotalTokens  int `json:"total_tokens"`
			}{PromptTokens: 5, TotalTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	az := &AzureOpenAI{
		apiKey:     "test-key",
		endpoint:   srv.URL,
		deployment: "my-deployment",
		apiVersion: "2024-06-01",
		client:     srv.Client(),
	}

	result, err := az.Embed(context.Background(), "hello world", "")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(result.Embedding) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(result.Embedding))
	}
	if result.Embedding[0] != 0.1 {
		t.Errorf("expected first dim 0.1, got %f", result.Embedding[0])
	}
	if result.TokenCount != 5 {
		t.Errorf("expected token count 5, got %d", result.TokenCount)
	}
}

func TestAzureOpenAI_EmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req azureEmbeddingRequest
		json.NewDecoder(r.Body).Decode(&req)

		data := make([]struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}, len(req.Input))
		for i := range req.Input {
			data[i] = struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				Embedding: []float32{float32(i) * 0.1, float32(i) * 0.2},
				Index:     i,
			}
		}

		resp := azureEmbeddingResponse{
			Data: data,
			Usage: struct {
				PromptTokens int `json:"prompt_tokens"`
				TotalTokens  int `json:"total_tokens"`
			}{PromptTokens: 10, TotalTokens: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	az := &AzureOpenAI{
		apiKey:     "test-key",
		endpoint:   srv.URL,
		deployment: "my-deployment",
		apiVersion: "2024-06-01",
		client:     srv.Client(),
	}

	results, err := az.EmbedBatch(context.Background(), []string{"hello", "world"}, "")
	if err != nil {
		t.Fatalf("EmbedBatch returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].TokenCount != 5 {
		t.Errorf("expected token count 5 per text, got %d", results[0].TokenCount)
	}
	if results[1].Embedding[0] != 0.1 {
		t.Errorf("expected second result first dim 0.1, got %f", results[1].Embedding[0])
	}
}

func TestAzureOpenAI_EmbeddingDimensions(t *testing.T) {
	az := &AzureOpenAI{}

	tests := []struct {
		model string
		want  int
	}{
		{"text-embedding-3-small", 1536},
		{"text-embedding-3-large", 3072},
		{"text-embedding-ada-002", 1536},
		{"unknown-model", 0},
	}

	for _, tt := range tests {
		got := az.EmbeddingDimensions(tt.model)
		if got != tt.want {
			t.Errorf("EmbeddingDimensions(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestAzureOpenAI_Embed_NoAPIKey(t *testing.T) {
	az := &AzureOpenAI{
		apiKey:     "",
		endpoint:   "https://example.openai.azure.com",
		deployment: "my-deployment",
		apiVersion: "2024-06-01",
		client:     http.DefaultClient,
	}

	_, err := az.Embed(context.Background(), "hello", "")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "AZURE_OPENAI_API_KEY not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAzureOpenAI_Embed_NoEndpoint(t *testing.T) {
	az := &AzureOpenAI{
		apiKey:     "test-key",
		endpoint:   "",
		deployment: "my-deployment",
		apiVersion: "2024-06-01",
		client:     http.DefaultClient,
	}

	_, err := az.Embed(context.Background(), "hello", "")
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
	if !strings.Contains(err.Error(), "AZURE_OPENAI_ENDPOINT not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAzureOpenAI_Embed_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer srv.Close()

	az := &AzureOpenAI{
		apiKey:     "test-key",
		endpoint:   srv.URL,
		deployment: "my-deployment",
		apiVersion: "2024-06-01",
		client:     srv.Client(),
	}

	_, err := az.Embed(context.Background(), "hello", "")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to contain status code 400: %v", err)
	}
}

func TestAzureOpenAI_Embed_ModelAsDeployment(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		resp := azureEmbeddingResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1}, Index: 0},
			},
			Usage: struct {
				PromptTokens int `json:"prompt_tokens"`
				TotalTokens  int `json:"total_tokens"`
			}{PromptTokens: 1, TotalTokens: 1},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	az := &AzureOpenAI{
		apiKey:     "test-key",
		endpoint:   srv.URL,
		deployment: "default-deployment",
		apiVersion: "2024-06-01",
		client:     srv.Client(),
	}

	_, err := az.Embed(context.Background(), "hello", "custom-embedding-deployment")
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}

	expectedPath := "/openai/deployments/custom-embedding-deployment/embeddings"
	if capturedPath != expectedPath {
		t.Errorf("expected URL path %q, got %q", expectedPath, capturedPath)
	}
}
