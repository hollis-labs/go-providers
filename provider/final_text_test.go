package provider

import (
	"encoding/json"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// The turn's own final message rides on the terminal event, so a consumer can
// take it as exact rather than guess the last block (CW-20261002-0061). These
// tests hold it to what the CLIs really wrote: the captured fixtures, where the
// provider's result must be the same words as the last message the agent wrote
// before it.

// streamLines returns the lines the CLI wrote to stdout in a fixture: a
// per-turn fixture is its stdout; a duplex transcript is the frames it sent.
func streamLines(t *testing.T, name string) [][]byte {
	t.Helper()
	lines := providertest.FixtureLines(t, name)
	if !strings.HasSuffix(name, ".transcript.jsonl") {
		return lines
	}
	var out [][]byte
	for _, line := range lines {
		var step struct {
			Send json.RawMessage `json:"send"`
		}
		if json.Unmarshal(line, &step) == nil && len(step.Send) > 0 {
			out = append(out, step.Send)
		}
	}
	return out
}

func TestClaudeDoneCarriesTheFinalMessage(t *testing.T) {
	for _, name := range []string{
		"claude/print_turn1.jsonl",
		"claude/print_turn2_resume.jsonl",
		"claude/print_tool_use.jsonl",
		"claude/print_tool_denied.jsonl",
		"claude/stream_resume.transcript.jsonl",
		"claude/stream_two_turns.transcript.jsonl",
	} {
		t.Run(name, func(t *testing.T) {
			a := NewClaudeAdapter()
			var last string
			var streamDones, typedDones int
			for _, line := range streamLines(t, name) {
				var probe struct {
					Type    string `json:"type"`
					Message struct {
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"message"`
				}
				if err := json.Unmarshal(line, &probe); err != nil {
					continue
				}
				if probe.Type == "assistant" {
					for _, block := range probe.Message.Content {
						if block.Type == "text" && block.Text != "" {
							last = block.Text
						}
					}
				}
				stream, err := a.ParseLine(line)
				if err != nil {
					t.Fatalf("ParseLine: %v", err)
				}
				for _, ev := range stream {
					if ev.Type == llmtypes.EventDone {
						streamDones++
						if ev.Content != last {
							t.Errorf("stream done = %q, want the last message %q", ev.Content, last)
						}
					}
				}
				typed, err := a.ParseLineEvents(line)
				if err != nil {
					t.Fatalf("ParseLineEvents: %v", err)
				}
				for _, ev := range typed {
					if d, ok := ev.(events.Done); ok {
						typedDones++
						if d.Text != last {
							t.Errorf("typed done = %q, want the last message %q", d.Text, last)
						}
					}
				}
				if probe.Type == "result" {
					last = ""
				}
			}
			if streamDones == 0 || streamDones != typedDones {
				t.Fatalf("%d stream done(s), %d typed done(s), want the same non-zero number", streamDones, typedDones)
			}
		})
	}
}

// A failed result is an error with its message, not a done with text.
func TestClaudeFailedResultHasNoDoneText(t *testing.T) {
	a := NewClaudeAdapter()
	for _, line := range providertest.FixtureLines(t, "claude/print_error_unknown_model.jsonl") {
		stream, _ := a.ParseLine(line)
		typed, _ := a.ParseLineEvents(line)
		for _, ev := range stream {
			if ev.Type == llmtypes.EventDone {
				t.Errorf("a failed result produced a done: %+v", ev)
			}
		}
		for _, ev := range typed {
			if _, ok := ev.(events.Done); ok {
				t.Errorf("a failed result produced a typed done: %+v", ev)
			}
		}
	}
}

func TestAntigravityDoneCarriesTheFinalMessage(t *testing.T) {
	for _, name := range []string{
		"antigravity/print_turn1.jsonl",
		"antigravity/print_turn2_resume.jsonl",
		"antigravity/print_tool_run.jsonl",
		"antigravity/print_mcp_tool.jsonl",
		"antigravity/print_tool_denied.jsonl",
	} {
		t.Run(name, func(t *testing.T) {
			a := NewAntigravityAdapter()
			// agy writes a message as several text_delta updates of one step; the
			// final message is the text of the last step that wrote any.
			var stepText = map[int]string{}
			var order []int
			var streamDone, typedDone *string
			for _, line := range providertest.FixtureLines(t, name) {
				var probe struct {
					Event      string `json:"event"`
					StepUpdate struct {
						StepIndex int    `json:"step_index"`
						StepType  string `json:"step_type"`
						TextDelta string `json:"text_delta"`
					} `json:"step_update"`
				}
				if json.Unmarshal(line, &probe) == nil && probe.Event == "step_update" &&
					probe.StepUpdate.StepType == "agent_response" && strings.TrimSpace(probe.StepUpdate.TextDelta) != "" {
					if _, seen := stepText[probe.StepUpdate.StepIndex]; !seen {
						order = append(order, probe.StepUpdate.StepIndex)
					}
					stepText[probe.StepUpdate.StepIndex] += probe.StepUpdate.TextDelta
				}
				stream, _ := a.ParseLine(line)
				for _, ev := range stream {
					if ev.Type == llmtypes.EventDone {
						c := ev.Content
						streamDone = &c
					}
				}
				typed, _ := a.ParseLineEvents(line)
				for _, ev := range typed {
					if d, ok := ev.(events.Done); ok {
						x := d.Text
						typedDone = &x
					}
				}
			}
			want := ""
			if len(order) > 0 {
				want = strings.TrimSpace(stepText[order[len(order)-1]])
			}
			if streamDone == nil || typedDone == nil {
				t.Fatalf("no done: stream %v, typed %v", streamDone, typedDone)
			}
			if got := strings.TrimSpace(*streamDone); got != want {
				t.Errorf("stream done = %q, want the last step's text %q", got, want)
			}
			if got := strings.TrimSpace(*typedDone); got != want {
				t.Errorf("typed done = %q, want the last step's text %q", got, want)
			}
		})
	}
}

// An adapter with no typed parser has its done translated from the stream, text
// included.
func TestTranslateStreamEventsKeepsTheDoneText(t *testing.T) {
	got := translateStreamEvents([]llmtypes.StreamEvent{{Type: llmtypes.EventDone, Content: "the answer"}})
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if d, ok := got[0].(events.Done); !ok || d.Text != "the answer" {
		t.Fatalf("got %+v, want a done carrying the text", got[0])
	}
}
