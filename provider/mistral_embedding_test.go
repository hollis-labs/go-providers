package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMistralEmbedSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		var req mistralEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Input) != 1 {
			t.Fatalf("expected 1 input, got %d", len(req.Input))
		}
		if req.Input[0] != "hello world" {
			t.Fatalf("unexpected input: %s", req.Input[0])
		}

		resp := mistralEmbeddingResponse{}
		resp.Data = []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}{
			{Embedding: []float32{0.1, 0.2, 0.3}, Index: 0},
		}
		resp.Usage.PromptTokens = 5
		resp.Usage.TotalTokens = 5

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m := &Mistral{apiKey: "test-key", client: &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL}}}

	result, err := m.Embed(context.Background(), "hello world", "mistral-embed")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(result.Embedding) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(result.Embedding))
	}
	if result.TokenCount != 5 {
		t.Fatalf("expected 5 tokens, got %d", result.TokenCount)
	}
}

func TestMistralEmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mistralEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Input) != 3 {
			t.Fatalf("expected 3 inputs, got %d", len(req.Input))
		}

		resp := mistralEmbeddingResponse{}
		resp.Data = []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}{
			{Embedding: []float32{0.1, 0.2}, Index: 0},
			{Embedding: []float32{0.3, 0.4}, Index: 1},
			{Embedding: []float32{0.5, 0.6}, Index: 2},
		}
		resp.Usage.PromptTokens = 15
		resp.Usage.TotalTokens = 15

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m := &Mistral{apiKey: "test-key", client: &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL}}}

	results, err := m.EmbedBatch(context.Background(), []string{"a", "b", "c"}, "mistral-embed")
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Each text gets 15/3 = 5 tokens.
	for i, r := range results {
		if r.TokenCount != 5 {
			t.Fatalf("result %d: expected 5 tokens, got %d", i, r.TokenCount)
		}
	}
	if results[2].Embedding[0] != 0.5 {
		t.Fatalf("expected results[2].Embedding[0] = 0.5, got %f", results[2].Embedding[0])
	}
}

func TestMistralEmbeddingDimensions(t *testing.T) {
	m := &Mistral{}
	if d := m.EmbeddingDimensions("mistral-embed"); d != 1024 {
		t.Fatalf("expected 1024, got %d", d)
	}
	if d := m.EmbeddingDimensions("unknown-model"); d != 0 {
		t.Fatalf("expected 0 for unknown model, got %d", d)
	}
}

func TestMistralEmbedNoAPIKey(t *testing.T) {
	m := &Mistral{apiKey: "", client: &http.Client{}}
	_, err := m.Embed(context.Background(), "test", "mistral-embed")
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
	if err.Error() != "MISTRAL_API_KEY not set" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMistralEmbedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request"}`))
	}))
	defer srv.Close()

	m := &Mistral{apiKey: "test-key", client: &http.Client{Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL}}}

	_, err := m.Embed(context.Background(), "test", "mistral-embed")
	if err == nil {
		t.Fatal("expected error on API error response")
	}
	if got := err.Error(); got != `mistral embeddings error 400: {"error":"invalid request"}` {
		t.Fatalf("unexpected error message: %s", got)
	}
}