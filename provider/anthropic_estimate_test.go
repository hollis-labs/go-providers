package provider

import (
	"context"
	"strings"
	"testing"
)

func TestComputeCacheablePrefixBytes_NoHints(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":"hi","cache_control":{"type":"ephemeral"}}]}`)
	got := computeCacheablePrefixBytes(payload, nil)
	if got != 0 {
		t.Errorf("expected 0 with nil hints, got %d", got)
	}
	got = computeCacheablePrefixBytes(payload, []CacheHint{})
	if got != 0 {
		t.Errorf("expected 0 with empty hints slice, got %d", got)
	}
}

func TestComputeCacheablePrefixBytes_NoMarkerInPayload(t *testing.T) {
	payload := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	hints := DefaultCacheStrategy()
	got := computeCacheablePrefixBytes(payload, hints)
	if got != 0 {
		t.Errorf("expected 0 when no cache_control marker present, got %d", got)
	}
}

func TestComputeCacheablePrefixBytes_ReturnsLastMarkerOffset(t *testing.T) {
	prefix := `{"system":[{"type":"text","text":"`
	systemBody := "this is a long system prompt"
	systemMarker := `","cache_control":{"type":"ephemeral"}}],"messages":[`
	messagesBody := `{"role":"user","content":"first"},{"role":"user","content":"second"}`
	tail := `]}`
	payload := []byte(prefix + systemBody + systemMarker + messagesBody + tail)

	hints := []CacheHint{{Position: "system", Index: 0}}
	got := computeCacheablePrefixBytes(payload, hints)

	// Marker offset must be > end of system body (cache_control comes after the
	// system text) and < start of messages array.
	systemEnd := len(prefix) + len(systemBody)
	messagesStart := systemEnd + len(systemMarker)
	if got <= systemEnd {
		t.Errorf("offset %d should be after system body end %d", got, systemEnd)
	}
	if got >= messagesStart {
		t.Errorf("offset %d should be before messages array start %d", got, messagesStart)
	}
}

func TestComputeCacheablePrefixBytes_IgnoresLiteralUserText(t *testing.T) {
	// User content contains the literal substring "cache_control" (e.g. a
	// JSON-shaped message echoing the field name) but the payload has no real
	// cache_control marker. The tightened "cache_control":{ pattern must not
	// match the bare token so the helper returns 0 instead of a false-positive
	// offset that would shrink the rate-budget estimate incorrectly.
	payload := []byte(`{"messages":[{"role":"user","content":"docs say cache_control is the field name to use, no marker here"}]}`)
	hints := DefaultCacheStrategy()
	got := computeCacheablePrefixBytes(payload, hints)
	if got != 0 {
		t.Errorf("expected 0 for payload with literal cache_control text but no real marker, got %d", got)
	}
}

func TestComputeCacheablePrefixBytes_PicksLastMarker(t *testing.T) {
	first := `{"a":1,"cache_control":{"type":"ephemeral"},`
	middle := `"b":2,`
	second := `"c":3,"cache_control":{"type":"ephemeral"}}`
	payload := []byte(first + middle + second)

	hints := DefaultCacheStrategy()
	got := computeCacheablePrefixBytes(payload, hints)

	firstMarkerEnd := len(first)
	if got < firstMarkerEnd {
		t.Errorf("offset %d should pick the LAST marker, not the first (which ends at %d)", got, firstMarkerEnd)
	}
}

// TestRateBudgetEstimate_CacheHintsReduceEstimate exercises the integration of
// computeCacheablePrefixBytes with the rate-budget pre-flight: a payload with
// cache_control markers should produce a smaller estimated-tokens value than
// the same-shape payload without markers.
func TestRateBudgetEstimate_CacheHintsReduceEstimate(t *testing.T) {
	// Simulate two payloads of identical size. One has cache_control markers
	// (roughly mid-payload), the other does not. The estimate-after-subtraction
	// should be smaller on the cached one.
	body := make([]byte, 8000)
	for i := range body {
		body[i] = 'x'
	}

	uncached := append([]byte(nil), body...)

	// Marker placed deep into the payload (7000/8000) to model the realistic
	// case where most of the request — system + tools + prior turns — sits
	// behind a cache breakpoint and only the trailing user message is new.
	cached := append([]byte(nil), body[:7000]...)
	cached = append(cached, []byte(`"cache_control":{"type":"ephemeral"}`)...)
	cached = append(cached, body[7000:]...)

	hints := DefaultCacheStrategy()

	uncachedEst := len(uncached) / 4
	cachedEst := len(cached)/4 - computeCacheablePrefixBytes(cached, hints)/4
	if cachedEst < 0 {
		cachedEst = 0
	}

	if cachedEst >= uncachedEst {
		t.Errorf("cached estimate %d should be smaller than uncached %d", cachedEst, uncachedEst)
	}
	// With a marker at 87.5% depth, the post-marker remainder should be < 1/4
	// of the uncached estimate.
	if cachedEst > uncachedEst/4 {
		t.Errorf("cached estimate %d should be markedly smaller (< 1/4) of uncached %d", cachedEst, uncachedEst)
	}
}

func TestEstimateCacheablePrefix_NoHints(t *testing.T) {
	a := &Anthropic{}
	in := ChatRequest{
		Model:        "claude-sonnet-4-20250514",
		SystemPrompt: "you are a helpful assistant",
		Messages:     []ChatMessage{{Role: "user", Content: "hi"}},
	}
	if got := a.EstimateCacheablePrefix(context.Background(), in); got != 0 {
		t.Errorf("expected 0 with no cache hints, got %d", got)
	}
}

func TestEstimateCacheablePrefix_WithHintsReturnsPositiveTokens(t *testing.T) {
	a := &Anthropic{}
	a.SetCacheHints(DefaultCacheStrategy())
	in := ChatRequest{
		Model:        "claude-sonnet-4-20250514",
		SystemPrompt: strings.Repeat("system context ", 200),
		Messages: []ChatMessage{
			{Role: "user", Content: "first turn"},
			{Role: "assistant", Content: "first response"},
			{Role: "user", Content: "second turn"},
		},
	}
	got := a.EstimateCacheablePrefix(context.Background(), in)
	if got <= 0 {
		t.Errorf("expected positive estimate with cache hints + system prompt, got %d", got)
	}
	// Sanity: estimate should be approximately bytes/4. With ~3KB of system text
	// the prefix should be in the high-hundreds-of-tokens range.
	if got < 100 {
		t.Errorf("estimate %d is implausibly small for a 200-repetition system prompt", got)
	}
}

func TestEstimateCacheablePrefix_TokensApproximateBytesOverFour(t *testing.T) {
	a := &Anthropic{}
	a.SetCacheHints(DefaultCacheStrategy())
	in := ChatRequest{
		Model:        "claude-sonnet-4-20250514",
		SystemPrompt: strings.Repeat("x", 4000),
		Messages:     []ChatMessage{{Role: "user", Content: "hi"}},
	}
	tokens := a.EstimateCacheablePrefix(context.Background(), in)
	if tokens <= 0 {
		t.Fatalf("expected positive estimate, got %d", tokens)
	}
	// Implementation contract: tokens = bytes/4 of the same payload
	// computeCacheablePrefixBytes would see. We don't recompute here, but we
	// guard the rough magnitude — for ~4KB of system content the estimate
	// should be O(1000) tokens, not single-digit and not way past the input.
	if tokens > 4096 {
		t.Errorf("estimate %d larger than input system bytes — heuristic broken", tokens)
	}
	if tokens < 500 {
		t.Errorf("estimate %d too small for 4000-byte system prompt", tokens)
	}
}
