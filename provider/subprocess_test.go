package provider

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/providertest"
)

// textPassthroughAdapter is a minimal CLIAdapter whose ParseLine returns each
// raw line as an EventDelta and never emits a terminal event. Used by tests
// that exercise the bridge's clean-exit synthesis path (the bridge synthesizes
// EventDone when the wrapped CLI exits 0 with no structured terminal event).
type textPassthroughAdapter struct{}

func (textPassthroughAdapter) Name() string { return "test-text" }
func (textPassthroughAdapter) BuildArgs(prompt, _, _ string) []string {
	return []string{prompt}
}
func (textPassthroughAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}
	return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: string(line) + "\n"}}, nil
}
func (textPassthroughAdapter) Detect() (string, bool) { return "", false }

func TestSubprocessBridge_Capabilities(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	caps := bridge.Capabilities()

	if !caps.SupportsStreamJSON {
		t.Error("expected SupportsStreamJSON=true")
	}
	if !caps.SupportsToolCalling {
		t.Error("expected SupportsToolCalling=true")
	}
	if caps.SupportsSystemPromptCaching {
		t.Error("expected SupportsSystemPromptCaching=false")
	}
	if caps.SupportsBatch {
		t.Error("expected SupportsBatch=false")
	}
}

func TestSubprocessBridge_StreamChat_NoUserMessage(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	_, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestSubprocessBridge_StreamChat_SystemOnly(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	_, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "system", Content: "test"},
	}})
	if err == nil {
		t.Fatal("expected error for no user message")
	}
}

func TestSubprocessBridge_Complete_MockCLI(t *testing.T) {
	// A captured claude -p turn, replayed by a fake claude.
	script := providertest.New(t, "claude", providertest.Replay("claude/print_turn1")).Path
	want := claudeFixtureResult(t, "claude/print_turn1.jsonl")

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	result, err := bridge.Complete(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test prompt"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != want {
		t.Errorf("result = %q, want the captured reply %q", result, want)
	}
}

// claudeFixtureResult returns the result text of a captured claude
// stream-json turn.
func claudeFixtureResult(t *testing.T, name string) string {
	t.Helper()
	for _, l := range providertest.FixtureLines(t, name) {
		var ev struct {
			Type   string `json:"type"`
			Result string `json:"result"`
		}
		if json.Unmarshal(l, &ev) == nil && ev.Type == "result" {
			return ev.Result
		}
	}
	t.Fatalf("%s: no result event", name)
	return ""
}

func TestSubprocessBridge_StreamChat_MockCLI(t *testing.T) {
	script := providertest.New(t, "claude", providertest.Lines(
		`{"type":"system","subtype":"init","cwd":"/tmp","session_id":"sess-abc"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta one"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta two"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`,
	)).Path

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []llmtypes.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: session_id, delta, delta, usage, done
	hasSessionID := false
	deltaCount := 0
	hasDone := false
	for _, ev := range events {
		switch ev.Type {
		case llmtypes.EventSessionID:
			hasSessionID = true
		case llmtypes.EventDelta:
			deltaCount++
		case llmtypes.EventDone:
			hasDone = true
		}
	}

	if !hasSessionID {
		t.Error("expected session_id event")
	}
	if deltaCount != 2 {
		t.Errorf("expected 2 delta events, got %d", deltaCount)
	}
	if !hasDone {
		t.Error("expected done event")
	}
}

func TestSubprocessBridge_SandboxDir(t *testing.T) {
	fake := providertest.New(t, "claude", providertest.Replay("claude/print_turn1"))

	sandboxDir := t.TempDir()
	ctx := WithSandboxDir(context.Background(), sandboxDir)

	bridge := NewSubprocessBridge(NewClaudeAdapter(), fake.Path)
	if _, err := bridge.Complete(ctx, llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := filepath.EvalSymlinks(fake.Call(0).Dir)
	want, _ := filepath.EvalSymlinks(sandboxDir)
	if got != want {
		t.Errorf("CLI ran in %s, want the sandbox dir %s", got, want)
	}
}

// TestSubprocessBridge_NoSilentDrop_ToolUseOnly verifies that when the CLI
// emits only tool_use blocks (no text deltas), the bridge injects an
// explicit "CLI bridge cannot forward tool calls" error event *before* the
// terminal "done" event so consumers that stop reading at "done" still see
// the failure as the SOLE terminal event. Per the llmtypes.IsTurnComplete contract,
// llmtypes.EventError and llmtypes.EventDone are mutually exclusive — the bridge must NOT
// forward the adapter's llmtypes.EventDone after the guard fires.
func TestSubprocessBridge_NoSilentDrop_ToolUseOnly(t *testing.T) {
	script := providertest.New(t, "claude", providertest.Lines(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"do_thing","input":{}}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`,
	)).Path

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []llmtypes.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	const guardMsg = "CLI bridge cannot forward tool calls"
	guardCount, terminalCount := 0, 0
	for _, ev := range events {
		if ev.Type == llmtypes.EventError && ev.Error == guardMsg {
			guardCount++
		}
		if llmtypes.IsTurnComplete(ev) {
			terminalCount++
		}
	}

	if guardCount != 1 {
		t.Errorf("expected exactly one guard error event, got %d (events: %+v)", guardCount, events)
	}
	if terminalCount != 1 {
		t.Errorf("expected exactly one terminal event (guard error replaces adapter's llmtypes.EventDone), got %d (events: %+v)", terminalCount, events)
	}
	if len(events) == 0 || !llmtypes.IsTurnComplete(events[len(events)-1]) {
		t.Fatalf("expected last event to be turn-terminal; got: %+v", events)
	}
	if last := events[len(events)-1]; last.Type != llmtypes.EventError || last.Error != guardMsg {
		t.Errorf("expected the terminal event to be the guard llmtypes.EventError; got %+v", last)
	}
}

// TestSubprocessBridge_NoSilentDrop_NotFiredWhenDeltaPresent verifies the
// guard does NOT fire when the CLI mixed tool_use with at least one text
// delta — that's a normal stream and consumers can use the deltas.
func TestSubprocessBridge_NoSilentDrop_NotFiredWhenDeltaPresent(t *testing.T) {
	script := providertest.New(t, "claude", providertest.Lines(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"do_thing","input":{}},{"type":"text","text":"hello"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}`,
	)).Path

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for ev := range ch {
		if ev.Type == llmtypes.EventError && ev.Error == "CLI bridge cannot forward tool calls" {
			t.Errorf("guard fired but a delta was present in the stream")
		}
	}
}

// TestSubprocessBridge_GracePeriodOrdering verifies the SIGTERM-then-SIGKILL
// contract on context cancellation: the spawner sends SIGTERM first, waits at
// least WaitDelay for the process to exit, and only then sends SIGKILL.
//
// Three independent assertions:
//  1. Upper bound: drain elapsed ≤ WaitDelay + slack — proves SIGKILL eventually
//     fired (the process loops forever, so without SIGKILL we'd hang).
//  2. Lower bound: drain elapsed ≥ WaitDelay − tolerance — proves the bridge
//     actually waited the configured grace period instead of SIGKILL'ing
//     immediately after SIGTERM. This is the assertion Copilot flagged on the
//     first revision; the upper bound alone permitted a regression where
//     WaitDelay went un-wired.
//  3. SIGTERM-delivery: the fake recorded SIGTERM — proves SIGTERM, not
//     SIGKILL, was the first signal (SIGKILL is uncatchable, so nothing could
//     be recorded if SIGKILL came first).
func TestSubprocessBridge_GracePeriodOrdering(t *testing.T) {
	// A CLI that records SIGTERM and keeps running, so only SIGKILL ends it.
	fake := providertest.New(t, "claude", providertest.Script(
		providertest.Stdout(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"start"}]}}`),
		providertest.Hang(),
	).IgnoringSIGTERM())

	const waitDelay = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(WithWaitDelay(context.Background(), waitDelay))
	bridge := NewSubprocessBridge(NewClaudeAdapter(), fake.Path)
	ch, err := bridge.StreamChat(ctx, llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read the first event so we know the fake is running and handles SIGTERM.
	first := <-ch
	if first.Type != llmtypes.EventDelta {
		t.Fatalf("expected first event to be delta, got %s", first.Type)
	}

	start := time.Now()
	cancel()

	// Drain remaining events. Bridge must close the channel after the process
	// is fully terminated; if it hangs past WaitDelay+slack, SIGKILL didn't fire.
	for range ch {
	}
	elapsed := time.Since(start)

	// SIGTERM must have been delivered and recorded.
	if c := fake.Call(0); len(c.Signals) == 0 || c.Signals[0] != "SIGTERM" {
		t.Errorf("fake recorded signals %v, want SIGTERM first (process was likely SIGKILL'd immediately)", c.Signals)
	}
	// Lower bound: bridge must have waited at least WaitDelay (minus a small
	// tolerance for measurement jitter) before SIGKILL. The fake never
	// exits on its own, so elapsed is gated by WaitDelay alone — if WaitDelay were 0 or unwired, SIGKILL would fire immediately
	// and elapsed would be ~0.
	const tolerance = 50 * time.Millisecond
	if elapsed < waitDelay-tolerance {
		t.Errorf("drain took %v, expected ≥ WaitDelay (%v) − tolerance (%v); bridge SIGKILL'd before grace period elapsed", elapsed, waitDelay, tolerance)
	}
	// Upper bound: SIGKILL must have fired within WaitDelay+slack; otherwise
	// the bridge isn't escalating from SIGTERM to SIGKILL at all.
	const slack = 5 * time.Second
	if elapsed > waitDelay+slack {
		t.Errorf("drain took %v, expected ≤ WaitDelay (%v) + slack (%v)", elapsed, waitDelay, slack)
	}
}

// TestSubprocessBridge_SyntheticDoneOnCleanExit verifies the terminal-event
// contract: when the adapter doesn't emit llmtypes.EventDone (e.g. unstructured copilot
// output), the bridge synthesizes one on clean process exit so consumers always
// see an explicit boundary before the channel closes.
func TestSubprocessBridge_SyntheticDoneOnCleanExit(t *testing.T) {
	script := providertest.New(t, "claude", providertest.Lines("this is plain text", "another line")).Path

	bridge := NewSubprocessBridge(textPassthroughAdapter{}, script)
	ch, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []llmtypes.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) == 0 {
		t.Fatal("expected at least a synthetic llmtypes.EventDone, got no events")
	}
	last := events[len(events)-1]
	if !llmtypes.IsTurnComplete(last) {
		t.Errorf("last event must be turn-terminal; got %+v", last)
	}
	if last.Type != llmtypes.EventDone {
		t.Errorf("clean exit must synthesize llmtypes.EventDone, got %q", last.Type)
	}
}

func TestSubprocessBridge_ContextCancellation(t *testing.T) {
	script := providertest.New(t, "claude", providertest.Script(
		providertest.Stdout(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"start"}]}}`),
		providertest.Sleep(30*time.Second),
		providertest.Stdout(`{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}`),
	)).Path

	ctx, cancel := context.WithCancel(context.Background())
	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(ctx, llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read the first event, then cancel.
	ev := <-ch
	if ev.Type != "delta" {
		t.Errorf("expected first event to be delta, got %s", ev.Type)
	}
	cancel()

	// Drain remaining events — should get error or channel close.
	for ev := range ch {
		_ = ev
	}
	// If we get here without hanging, the test passed.
}
