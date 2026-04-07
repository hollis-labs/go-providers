package provider

import "testing"

func TestParseGeminiStreamLine_Empty(t *testing.T) {
	events, err := parseGeminiStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseGeminiStreamLine_Init(t *testing.T) {
	line := []byte(`{"type":"init","session_id":"gem-sess-001"}`)
	events, err := parseGeminiStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "session_id" {
		t.Errorf("expected type=session_id, got %s", events[0].Type)
	}
	if events[0].SessionID != "gem-sess-001" {
		t.Errorf("expected SessionID=gem-sess-001, got %s", events[0].SessionID)
	}
}

func TestParseGeminiStreamLine_AssistantMessage(t *testing.T) {
	line := []byte(`{"type":"message","role":"assistant","content":"","delta":"Hello from Gemini!"}`)
	events, err := parseGeminiStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello from Gemini!" {
		t.Errorf("expected 'Hello from Gemini!', got %q", events[0].Content)
	}
}

func TestParseGeminiStreamLine_ToolUse(t *testing.T) {
	line := []byte(`{"type":"tool_use","tool_name":"read_file","args":{"path":"/tmp/test.txt"}}`)
	events, err := parseGeminiStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "tool_use" {
		t.Errorf("expected type=tool_use, got %s", events[0].Type)
	}
	if events[0].ToolUse.Name != "read_file" {
		t.Errorf("expected tool name=read_file, got %s", events[0].ToolUse.Name)
	}
}

func TestParseGeminiStreamLine_Result(t *testing.T) {
	line := []byte(`{"type":"result","response":"Done.","stats":{"tokens_input":50,"tokens_output":10}}`)
	events, err := parseGeminiStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (usage + done), got %d", len(events))
	}
	if events[0].Type != "usage" {
		t.Errorf("expected type=usage, got %s", events[0].Type)
	}
	if events[0].Usage.InputTokens != 50 {
		t.Errorf("expected input_tokens=50, got %d", events[0].Usage.InputTokens)
	}
	if events[1].Type != "done" {
		t.Errorf("expected type=done, got %s", events[1].Type)
	}
}

func TestParseGeminiStreamLine_ResultError(t *testing.T) {
	line := []byte(`{"type":"result","error":"quota exceeded"}`)
	events, err := parseGeminiStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "quota exceeded" {
		t.Errorf("expected 'quota exceeded', got %q", events[0].Error)
	}
}

func TestGeminiAdapter_BuildArgs(t *testing.T) {
	a := NewGeminiAdapter()

	// First turn — no session ID.
	args := a.BuildArgs("what is go", "", "")
	if args[0] != "-p" || args[1] != "what is go" {
		t.Errorf("expected -p prompt, got %v", args[:2])
	}

	// Resume turn.
	args = a.BuildArgs("continue", "", "gem-sess-001")
	if args[0] != "--resume" || args[1] != "gem-sess-001" {
		t.Errorf("expected --resume gem-sess-001, got %v", args[:2])
	}
}
