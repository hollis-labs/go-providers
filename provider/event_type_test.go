package provider

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// TestIsTurnComplete asserts the predicate for every named llmtypes.EventType.
// llmtypes.EventDone and llmtypes.EventError are turn-terminal; everything else is non-terminal.
func TestIsTurnComplete(t *testing.T) {
	cases := []struct {
		kind llmtypes.EventType
		want bool
	}{
		{llmtypes.EventDelta, false},
		{llmtypes.EventToolUse, false},
		{llmtypes.EventUsage, false},
		{llmtypes.EventSessionID, false},
		{llmtypes.EventDone, true},
		{llmtypes.EventError, true},
		{llmtypes.EventType(""), false}, // zero value: not a real event
	}
	for _, c := range cases {
		got := llmtypes.IsTurnComplete(llmtypes.StreamEvent{Type: c.kind})
		if got != c.want {
			t.Errorf("llmtypes.IsTurnComplete(%q) = %v, want %v", c.kind, got, c.want)
		}
	}
}

// TestEventTypeConstants pins the wire values of the canonical event types.
// External tools may scan logs or fixtures expecting these literal strings;
// changing them is a breaking change to consumers.
func TestEventTypeConstants(t *testing.T) {
	cases := []struct {
		got, want llmtypes.EventType
	}{
		{llmtypes.EventDelta, "delta"},
		{llmtypes.EventToolUse, "tool_use"},
		{llmtypes.EventUsage, "usage"},
		{llmtypes.EventError, "error"},
		{llmtypes.EventDone, "done"},
		{llmtypes.EventSessionID, "session_id"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("event constant drifted: got %q, want %q", c.got, c.want)
		}
	}
}

// TestPerAdapterTerminalEvent verifies each CLI adapter's ParseLine produces a
// turn-terminal event from its canonical "result"-style golden line, AND that
// llmtypes.IsTurnComplete agrees. Adapters whose output is unstructured text
// (no structured terminal event) rely on the bridge (PTYBridge / SubprocessBridge)
// to synthesize llmtypes.EventDone on clean process exit; those are exercised
// via TestSubprocessBridge_SyntheticDoneOnCleanExit instead of here.
func TestPerAdapterTerminalEvent(t *testing.T) {
	cases := []struct {
		name         string
		adapter      CLIAdapter
		goldenLine   string
		wantTermKind llmtypes.EventType
	}{
		{
			name:    "claude",
			adapter: NewClaudeAdapter(),
			goldenLine: `{"type":"result","subtype":"success","is_error":false,"result":"ok",` +
				`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`,
			wantTermKind: llmtypes.EventDone,
		},
		{
			name:         "codex",
			adapter:      NewCodexAdapter(),
			goldenLine:   `{"type":"turn.completed"}`,
			wantTermKind: llmtypes.EventDone,
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
				if llmtypes.IsTurnComplete(ev) {
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
}
