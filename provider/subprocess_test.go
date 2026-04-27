package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		case "session_id":
			hasSessionID = true
		case "delta":
			deltaCount++
		case "done":
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
// the failure.
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
	guardIdx, doneIdx, guardCount := -1, -1, 0
	for i, ev := range events {
		if ev.Type == "error" && ev.Error == guardMsg {
			guardCount++
			if guardIdx == -1 {
				guardIdx = i
			}
		}
		if ev.Type == "done" && doneIdx == -1 {
			doneIdx = i
		}
	}

	if guardCount != 1 {
		t.Errorf("expected exactly one guard error event, got %d (events: %+v)", guardCount, events)
	}
	if guardIdx == -1 {
		t.Fatalf("expected guard error event %q, none found in: %+v", guardMsg, events)
	}
	if doneIdx == -1 {
		t.Fatalf("expected done event, not found in: %+v", events)
	}
	if guardIdx >= doneIdx {
		t.Errorf("guard error must come before done; guard at %d, done at %d", guardIdx, doneIdx)
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
		if ev.Type == "error" && ev.Error == "CLI bridge cannot forward tool calls" {
			t.Errorf("guard fired but a delta was present in the stream")
		}
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
