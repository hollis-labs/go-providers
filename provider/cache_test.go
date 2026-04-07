package provider

import (
	"testing"
)

func TestDefaultCacheStrategy_HasExpectedHints(t *testing.T) {
	hints := DefaultCacheStrategy()

	if len(hints) != 4 {
		t.Fatalf("expected 4 hints, got %d", len(hints))
	}

	// Verify we have exactly one "system" hint.
	systemCount := 0
	for _, h := range hints {
		if h.Position == "system" {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("expected 1 system hint, got %d", systemCount)
	}

	// Verify we have exactly one "tools" hint.
	toolsCount := 0
	for _, h := range hints {
		if h.Position == "tools" {
			toolsCount++
		}
	}
	if toolsCount != 1 {
		t.Errorf("expected 1 tools hint, got %d", toolsCount)
	}

	// Verify we have exactly 2 "recent_message" hints with indices 0 and 1.
	msgHints := make(map[int]bool)
	for _, h := range hints {
		if h.Position == "recent_message" {
			msgHints[h.Index] = true
		}
	}
	if len(msgHints) != 2 {
		t.Errorf("expected 2 recent_message hints, got %d", len(msgHints))
	}
	if !msgHints[0] {
		t.Error("expected recent_message hint with Index=0")
	}
	if !msgHints[1] {
		t.Error("expected recent_message hint with Index=1")
	}
}

func TestCacheHints_AppliedToAnthropicProvider(t *testing.T) {
	a := NewAnthropic()

	t.Run("with default cache strategy", func(t *testing.T) {
		a.SetCacheHints(DefaultCacheStrategy())

		// System blocks should have cache_control.
		sysBlocks := a.buildSystemBlocks("You are helpful.")
		if len(sysBlocks) != 1 {
			t.Fatalf("expected 1 system block, got %d", len(sysBlocks))
		}
		cc, ok := sysBlocks[0]["cache_control"].(map[string]string)
		if !ok {
			t.Fatal("system block should have cache_control")
		}
		if cc["type"] != "ephemeral" {
			t.Errorf("expected ephemeral, got %v", cc["type"])
		}

		// Tools: last tool should have cache_control.
		tools := []ToolDefinition{
			{Name: "a", Description: "tool a", InputSchema: map[string]any{"type": "object"}},
			{Name: "b", Description: "tool b", InputSchema: map[string]any{"type": "object"}},
		}
		builtTools := a.buildToolsWithCacheControl(tools)
		if len(builtTools) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(builtTools))
		}
		first := builtTools[0].(map[string]any)
		if _, ok := first["cache_control"]; ok {
			t.Error("first tool should NOT have cache_control")
		}
		last := builtTools[1].(map[string]any)
		if _, ok := last["cache_control"]; !ok {
			t.Error("last tool should have cache_control")
		}

		// Messages: last 2 user messages should have cache_control.
		messages := []ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi"},
			{Role: "user", Content: "How are you?"},
			{Role: "assistant", Content: "Good"},
			{Role: "user", Content: "Tell me something"},
		}
		marshaled := a.marshalMessages(messages)

		// First user (index 0) should NOT be cached.
		firstMsg := marshaled[0].(map[string]any)
		if _, ok := firstMsg["content"].(string); !ok {
			t.Errorf("first user message should be plain string, got %T", firstMsg["content"])
		}

		// Second user (index 2) should be cached.
		secondMsg := marshaled[2].(map[string]any)
		blocks, ok := secondMsg["content"].([]map[string]any)
		if !ok {
			t.Fatalf("second user message should have content blocks, got %T", secondMsg["content"])
		}
		if _, ok := blocks[0]["cache_control"]; !ok {
			t.Error("second-to-last user message should have cache_control")
		}

		// Third user (index 4) should be cached.
		thirdMsg := marshaled[4].(map[string]any)
		blocks, ok = thirdMsg["content"].([]map[string]any)
		if !ok {
			t.Fatalf("last user message should have content blocks, got %T", thirdMsg["content"])
		}
		if _, ok := blocks[0]["cache_control"]; !ok {
			t.Error("last user message should have cache_control")
		}
	})

	t.Run("with no cache hints", func(t *testing.T) {
		a.SetCacheHints(nil)

		// System blocks should NOT have cache_control.
		sysBlocks := a.buildSystemBlocks("You are helpful.")
		if _, ok := sysBlocks[0]["cache_control"]; ok {
			t.Error("system block should NOT have cache_control when no hints set")
		}

		// Tools: no tool should have cache_control.
		tools := []ToolDefinition{
			{Name: "a", Description: "tool a", InputSchema: map[string]any{"type": "object"}},
		}
		builtTools := a.buildToolsWithCacheControl(tools)
		only := builtTools[0].(map[string]any)
		if _, ok := only["cache_control"]; ok {
			t.Error("tool should NOT have cache_control when no hints set")
		}

		// Messages: no user messages should have cache_control.
		messages := []ChatMessage{
			{Role: "user", Content: "Hello"},
		}
		marshaled := a.marshalMessages(messages)
		msg := marshaled[0].(map[string]any)
		if _, ok := msg["content"].(string); !ok {
			t.Errorf("user message should be plain string when no hints, got %T", msg["content"])
		}
	})

	t.Run("with partial hints — system only", func(t *testing.T) {
		a.SetCacheHints([]CacheHint{
			{Position: "system", Index: 0},
		})

		// System should be cached.
		sysBlocks := a.buildSystemBlocks("Test")
		if _, ok := sysBlocks[0]["cache_control"]; !ok {
			t.Error("system block should have cache_control with system hint")
		}

		// Tools should NOT be cached.
		tools := []ToolDefinition{
			{Name: "a", Description: "tool a", InputSchema: map[string]any{"type": "object"}},
		}
		builtTools := a.buildToolsWithCacheControl(tools)
		only := builtTools[0].(map[string]any)
		if _, ok := only["cache_control"]; ok {
			t.Error("tool should NOT have cache_control without tools hint")
		}

		// Messages should NOT be cached (no recent_message hints).
		messages := []ChatMessage{
			{Role: "user", Content: "Hello"},
		}
		marshaled := a.marshalMessages(messages)
		msg := marshaled[0].(map[string]any)
		if _, ok := msg["content"].(string); !ok {
			t.Errorf("user message should be plain string without recent_message hints, got %T", msg["content"])
		}
	})

	t.Run("implements CacheableProvider interface", func(t *testing.T) {
		var _ CacheableProvider = a
	})
}
