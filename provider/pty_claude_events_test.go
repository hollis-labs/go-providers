package provider

import (
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider/events"
)

func TestClaudeParseLineEvents_AssistantText(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello!"}]}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	d, ok := got[0].(events.Delta)
	if !ok {
		t.Fatalf("expected Delta, got %T", got[0])
	}
	if d.Text != "Hello!" {
		t.Errorf("Text mismatch: %q", d.Text)
	}
	if d.Phase != "narration" {
		t.Errorf("Phase=narration expected, got %q", d.Phase)
	}
}

func TestClaudeParseLineEvents_ToolUseAndSubagentSpawn(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Task","input":{"description":"sub-agent x"}}]}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events (ToolUse + SubagentSpawn), got %d", len(got))
	}
	tu, ok := got[0].(events.ToolUse)
	if !ok {
		t.Fatalf("idx 0: expected ToolUse, got %T", got[0])
	}
	if tu.Name != "Task" || tu.ID != "tu_1" {
		t.Errorf("ToolUse fields: %+v", tu)
	}
	if _, ok := got[1].(events.SubagentSpawn); !ok {
		t.Errorf("idx 1: expected SubagentSpawn, got %T", got[1])
	}
}

func TestClaudeParseLineEvents_ToolUseNonTaskDoesNotSpawn(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_2","name":"Read","input":{"path":"/x"}}]}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event (ToolUse only), got %d", len(got))
	}
	if _, ok := got[0].(events.ToolUse); !ok {
		t.Errorf("expected ToolUse, got %T", got[0])
	}
}

func TestClaudeParseLineEvents_UserToolResult_String(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"file contents here","is_error":false}]}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	tr, ok := got[0].(events.ToolResult)
	if !ok {
		t.Fatalf("expected ToolResult, got %T", got[0])
	}
	if tr.ID != "tu_1" {
		t.Errorf("ID: %q", tr.ID)
	}
	if tr.IsError {
		t.Error("IsError should be false")
	}
	if tr.ContentPreview != "file contents here" {
		t.Errorf("ContentPreview: %q", tr.ContentPreview)
	}
}

func TestClaudeParseLineEvents_UserToolResult_ArrayContent(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_2","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}],"is_error":true}]}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	tr := got[0].(events.ToolResult)
	if !tr.IsError {
		t.Error("IsError should be true")
	}
	if tr.ContentPreview != "line1\nline2" {
		t.Errorf("ContentPreview: %q", tr.ContentPreview)
	}
}

func TestClaudeParseLineEvents_ResultSuccess(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Done.","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":500,"cache_read_input_tokens":0}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events (Usage + Done), got %d", len(got))
	}
	u := got[0].(events.Usage)
	if u.InputTokens != 100 || u.OutputTokens != 20 || u.CacheCreationTokens != 500 {
		t.Errorf("Usage fields: %+v", u)
	}
	if u.StopReason != "end_turn" {
		t.Errorf("StopReason: %q", u.StopReason)
	}
	if d, ok := got[1].(events.Done); !ok || d.StopReason != "end_turn" {
		t.Errorf("Done: %+v", got[1])
	}
}

func TestClaudeParseLineEvents_ResultError(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"result","subtype":"error","is_error":true,"result":"Failed"}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e, ok := got[0].(events.Error)
	if !ok || e.Message != "Failed" {
		t.Errorf("Error mismatch: %+v", got[0])
	}
}

func TestClaudeParseLineEvents_SystemInit(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"system","subtype":"init","session_id":"s_42"}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	s := got[0].(events.SessionID)
	if s.ID != "s_42" {
		t.Errorf("SessionID.ID: %q", s.ID)
	}
}

func TestClaudeParseLineEvents_TopLevelError(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"error","error":{"message":"key invalid"}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e := got[0].(events.Error)
	if e.Message != "key invalid" {
		t.Errorf("error message: %q", e.Message)
	}
}

func TestClaudeParseLineEvents_RateLimit_NoOp(t *testing.T) {
	a := NewClaudeAdapter()
	line := []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("rate_limit_event should produce no events, got %d", len(got))
	}
}

func TestClaudeParseLineEvents_AssertImpl(t *testing.T) {
	var a CLIAdapter = NewClaudeAdapter()
	if _, ok := a.(EventParser); !ok {
		t.Fatal("ClaudeAdapter should implement EventParser")
	}
}

func TestCodexParseLineEvents_DeltaAndDone(t *testing.T) {
	a := NewCodexAdapter()
	line := []byte(`{"type":"item.message","role":"assistant","delta":"Hi"}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	d := got[0].(events.Delta)
	if d.Text != "Hi" || d.Phase != "narration" {
		t.Errorf("Delta: %+v", d)
	}

	line2 := []byte(`{"type":"turn.completed"}`)
	got2, err := a.ParseLineEvents(line2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got2) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got2))
	}
	if _, ok := got2[0].(events.Done); !ok {
		t.Errorf("expected Done, got %T", got2[0])
	}
}

func TestCodexParseLineEvents_FinalContent(t *testing.T) {
	a := NewCodexAdapter()
	line := []byte(`{"type":"item.message","role":"assistant","content":"The answer."}`)
	got, _ := a.ParseLineEvents(line)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	d := got[0].(events.Delta)
	if d.Text != "The answer." {
		t.Errorf("Text: %q", d.Text)
	}
	if d.Phase != "final" {
		t.Errorf("Phase=final expected, got %q", d.Phase)
	}
}

// TestTruncate_UTF8Boundary verifies that truncate cuts at a rune
// boundary so multi-byte UTF-8 runes are never split in the preview.
// Regression: PR #9 review (Copilot) flagged byte-slice truncation
// as producing invalid text in ToolResult previews.
func TestTruncate_UTF8Boundary(t *testing.T) {
	// "héllo" — é is two bytes (0xC3 0xA9) at offsets 1-2.
	// Truncate at byte 2 would split é. truncate(s, 2) should walk
	// back to byte 1 (the "h") so the result is valid UTF-8.
	s := "héllo"
	got := truncate(s, 2)
	for i, r := range got {
		if r == '�' {
			t.Errorf("truncate(%q, 2) produced replacement char at byte %d: %q", s, i, got)
		}
	}
	// Should end with the ellipsis the function appends.
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate should append ellipsis when truncating, got %q", got)
	}

	// 4-byte rune (👍 = 0xF0 0x9F 0x91 0x8D). Truncate at any byte
	// inside it should walk back to the "h" before it.
	emoji := "h👍i"
	for cut := 1; cut <= 4; cut++ {
		out := truncate(emoji, cut)
		if !validUTF8(out) {
			t.Errorf("truncate(%q, %d) produced invalid UTF-8: %q", emoji, cut, out)
		}
	}

	// No-op when within budget.
	if truncate("hi", 100) != "hi" {
		t.Error("truncate within budget should return s unchanged")
	}

	// Empty string within any budget.
	if truncate("", 0) != "" {
		t.Error("truncate empty string should return empty")
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestCodexParseLineEvents_TurnFailed(t *testing.T) {
	a := NewCodexAdapter()
	line := []byte(`{"type":"turn.failed","message":"context limit exceeded"}`)
	got, err := a.ParseLineEvents(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	e := got[0].(events.Error)
	if e.Message != "context limit exceeded" {
		t.Errorf("Error message: %q", e.Message)
	}
}
