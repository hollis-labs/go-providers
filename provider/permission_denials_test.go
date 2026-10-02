package provider

import (
	"encoding/json"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// Claude records the tool calls it refused because they needed an approval it
// could not ask for in result.permission_denials, and the adapters dropped it, so
// a refusal was a silent no-op on every surface (CW-20261002-0073). The refusal is
// reported as a typed PermissionDenied before the done, as agy's is, with the id
// of the refused call so a consumer can tell a refusal the turn ended on from one
// the agent worked around.

func TestClaudeReportsItsPermissionDenials(t *testing.T) {
	a := NewClaudeAdapter()
	var typed []events.Event
	var stream []llmtypes.StreamEvent
	for _, line := range providertest.FixtureLines(t, "claude/print_tool_denied.jsonl") {
		evs, err := a.ParseLineEvents(line)
		if err != nil {
			t.Fatalf("ParseLineEvents: %v", err)
		}
		typed = append(typed, evs...)
		se, err := a.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine: %v", err)
		}
		stream = append(stream, se...)
	}

	var denials []events.PermissionDenied
	denialAt, doneAt, toolAt := -1, -1, -1
	for i, ev := range typed {
		switch e := ev.(type) {
		case events.PermissionDenied:
			denials = append(denials, e)
			denialAt = i
		case events.Done:
			doneAt = i
		case events.ToolUse:
			if e.ID == "toolu_fixture0002" {
				toolAt = i
			}
		}
	}
	want := events.PermissionDenied{Action: "Bash", DisplayName: "touch providertest.txt", ToolUseID: "toolu_fixture0002"}
	if len(denials) != 1 || denials[0] != want {
		t.Fatalf("denials = %+v, want exactly %+v", denials, want)
	}
	if toolAt < 0 || toolAt >= denialAt || denialAt >= doneAt {
		t.Fatalf("order: the refused tool call at %d, its denial at %d, the done at %d; want the call, then the denial, then the done", toolAt, denialAt, doneAt)
	}
	// The denial has no StreamEvent form: the legacy stream is what it was.
	for _, ev := range stream {
		if ev.Type != llmtypes.EventDelta && ev.Type != llmtypes.EventToolUse && ev.Type != llmtypes.EventUsage &&
			ev.Type != llmtypes.EventDone && ev.Type != llmtypes.EventSessionID {
			t.Errorf("unexpected stream event %+v", ev)
		}
	}
}

// Every other capture refused nothing, and nothing is invented.
func TestClaudeReportsNoDenialWhenNothingWasRefused(t *testing.T) {
	for _, name := range []string{
		"claude/print_turn1.jsonl",
		"claude/print_turn2_resume.jsonl",
		"claude/print_tool_use.jsonl",
		"claude/stream_resume.transcript.jsonl",
		"claude/stream_two_turns.transcript.jsonl",
		"claude/stream_interrupt.transcript.jsonl",
	} {
		t.Run(name, func(t *testing.T) {
			a := NewClaudeAdapter()
			for _, line := range streamLines(t, name) {
				typed, err := a.ParseLineEvents(line)
				if err != nil {
					t.Fatalf("ParseLineEvents: %v", err)
				}
				for _, ev := range typed {
					if d, ok := ev.(events.PermissionDenied); ok {
						t.Fatalf("a turn that refused nothing reported %+v", d)
					}
				}
			}
		})
	}
}

func TestClaudeDenialLabels(t *testing.T) {
	result := func(denials string) []byte {
		return []byte(`{"type":"result","subtype":"success","is_error":false,"result":"x","stop_reason":"end_turn","permission_denials":` + denials + `}`)
	}
	tests := []struct {
		name    string
		denials string
		want    []events.PermissionDenied
	}{
		{"command", `[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"rm -rf build","description":"clean"}}]`,
			[]events.PermissionDenied{{Action: "Bash", DisplayName: "rm -rf build", ToolUseID: "t1"}}},
		{"path", `[{"tool_name":"Write","tool_use_id":"t2","tool_input":{"file_path":"/work/a.go","content":"x"}}]`,
			[]events.PermissionDenied{{Action: "Write", DisplayName: "/work/a.go", ToolUseID: "t2"}}},
		{"description", `[{"tool_name":"Task","tool_use_id":"t3","tool_input":{"description":"audit"}}]`,
			[]events.PermissionDenied{{Action: "Task", DisplayName: "audit", ToolUseID: "t3"}}},
		{"the tool name when the input says nothing", `[{"tool_name":"AskUserQuestion","tool_use_id":"t4","tool_input":{"questions":[]}}]`,
			[]events.PermissionDenied{{Action: "AskUserQuestion", DisplayName: "AskUserQuestion", ToolUseID: "t4"}}},
		{"two refusals, in order", `[{"tool_name":"Bash","tool_use_id":"a","tool_input":{"command":"one"}},{"tool_name":"Bash","tool_use_id":"b","tool_input":{"command":"two"}}]`,
			[]events.PermissionDenied{{Action: "Bash", DisplayName: "one", ToolUseID: "a"}, {Action: "Bash", DisplayName: "two", ToolUseID: "b"}}},
		{"none", `[]`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typed, err := NewClaudeAdapter().ParseLineEvents(result(tt.denials))
			if err != nil {
				t.Fatal(err)
			}
			var got []events.PermissionDenied
			for _, ev := range typed {
				if d, ok := ev.(events.PermissionDenied); ok {
					got = append(got, d)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("denials = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("denial %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
			// The denials come before the done.
			if _, isDone := typed[len(typed)-1].(events.Done); !isDone {
				t.Fatalf("last event = %+v, want the done", typed[len(typed)-1])
			}
		})
	}

	t.Run("a long command is cut", func(t *testing.T) {
		long, _ := json.Marshal(strings.Repeat("x", 1000))
		typed, _ := NewClaudeAdapter().ParseLineEvents(result(`[{"tool_name":"Bash","tool_use_id":"t","tool_input":{"command":` + string(long) + `}}]`))
		d := typed[0].(events.PermissionDenied)
		if len(d.DisplayName) > 256+len("…") {
			t.Fatalf("display name is %d bytes, want it cut near 256", len(d.DisplayName))
		}
	})
}
