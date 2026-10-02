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

// ocLine builds one `opencode run --format json` line.
func ocLine(t *testing.T, typ, session string, part map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": typ, "sessionID": session, "part": part})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ocText(t *testing.T, session, id, text string) []byte {
	return ocLine(t, "text", session, map[string]any{"id": id, "type": "text", "text": text})
}

func ocStep(t *testing.T, typ, session, id, reason string) []byte {
	part := map[string]any{"id": id, "type": strings.ReplaceAll(typ, "_", "-")}
	if reason != "" {
		part["reason"] = reason
	}
	return ocLine(t, typ, session, part)
}

// feedOpencode runs lines through the adapter on both parse surfaces, twice each,
// as a session that taps the adapter can, and returns the text of every done,
// requiring the four calls to agree.
func feedOpencode(t *testing.T, a *OpencodeAdapter, lines ...[]byte) []string {
	t.Helper()
	var dones []string
	for _, line := range lines {
		var texts []string
		for range 2 {
			stream, _ := a.ParseLine(line)
			for _, ev := range stream {
				if ev.Type == llmtypes.EventDone {
					texts = append(texts, ev.Content)
				}
			}
			typed, _ := a.ParseLineEvents(line)
			for _, ev := range typed {
				if d, ok := ev.(events.Done); ok {
					texts = append(texts, d.Text)
				}
			}
		}
		if len(texts) == 0 {
			continue
		}
		if len(texts) != 4 {
			t.Fatalf("%d done(s) from four parses of %s, want 4 or none", len(texts), line)
		}
		for _, x := range texts[1:] {
			if x != texts[0] {
				t.Fatalf("the parses of %s disagree: %q", line, texts)
			}
		}
		dones = append(dones, texts[0])
	}
	return dones
}

func TestOpenCodeDoneCarriesTheFinalStepText(t *testing.T) {
	const s = "ses_1"

	t.Run("captured turns", func(t *testing.T) {
		for _, tc := range []struct{ fixture, want string }{
			{"opencode/run_turn1.jsonl", "OK."},
			{"opencode/run_tool_use.jsonl", "hello fixture"},
		} {
			dones := feedOpencode(t, NewOpencodeAdapter(), providertest.FixtureLines(t, tc.fixture)...)
			if len(dones) != 1 || dones[0] != tc.want {
				t.Errorf("%s: done text %q, want %q", tc.fixture, dones, tc.want)
			}
		}
	})

	t.Run("only the step that ends the turn", func(t *testing.T) {
		dones := feedOpencode(t, NewOpencodeAdapter(),
			ocStep(t, "step_start", s, "p1", ""),
			ocText(t, s, "p2", "I'll check the tests."),
			ocLine(t, "tool_use", s, map[string]any{"id": "p3", "type": "tool", "tool": "bash", "callID": "c1"}),
			ocStep(t, "step_finish", s, "p4", "tool-calls"),
			ocStep(t, "step_start", s, "p5", ""),
			ocText(t, s, "p6", "All green."),
			ocStep(t, "step_finish", s, "p7", "stop"),
		)
		if len(dones) != 1 || dones[0] != "All green." {
			t.Fatalf("done text %q, want only the last step's", dones)
		}
	})

	t.Run("several text parts in the last step", func(t *testing.T) {
		dones := feedOpencode(t, NewOpencodeAdapter(),
			ocStep(t, "step_start", s, "p1", ""),
			ocText(t, s, "p2", "First."),
			ocLine(t, "reasoning", s, map[string]any{"id": "p3", "type": "reasoning", "text": "private thoughts"}),
			ocText(t, s, "p4", "Second."),
			ocStep(t, "step_finish", s, "p5", "stop"),
		)
		if len(dones) != 1 || dones[0] != "First.\n\nSecond." {
			t.Fatalf("done text %q, want the text parts without the reasoning", dones)
		}
	})

	t.Run("a last step with no text has no final text", func(t *testing.T) {
		dones := feedOpencode(t, NewOpencodeAdapter(),
			ocStep(t, "step_start", s, "p1", ""),
			ocText(t, s, "p2", "Working on it."),
			ocStep(t, "step_finish", s, "p3", "tool-calls"),
			ocStep(t, "step_start", s, "p4", ""),
			ocStep(t, "step_finish", s, "p5", "stop"),
		)
		if len(dones) != 1 || dones[0] != "" {
			t.Fatalf("done text %q, want none: the earlier step's text is not the answer", dones)
		}
	})

	t.Run("a failed step reports no done", func(t *testing.T) {
		dones := feedOpencode(t, NewOpencodeAdapter(),
			ocStep(t, "step_start", s, "p1", ""),
			ocText(t, s, "p2", "partial"),
			ocStep(t, "step_finish", s, "p3", "error"),
		)
		if len(dones) != 0 {
			t.Fatalf("got done text %q for a failed step", dones)
		}
	})

	t.Run("sessions do not mix", func(t *testing.T) {
		dones := feedOpencode(t, NewOpencodeAdapter(),
			ocStep(t, "step_start", "ses_a", "a1", ""),
			ocStep(t, "step_start", "ses_b", "b1", ""),
			ocText(t, "ses_a", "a2", "answer of a"),
			ocText(t, "ses_b", "b2", "answer of b"),
			ocStep(t, "step_finish", "ses_b", "b3", "stop"),
			ocStep(t, "step_finish", "ses_a", "a3", "stop"),
		)
		if len(dones) != 2 || dones[0] != "answer of b" || dones[1] != "answer of a" {
			t.Fatalf("done text %q, want each session's own", dones)
		}
	})

	t.Run("a second turn does not inherit the first", func(t *testing.T) {
		a := NewOpencodeAdapter()
		first := feedOpencode(t, a,
			ocStep(t, "step_start", s, "p1", ""), ocText(t, s, "p2", "one"), ocStep(t, "step_finish", s, "p3", "stop"))
		second := feedOpencode(t, a,
			ocStep(t, "step_start", s, "p4", ""), ocText(t, s, "p5", "two"), ocStep(t, "step_finish", s, "p6", "stop"))
		if len(first) != 1 || first[0] != "one" || len(second) != 1 || second[0] != "two" {
			t.Fatalf("first %q, second %q", first, second)
		}
	})

	t.Run("a literal adapter works and a stateless parse reports no text", func(t *testing.T) {
		dones := feedOpencode(t, &OpencodeAdapter{},
			ocStep(t, "step_start", s, "p1", ""), ocText(t, s, "p2", "hi"), ocStep(t, "step_finish", s, "p3", "stop"))
		if len(dones) != 1 || dones[0] != "hi" {
			t.Fatalf("done text %q", dones)
		}
		for _, ev := range parseOpencodeStreamLine(ocStep(t, "step_finish", s, "p4", "stop")) {
			if ev.Type == llmtypes.EventDone && ev.Content != "" {
				t.Fatalf("a stateless parse reported %q", ev.Content)
			}
		}
	})

	t.Run("serve-http reports nothing", func(t *testing.T) {
		a := NewOpencodeAdapterServeHTTP()
		if stream, _ := a.ParseLine(ocText(t, s, "p1", "hi")); len(stream) != 0 {
			t.Fatalf("serve-http parse reported %+v", stream)
		}
	})
}

// agy writes a message as several updates of one step; the step's index tells
// one message from the next.
func TestAntigravityDeltasCarryTheirStepAsBlockID(t *testing.T) {
	a := NewAntigravityAdapter()
	blocks := map[string]string{}
	var order []string
	for _, line := range providertest.FixtureLines(t, "antigravity/print_mcp_tool.jsonl") {
		stream, _ := a.ParseLine(line)
		typed, _ := a.ParseLineEvents(line)
		var fromStream, fromTyped []string
		for _, ev := range stream {
			if ev.Type == llmtypes.EventDelta {
				fromStream = append(fromStream, ev.BlockID)
				if _, seen := blocks[ev.BlockID]; !seen {
					order = append(order, ev.BlockID)
				}
				blocks[ev.BlockID] += ev.Content
			}
		}
		for _, ev := range typed {
			if d, ok := ev.(events.Delta); ok {
				fromTyped = append(fromTyped, d.BlockID)
			}
		}
		if strings.Join(fromStream, ",") != strings.Join(fromTyped, ",") {
			t.Fatalf("surfaces disagree on block ids: %v vs %v", fromStream, fromTyped)
		}
	}
	if len(order) != 1 || order[0] != "step-5" ||
		strings.TrimSpace(blocks["step-5"]) != "MCP=SECRET-WORD-MAGNOLIA; ADR=WORKSPACE-OVERRIDE-MARKER skill for probing precedence" {
		t.Fatalf("blocks %v %v, want the one message under step-5", order, blocks)
	}
}
