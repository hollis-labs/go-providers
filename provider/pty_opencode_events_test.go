package provider

import (
	"reflect"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// The opencode fixtures are verbatim `opencode run --format json` stdout;
// providertest/fixtures/README.md describes each one.

func opencodeFixtureLines(t *testing.T, name string) [][]byte {
	t.Helper()
	return providertest.FixtureLines(t, "opencode/"+name)
}

func parseOpencodeFixture(t *testing.T, name string) []llmtypes.StreamEvent {
	t.Helper()
	a := &OpencodeAdapter{}
	var out []llmtypes.StreamEvent
	for _, line := range opencodeFixtureLines(t, name) {
		evs, err := a.ParseLine(line)
		if err != nil {
			t.Fatalf("%s: ParseLine: %v", name, err)
		}
		out = append(out, evs...)
	}
	return out
}

func opencodeEventTypes(evs []llmtypes.StreamEvent) []llmtypes.EventType {
	out := make([]llmtypes.EventType, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

func TestOpencodeParseLine_Fixtures(t *testing.T) {
	t.Run("single step turn", func(t *testing.T) {
		evs := parseOpencodeFixture(t, "run_turn1.jsonl")
		want := []llmtypes.EventType{llmtypes.EventSessionID, llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventDone}
		if got := opencodeEventTypes(evs); !reflect.DeepEqual(got, want) {
			t.Fatalf("types = %v; want %v", got, want)
		}
		if evs[0].SessionID != "ses_f0d6f8b4bffeveyfKIA5MI2bYi" {
			t.Errorf("session id = %q", evs[0].SessionID)
		}
		if evs[1].Content != "OK." {
			t.Errorf("delta = %q", evs[1].Content)
		}
		wantUsage := llmtypes.Usage{InputTokens: 3, OutputTokens: 5, CacheCreationTokens: 30669, StopReason: "stop"}
		if *evs[2].Usage != wantUsage {
			t.Errorf("usage = %+v; want %+v", *evs[2].Usage, wantUsage)
		}
	})

	t.Run("resumed turn keeps the session id", func(t *testing.T) {
		evs := parseOpencodeFixture(t, "run_turn2_resume.jsonl")
		if evs[0].Type != llmtypes.EventSessionID || evs[0].SessionID != "ses_f0d6f8b4bffeveyfKIA5MI2bYi" {
			t.Errorf("first event = %+v", evs[0])
		}
		if evs[1].Content != "PERIWINKLE-42" {
			t.Errorf("delta = %q", evs[1].Content)
		}
		if evs[2].Usage.CacheReadTokens != 30669 {
			t.Errorf("cache read = %d; want the whole turn-1 context", evs[2].Usage.CacheReadTokens)
		}
	})

	t.Run("multi step turn is done only at the last step", func(t *testing.T) {
		evs := parseOpencodeFixture(t, "run_tool_use.jsonl")
		want := []llmtypes.EventType{
			llmtypes.EventSessionID, llmtypes.EventToolUse, llmtypes.EventUsage,
			llmtypes.EventSessionID, llmtypes.EventToolUse, llmtypes.EventUsage,
			llmtypes.EventSessionID, llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventDone,
		}
		if got := opencodeEventTypes(evs); !reflect.DeepEqual(got, want) {
			t.Fatalf("types = %v; want %v", got, want)
		}
		glob := evs[1].ToolUse
		if glob.Name != "glob" || glob.ID != "toolu_01UsKRtuKcwxHxyDn2fAsFZF" || glob.Input["pattern"] != "**/note.txt" {
			t.Errorf("glob tool use = %+v", glob)
		}
		if evs[5].Usage.StopReason != "tool-calls" || evs[8].Usage.StopReason != "stop" {
			t.Errorf("stop reasons = %q, %q", evs[5].Usage.StopReason, evs[8].Usage.StopReason)
		}
		var out int
		for _, ev := range evs {
			if ev.Type == llmtypes.EventUsage {
				out += ev.Usage.OutputTokens
			}
		}
		if out != 56+113+5 {
			t.Errorf("summed output tokens = %d; want per-step usage that sums to 174", out)
		}
	})
}

func TestOpencodeParseLineEvents_Fixtures(t *testing.T) {
	a := &OpencodeAdapter{}
	var got []events.Event
	for _, line := range opencodeFixtureLines(t, "run_tool_use.jsonl") {
		evs, err := a.ParseLineEvents(line)
		if err != nil {
			t.Fatalf("ParseLineEvents: %v", err)
		}
		got = append(got, evs...)
	}
	var uses, results, usages, dones int
	for _, ev := range got {
		switch e := ev.(type) {
		case events.ToolUse:
			uses++
		case events.ToolResult:
			results++
			if e.IsError || e.ContentPreview == "" {
				t.Errorf("tool result = %+v", e)
			}
		case events.Usage:
			usages++
		case events.Done:
			dones++
			if e.StopReason != "stop" {
				t.Errorf("done stop reason = %q", e.StopReason)
			}
		}
	}
	if uses != 2 || results != 2 || usages != 3 || dones != 1 {
		t.Errorf("tool uses=%d results=%d usages=%d dones=%d; want 2 2 3 1", uses, results, usages, dones)
	}
	if d, ok := got[len(got)-1].(events.Done); !ok {
		t.Errorf("last event = %#v; want Done", got[len(got)-1])
	} else if d.StopReason != "stop" {
		t.Errorf("Done.StopReason = %q", d.StopReason)
	}

	serve := NewOpencodeAdapterServeHTTP()
	evs, err := serve.ParseLineEvents(opencodeFixtureLines(t, "run_turn1.jsonl")[1])
	if err != nil || len(evs) != 0 {
		t.Errorf("serve-http ParseLineEvents = %v, %v; want nothing", evs, err)
	}
}

func TestOpencodeParseLineEvents_ToolError(t *testing.T) {
	line := []byte(`{"type":"tool_use","sessionID":"ses_x","part":{"type":"tool","tool":"read","callID":"c1","state":{"status":"error","input":{"filePath":"/nope"},"error":"File not found: /nope"}}}`)
	evs, err := (&OpencodeAdapter{}).ParseLineEvents(line)
	if err != nil {
		t.Fatal(err)
	}
	want := []events.Event{
		events.ToolUse{ID: "c1", Name: "read", Args: map[string]any{"filePath": "/nope"}},
		events.ToolResult{ID: "c1", IsError: true, ContentPreview: "File not found: /nope"},
	}
	if !reflect.DeepEqual(evs, want) {
		t.Errorf("events = %#v; want %#v", evs, want)
	}
}

func TestOpencodeIsSessionLost(t *testing.T) {
	var _ SessionLostClassifier = (*OpencodeAdapter)(nil)
	a := NewOpencodeAdapter()
	// Verbatim stderr from `opencode run --session ses_doesnotexist`.
	if !a.IsSessionLost([]byte("\x1b[91m\x1b[1mError: \x1b[0mSession not found\n")) {
		t.Error("stale session stderr not classified as lost")
	}
	if a.IsSessionLost([]byte("Error: rate limited\n")) {
		t.Error("unrelated error classified as session lost")
	}
}

// A failed step reports usage but never done: opencode's error line (or
// the non-zero exit) is the turn's one terminal event.
func TestOpencodeParseLine_ErrorStepIsNotDone(t *testing.T) {
	line := []byte(`{"type":"step_finish","sessionID":"ses_x","part":{"type":"step-finish","reason":"error","tokens":{"input":1,"output":2,"reasoning":0,"cache":{"read":0,"write":0}}}}`)
	a := &OpencodeAdapter{}
	evs, err := a.ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if got := opencodeEventTypes(evs); !reflect.DeepEqual(got, []llmtypes.EventType{llmtypes.EventUsage}) {
		t.Errorf("ParseLine types = %v; want usage only", got)
	}
	typed, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range typed {
		if _, ok := ev.(events.Done); ok {
			t.Errorf("ParseLineEvents emitted Done for a failed step: %#v", typed)
		}
	}
}
