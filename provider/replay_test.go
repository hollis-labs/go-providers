package provider

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/go-providers/providertest"
)

// These tests replay captured CLI output through a fake binary and the
// subprocess bridge, so each adapter is checked against what its CLI
// really writes, argv and process exit included. The fixtures are
// described in providertest/fixtures/README.md.

const unknownSessionID = "00000000-0000-4000-8000-0000000000ff"

// turnResult is what one bridged turn produced.
type turnResult struct {
	events []llmtypes.StreamEvent
	stderr []string
}

func (r turnResult) text() string {
	var sb strings.Builder
	for _, ev := range r.events {
		if ev.Type == llmtypes.EventDelta {
			sb.WriteString(ev.Content)
		}
	}
	return sb.String()
}

func (r turnResult) first(typ llmtypes.EventType) (llmtypes.StreamEvent, bool) {
	for _, ev := range r.events {
		if ev.Type == typ {
			return ev, true
		}
	}
	return llmtypes.StreamEvent{}, false
}

func (r turnResult) terminal(t *testing.T) llmtypes.StreamEvent {
	t.Helper()
	if len(r.events) == 0 || !llmtypes.IsTurnComplete(r.events[len(r.events)-1]) {
		t.Fatalf("turn did not end with a terminal event: %+v", r.events)
	}
	return r.events[len(r.events)-1]
}

// runTurn drives one turn through a subprocess bridge, resuming
// sessionID when it is set, and collects the stream and the stderr lines.
func runTurn(t *testing.T, adapter CLIAdapter, cliPath, sessionID string) turnResult {
	t.Helper()
	var (
		mu  sync.Mutex
		res turnResult
	)
	ctx := WithEvents(context.Background(), func(ev events.Event) {
		if s, ok := ev.(events.SubprocessStderr); ok {
			mu.Lock()
			res.stderr = append(res.stderr, s.Line)
			mu.Unlock()
		}
	})
	ctx = WithHeartbeatInterval(ctx, 0)
	if sessionID != "" {
		ctx = WithCLISessionID(ctx, sessionID)
	}
	ch, err := NewSubprocessBridge(adapter, cliPath).StreamChat(ctx, llmtypes.ChatRequest{Messages: []llmtypes.ChatMessage{
		{Role: "user", Content: "say hi"},
	}})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for ev := range ch {
		res.events = append(res.events, ev)
	}
	mu.Lock()
	defer mu.Unlock()
	return res
}

func TestReplay_ClaudePrint(t *testing.T) {
	fake := providertest.New(t, "claude",
		providertest.Replay("claude/print_turn1"),
		providertest.Replay("claude/print_turn2_resume"),
		providertest.Replay("claude/print_resume_unknown_id"),
		providertest.Replay("claude/print_tool_use"),
		providertest.Replay("claude/print_tool_denied"),
		providertest.Replay("claude/print_error_unknown_model"),
	)
	adapter := NewClaudeAdapter()

	turn1 := runTurn(t, adapter, fake.Path, "")
	sid, ok := turn1.first(llmtypes.EventSessionID)
	if !ok || sid.SessionID == "" {
		t.Fatalf("turn 1 reported no session id: %+v", turn1.events)
	}
	if turn1.terminal(t).Type != llmtypes.EventDone || turn1.text() == "" {
		t.Errorf("turn 1: text %q, terminal %+v", turn1.text(), turn1.terminal(t))
	}
	if fake.Call(0).HasArg("--resume") {
		t.Errorf("first turn passed --resume: %q", fake.Call(0).Args)
	}

	turn2 := runTurn(t, adapter, fake.Path, sid.SessionID)
	if got, _ := fake.Call(1).ArgAfter("--resume"); got != sid.SessionID {
		t.Errorf("resume turn passed --resume %q, want %q", got, sid.SessionID)
	}
	if again, _ := turn2.first(llmtypes.EventSessionID); again.SessionID != sid.SessionID {
		t.Errorf("resume reported session %q, want %q", again.SessionID, sid.SessionID)
	}
	if turn2.terminal(t).Type != llmtypes.EventDone {
		t.Errorf("resume turn terminal = %+v", turn2.terminal(t))
	}

	lost := runTurn(t, adapter, fake.Path, unknownSessionID)
	if lost.terminal(t).Type != llmtypes.EventError {
		t.Errorf("unknown resume id: terminal = %+v, want an error", lost.terminal(t))
	}
	if !strings.Contains(strings.Join(lost.stderr, "\n"), "No conversation found with session ID") {
		t.Errorf("unknown resume id: stderr = %q", lost.stderr)
	}
	if !adapter.IsSessionLost([]byte(strings.Join(lost.stderr, "\n"))) {
		t.Errorf("IsSessionLost(%q) = false, want the lost resume id classified", lost.stderr)
	}
	for name, ok := range map[string]turnResult{"turn 1": turn1, "resume turn": turn2} {
		if adapter.IsSessionLost([]byte(strings.Join(ok.stderr, "\n"))) {
			t.Errorf("IsSessionLost(%s stderr %q) = true", name, ok.stderr)
		}
	}

	tool := runTurn(t, adapter, fake.Path, "")
	if tu, ok := tool.first(llmtypes.EventToolUse); !ok || tu.ToolUse == nil || tu.ToolUse.Name != "Bash" {
		t.Errorf("tool turn: no Bash tool_use in %+v", tool.events)
	}
	if tool.terminal(t).Type != llmtypes.EventDone || !strings.Contains(tool.text(), "providertest") {
		t.Errorf("tool turn: text %q, terminal %+v", tool.text(), tool.terminal(t))
	}

	denied := runTurn(t, adapter, fake.Path, "")
	if _, ok := denied.first(llmtypes.EventToolUse); !ok || denied.terminal(t).Type != llmtypes.EventDone {
		t.Errorf("denied tool turn: %+v", denied.events)
	}

	badModel := runTurn(t, adapter, fake.Path, "")
	if badModel.terminal(t).Type != llmtypes.EventError {
		t.Errorf("unknown model: terminal = %+v, want an error", badModel.terminal(t))
	}
}

func TestReplay_CodexExec(t *testing.T) {
	fake := providertest.New(t, "codex",
		providertest.Replay("codex/exec_turn1"),
		providertest.Replay("codex/exec_resume_unknown_id"),
		providertest.Replay("codex/exec_tool_use"),
		providertest.Replay("codex/exec_error_unknown_model"),
	)
	adapter := NewCodexAdapter()

	// The exec adapter neither reports thread.started's thread_id nor
	// builds resume argv; exec mode is single-turn.
	turn1 := runTurn(t, adapter, fake.Path, "")
	if turn1.terminal(t).Type != llmtypes.EventDone || turn1.text() == "" {
		t.Errorf("turn 1: text %q, terminal %+v", turn1.text(), turn1.terminal(t))
	}
	if c := fake.Call(0); c.Args[0] != "exec" || !c.HasArg("--json") {
		t.Errorf("argv = %q", c.Args)
	}

	// The fixture is `codex exec resume <unknown id>`; with no resume argv
	// in the adapter, this checks only how the bridge surfaces the failure.
	// CodexAdapter has no IsSessionLost on purpose: exec does not resume
	// (see the codex descriptor in package registry).
	lost := runTurn(t, adapter, fake.Path, "")
	if lost.terminal(t).Type != llmtypes.EventError {
		t.Errorf("unknown thread: terminal = %+v, want an error", lost.terminal(t))
	}
	if !strings.Contains(strings.Join(lost.stderr, "\n"), "no rollout found for thread id") {
		t.Errorf("unknown thread: stderr = %q", lost.stderr)
	}

	tool := runTurn(t, adapter, fake.Path, "")
	if tool.terminal(t).Type != llmtypes.EventDone || !strings.Contains(tool.text(), "providertest") {
		t.Errorf("tool turn: text %q, terminal %+v", tool.text(), tool.terminal(t))
	}

	badModel := runTurn(t, adapter, fake.Path, "")
	if badModel.terminal(t).Type != llmtypes.EventError {
		t.Errorf("unknown model: terminal = %+v, want an error", badModel.terminal(t))
	}
}

func TestReplay_OpencodeRun(t *testing.T) {
	fake := providertest.New(t, "opencode",
		providertest.Replay("opencode/run_turn1"),
		providertest.Replay("opencode/run_turn2_resume").When("--session"),
		providertest.Replay("opencode/run_tool_use"),
		providertest.Replay("opencode/run_error_unknown_model"),
	)
	adapter := NewOpencodeAdapter()

	turn1 := runTurn(t, adapter, fake.Path, "")
	sid, ok := turn1.first(llmtypes.EventSessionID)
	if !ok || sid.SessionID == "" || turn1.terminal(t).Type != llmtypes.EventDone {
		t.Fatalf("turn 1: %+v", turn1.events)
	}
	turn2 := runTurn(t, adapter, fake.Path, sid.SessionID)
	if got, _ := fake.Call(1).ArgAfter("--session"); got != sid.SessionID {
		t.Errorf("resume passed --session %q, want %q", got, sid.SessionID)
	}
	if turn2.terminal(t).Type != llmtypes.EventDone || turn2.text() == "" {
		t.Errorf("resume turn: %+v", turn2.events)
	}
	tool := runTurn(t, adapter, fake.Path, "")
	if _, ok := tool.first(llmtypes.EventToolUse); !ok || tool.terminal(t).Type != llmtypes.EventDone {
		t.Errorf("tool turn: %+v", tool.events)
	}

	// opencode 1.18.33 reports a model it cannot resolve as a generic
	// UnknownError; the error's name and ref are what tell it from a server
	// fault, so they must reach the surfaced error (CW-20261001-0122).
	badModel := runTurn(t, adapter, fake.Path, "")
	if got := badModel.terminal(t); got.Type != llmtypes.EventError || got.Error != "Unexpected server error. Check server logs for details. (UnknownError, ref err_7707db6c)" {
		t.Errorf("unknown model: terminal = %+v", got)
	}
}

func TestReplay_AntigravityPrint(t *testing.T) {
	fake := providertest.New(t, "antigravity",
		providertest.Replay("antigravity/print_turn1"),
		providertest.Replay("antigravity/print_resume_unknown_id"),
	)
	adapter := NewAntigravityAdapter()

	turn1 := runTurn(t, adapter, fake.Path, "")
	sid, ok := turn1.first(llmtypes.EventSessionID)
	if !ok || sid.SessionID == "" || turn1.terminal(t).Type != llmtypes.EventDone {
		t.Fatalf("turn 1: %+v", turn1.events)
	}

	// agy answers an unknown conversation id with a new conversation, a
	// stderr warning and exit 0: the turn succeeds under another id, and
	// only the classifier and the resume verifier catch it.
	lost := runTurn(t, adapter, fake.Path, unknownSessionID)
	if got, _ := fake.Call(1).ArgAfter("--conversation"); got != unknownSessionID {
		t.Errorf("resume argv = %q, want --conversation %s", fake.Call(1).Args, unknownSessionID)
	}
	if lost.terminal(t).Type != llmtypes.EventDone {
		t.Errorf("unknown conversation: terminal = %+v, want done", lost.terminal(t))
	}
	if !adapter.IsSessionLost([]byte(strings.Join(lost.stderr, "\n"))) {
		t.Errorf("IsSessionLost(%q) = false", lost.stderr)
	}
	if again, _ := lost.first(llmtypes.EventSessionID); again.SessionID == unknownSessionID {
		t.Errorf("agy kept the unknown id %q; the fixture says it starts a new conversation", again.SessionID)
	}
}

// Over streaming stdio, an unknown --resume id ends the session with the
// same stderr line as print mode (claude/stream_resume_unknown_id).
func TestReplay_ClaudeStreamingLostSessionIsClassified(t *testing.T) {
	fake := providertest.New(t, "claude", providertest.Replay("claude/stream_resume_unknown_id"))
	cmd := exec.Command(fake.Path, "--resume", unknownSessionID)
	cmd.Stdin = strings.NewReader(`{"type":"user","message":{"role":"user","content":"say hi"}}` + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("lost session exited 0; claude exits 1")
	}
	if !NewClaudeAdapter().IsSessionLost(stderr.Bytes()) {
		t.Fatalf("IsSessionLost(%q) = false", stderr.String())
	}
}
