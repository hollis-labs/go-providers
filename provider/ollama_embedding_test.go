package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if len(req.Input) != 1 {
			t.Fatalf("expected 1 input, got %d", len(req.Input))
		}
		if req.Input[0] != "hello world" {
			t.Fatalf("unexpected input: %s", req.Input[0])
		}

		resp := ollamaEmbedResponse{
			Model:           "nomic-embed-text",
			Embeddings:      [][]float64{{0.1, 0.2, 0.3}},
			PromptEvalCount: 5,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	o := &Ollama{host: srv.URL, client: srv.Client()}
	result, err := o.Embed(context.Background(), "hello world", "nomic-embed-text")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}

	if len(result.Embedding) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(result.Embedding))
	}
	if result.TokenCount != 5 {
		t.Errorf("expected token count 5, got %d", result.TokenCount)
	}
}

func TestOllamaEmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if len(req.Input) != 3 {
			t.Fatalf("expected 3 inputs, got %d", len(req.Input))
		}

		resp := ollamaEmbedResponse{
			Model: "nomic-embed-text",
			Embeddings: [][]float64{
				{0.1, 0.2},
				{0.3, 0.4},
				{0.5, 0.6},
			},
			PromptEvalCount: 15,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	o := &Ollama{host: srv.URL, client: srv.Client()}
	results, err := o.EmbedBatch(context.Background(), []string{"a", "b", "c"}, "nomic-embed-text")
	if err != nil {
		t.Fatalf("EmbedBatch() error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if len(r.Embedding) != 2 {
			t.Errorf("result[%d]: expected 2 dimensions, got %d", i, len(r.Embedding))
		}
		if r.TokenCount != 5 { // 15 / 3
			t.Errorf("result[%d]: expected token count 5, got %d", i, r.TokenCount)
		}
	}
}

func TestOllamaEmbeddingDimensions(t *testing.T) {
	o := &Ollama{}

	tests := []struct {
		model string
		want  int
	}{
		{"nomic-embed-text", 768},
		{"all-minilm", 384},
		{"mxbai-embed-large", 1024},
		{"unknown-model", 0},
	}

	for _, tt := range tests {
		got := o.EmbeddingDimensions(tt.model)
		if got != tt.want {
			t.Errorf("EmbeddingDimensions(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestOllamaEmbedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	o := &Ollama{host: srv.URL, client: srv.Client()}
	_, err := o.Embed(context.Background(), "test", "nonexistent")
	if err == nil {
		t.Fatal("expected error for bad API response")
	}

	expected := `ollama embed API error 400: {"error":"model not found"}`
	if err.Error() != expected {
		t.Errorf("unexpected error message:\ngot:  %s\nwant: %s", err.Error(), expected)
	}
}

func TestOllamaEmbedFloat64ToFloat32Precision(t *testing.T) {
	// Use values that are exactly representable in float32 to verify conversion.
	val := 0.15625 // 5/32, exact in both float32 and float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbedResponse{
			Model:           "nomic-embed-text",
			Embeddings:      [][]float64{{val, 1.0, -1.0, 0.0}},
			PromptEvalCount: 2,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	o := &Ollama{host: srv.URL, client: srv.Client()}
	result, err := o.Embed(context.Background(), "precision test", "nomic-embed-text")
	if err != nil {
		t.Fatalf("Embed() error: %v", err)
	}

	expected := []float32{float32(val), 1.0, -1.0, 0.0}
	for i, v := range result.Embedding {
		if math.Abs(float64(v-expected[i])) > 1e-7 {
			t.Errorf("embedding[%d] = %v, want %v", i, v, expected[i])
		}
	}
}
