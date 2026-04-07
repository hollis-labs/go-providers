package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbed(t *testing.T) {
	t.Run("single text embedding", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("unexpected auth header: %s", got)
			}

			var req openaiEmbeddingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if len(req.Input) != 1 {
				t.Fatalf("expected 1 input, got %d", len(req.Input))
			}
			if req.Input[0] != "hello world" {
				t.Errorf("unexpected input: %s", req.Input[0])
			}

			resp := openaiEmbeddingResponse{}
			resp.Data = []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.1, 0.2, 0.3}, Index: 0},
			}
			resp.Usage.PromptTokens = 2
			resp.Usage.TotalTokens = 2

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		o := &OpenAI{apiKey: "test-key", client: srv.Client()}
		// Override the endpoint by using a custom transport that redirects.
		origURL := openaiEmbeddingsAPI
		// We need to point the client at the test server.
		// Use a custom round-tripper to rewrite the URL.
		o.client = &http.Client{
			Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL},
		}
		_ = origURL

		result, err := o.Embed(context.Background(), "hello world", "text-embedding-3-small")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Embedding) != 3 {
			t.Fatalf("expected 3 dimensions, got %d", len(result.Embedding))
		}
		if result.Embedding[0] != 0.1 {
			t.Errorf("expected first dim 0.1, got %f", result.Embedding[0])
		}
		if result.TokenCount != 2 {
			t.Errorf("expected token count 2, got %d", result.TokenCount)
		}
	})
}

func TestOpenAIEmbedBatch(t *testing.T) {
	t.Run("batch of 3 texts", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req openaiEmbeddingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if len(req.Input) != 3 {
				t.Fatalf("expected 3 inputs, got %d", len(req.Input))
			}

			// Return results out of order to test index-based reordering.
			resp := openaiEmbeddingResponse{}
			resp.Data = []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				{Embedding: []float32{0.3, 0.3}, Index: 2},
				{Embedding: []float32{0.1, 0.1}, Index: 0},
				{Embedding: []float32{0.2, 0.2}, Index: 1},
			}
			resp.Usage.PromptTokens = 9
			resp.Usage.TotalTokens = 9

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer srv.Close()

		o := &OpenAI{
			apiKey: "test-key",
			client: &http.Client{
				Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL},
			},
		}

		results, err := o.EmbedBatch(context.Background(), []string{"a", "b", "c"}, "text-embedding-3-small")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		// Verify ordering by index.
		if results[0].Embedding[0] != 0.1 {
			t.Errorf("result[0] expected 0.1, got %f", results[0].Embedding[0])
		}
		if results[1].Embedding[0] != 0.2 {
			t.Errorf("result[1] expected 0.2, got %f", results[1].Embedding[0])
		}
		if results[2].Embedding[0] != 0.3 {
			t.Errorf("result[2] expected 0.3, got %f", results[2].Embedding[0])
		}
		// Token count split evenly: 9 / 3 = 3
		if results[0].TokenCount != 3 {
			t.Errorf("expected token count 3, got %d", results[0].TokenCount)
		}
	})
}

func TestOpenAIEmbeddingDimensions(t *testing.T) {
	o := &OpenAI{}
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
		t.Run(tt.model, func(t *testing.T) {
			got := o.EmbeddingDimensions(tt.model)
			if got != tt.want {
				t.Errorf("EmbeddingDimensions(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestOpenAIEmbedNoAPIKey(t *testing.T) {
	o := &OpenAI{apiKey: "", client: &http.Client{}}
	_, err := o.Embed(context.Background(), "hello", "text-embedding-3-small")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if err.Error() != "OPENAI_API_KEY not set" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestOpenAIEmbedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"message": "rate limited"}}`))
	}))
	defer srv.Close()

	o := &OpenAI{
		apiKey: "test-key",
		client: &http.Client{
			Transport: &rewriteTransport{base: srv.Client().Transport, targetURL: srv.URL},
		},
	}

	_, err := o.Embed(context.Background(), "hello", "text-embedding-3-small")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	expected := `openai embeddings error 429: {"error": {"message": "rate limited"}}`
	if err.Error() != expected {
		t.Errorf("unexpected error: %s", err.Error())
	}
}

// rewriteTransport rewrites request URLs to point at a test server.
type rewriteTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = t.targetURL[len("http://"):]
	if t.base != nil {
		return t.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}
