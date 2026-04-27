package provider

import (
	"encoding/json"
	"testing"
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
