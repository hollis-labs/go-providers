package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestBuildSystemFromRequest_SlotsWithCacheControl(t *testing.T) {
	a := NewAnthropic()
	in := ChatRequest{
		SlotBlocks: []SlotBlock{
			{Name: "system", Content: "stable system rules", Changed: false},
			{Name: "memory", Content: "fresh memory recall", Changed: true},
			{Name: "agent", Content: "stable agent profile", Changed: false},
			{Name: "conversation", Content: "", Changed: true}, // empty: skipped
		},
	}
	blocks := a.buildSystemFromRequest(in)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 non-empty blocks, got %d", len(blocks))
	}
	// Block 0: unchanged → cache_control present.
	if _, ok := blocks[0]["cache_control"]; !ok {
		t.Errorf("block 0 (unchanged) missing cache_control")
	}
	// Block 1: changed → cache_control absent.
	if _, ok := blocks[1]["cache_control"]; ok {
		t.Errorf("block 1 (changed) should not have cache_control")
	}
	// Block 2: unchanged → cache_control present.
	if _, ok := blocks[2]["cache_control"]; !ok {
		t.Errorf("block 2 (unchanged) missing cache_control")
	}
}

func TestBuildSystemFromRequest_FallsBackToSystemPromptWhenNoSlots(t *testing.T) {
	a := NewAnthropic()
	a.SetCacheHints([]CacheHint{{Position: "system"}})
	in := ChatRequest{SystemPrompt: "legacy flat prompt"}
	blocks := a.buildSystemFromRequest(in)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0]["text"] != "legacy flat prompt" {
		t.Errorf("unexpected text: %v", blocks[0]["text"])
	}
	if _, ok := blocks[0]["cache_control"]; !ok {
		t.Errorf("expected cache_control from CacheHint")
	}
}

func TestChatRequest_EffectiveSystemPrompt(t *testing.T) {
	t.Run("no slots returns SystemPrompt verbatim", func(t *testing.T) {
		got := ChatRequest{SystemPrompt: "hello"}.EffectiveSystemPrompt()
		if got != "hello" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("slots concatenate after SystemPrompt", func(t *testing.T) {
		in := ChatRequest{
			SystemPrompt: "base",
			SlotBlocks: []SlotBlock{
				{Name: "memory", Content: "mem"},
				{Name: "agent", Content: ""}, // skipped
				{Name: "rules", Content: "rules"},
			},
		}
		got := in.EffectiveSystemPrompt()
		want := "base\n\nmem\n\nrules"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})
	t.Run("slots only with no SystemPrompt", func(t *testing.T) {
		in := ChatRequest{
			SlotBlocks: []SlotBlock{
				{Name: "system", Content: "rules"},
				{Name: "memory", Content: ""},    // skipped
				{Name: "agent", Content: "agent"},
			},
		}
		got := in.EffectiveSystemPrompt()
		want := "rules\n\nagent"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})
	t.Run("all slot contents empty returns empty string", func(t *testing.T) {
		in := ChatRequest{
			SlotBlocks: []SlotBlock{
				{Name: "a", Content: ""},
				{Name: "b", Content: ""},
			},
		}
		got := in.EffectiveSystemPrompt()
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestBuildSystemBlocks(t *testing.T) {
	t.Run("empty prompt returns nil", func(t *testing.T) {
		blocks := buildSystemBlocks("")
		if blocks != nil {
			t.Fatalf("expected nil, got %v", blocks)
		}
	})

	t.Run("non-empty prompt has cache_control", func(t *testing.T) {
		blocks := buildSystemBlocks("You are a helpful assistant.")
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		block := blocks[0]
		if block["type"] != "text" {
			t.Errorf("expected type=text, got %v", block["type"])
		}
		if block["text"] != "You are a helpful assistant." {
			t.Errorf("unexpected text: %v", block["text"])
		}
		cc, ok := block["cache_control"].(map[string]string)
		if !ok {
			t.Fatalf("cache_control missing or wrong type: %v", block["cache_control"])
		}
		if cc["type"] != "ephemeral" {
			t.Errorf("expected ephemeral, got %v", cc["type"])
		}
	})
}

func boolPtr(b bool) *bool { return &b }

func TestBuildToolsWithCacheControl(t *testing.T) {
	t.Run("empty tools returns nil", func(t *testing.T) {
		result := buildToolsWithCacheControl(nil)
		if result != nil {
			t.Fatalf("expected nil, got %v", result)
		}
	})

	t.Run("last tool gets cache_control", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "First tool", InputSchema: map[string]any{"type": "object"}},
			{Name: "tool_b", Description: "Second tool", InputSchema: map[string]any{"type": "object"}},
		}
		result := buildToolsWithCacheControl(tools)
		if len(result) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(result))
		}

		// First tool should NOT have cache_control.
		first := result[0].(map[string]any)
		if _, ok := first["cache_control"]; ok {
			t.Error("first tool should not have cache_control")
		}

		// Last tool should have cache_control.
		last := result[1].(map[string]any)
		cc, ok := last["cache_control"].(map[string]string)
		if !ok {
			t.Fatalf("last tool missing cache_control: %v", last)
		}
		if cc["type"] != "ephemeral" {
			t.Errorf("expected ephemeral, got %v", cc["type"])
		}
	})

	t.Run("single tool gets cache_control", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "Only tool", InputSchema: map[string]any{"type": "object"}},
		}
		result := buildToolsWithCacheControl(tools)
		only := result[0].(map[string]any)
		if _, ok := only["cache_control"]; !ok {
			t.Error("single tool should have cache_control")
		}
	})

	t.Run("strict true by default when Strict is nil", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "Tool with nil Strict", InputSchema: map[string]any{"type": "object"}},
		}
		result := buildToolsWithCacheControl(tools)
		entry := result[0].(map[string]any)
		v, ok := entry["strict"]
		if !ok {
			t.Fatal("expected strict key present when Strict is nil (default-on)")
		}
		if v != true {
			t.Errorf("expected strict=true, got %v", v)
		}
	})

	t.Run("strict true when Strict is explicitly set to true", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "Tool with Strict=true", InputSchema: map[string]any{"type": "object"}, Strict: boolPtr(true)},
		}
		result := buildToolsWithCacheControl(tools)
		entry := result[0].(map[string]any)
		v, ok := entry["strict"]
		if !ok {
			t.Fatal("expected strict key present when Strict=true")
		}
		if v != true {
			t.Errorf("expected strict=true, got %v", v)
		}
	})

	t.Run("strict absent when Strict is explicitly set to false (opt-out)", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "tool_a", Description: "Tool with Strict=false (opt-out)", InputSchema: map[string]any{"type": "object"}, Strict: boolPtr(false)},
		}
		result := buildToolsWithCacheControl(tools)
		entry := result[0].(map[string]any)
		if v, ok := entry["strict"]; ok {
			t.Errorf("expected strict absent for opt-out tool, got %v", v)
		}
	})
}

func TestMarshalMessagesCacheControl(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "Great!"},
		{Role: "user", Content: "Tell me a joke"},
	}

	result := marshalMessages(messages)

	// Last 2 user messages are at indices 2 and 4.
	// Index 0 (first user) should be plain string.
	first := result[0].(map[string]any)
	if _, ok := first["content"].(string); !ok {
		t.Errorf("first user message should have plain string content, got %T", first["content"])
	}

	// Index 2 (second user) should have cache_control.
	second := result[2].(map[string]any)
	blocks, ok := second["content"].([]map[string]any)
	if !ok {
		t.Fatalf("expected content blocks at index 2, got %T", second["content"])
	}
	if _, ok := blocks[0]["cache_control"]; !ok {
		t.Error("second-to-last user message should have cache_control")
	}

	// Index 4 (last user) should have cache_control.
	last := result[4].(map[string]any)
	blocks, ok = last["content"].([]map[string]any)
	if !ok {
		t.Fatalf("expected content blocks at index 4, got %T", last["content"])
	}
	if _, ok := blocks[0]["cache_control"]; !ok {
		t.Error("last user message should have cache_control")
	}

	// Assistant messages should not have cache_control.
	assistant := result[1].(map[string]any)
	if _, ok := assistant["content"].(string); !ok {
		t.Errorf("assistant message should have plain string content, got %T", assistant["content"])
	}
}

func TestAnthropicRequestJSON(t *testing.T) {
	// Verify the anthropicRequest marshals correctly with structured system blocks.
	req := anthropicRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		System:    buildSystemBlocks("test system"),
		Messages:  marshalMessages([]ChatMessage{{Role: "user", Content: "hi"}}),
		Stream:    true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// System should be an array, not a string.
	system, ok := parsed["system"].([]any)
	if !ok {
		t.Fatalf("system should be array, got %T", parsed["system"])
	}
	if len(system) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(system))
	}
	block := system[0].(map[string]any)
	if block["type"] != "text" {
		t.Errorf("expected type=text, got %v", block["type"])
	}
	cc := block["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" {
		t.Errorf("expected ephemeral cache_control")
	}
}

// ---------------------------------------------------------------------------
// F3 (CW-20260420-0023) — interleaved thinking tests
// ---------------------------------------------------------------------------

// TestModelSupportsInterleavedThinking verifies the feature-detect function.
func TestModelSupportsInterleavedThinking(t *testing.T) {
	supported := []string{
		"claude-opus-4-20250514",
		"claude-sonnet-4-20250514",
		"claude-opus-4-5-20250514",
		"claude-sonnet-4-5-20250514",
	}
	for _, m := range supported {
		if !modelSupportsInterleavedThinking(m) {
			t.Errorf("expected %q to support interleaved thinking", m)
		}
	}

	unsupported := []string{
		"claude-3-opus-20240229",
		"claude-3-sonnet-20240229",
		"gpt-4o",
		"gemini-1.5-pro",
		"claude-2.1",
		"",
	}
	for _, m := range unsupported {
		if modelSupportsInterleavedThinking(m) {
			t.Errorf("expected %q NOT to support interleaved thinking", m)
		}
	}
}

// TestReasoningConfigContext verifies that WithReasoningConfig / ReasoningConfigFromContext
// round-trip correctly via context transport.
func TestReasoningConfigContext(t *testing.T) {
	cfg := ReasoningConfig{
		Enabled:      true,
		BudgetTokens: 8000,
		BetasHeader:  InterleavedThinkingBetaHeader,
	}
	ctx := WithReasoningConfig(context.Background(), cfg)
	got := ReasoningConfigFromContext(ctx)
	if got.Enabled != cfg.Enabled {
		t.Errorf("Enabled: got %v want %v", got.Enabled, cfg.Enabled)
	}
	if got.BudgetTokens != cfg.BudgetTokens {
		t.Errorf("BudgetTokens: got %d want %d", got.BudgetTokens, cfg.BudgetTokens)
	}
	if got.BetasHeader != cfg.BetasHeader {
		t.Errorf("BetasHeader: got %q want %q", got.BetasHeader, cfg.BetasHeader)
	}

	// Zero context returns zero value.
	zero := ReasoningConfigFromContext(context.Background())
	if zero.Enabled {
		t.Error("zero context should return Enabled=false")
	}
}

// TestHandleSSEData_ThinkingDelta verifies that thinking_delta events are
// accumulated and emitted as EventThinking on content_block_stop.
func TestHandleSSEData_ThinkingDelta(t *testing.T) {
	a := NewAnthropic()
	ch := make(chan StreamEvent, 16)
	var currentToolUse *toolUseAccumulator
	var currentThinking *thinkingAccumulator
	var blockIdx int

	// Start a thinking block.
	a.handleSSEData("content_block_start",
		`{"index":0,"content_block":{"type":"thinking","signature":""}}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, true)
	if currentThinking == nil {
		t.Fatal("expected thinkingAccumulator to be set after content_block_start")
	}

	// Send thinking_delta events.
	a.handleSSEData("content_block_delta",
		`{"index":0,"delta":{"type":"thinking_delta","thinking":"Let me think about"}}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, true)
	a.handleSSEData("content_block_delta",
		`{"index":0,"delta":{"type":"thinking_delta","thinking":" this carefully."}}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, true)

	// Send signature_delta.
	a.handleSSEData("content_block_delta",
		`{"index":0,"delta":{"type":"signature_delta","signature":"sig-abc-123"}}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, true)

	// Stop the block — should emit EventThinking.
	a.handleSSEData("content_block_stop",
		`{"index":0}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, true)

	close(ch)

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 EventThinking event, got %d: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Type != EventThinking {
		t.Errorf("Type: got %q want %q", ev.Type, EventThinking)
	}
	if ev.ThinkingBlock == nil {
		t.Fatal("ThinkingBlock must not be nil")
	}
	if ev.ThinkingBlock.Thinking != "Let me think about this carefully." {
		t.Errorf("Thinking: got %q", ev.ThinkingBlock.Thinking)
	}
	if ev.ThinkingBlock.Signature != "sig-abc-123" {
		t.Errorf("Signature: got %q", ev.ThinkingBlock.Signature)
	}
}

// TestHandleSSEData_ThinkingDisabled verifies that thinking_delta events are
// ignored when interleavedThinking=false (non-supporting models).
func TestHandleSSEData_ThinkingDisabled(t *testing.T) {
	a := NewAnthropic()
	ch := make(chan StreamEvent, 16)
	var currentToolUse *toolUseAccumulator
	var currentThinking *thinkingAccumulator
	var blockIdx int

	// Even with type=thinking in the block start, no accumulator is set.
	a.handleSSEData("content_block_start",
		`{"index":0,"content_block":{"type":"thinking"}}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, false)
	if currentThinking != nil {
		t.Error("expected thinkingAccumulator to be nil when interleavedThinking=false")
	}

	// thinking_delta should not emit anything.
	a.handleSSEData("content_block_delta",
		`{"index":0,"delta":{"type":"thinking_delta","thinking":"ignored"}}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, false)
	a.handleSSEData("content_block_stop",
		`{"index":0}`,
		ch, &currentToolUse, &currentThinking, &blockIdx, false)

	close(ch)
	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 0 {
		t.Errorf("expected no events, got %d: %+v", len(events), events)
	}
}

// TestContentBlockToMap_ThinkingBlock verifies that thinking ContentBlocks are
// serialized with "thinking" and "signature" keys (not "text"), preserving
// the signed block verbatim for round-trip.
func TestContentBlockToMap_ThinkingBlock(t *testing.T) {
	b := ContentBlock{
		Type:      "thinking",
		Text:      "I reasoned about this.",
		Signature: "sig-xyz-456",
	}
	m := contentBlockToMap(b)
	if m["type"] != "thinking" {
		t.Errorf("type: got %v", m["type"])
	}
	if m["thinking"] != "I reasoned about this." {
		t.Errorf("thinking: got %v", m["thinking"])
	}
	if m["signature"] != "sig-xyz-456" {
		t.Errorf("signature: got %v", m["signature"])
	}
	// Must NOT include "text" key (Anthropic rejects unknown fields on thinking blocks).
	if _, ok := m["text"]; ok {
		t.Error("thinking block must not have 'text' key")
	}
}

// TestThinkingBlockRoundTrip verifies that a thinking ContentBlock serialized
// via marshalMessagesWithCacheCount produces the correct JSON shape.
func TestThinkingBlockRoundTrip(t *testing.T) {
	messages := []ChatMessage{
		{
			Role: "assistant",
			ContentBlocks: []ContentBlock{
				{Type: "thinking", Text: "reasoning here", Signature: "sig-round-trip"},
				{Type: "text", Text: "Here is my answer."},
			},
		},
	}
	result := marshalMessagesWithCacheCount(messages, 0)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	msg, ok := result[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result[0])
	}
	content, ok := msg["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content should be []map, got %T", msg["content"])
	}
	if len(content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content))
	}

	// Block 0: thinking
	tb := content[0]
	if tb["type"] != "thinking" {
		t.Errorf("block 0 type: got %v", tb["type"])
	}
	if tb["thinking"] != "reasoning here" {
		t.Errorf("block 0 thinking: got %v", tb["thinking"])
	}
	if tb["signature"] != "sig-round-trip" {
		t.Errorf("block 0 signature: got %v", tb["signature"])
	}
	if _, hasText := tb["text"]; hasText {
		t.Error("block 0 must not have 'text' key")
	}

	// Block 1: text
	textBlock := content[1]
	if textBlock["type"] != "text" {
		t.Errorf("block 1 type: got %v", textBlock["type"])
	}
}

// TestAnthropic_StreamChat_ReturnsRequestExceedsBudgetSentinel verifies that
// when the estimated request size exceeds the per-minute rate-limit window,
// StreamChat returns ErrRequestExceedsRateBudget immediately without waiting.
// This is the signal nanite's compaction pipeline uses to trim history
// instead of repeating 58s pacing waits until the outer context deadline.
func TestAnthropic_StreamChat_ReturnsRequestExceedsBudgetSentinel(t *testing.T) {
	a := NewAnthropic()
	a.SetAPIKey("test-key")
	// Force an absurdly small window so any request exceeds it.
	a.RateTracker.UpdateLimit(10)

	in := ChatRequest{
		Model:        "claude-sonnet-4-20250514",
		SystemPrompt: "you are a helpful assistant used in a test",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello, this is a test message that will clearly exceed a 10-token budget when estimated"},
		},
	}

	start := time.Now()
	_, err := a.StreamChat(context.Background(), in)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrRequestExceedsRateBudget) {
		t.Fatalf("expected ErrRequestExceedsRateBudget, got %v", err)
	}
	// Must fire BEFORE any wait — generous bound, just ensuring we didn't
	// block on WaitTime (which would be tens of seconds).
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected immediate return, took %s", elapsed)
	}
}
