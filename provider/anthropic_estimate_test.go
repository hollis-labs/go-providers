package provider

import (
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
