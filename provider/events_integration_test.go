//go:build !windows

package provider

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider/events"
)

// TestPTYBridge_TypedEvents_EndToEnd spawns a fake claude-shaped CLI
// that emits known stream-json over a PTY and verifies typed events
// arrive via the WithEvents callback in the expected order, alongside
// the legacy StreamEvent channel which must continue to work.
func TestPTYBridge_TypedEvents_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	stream := `{"type":"system","subtype":"init","session_id":"s_42"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello!"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"path":"/x"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"file body","is_error":false}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done!"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Done!","stop_reason":"end_turn","usage":{"input_tokens":42,"output_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}
`
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'EOF'\n"+stream+"EOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	bridge := NewPTYBridgeWithAdapter(NewClaudeAdapter(), script)

	var (
		mu     sync.Mutex
		typed  []events.Event
	)
	cb := func(ev events.Event) {
		mu.Lock()
		typed = append(typed, ev)
		mu.Unlock()
	}

	ctx := WithEvents(context.Background(), cb)
	ctx = WithHeartbeatInterval(ctx, 0) // disable heartbeat noise for this test

	ch, err := bridge.StreamChat(ctx, ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var legacy []StreamEvent
	for ev := range ch {
		legacy = append(legacy, ev)
	}

	// Legacy channel must still work — at least one terminal event.
	terminalCount := 0
	for _, ev := range legacy {
		if IsTurnComplete(ev) {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Errorf("legacy channel: expected 1 terminal event, got %d (events: %+v)", terminalCount, legacy)
	}

	mu.Lock()
	defer mu.Unlock()

	// Typed callback should have observed: SessionID, Delta, ToolUse, ToolResult, Delta, Usage, Done.
	wantKinds := []string{"SessionID", "Delta", "ToolUse", "ToolResult", "Delta", "Usage", "Done"}
	gotKinds := make([]string, 0, len(typed))
	for _, ev := range typed {
		switch ev.(type) {
		case events.SessionID:
			gotKinds = append(gotKinds, "SessionID")
		case events.Delta:
			gotKinds = append(gotKinds, "Delta")
		case events.ToolUse:
			gotKinds = append(gotKinds, "ToolUse")
		case events.ToolResult:
			gotKinds = append(gotKinds, "ToolResult")
		case events.Usage:
			gotKinds = append(gotKinds, "Usage")
		case events.Done:
			gotKinds = append(gotKinds, "Done")
		case events.Error:
			gotKinds = append(gotKinds, "Error")
		case events.SubagentSpawn:
			gotKinds = append(gotKinds, "SubagentSpawn")
		default:
			gotKinds = append(gotKinds, "Other")
		}
	}

	// SessionID may or may not be observed depending on whether the
	// PTY init event reaches the parser — assert presence of every
	// required typed kind, allowing extra interleaved events.
	requireKindsInOrder(t, wantKinds, gotKinds)

	// ToolUse args should be the full input, not fingerprinted (default off).
	for _, ev := range typed {
		if tu, ok := ev.(events.ToolUse); ok {
			if tu.Fingerprint {
				t.Errorf("default ctx should produce non-fingerprinted ToolUse")
			}
			if tu.Args["path"] != "/x" {
				t.Errorf("ToolUse Args path: want /x, got %v", tu.Args["path"])
			}
		}
	}
}

func TestPTYBridge_TypedEvents_FingerprintMode(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	stream := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"path":"/secrets/token"}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"hi","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}
`
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'EOF'\n"+stream+"EOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	bridge := NewPTYBridgeWithAdapter(NewClaudeAdapter(), script)

	var (
		mu    sync.Mutex
		typed []events.Event
	)
	cb := func(ev events.Event) {
		mu.Lock()
		typed = append(typed, ev)
		mu.Unlock()
	}

	ctx := WithEvents(context.Background(), cb)
	ctx = WithToolArgFingerprint(ctx, true)
	ctx = WithHeartbeatInterval(ctx, 0)

	ch, err := bridge.StreamChat(ctx, ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}

	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, ev := range typed {
		if tu, ok := ev.(events.ToolUse); ok {
			found = true
			if !tu.Fingerprint {
				t.Errorf("expected Fingerprint=true under WithToolArgFingerprint")
			}
			if v, _ := tu.Args["path"].(string); len(v) < 7 || v[:7] != "sha256:" {
				t.Errorf("path arg should be sha256-hashed, got %v", tu.Args["path"])
			}
		}
	}
	if !found {
		t.Errorf("expected at least one ToolUse, got events: %v", typed)
	}
}

func TestPTYBridge_TypedEvents_BackwardCompat(t *testing.T) {
	// When WithEvents is NOT in context, behavior matches v0.7.0 exactly:
	// StreamEvent channel only, no callback, no stderr capture.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	stream := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"hi","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}
`
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'EOF'\n"+stream+"EOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	bridge := NewPTYBridgeWithAdapter(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var legacy []StreamEvent
	for ev := range ch {
		legacy = append(legacy, ev)
	}

	if len(legacy) == 0 {
		t.Fatal("expected at least one StreamEvent on legacy channel")
	}
	if !IsTurnComplete(legacy[len(legacy)-1]) {
		t.Errorf("last legacy event must be turn-terminal: %+v", legacy)
	}
}

// requireKindsInOrder asserts that each item in want appears in got in
// order (allowing interleaved extras). Reports the first missing item
// to make failures easy to read.
func requireKindsInOrder(t *testing.T, want, got []string) {
	t.Helper()
	i := 0
	for _, kind := range got {
		if i < len(want) && kind == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Errorf("want kinds in order %v, got %v (matched up to index %d)", want, got, i)
	}
}

// Sanity check that the integration test does not hang.
func TestPTYBridge_TypedEvents_DoesNotHang(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"\",\"stop_reason\":\"end_turn\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	bridge := NewPTYBridgeWithAdapter(NewClaudeAdapter(), script)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := bridge.StreamChat(WithEvents(ctx, func(events.Event) {}), ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}
}
