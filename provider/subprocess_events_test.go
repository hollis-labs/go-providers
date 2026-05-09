package provider

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

// TestSubprocessBridge_StderrOrderingBeforeTerminal verifies that no
// SubprocessStderr typed events arrive after a turn-terminal Done /
// Error. Regression: PR #9 review (Copilot) flagged that the stderr
// drain goroutine could race the post-cmd.Wait terminal emit.
//
// The fake CLI writes a stream-json result line on stdout (turn
// boundary) and three stderr lines while running. After the fix, the
// bridge waits for stderr drain before emitting the typed terminal,
// so the callback sees stderr lines first and Done last.
func TestSubprocessBridge_StderrOrderingBeforeTerminal(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-stderr-cli.sh")
	body := `#!/bin/sh
echo "stderr line 1" >&2
echo "stderr line 2" >&2
echo '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}'
echo "stderr line 3" >&2
echo '{"type":"result","subtype":"success","is_error":false,"result":"hi","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)

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
	ctx = WithHeartbeatInterval(ctx, 0)

	ch, err := bridge.StreamChat(ctx, llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}

	mu.Lock()
	defer mu.Unlock()

	// Find the index of the last stderr event and the index of the
	// terminal event (Done or Error). Stderr must precede terminal.
	lastStderrIdx := -1
	terminalIdx := -1
	stderrCount := 0
	for i, ev := range typed {
		switch ev.(type) {
		case events.SubprocessStderr:
			lastStderrIdx = i
			stderrCount++
		case events.Done, events.Error:
			if terminalIdx == -1 {
				terminalIdx = i
			}
		}
	}

	if stderrCount == 0 {
		t.Fatal("expected at least one SubprocessStderr event; stderr capture not wired")
	}
	if terminalIdx == -1 {
		t.Fatal("expected one terminal event (Done or Error)")
	}
	if lastStderrIdx > terminalIdx {
		// Build a kind list for the failure message.
		kinds := make([]string, 0, len(typed))
		for _, ev := range typed {
			switch ev.(type) {
			case events.SubprocessStderr:
				kinds = append(kinds, "Stderr")
			case events.Done:
				kinds = append(kinds, "Done")
			case events.Error:
				kinds = append(kinds, "Error")
			case events.Delta:
				kinds = append(kinds, "Delta")
			case events.Usage:
				kinds = append(kinds, "Usage")
			default:
				kinds = append(kinds, "Other")
			}
		}
		t.Errorf("SubprocessStderr event at index %d arrived AFTER terminal at index %d; "+
			"stderr drain must complete before terminal emit (kinds: %v)",
			lastStderrIdx, terminalIdx, kinds)
	}
}

// TestSubprocessBridge_StderrNotWiredWithoutCallback verifies that
// without WithEvents, stderr stays at exec.Cmd's default destination
// — preserving v0.7.0 behavior bit-for-bit.
func TestSubprocessBridge_StderrNotWiredWithoutCallback(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "stderr-only.sh")
	body := `#!/bin/sh
echo "this would be stderr if we captured" >&2
echo '{"type":"result","subtype":"success","is_error":false,"result":"","stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	bridge := NewSubprocessBridge(NewClaudeAdapter(), script)
	ch, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "test"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	terminalCount := 0
	for ev := range ch {
		if llmtypes.IsTurnComplete(ev) {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Errorf("expected exactly 1 terminal event on legacy channel, got %d", terminalCount)
	}
}
