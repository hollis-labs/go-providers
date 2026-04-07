package provider

import "testing"

func TestParseJunieStreamLine_Empty(t *testing.T) {
	events, err := parseJunieStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseJunieStreamLine_AssistantMessage(t *testing.T) {
	line := []byte(`{"type":"message","role":"assistant","content":"","delta":"Hello from Junie!"}`)
	events, err := parseJunieStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello from Junie!" {
		t.Errorf("expected 'Hello from Junie!', got %q", events[0].Content)
	}
}

func TestParseJunieStreamLine_ToolUse(t *testing.T) {
	line := []byte(`{"type":"tool_use","tool_name":"edit","tool_id":"t1","input":{"file":"main.go"}}`)
	events, err := parseJunieStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "tool_use" {
		t.Errorf("expected type=tool_use, got %s", events[0].Type)
	}
}

func TestParseJunieStreamLine_Session(t *testing.T) {
	line := []byte(`{"type":"session","session_id":"junie-abc-123"}`)
	events, err := parseJunieStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "session_id" {
		t.Errorf("expected type=session_id, got %s", events[0].Type)
	}
	if events[0].SessionID != "junie-abc-123" {
		t.Errorf("expected junie-abc-123, got %q", events[0].SessionID)
	}
}

func TestParseJunieStreamLine_Result(t *testing.T) {
	line := []byte(`{"type":"result","result":"ok","usage":{"input_tokens":200,"output_tokens":75}}`)
	events, err := parseJunieStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (usage + done), got %d", len(events))
	}
	if events[0].Type != "usage" {
		t.Errorf("expected type=usage, got %s", events[0].Type)
	}
	if events[1].Type != "done" {
		t.Errorf("expected type=done, got %s", events[1].Type)
	}
}

func TestParseJunieStreamLine_Error(t *testing.T) {
	line := []byte(`{"type":"error","error":"auth failed"}`)
	events, err := parseJunieStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
}

func TestParseJunieStreamLine_Banner(t *testing.T) {
	line := []byte("       ///////               ///")
	events, err := parseJunieStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for banner line, got %d", len(events))
	}
}

func TestJunieAdapter_BuildArgs(t *testing.T) {
	a := NewJunieAdapter()
	args := a.BuildArgs("fix tests", "", "")
	if args[0] != "--output-format" || args[1] != "json" {
		t.Errorf("expected --output-format json, got %v", args[:2])
	}
}

func TestJunieAdapter_BuildArgs_Resume(t *testing.T) {
	a := NewJunieAdapter()
	args := a.BuildArgs("continue", "", "sess-xyz")
	found := false
	for i, arg := range args {
		if arg == "--session-id" && i+1 < len(args) && args[i+1] == "sess-xyz" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --session-id sess-xyz in args: %v", args)
	}
}
