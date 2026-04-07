package provider

import "testing"

func TestParseQwenStreamLine_Empty(t *testing.T) {
	events, err := parseQwenStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseQwenStreamLine_Assistant(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from Qwen!"}]}}`)
	events, err := parseQwenStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello from Qwen!" {
		t.Errorf("expected 'Hello from Qwen!', got %q", events[0].Content)
	}
}

func TestParseQwenStreamLine_ToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"read_file","input":{"path":"go.mod"}}]}}`)
	events, err := parseQwenStreamLine(line)
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
		t.Errorf("expected tool=read_file, got %s", events[0].ToolUse.Name)
	}
	if events[0].ToolUse.ID != "tu1" {
		t.Errorf("expected id=tu1, got %s", events[0].ToolUse.ID)
	}
}

func TestParseQwenStreamLine_Result(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn","usage":{"input_tokens":500,"output_tokens":200}}`)
	events, err := parseQwenStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (usage + done), got %d", len(events))
	}
	if events[0].Type != "usage" {
		t.Errorf("expected type=usage, got %s", events[0].Type)
	}
	if events[0].Usage.InputTokens != 500 {
		t.Errorf("expected 500 input tokens, got %d", events[0].Usage.InputTokens)
	}
	if events[0].Usage.OutputTokens != 200 {
		t.Errorf("expected 200 output tokens, got %d", events[0].Usage.OutputTokens)
	}
	if events[1].Type != "done" {
		t.Errorf("expected type=done, got %s", events[1].Type)
	}
}

func TestParseQwenStreamLine_ResultError(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"error","is_error":true,"result":"rate limited"}`)
	events, err := parseQwenStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "rate limited" {
		t.Errorf("expected 'rate limited', got %q", events[0].Error)
	}
}

func TestParseQwenStreamLine_System(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","session_id":"qwen-sess-abc"}`)
	events, err := parseQwenStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "session_id" {
		t.Errorf("expected type=session_id, got %s", events[0].Type)
	}
	if events[0].SessionID != "qwen-sess-abc" {
		t.Errorf("expected qwen-sess-abc, got %q", events[0].SessionID)
	}
}

func TestParseQwenStreamLine_Error(t *testing.T) {
	line := []byte(`{"type":"error","error":{"message":"API error"}}`)
	events, err := parseQwenStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "API error" {
		t.Errorf("expected 'API error', got %q", events[0].Error)
	}
}

func TestParseQwenStreamLine_UnknownType(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event"}`)
	events, err := parseQwenStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
}

func TestQwenAdapter_BuildArgs(t *testing.T) {
	a := NewQwenAdapter()
	args := a.BuildArgs("fix bug", "", "")
	if args[0] != "-o" || args[1] != "stream-json" {
		t.Errorf("expected -o stream-json, got %v", args[:2])
	}
	if args[2] != "fix bug" {
		t.Errorf("expected prompt at args[2], got %s", args[2])
	}
}

func TestQwenAdapter_BuildArgs_WithSystemPrompt(t *testing.T) {
	a := NewQwenAdapter()
	args := a.BuildArgs("fix bug", "You are helpful", "")
	found := false
	for i, arg := range args {
		if arg == "--system-prompt" && i+1 < len(args) && args[i+1] == "You are helpful" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --system-prompt in args: %v", args)
	}
}
