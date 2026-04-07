package provider

import "testing"

func TestParseKiroStreamLine_Empty(t *testing.T) {
	events, err := parseKiroStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseKiroStreamLine_JSONMessage(t *testing.T) {
	line := []byte(`{"type":"message","content":"Hello from Kiro!"}`)
	events, err := parseKiroStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello from Kiro!" {
		t.Errorf("expected 'Hello from Kiro!', got %q", events[0].Content)
	}
}

func TestParseKiroStreamLine_JSONDone(t *testing.T) {
	line := []byte(`{"type":"done"}`)
	events, err := parseKiroStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "done" {
		t.Errorf("expected type=done, got %s", events[0].Type)
	}
}

func TestParseKiroStreamLine_JSONError(t *testing.T) {
	line := []byte(`{"type":"error","error":"auth failed"}`)
	events, err := parseKiroStreamLine(line)
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

func TestParseKiroStreamLine_PlainText(t *testing.T) {
	line := []byte("Some output from kiro")
	events, err := parseKiroStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
}

func TestParseKiroStreamLine_ErrorPattern(t *testing.T) {
	line := []byte("Error: connection refused")
	events, err := parseKiroStreamLine(line)
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

func TestParseKiroStreamLine_BoxDrawing(t *testing.T) {
	for _, line := range []string{"╭────────────╮", "│ chat       │", "╰────────────╯"} {
		events, err := parseKiroStreamLine([]byte(line))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events for %q, got %d", line, len(events))
		}
	}
}

func TestKiroAdapter_BuildArgs(t *testing.T) {
	a := NewKiroAdapter()
	args := a.BuildArgs("fix bug", "", "")
	if args[0] != "chat" {
		t.Errorf("expected 'chat', got %s", args[0])
	}
	if args[1] != "--no-interactive" {
		t.Errorf("expected --no-interactive, got %s", args[1])
	}
	// Last arg should be the prompt.
	if args[len(args)-1] != "fix bug" {
		t.Errorf("expected prompt as last arg, got %s", args[len(args)-1])
	}
}

func TestKiroAdapter_BuildArgs_Resume(t *testing.T) {
	a := NewKiroAdapter()
	args := a.BuildArgs("continue", "", "sess-123")
	found := false
	for _, arg := range args {
		if arg == "--resume" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --resume in args: %v", args)
	}
}
