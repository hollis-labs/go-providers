package provider

import "testing"

// TestIsTurnComplete asserts the predicate for every named EventType.
// EventDone and EventError are turn-terminal; everything else is non-terminal.
func TestIsTurnComplete(t *testing.T) {
	cases := []struct {
		kind EventType
		want bool
	}{
		{EventDelta, false},
		{EventToolUse, false},
		{EventUsage, false},
		{EventSessionID, false},
		{EventDone, true},
		{EventError, true},
		{EventType(""), false}, // zero value: not a real event
	}
	for _, c := range cases {
		got := IsTurnComplete(StreamEvent{Type: c.kind})
		if got != c.want {
			t.Errorf("IsTurnComplete(%q) = %v, want %v", c.kind, got, c.want)
		}
	}
}

// TestEventTypeConstants pins the wire values of the canonical event types.
// External tools may scan logs or fixtures expecting these literal strings;
// changing them is a breaking change to consumers.
func TestEventTypeConstants(t *testing.T) {
	cases := []struct {
		got, want EventType
	}{
		{EventDelta, "delta"},
		{EventToolUse, "tool_use"},
		{EventUsage, "usage"},
		{EventError, "error"},
		{EventDone, "done"},
		{EventSessionID, "session_id"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("event constant drifted: got %q, want %q", c.got, c.want)
		}
	}
}

// TestPerAdapterTerminalEvent verifies each CLI adapter's ParseLine produces a
// turn-terminal event from its canonical "result"-style golden line, AND that
// IsTurnComplete agrees. Copilot is the documented outlier: its output is
// unstructured text so ParseLine never emits a terminal event; the bridge
// (PTYBridge / SubprocessBridge) synthesizes EventDone on clean process exit.
func TestPerAdapterTerminalEvent(t *testing.T) {
	cases := []struct {
		name        string
		adapter     CLIAdapter
		goldenLine  string
		wantTermKind EventType
	}{
		{
			name:    "claude",
			adapter: NewClaudeAdapter(),
			goldenLine: `{"type":"result","subtype":"success","is_error":false,"result":"ok",` +
				`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`,
			wantTermKind: EventDone,
		},
		{
			name:    "qwen",
			adapter: NewQwenAdapter(),
			goldenLine: `{"type":"result","subtype":"success","is_error":false,"result":"ok",` +
				`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`,
			wantTermKind: EventDone,
		},
		{
			name:    "gemini",
			adapter: NewGeminiAdapter(),
			goldenLine: `{"type":"result","subtype":"success","is_error":false,"result":"ok",` +
				`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`,
			wantTermKind: EventDone,
		},
		{
			name:    "junie",
			adapter: NewJunieAdapter(),
			goldenLine: `{"type":"result","subtype":"success","is_error":false,"result":"ok",` +
				`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`,
			wantTermKind: EventDone,
		},
		{
			name:         "codex",
			adapter:      NewCodexAdapter(),
			goldenLine:   `{"type":"turn.completed"}`,
			wantTermKind: EventDone,
		},
		{
			name:         "aider",
			adapter:      NewAiderAdapter(),
			goldenLine:   `{"type":"done"}`,
			wantTermKind: EventDone,
		},
		{
			name:         "kiro",
			adapter:      NewKiroAdapter(),
			goldenLine:   `{"type":"done"}`,
			wantTermKind: EventDone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events, err := c.adapter.ParseLine([]byte(c.goldenLine))
			if err != nil {
				t.Fatalf("ParseLine: %v", err)
			}
			var found bool
			for _, ev := range events {
				if IsTurnComplete(ev) {
					found = true
					if ev.Type != c.wantTermKind {
						t.Errorf("terminal event kind = %q, want %q", ev.Type, c.wantTermKind)
					}
				}
			}
			if !found {
				t.Errorf("expected a turn-terminal event from golden line; got %+v", events)
			}
		})
	}

	// Copilot: ParseLine is text-passthrough; no structured terminal event.
	// The bridge synthesizes EventDone on clean process exit. See bridge tests.
	t.Run("copilot_no_terminal_in_parse", func(t *testing.T) {
		adapter := NewCopilotAdapter()
		events, err := adapter.ParseLine([]byte("any text content"))
		if err != nil {
			t.Fatalf("ParseLine: %v", err)
		}
		for _, ev := range events {
			if IsTurnComplete(ev) {
				t.Errorf("copilot ParseLine should never produce terminal events; got %+v", ev)
			}
		}
	})
}
