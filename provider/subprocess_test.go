package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	_, err := bridge.StreamChat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestSubprocessBridge_StreamChat_SystemOnly(t *testing.T) {
	bridge := NewSubprocessBridge(NewClaudeAdapter(), "/usr/bin/echo")
	_, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "system", Content: "test"},
	}})
	if err == nil {
		t.Fatal("expected error for no user message")
	}
}

func TestSubprocessBridge_Complete_MockCLI(t *testing.T) {
	// Create a mock CLI script that outputs stream-json events.
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from subprocess!"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Hello from subprocess!","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	result, err := bridge.Complete(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test prompt"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello from subprocess!") {
		t.Errorf("expected 'Hello from subprocess!' in result, got: %s", result)
	}
}

func TestSubprocessBridge_StreamChat_MockCLI(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"system","subtype":"init","cwd":"/tmp","session_id":"sess-abc"}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta one"}]}}'
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"delta two"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: session_id, delta, delta, usage, done
	hasSessionID := false
	deltaCount := 0
	hasDone := false
	for _, ev := range events {
		switch ev.Type {
		case EventSessionID:
			hasSessionID = true
		case EventDelta:
			deltaCount++
		case EventDone:
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
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-cli.sh")
	// Script outputs the working directory as a delta so we can verify sandbox dir was set.
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"$(pwd)\"}]}}"
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	sandboxDir := t.TempDir()
	ctx := WithSandboxDir(context.Background(), sandboxDir)

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	result, err := bridge.Complete(ctx, ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, sandboxDir) {
		t.Errorf("expected sandbox dir %s in result, got: %s", sandboxDir, result)
	}
}

// TestSubprocessBridge_NoSilentDrop_ToolUseOnly verifies that when the CLI
// emits only tool_use blocks (no text deltas), the bridge injects an
// explicit "CLI bridge cannot forward tool calls" error event *before* the
// terminal "done" event so consumers that stop reading at "done" still see
// the failure as the SOLE terminal event. Per the IsTurnComplete contract,
// EventError and EventDone are mutually exclusive — the bridge must NOT
// forward the adapter's EventDone after the guard fires.
func TestSubprocessBridge_NoSilentDrop_ToolUseOnly(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tool-only-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"do_thing","input":{}}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	const guardMsg = "CLI bridge cannot forward tool calls"
	guardCount, terminalCount := 0, 0
	for _, ev := range events {
		if ev.Type == EventError && ev.Error == guardMsg {
			guardCount++
		}
		if IsTurnComplete(ev) {
			terminalCount++
		}
	}

	if guardCount != 1 {
		t.Errorf("expected exactly one guard error event, got %d (events: %+v)", guardCount, events)
	}
	if terminalCount != 1 {
		t.Errorf("expected exactly one terminal event (guard error replaces adapter's EventDone), got %d (events: %+v)", terminalCount, events)
	}
	if len(events) == 0 || !IsTurnComplete(events[len(events)-1]) {
		t.Fatalf("expected last event to be turn-terminal; got: %+v", events)
	}
	if last := events[len(events)-1]; last.Type != EventError || last.Error != guardMsg {
		t.Errorf("expected the terminal event to be the guard EventError; got %+v", last)
	}
}

// TestSubprocessBridge_NoSilentDrop_NotFiredWhenDeltaPresent verifies the
// guard does NOT fire when the CLI mixed tool_use with at least one text
// delta — that's a normal stream and consumers can use the deltas.
func TestSubprocessBridge_NoSilentDrop_NotFiredWhenDeltaPresent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mixed-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"do_thing","input":{}},{"type":"text","text":"hello"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for ev := range ch {
		if ev.Type == EventError && ev.Error == "CLI bridge cannot forward tool calls" {
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
//  3. SIGTERM-delivery: trap-marker file exists — proves SIGTERM, not SIGKILL,
//     was the first signal (SIGKILL is uncatchable so the trap couldn't run if
//     SIGKILL came first).
func TestSubprocessBridge_GracePeriodOrdering(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "sigterm.marker")
	script := filepath.Join(dir, "stubborn-cli.sh")
	// The script must not let any child process inherit the stdout pipe;
	// otherwise the inherited fd keeps the pipe open after the parent shell
	// is SIGKILL'd, and cmd.Wait blocks until the child dies naturally.
	// Each `sleep` is redirected to /dev/null for stdin/stdout/stderr.
	scriptBody := `#!/bin/sh
trap 'touch "` + marker + `"' TERM
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"start"}]}}'
while true; do
    sleep 0.1 </dev/null >/dev/null 2>/dev/null
done
`
	if err := os.WriteFile(script, []byte(scriptBody), 0755); err != nil {
		t.Fatal(err)
	}

	const waitDelay = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(WithWaitDelay(context.Background(), waitDelay))
	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(ctx, ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read the first event so we know the script is running and has installed its trap.
	first := <-ch
	if first.Type != EventDelta {
		t.Fatalf("expected first event to be delta, got %s", first.Type)
	}

	start := time.Now()
	cancel()

	// Drain remaining events. Bridge must close the channel after the process
	// is fully terminated; if it hangs past WaitDelay+slack, SIGKILL didn't fire.
	for range ch {
	}
	elapsed := time.Since(start)

	// SIGTERM must have been delivered: the trap ran.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("SIGTERM marker file missing: %v (process was likely SIGKILL'd immediately)", err)
	}
	// Lower bound: bridge must have waited at least WaitDelay (minus a small
	// tolerance for measurement jitter) before SIGKILL. The script loops on a
	// 100ms sleep that never naturally exits, so elapsed is gated by WaitDelay
	// alone — if WaitDelay were 0 or unwired, SIGKILL would fire immediately
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
// contract: when the adapter doesn't emit EventDone (e.g. unstructured copilot
// output), the bridge synthesizes one on clean process exit so consumers always
// see an explicit boundary before the channel closes.
func TestSubprocessBridge_SyntheticDoneOnCleanExit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "text-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo "this is plain text"
echo "another line"
`), 0755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewCopilotAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) == 0 {
		t.Fatal("expected at least a synthetic EventDone, got no events")
	}
	last := events[len(events)-1]
	if !IsTurnComplete(last) {
		t.Errorf("last event must be turn-terminal; got %+v", last)
	}
	if last.Type != EventDone {
		t.Errorf("clean exit must synthesize EventDone, got %q", last.Type)
	}
}

func TestSubprocessBridge_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "slow-cli.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"start"}]}}'
sleep 30
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","stop_reason":"end_turn"}'
`), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(ctx, ChatRequest{Messages: []ChatMessage{
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
