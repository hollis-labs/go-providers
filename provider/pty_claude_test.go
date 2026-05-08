package provider

import (
	"reflect"
	"slices"
	"testing"
)

// printModeForbiddenInPTY are flags that must never appear in PTY-mode argv.
// claude's interactive (TUI) entrypoint rejects --print/-p outright; the
// other three are only meaningful for stream-json subprocess-per-turn use.
var printModeForbiddenInPTY = []string{
	"-p",
	"--print",
	"--system-prompt",
	"--output-format",
	"--verbose",
}

func assertNoPrintModeFlags(t *testing.T, args []string) {
	t.Helper()
	for _, forbidden := range printModeForbiddenInPTY {
		if slices.Contains(args, forbidden) {
			t.Errorf("PTY-mode args must not contain %q; got %v", forbidden, args)
		}
	}
}

func TestClaudeBuildArgs_PTY_Empty(t *testing.T) {
	a := &ClaudeAdapter{PTY: true}
	args := a.BuildArgs("", "", "")
	if len(args) != 0 {
		t.Errorf("PTY mode with no resume + no skip-permissions: expected empty args, got %v", args)
	}
	assertNoPrintModeFlags(t, args)
}

func TestClaudeBuildArgs_PTY_SkipPermissions(t *testing.T) {
	a := &ClaudeAdapter{PTY: true, SkipPermissions: true}
	args := a.BuildArgs("", "", "")
	want := []string{"--dangerously-skip-permissions"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
	assertNoPrintModeFlags(t, args)
}

func TestClaudeBuildArgs_PTY_Resume(t *testing.T) {
	a := &ClaudeAdapter{PTY: true}
	args := a.BuildArgs("", "", "sess-abc")
	want := []string{"--resume", "sess-abc"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
	assertNoPrintModeFlags(t, args)
}

func TestClaudeBuildArgs_PTY_ResumeAndSkipPermissions(t *testing.T) {
	a := &ClaudeAdapter{PTY: true, SkipPermissions: true}
	args := a.BuildArgs("", "", "sess-abc")
	want := []string{"--resume", "sess-abc", "--dangerously-skip-permissions"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
	assertNoPrintModeFlags(t, args)
}

func TestClaudeBuildArgs_PTY_IgnoresPromptAndSystemPrompt(t *testing.T) {
	// Prompt and systemPrompt must NOT leak into PTY-mode args. The contract
	// is that per-turn payloads arrive over PTY stdin and system prompts
	// route via BootPrompt at the lib layer.
	a := &ClaudeAdapter{PTY: true}
	args := a.BuildArgs("hello", "you are a helpful assistant", "")
	if len(args) != 0 {
		t.Errorf("PTY mode must ignore prompt/systemPrompt; got %v", args)
	}
	assertNoPrintModeFlags(t, args)
}

func TestClaudeBuildArgs_PrintMode_NoSystemPromptNoResume(t *testing.T) {
	a := &ClaudeAdapter{}
	args := a.BuildArgs("hello", "", "")
	want := []string{"-p", "hello", "--output-format", "stream-json", "--verbose"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
}

func TestClaudeBuildArgs_PrintMode_WithSystemPrompt(t *testing.T) {
	a := &ClaudeAdapter{}
	args := a.BuildArgs("hello", "sys", "")
	want := []string{"-p", "hello", "--output-format", "stream-json", "--verbose", "--system-prompt", "sys"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
}

func TestClaudeBuildArgs_PrintMode_WithResumeIgnoresSystemPrompt(t *testing.T) {
	// Existing behavior: --resume path does NOT also send --system-prompt.
	// Pin this so the PTY refactor doesn't accidentally change it.
	a := &ClaudeAdapter{}
	args := a.BuildArgs("hello", "sys", "sess-x")
	want := []string{"--resume", "sess-x", "-p", "hello", "--output-format", "stream-json", "--verbose"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
}

func TestClaudeBuildArgs_PrintMode_DevSkipPermissions(t *testing.T) {
	a := &ClaudeAdapter{SkipPermissions: true}
	args := a.BuildArgs("hello", "", "")
	want := []string{"-p", "hello", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("expected %v, got %v", want, args)
	}
}

func TestClaudeAdapterConstructors_Defaults(t *testing.T) {
	if a := NewClaudeAdapter(); a.PTY || a.SkipPermissions {
		t.Errorf("NewClaudeAdapter: expected zero-value, got %+v", *a)
	}
	if a := NewClaudeAdapterDev(); a.PTY || !a.SkipPermissions {
		t.Errorf("NewClaudeAdapterDev: expected SkipPermissions only, got %+v", *a)
	}
	if a := NewClaudeAdapterPTY(); !a.PTY || a.SkipPermissions {
		t.Errorf("NewClaudeAdapterPTY: expected PTY only, got %+v", *a)
	}
	if a := NewClaudeAdapterDevPTY(); !a.PTY || !a.SkipPermissions {
		t.Errorf("NewClaudeAdapterDevPTY: expected both, got %+v", *a)
	}
}

func TestParseClaudeStreamLine_Empty(t *testing.T) {
	events, err := parseClaudeStreamLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_SystemEvent(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","cwd":"/tmp","session_id":"abc","tools":["Read","Write"],"model":"claude-opus-4-6"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for system init, got %d", len(events))
	}
	if events[0].Type != "session_id" {
		t.Errorf("expected type=session_id, got %s", events[0].Type)
	}
	if events[0].SessionID != "abc" {
		t.Errorf("expected SessionID=abc, got %s", events[0].SessionID)
	}
}

func TestParseClaudeStreamLine_SystemEventNonInit(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"api_retry","session_id":"abc"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for non-init system event, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_SystemEventNoSessionID(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","cwd":"/tmp"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for init without session_id, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_AssistantText(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello there!"}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("expected type=delta, got %s", events[0].Type)
	}
	if events[0].Content != "Hello there!" {
		t.Errorf("expected content='Hello there!', got %q", events[0].Content)
	}
}

func TestParseClaudeStreamLine_AssistantToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_123","name":"Read","input":{"file_path":"/tmp/test.txt"}}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "tool_use" {
		t.Errorf("expected type=tool_use, got %s", events[0].Type)
	}
	if events[0].ToolUse == nil {
		t.Fatal("expected non-nil ToolUse")
	}
	if events[0].ToolUse.Name != "Read" {
		t.Errorf("expected tool name=Read, got %s", events[0].ToolUse.Name)
	}
	if events[0].ToolUse.ID != "tu_123" {
		t.Errorf("expected tool id=tu_123, got %s", events[0].ToolUse.ID)
	}
	fp, ok := events[0].ToolUse.Input["file_path"]
	if !ok || fp != "/tmp/test.txt" {
		t.Errorf("expected input file_path=/tmp/test.txt, got %v", fp)
	}
}

func TestParseClaudeStreamLine_AssistantMixed(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me read that file."},{"type":"tool_use","id":"tu_456","name":"Bash","input":{"command":"ls"}}]}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "delta" {
		t.Errorf("events[0]: expected type=delta, got %s", events[0].Type)
	}
	if events[1].Type != "tool_use" {
		t.Errorf("events[1]: expected type=tool_use, got %s", events[1].Type)
	}
}

func TestParseClaudeStreamLine_ResultSuccess(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Done.","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":500,"cache_read_input_tokens":0}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (usage + done), got %d", len(events))
	}
	if events[0].Type != "usage" {
		t.Errorf("events[0]: expected type=usage, got %s", events[0].Type)
	}
	if events[0].Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if events[0].Usage.InputTokens != 100 {
		t.Errorf("expected input_tokens=100, got %d", events[0].Usage.InputTokens)
	}
	if events[0].Usage.OutputTokens != 20 {
		t.Errorf("expected output_tokens=20, got %d", events[0].Usage.OutputTokens)
	}
	if events[0].Usage.CacheCreationTokens != 500 {
		t.Errorf("expected cache_creation_tokens=500, got %d", events[0].Usage.CacheCreationTokens)
	}
	if events[0].Usage.StopReason != "end_turn" {
		t.Errorf("expected stop_reason=end_turn, got %s", events[0].Usage.StopReason)
	}
	if events[1].Type != "done" {
		t.Errorf("events[1]: expected type=done, got %s", events[1].Type)
	}
}

func TestParseClaudeStreamLine_ResultError(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"error","is_error":true,"result":"Something went wrong"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "error" {
		t.Errorf("expected type=error, got %s", events[0].Type)
	}
	if events[0].Error != "Something went wrong" {
		t.Errorf("expected error message, got %q", events[0].Error)
	}
}

func TestParseClaudeStreamLine_TopLevelError(t *testing.T) {
	line := []byte(`{"type":"error","error":{"message":"API key invalid"}}`)
	events, err := parseClaudeStreamLine(line)
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
		t.Errorf("expected error='API key invalid', got %q", events[0].Error)
	}
}

func TestParseClaudeStreamLine_RateLimitEvent(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for rate_limit_event, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_UnknownType(t *testing.T) {
	line := []byte(`{"type":"future_event","data":"something"}`)
	events, err := parseClaudeStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
}

func TestParseClaudeStreamLine_InvalidJSON(t *testing.T) {
	line := []byte(`not valid json`)
	_, err := parseClaudeStreamLine(line)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
