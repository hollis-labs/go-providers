package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

type exampleProvider struct{}

func (exampleProvider) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func (exampleProvider) Complete(ctx context.Context, in ChatRequest) (string, error) {
	return "", nil
}

func (exampleProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{}
}

type exampleRewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt exampleRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = rt.target.Scheme
	req2.URL.Host = rt.target.Host
	req2.Host = rt.target.Host
	return rt.base.RoundTrip(req2)
}

func ExampleRegistry() {
	reg := NewRegistry()
	reg.Register("demo", exampleProvider{})

	_, ok := reg.Get("demo")
	fmt.Println(ok)
	// Output:
	// true
}

func ExampleAnthropic_StreamChat() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, "data: {}\n\n")
	}))
	defer srv.Close()

	target, _ := url.Parse(srv.URL)

	a := NewAnthropic()
	a.apiKey = "test-key"
	a.client = &http.Client{
		Transport: exampleRewriteTransport{
			target: target,
			base:   http.DefaultTransport,
		},
	}

	stream, err := a.StreamChat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Model:    "demo-model",
	})
	if err != nil {
		panic(err)
	}

	for ev := range stream {
		if ev.Type == "delta" {
			fmt.Print(strings.TrimSpace(ev.Content))
		}
		if ev.Type == "done" {
			break
		}
	}
	// Output:
	// Hello
}
