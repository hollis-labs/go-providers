package provider

import (
	"encoding/json"
	"math"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// CW-20260930-0137 (event vocabulary), CW-20260930-0228 (block boundaries,
// stop reason) and CW-20260930-0222 L1 (cost), checked against the captured
// fixtures in providertest/fixtures.

// resetClaudeCosts gives a test a fresh process-wide cost ledger.
func resetClaudeCosts(t *testing.T) {
	t.Helper()
	saved := claudeCosts
	claudeCosts = &claudeCostLedger{entries: map[string]*claudeCostEntry{}}
	t.Cleanup(func() { claudeCosts = saved })
}

// claudeSendLines returns what the CLI wrote in a fixture: every line of a
// plain .jsonl, or the "send" frames of a .transcript.jsonl.
func claudeSendLines(t *testing.T, name string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, line := range providertest.FixtureLines(t, "claude/"+name) {
		var frame struct {
			Send json.RawMessage `json:"send"`
		}
		if err := json.Unmarshal(line, &frame); err == nil && len(frame.Send) > 0 {
			out = append(out, frame.Send)
			continue
		}
		var probe struct {
			Recv json.RawMessage `json:"recv"`
		}
		if err := json.Unmarshal(line, &probe); err == nil && len(probe.Recv) > 0 {
			continue
		}
		out = append(out, line)
	}
	return out
}

// claudeTurnCosts parses a fixture with both parsers, as agentkit does, and
// returns each usage's cost from the legacy and typed surfaces.
func claudeTurnCosts(t *testing.T, name string) (legacy, typed []float64) {
	t.Helper()
	a := &ClaudeAdapter{}
	for _, line := range claudeSendLines(t, name) {
		evs, err := a.ParseLine(line)
		if err != nil {
			t.Fatalf("%s: ParseLine: %v", name, err)
		}
		for _, ev := range evs {
			if ev.Type == llmtypes.EventUsage {
				legacy = append(legacy, ev.Usage.CostUSD)
			}
		}
		tevs, err := a.ParseLineEvents(line)
		if err != nil {
			t.Fatalf("%s: ParseLineEvents: %v", name, err)
		}
		for _, ev := range tevs {
			if u, ok := ev.(events.Usage); ok {
				typed = append(typed, u.CostUSD)
			}
		}
	}
	return legacy, typed
}

func approxEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-9 {
			return false
		}
	}
	return true
}

// A streaming session reports total_cost_usd as a running total; each turn's
// usage carries only that turn's cost.
func TestClaudeCostIsPerTurnDeltaInAStreamingSession(t *testing.T) {
	resetClaudeCosts(t)
	legacy, typed := claudeTurnCosts(t, "stream_two_turns.transcript.jsonl")
	want := []float64{0.0023779, 0.0049378 - 0.0023779}
	if !approxEqual(legacy, want) {
		t.Errorf("legacy costs = %v; want %v", legacy, want)
	}
	// Both parsers see every line; the typed surface must not see a zero
	// because the legacy one already moved the baseline.
	if !approxEqual(typed, want) {
		t.Errorf("typed costs = %v; want %v", typed, want)
	}
}

// --resume in a new process carries the running total on. With the first
// turn seen in this process, the second turn is the difference.
func TestClaudeCostAcrossAResumeSeenFromTheStart(t *testing.T) {
	resetClaudeCosts(t)
	first, _ := claudeTurnCosts(t, "print_turn1.jsonl")
	second, _ := claudeTurnCosts(t, "print_turn2_resume.jsonl")
	if !approxEqual(first, []float64{0.005329800000000001}) {
		t.Errorf("turn 1 cost = %v", first)
	}
	if !approxEqual(second, []float64{0.007881700000000002 - 0.005329800000000001}) {
		t.Errorf("turn 2 cost = %v; want the difference", second)
	}
}

// A resumed session this process never saw: its running total includes turns
// from before, which are not this turn's cost. Zero, not the whole history.
func TestClaudeCostOfAnUnseenResumeIsZero(t *testing.T) {
	resetClaudeCosts(t)
	got, _ := claudeTurnCosts(t, "print_turn2_resume.jsonl")
	if !approxEqual(got, []float64{0}) {
		t.Errorf("cost = %v; want 0 for a resume with no baseline", got)
	}
}

func TestClaudeCostLedgerEvictsOldest(t *testing.T) {
	resetClaudeCosts(t)
	for i := 0; i < claudeCostLedgerCap+5; i++ {
		claudeCosts.delta(string(rune('a'+i%26))+string(rune(i)), "", 1, true)
	}
	if n := len(claudeCosts.entries); n > claudeCostLedgerCap {
		t.Errorf("ledger holds %d entries; cap is %d", n, claudeCostLedgerCap)
	}
}

// Claude writes one assistant event per block, all at content index 0 and
// sharing the message id, so block ids come from the event uuid.
func TestClaudeBlockIDsSeparateBlocks(t *testing.T) {
	resetClaudeCosts(t)
	a := &ClaudeAdapter{}
	var ids []string
	var typedIDs []string
	for _, line := range claudeSendLines(t, "print_tool_use.jsonl") {
		evs, _ := a.ParseLine(line)
		for _, ev := range evs {
			if ev.Type == llmtypes.EventDelta {
				ids = append(ids, ev.BlockID)
			}
		}
		tevs, _ := a.ParseLineEvents(line)
		for _, ev := range tevs {
			if d, ok := ev.(events.Delta); ok {
				typedIDs = append(typedIDs, d.BlockID)
			}
		}
	}
	if len(ids) != 1 || ids[0] != "00000000-0000-4000-8000-000000000020" {
		t.Errorf("legacy delta block ids = %v; want the text event's uuid", ids)
	}
	if len(typedIDs) != 1 || typedIDs[0] != ids[0] {
		t.Errorf("typed delta block ids = %v; want %v", typedIDs, ids)
	}

	// Without a uuid, the message id and index stand in.
	ev := claudeAssistantEvent{Message: claudeAssistantMsg{ID: "msg_1"}}
	if got := claudeBlockID(ev, 2); got != "msg_1:2" {
		t.Errorf("fallback block id = %q", got)
	}
	ev.UUID = "u1"
	if got := claudeBlockID(ev, 1); got != "u1:1" {
		t.Errorf("multi-block event id = %q", got)
	}
}

func TestClaudeStopReasonIsNormalised(t *testing.T) {
	resetClaudeCosts(t)
	a := &ClaudeAdapter{}
	line := []byte(`{"type":"result","subtype":"success","stop_reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":2}}`)
	evs, _ := a.ParseLine(line)
	if evs[0].Usage.StopReason != llmtypes.StopReasonMaxTokens {
		t.Errorf("stop reason = %q", evs[0].Usage.StopReason)
	}
	line = []byte(`{"type":"result","subtype":"success","usage":{"input_tokens":1,"output_tokens":2}}`)
	tevs, _ := a.ParseLineEvents(line)
	if u, ok := tevs[0].(events.Usage); !ok || u.StopReason != llmtypes.StopReasonEndTurn {
		t.Errorf("typed usage = %#v; want end_turn by default", tevs[0])
	}
}

// codex exec: item.id is the block id, a completed message is final, and a
// completed turn ended normally.
func TestCodexExecBlockIDsPhaseAndStopReason(t *testing.T) {
	a := NewCodexAdapter()
	var deltas []llmtypes.StreamEvent
	var usage *llmtypes.Usage
	var typedDone events.Done
	var typedDelta events.Delta
	for _, line := range providertest.FixtureLines(t, "codex/exec_tool_use.jsonl") {
		evs, err := a.ParseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			switch ev.Type {
			case llmtypes.EventDelta:
				deltas = append(deltas, ev)
			case llmtypes.EventUsage:
				usage = ev.Usage
			}
		}
		tevs, _ := a.ParseLineEvents(line)
		for _, ev := range tevs {
			switch e := ev.(type) {
			case events.Done:
				typedDone = e
			case events.Delta:
				typedDelta = e
			}
		}
	}
	if len(deltas) != 1 || deltas[0].BlockID != "item_1" || deltas[0].Phase != llmtypes.PhaseFinal {
		t.Errorf("deltas = %+v; want one final delta with block id item_1", deltas)
	}
	if typedDelta.BlockID != "item_1" {
		t.Errorf("typed delta = %+v", typedDelta)
	}
	if usage == nil || usage.StopReason != llmtypes.StopReasonEndTurn {
		t.Errorf("usage = %+v; want stop reason end_turn", usage)
	}
	if typedDone.StopReason != llmtypes.StopReasonEndTurn {
		t.Errorf("typed done = %+v", typedDone)
	}
}

// opencode text and reasoning parts carry their part id as the block id on
// both surfaces.
func TestOpencodeBlockIDsOnBothSurfaces(t *testing.T) {
	a := &OpencodeAdapter{}
	line := []byte(`{"type":"reasoning","sessionID":"s","part":{"id":"prt_r1","type":"reasoning","text":"hmm"}}`)
	evs := parseOpencodeStreamLine(line)
	if len(evs) != 1 || evs[0].BlockID != "prt_r1" || evs[0].Phase != llmtypes.PhaseThinking {
		t.Errorf("reasoning = %+v", evs)
	}
	tevs, _ := a.ParseLineEvents(line)
	if th, ok := tevs[0].(events.Thinking); !ok || th.BlockID != "prt_r1" {
		t.Errorf("typed reasoning = %#v", tevs)
	}
}

// The legacy-to-typed translation keeps the new fields.
func TestTranslateStreamEventsKeepsVocabulary(t *testing.T) {
	got := translateStreamEvents([]llmtypes.StreamEvent{
		{Type: llmtypes.EventDelta, Content: "x", BlockID: "b1", Phase: llmtypes.PhaseFinal},
		{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{CostUSD: 0.5, StopReason: "end_turn"}},
		{Type: llmtypes.EventThinking, ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: "t"}, BlockID: "b2"},
	})
	if d := got[0].(events.Delta); d.BlockID != "b1" || d.Phase != llmtypes.PhaseFinal {
		t.Errorf("delta = %+v", d)
	}
	if u := got[1].(events.Usage); u.CostUSD != 0.5 {
		t.Errorf("usage = %+v", u)
	}
	if th := got[2].(events.Thinking); th.BlockID != "b2" {
		t.Errorf("thinking = %+v", th)
	}
}
