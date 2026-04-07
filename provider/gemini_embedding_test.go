package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// geminiTestTransport rewrites request URLs to point at a test server.
type geminiTestTransport struct {
	base http.RoundTripper
	url  string
}

func (t *geminiTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.url[len("http://"):]
	if t.base != nil {
		return t.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestGeminiEmbedSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		var req geminiBatchEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if len(req.Requests) != 1 {
			t.Fatalf("expected 1 request, got %d", len(req.Requests))
		}

		if req.Requests[0].Content.Parts[0].Text != "hello world" {
			t.Fatalf("unexpected text: %s", req.Requests[0].Content.Parts[0].Text)
		}

		resp := geminiBatchEmbedResponse{
			Embeddings: []geminiEmbeddingValue{
				{Values: []float64{0.1, 0.2, 0.3}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := &Gemini{
		apiKey: "test-key",
		client: &http.Client{
			Transport: &geminiTestTransport{base: srv.Client().Transport, url: srv.URL},
		},
	}

	result, err := g.Embed(context.Background(), "hello world", "text-embedding-004")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(result.Embedding) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(result.Embedding))
	}

	if result.Embedding[0] != float32(0.1) {
		t.Errorf("expected 0.1, got %f", result.Embedding[0])
	}
}

func TestGeminiEmbedBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req geminiBatchEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if len(req.Requests) != 3 {
			t.Fatalf("expected 3 requests, got %d", len(req.Requests))
		}

		resp := geminiBatchEmbedResponse{
			Embeddings: []geminiEmbeddingValue{
				{Values: []float64{0.1, 0.2}},
				{Values: []float64{0.3, 0.4}},
				{Values: []float64{0.5, 0.6}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := &Gemini{
		apiKey: "test-key",
		client: &http.Client{
			Transport: &geminiTestTransport{base: srv.Client().Transport, url: srv.URL},
		},
	}

	results, err := g.EmbedBatch(context.Background(), []string{"a", "b", "c"}, "text-embedding-004")
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[2].Embedding[0] != float32(0.5) {
		t.Errorf("expected 0.5, got %f", results[2].Embedding[0])
	}
}

func TestGeminiEmbeddingDimensions(t *testing.T) {
	g := &Gemini{}

	if d := g.EmbeddingDimensions("text-embedding-004"); d != 768 {
		t.Errorf("expected 768, got %d", d)
	}

	if d := g.EmbeddingDimensions("unknown-model"); d != 0 {
		t.Errorf("expected 0, got %d", d)
	}
}

func TestGeminiEmbedNoAPIKey(t *testing.T) {
	g := &Gemini{
		apiKey: "",
		client: &http.Client{},
	}

	_, err := g.Embed(context.Background(), "hello", "text-embedding-004")
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}

	if err.Error() != "GOOGLE_API_KEY not set" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGeminiEmbedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "invalid request"}}`))
	}))
	defer srv.Close()

	g := &Gemini{
		apiKey: "test-key",
		client: &http.Client{
			Transport: &geminiTestTransport{base: srv.Client().Transport, url: srv.URL},
		},
	}

	_, err := g.Embed(context.Background(), "hello", "text-embedding-004")
	if err == nil {
		t.Fatal("expected error on API error response")
	}

	expected := `gemini embeddings error 400: {"error": {"message": "invalid request"}}`
	if err.Error() != expected {
		t.Errorf("unexpected error:\n  got:  %s\n  want: %s", err.Error(), expected)
	}
}
