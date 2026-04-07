package provider

import "testing"

func TestStaticModelSelector_fallback(t *testing.T) {
	sel := NewStaticModelSelector("anthropic", "claude-haiku")

	prov, model, ok := sel.ModelForOperation(OpSummarization)
	if !ok {
		t.Fatal("expected ok from fallback")
	}
	if prov != "anthropic" || model != "claude-haiku" {
		t.Errorf("expected anthropic/claude-haiku, got %s/%s", prov, model)
	}
}

func TestStaticModelSelector_explicit(t *testing.T) {
	sel := NewStaticModelSelector("anthropic", "claude-sonnet")
	sel.SetOperation(OpSummarization, "anthropic", "claude-haiku")

	// Summarization should use haiku.
	prov, model, ok := sel.ModelForOperation(OpSummarization)
	if !ok {
		t.Fatal("expected ok")
	}
	if model != "claude-haiku" {
		t.Errorf("expected claude-haiku, got %s", model)
	}
	if prov != "anthropic" {
		t.Errorf("expected anthropic, got %s", prov)
	}

	// Chat should fall back.
	prov, model, ok = sel.ModelForOperation(OpChat)
	if !ok {
		t.Fatal("expected ok from fallback")
	}
	if model != "claude-sonnet" {
		t.Errorf("expected claude-sonnet fallback, got %s", model)
	}
}

func TestStaticModelSelector_noConfig(t *testing.T) {
	sel := NewStaticModelSelector("", "")
	_, _, ok := sel.ModelForOperation(OpSummarization)
	if ok {
		t.Error("expected not ok when no config")
	}
}
