package provider

import (
	"reflect"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

func TestParseCodexStreamLine_Empty(t *testing.T) {
	events, err := parseCodexStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseCodexStreamLine_AssistantMessage(t *testing.T) {
	line := []byte(`{"type":"item.message","role":"assistant","content":"","delta":"Hello from Codex!"}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello from Codex!" {
		t.Errorf("expected 'Hello from Codex!', got %q", events[0].Content)
	}
}

func TestParseCodexStreamLine_AssistantContent(t *testing.T) {
	line := []byte(`{"type":"item.message","role":"assistant","content":"Full content here","delta":""}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Content != "Full content here" {
		t.Errorf("expected 'Full content here', got %q", events[0].Content)
	}
}

func TestParseCodexStreamLine_UserMessage(t *testing.T) {
	line := []byte(`{"type":"item.message","role":"user","content":"ignored"}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for user message, got %d", len(events))
	}
}

func TestParseCodexStreamLine_TurnCompleted(t *testing.T) {
	line := []byte(`{"type":"turn.completed","turn_id":"abc","usage":{"input_tokens":12746,"cached_input_tokens":7552,"output_tokens":18,"reasoning_output_tokens":8}}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "usage" {
		t.Fatalf("expected type=usage, got %s", events[0].Type)
	}
	if events[0].Usage == nil || events[0].Usage.InputTokens != 12746 || events[0].Usage.OutputTokens != 18 || events[0].Usage.CacheReadTokens != 7552 {
		t.Fatalf("unexpected usage payload: %+v", events[0].Usage)
	}
	if events[1].Type != "done" {
		t.Errorf("expected type=done, got %s", events[1].Type)
	}
}

func TestParseCodexStreamLine_ItemCompleted(t *testing.T) {
	line := []byte(`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"telemetry probe ok"}}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Fatalf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "telemetry probe ok" {
		t.Fatalf("unexpected delta: %q", events[0].Content)
	}
}

func TestParseCodexStreamLine_Error(t *testing.T) {
	line := []byte(`{"type":"error","message":"API key invalid"}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "API key invalid" {
		t.Errorf("expected 'API key invalid', got %q", events[0].Error)
	}
}

// thread.started's thread id is the session id a resume turn passes back.
func TestParseCodexStreamLine_ThreadStarted(t *testing.T) {
	line := []byte(`{"type":"thread.started","thread_id":"xyz"}`)
	evs, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(evs) != 1 || evs[0].Type != llmtypes.EventSessionID || evs[0].SessionID != "xyz" {
		t.Errorf("thread.started = %+v, want one session id event for xyz", evs)
	}
	typed, err := NewCodexAdapter().ParseLineEvents(line)
	if err != nil {
		t.Fatalf("ParseLineEvents: %v", err)
	}
	if len(typed) != 1 || typed[0] != (events.SessionID{ID: "xyz"}) {
		t.Errorf("ParseLineEvents(thread.started) = %+v, want events.SessionID{xyz}", typed)
	}
	if evs, _ := parseCodexStreamLine([]byte(`{"type":"thread.started"}`)); len(evs) != 0 {
		t.Errorf("thread.started without an id = %+v, want no event", evs)
	}
}

func TestCodexAdapter_BuildArgs(t *testing.T) {
	a := NewCodexAdapter()
	args := a.BuildArgs("fix bug", "system prompt", "")
	// Codex has no system prompt flag; a first turn does not resume.
	// The prompt is last, after "--" (CW-20261001-0069).
	want := []string{"exec", "--json", "--skip-git-repo-check", "--", "fix bug"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

// A resume turn adds `resume <id>` after every exec option and before the
// prompt: --cd, -c and the extras stay in front of the subcommand, where
// codex applies them to the resumed turn (CW-20261001-0109).
func TestCodexAdapter_BuildArgs_Resume(t *testing.T) {
	a := &CodexAdapter{Model: "gpt-6-luna", ProjectDir: "/work/project", ExtraArgs: []string{"-c", `sandbox_mode="read-only"`}}
	got := a.BuildArgs("--dangerously-bypass-approvals-and-sandbox", "ignored", "thread-1")
	want := []string{"exec", "-c", `model="gpt-6-luna"`, "-c", `sandbox_mode="read-only"`, "--json", "--skip-git-repo-check",
		"--cd", "/work/project", "resume", "thread-1", "--", "--dangerously-bypass-approvals-and-sandbox"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resume argv\n got %q\nwant %q", got, want)
	}
	for _, arg := range a.BuildArgs("hi", "", "") {
		if arg == "resume" {
			t.Errorf("a first turn passed resume: %q", a.BuildArgs("hi", "", ""))
		}
	}
}

func TestCodexAdapter_BuildArgs_ExecMode_SkipsGitRepoCheck(t *testing.T) {
	// Pin: exec mode always runs against a throwaway, non-git BootDirSpec
	// tempdir. Without --skip-git-repo-check the real codex CLI refuses to
	// run non-interactively ("Not inside a trusted directory and
	// --skip-git-repo-check was not specified") on every single turn —
	// confirmed against a real codex-cli 0.147.0 binary. This must never
	// regress.
	a := NewCodexAdapter()
	args := a.BuildArgs("prompt", "system", "")
	found := false
	for _, arg := range args {
		if arg == "--skip-git-repo-check" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --skip-git-repo-check in exec-mode argv, got %v", args)
	}
}

func TestCodexAdapter_Defaults(t *testing.T) {
	if a := NewCodexAdapter(); a.Mode != "" {
		t.Errorf("NewCodexAdapter: expected Mode=\"\", got %q", a.Mode)
	}
	if a := NewCodexAdapterAppServer(); a.Mode != "app-server" {
		t.Errorf("NewCodexAdapterAppServer: expected Mode=\"app-server\", got %q", a.Mode)
	}
}

func TestCodexAdapter_AppServer_BuildArgs(t *testing.T) {
	a := NewCodexAdapterAppServer()
	args := a.BuildArgs("ignored prompt", "ignored system", "ignored session")
	if len(args) != 1 || args[0] != "app-server" {
		t.Errorf("expected [\"app-server\"], got %v", args)
	}
}

func TestCodexAdapter_AppServer_BuildArgs_IgnoresAllParams(t *testing.T) {
	// Pin: in app-server mode the per-turn prompt, systemPrompt, and
	// cliSessionID must not leak into argv. thread/start and thread/resume
	// are JSON-RPC methods, not CLI flags.
	a := NewCodexAdapterAppServer()
	args := a.BuildArgs("prompt that should not appear", "system that should not appear", "sess-that-should-not-appear")
	for _, arg := range args {
		switch arg {
		case "prompt that should not appear",
			"system that should not appear",
			"sess-that-should-not-appear",
			"exec", "--json", "--resume", "--skip-git-repo-check":
			t.Errorf("app-server mode leaked exec-mode arg %q: full args=%v", arg, args)
		}
	}
}

func TestCodexAdapter_AppServer_ParseLineIsPassThrough(t *testing.T) {
	// Pin: ParseLine returns (nil, nil) in app-server mode. JSON-RPC
	// framing lives in the consumer runtime, not in this adapter.
	a := NewCodexAdapterAppServer()
	jsonRPC := []byte(`{"jsonrpc":"2.0","id":1,"method":"thread/start","params":{}}`)
	events, err := a.ParseLine(jsonRPC)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events (pass-through), got %v", events)
	}
}

func TestCodexAdapter_ExecMode_ParseLineStillWorks(t *testing.T) {
	// Pin: regression guard for the default exec mode after introducing
	// the Mode field — ParseLine must still dispatch through
	// parseCodexStreamLine and surface deltas.
	a := NewCodexAdapter()
	line := []byte(`{"type":"item.message","role":"assistant","content":"","delta":"hi"}`)
	events, err := a.ParseLine(line)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(events) != 1 || events[0].Type != "delta" || events[0].Content != "hi" {
		t.Errorf("expected single delta event with content 'hi', got %v", events)
	}
}
