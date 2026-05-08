package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider/events"
)

func TestWithEvents_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if cb, ok := EventsCallbackFromContext(ctx); ok || cb != nil {
		t.Fatalf("expected no callback when ctx is bare, got cb=%v ok=%v", cb, ok)
	}

	called := 0
	ctx2 := WithEvents(ctx, func(ev events.Event) { called++ })
	cb, ok := EventsCallbackFromContext(ctx2)
	if !ok || cb == nil {
		t.Fatal("expected callback to be set")
	}
	cb(events.Done{})
	if called != 1 {
		t.Errorf("callback fire count: want 1, got %d", called)
	}
}

func TestWithEvents_NilCallbackPreservesCtx(t *testing.T) {
	ctx := context.Background()
	ctx2 := WithEvents(ctx, nil)
	if _, ok := EventsCallbackFromContext(ctx2); ok {
		t.Error("nil callback should not register in context")
	}
}

func TestWithToolArgFingerprint_Default(t *testing.T) {
	ctx := context.Background()
	if ToolArgFingerprintFromContext(ctx) {
		t.Error("default ctx should not have fingerprinting enabled")
	}
	ctx = WithToolArgFingerprint(ctx, false)
	if ToolArgFingerprintFromContext(ctx) {
		t.Error("WithToolArgFingerprint(false) should not enable")
	}
	ctx = WithToolArgFingerprint(ctx, true)
	if !ToolArgFingerprintFromContext(ctx) {
		t.Error("WithToolArgFingerprint(true) should enable")
	}
}

func TestFingerprintArgs(t *testing.T) {
	in := map[string]any{
		"file_path": "/secrets/token.txt",
		"command":   "rm -rf /",
		"empty":     "",
	}
	out := fingerprintArgs(in)
	if len(out) != len(in) {
		t.Fatalf("expected %d keys, got %d", len(in), len(out))
	}
	for k, v := range out {
		s, ok := v.(string)
		if !ok {
			t.Errorf("key %q: expected string, got %T", k, v)
			continue
		}
		if len(s) < 7 || s[:7] != "sha256:" {
			t.Errorf("key %q: expected sha256: prefix, got %q", k, s)
		}
	}

	// Determinism: same input → same digest.
	again := fingerprintArgs(in)
	for k := range in {
		if out[k] != again[k] {
			t.Errorf("key %q: digest not deterministic", k)
		}
	}

	// Different value for same key → different digest.
	in2 := map[string]any{"file_path": "/different.txt"}
	out2 := fingerprintArgs(in2)
	if out["file_path"] == out2["file_path"] {
		t.Error("different values produced equal digest")
	}
}

func TestFingerprintArgs_Empty(t *testing.T) {
	if got := fingerprintArgs(nil); got != nil {
		t.Errorf("nil input should return nil, got %v", got)
	}
	if got := fingerprintArgs(map[string]any{}); got != nil {
		t.Errorf("empty input should return nil, got %v", got)
	}
}

func TestApplyToolArgFingerprint(t *testing.T) {
	in := []events.Event{
		events.Delta{Text: "hello"},
		events.ToolUse{
			ID:   "tu_1",
			Name: "Read",
			Args: map[string]any{"path": "/etc/passwd"},
		},
		events.SubagentSpawn{
			Tool: "Task",
			Args: map[string]any{"description": "do thing"},
		},
		events.Done{},
	}
	out := applyToolArgFingerprint(in)

	if d, ok := out[0].(events.Delta); !ok || d.Text != "hello" {
		t.Error("non-tool events should pass through unchanged")
	}

	tu, ok := out[1].(events.ToolUse)
	if !ok {
		t.Fatalf("index 1: expected ToolUse, got %T", out[1])
	}
	if !tu.Fingerprint {
		t.Error("ToolUse.Fingerprint should be true after apply")
	}
	if tu.Name != "Read" || tu.ID != "tu_1" {
		t.Errorf("ToolUse name/id should be preserved")
	}
	pathArg, ok := tu.Args["path"].(string)
	if !ok || len(pathArg) < 7 || pathArg[:7] != "sha256:" {
		t.Errorf("ToolUse path arg should be sha256-hashed, got %v", tu.Args["path"])
	}

	ss, ok := out[2].(events.SubagentSpawn)
	if !ok {
		t.Fatalf("index 2: expected SubagentSpawn, got %T", out[2])
	}
	descArg, ok := ss.Args["description"].(string)
	if !ok || len(descArg) < 7 || descArg[:7] != "sha256:" {
		t.Errorf("SubagentSpawn arg should be sha256-hashed, got %v", ss.Args["description"])
	}
}

func TestApplyToolArgFingerprint_Idempotent(t *testing.T) {
	first := []events.Event{
		events.ToolUse{
			ID:   "tu_1",
			Name: "Read",
			Args: map[string]any{"path": "/etc/passwd"},
		},
	}
	first = applyToolArgFingerprint(first)
	tu1 := first[0].(events.ToolUse)
	hashedPath := tu1.Args["path"].(string)

	// Run apply again — should be a no-op since Fingerprint=true.
	second := applyToolArgFingerprint([]events.Event{tu1})
	tu2 := second[0].(events.ToolUse)
	if tu2.Args["path"].(string) != hashedPath {
		t.Errorf("re-apply produced different digest")
	}
	if !tu2.Fingerprint {
		t.Errorf("Fingerprint flag dropped on re-apply")
	}
}

func TestTranslateStreamEvents_Coverage(t *testing.T) {
	in := []StreamEvent{
		{Type: EventDelta, Content: "hi"},
		{Type: EventToolUse, ToolUse: &ToolUseBlock{ID: "tu_1", Name: "Read", Input: map[string]any{"p": "/x"}}},
		{Type: EventUsage, Usage: &Usage{InputTokens: 10, OutputTokens: 20, StopReason: "end_turn"}},
		{Type: EventDone},
		{Type: EventError, Error: "boom"},
		{Type: EventSessionID, SessionID: "abc"},
		{Type: EventThinking, ThinkingBlock: &ThinkingBlock{Thinking: "think", Signature: "sig"}},
	}
	out := translateStreamEvents(in)
	if len(out) != len(in) {
		t.Fatalf("translate: want %d events, got %d", len(in), len(out))
	}
	if d, ok := out[0].(events.Delta); !ok || d.Text != "hi" {
		t.Errorf("idx 0: %T %v", out[0], out[0])
	}
	if tu, ok := out[1].(events.ToolUse); !ok || tu.ID != "tu_1" {
		t.Errorf("idx 1: %T %v", out[1], out[1])
	}
	if u, ok := out[2].(events.Usage); !ok || u.InputTokens != 10 {
		t.Errorf("idx 2: %T %v", out[2], out[2])
	}
	if _, ok := out[3].(events.Done); !ok {
		t.Errorf("idx 3: %T", out[3])
	}
	if e, ok := out[4].(events.Error); !ok || e.Message != "boom" {
		t.Errorf("idx 4: %T %v", out[4], out[4])
	}
	if s, ok := out[5].(events.SessionID); !ok || s.ID != "abc" {
		t.Errorf("idx 5: %T %v", out[5], out[5])
	}
	if th, ok := out[6].(events.Thinking); !ok || th.Signature != "sig" {
		t.Errorf("idx 6: %T %v", out[6], out[6])
	}
}

func TestTranslateStreamEvents_NilGuards(t *testing.T) {
	// ToolUse with nil ToolUse pointer should be skipped.
	in := []StreamEvent{{Type: EventToolUse, ToolUse: nil}}
	out := translateStreamEvents(in)
	if len(out) != 0 {
		t.Errorf("expected 0 events for nil ToolUse, got %d", len(out))
	}
}

func TestEmitTyped_FingerprintAppliedFromContext(t *testing.T) {
	var got []events.Event
	cb := func(ev events.Event) { got = append(got, ev) }

	ctx := WithToolArgFingerprint(context.Background(), true)
	state := newEventsBridgeState()

	emitTyped(ctx, cb, state, []events.Event{
		events.ToolUse{Name: "Read", Args: map[string]any{"p": "/x"}},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 typed event, got %d", len(got))
	}
	tu, ok := got[0].(events.ToolUse)
	if !ok {
		t.Fatalf("expected ToolUse, got %T", got[0])
	}
	if !tu.Fingerprint {
		t.Errorf("fingerprint flag should be set when ctx requested it")
	}
}

func TestEmitTyped_NoCallbackNoOp(t *testing.T) {
	emitTyped(context.Background(), nil, nil, []events.Event{events.Done{}})
	// no panic = pass
}

func TestStartHeartbeat_NoCallbackNoTicker(t *testing.T) {
	state := newEventsBridgeState()
	stop := startHeartbeat(context.Background(), nil, state, 10*time.Millisecond)
	stop() // should not panic; should not have started anything
}

func TestStartHeartbeat_FiresOnIdle(t *testing.T) {
	var (
		mu  sync.Mutex
		got []events.Event
	)
	cb := func(ev events.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}

	// Use mark() to advance lastActivityAt into the past via a fresh
	// state — newEventsBridgeState() initializes to time.Now(), so we
	// just wait long enough for the first tick to clear the interval.
	state := newEventsBridgeState()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const interval = 30 * time.Millisecond
	stop := startHeartbeat(ctx, cb, state, interval)
	defer stop()

	// Wait long enough for several intervals so the second tick is
	// guaranteed to find the gap exceeding interval (the first tick
	// races against the just-initialized lastActivityAt).
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	hbCount := 0
	for _, ev := range got {
		if _, ok := ev.(events.Heartbeat); ok {
			hbCount++
		}
	}
	gotCopy := append([]events.Event(nil), got...)
	mu.Unlock()

	if hbCount == 0 {
		t.Errorf("expected at least one Heartbeat, got 0 (events: %v)", gotCopy)
	}
}

func TestEmitTyped_ErrorEventCarriesUnderlying(t *testing.T) {
	var got events.Error
	cb := func(ev events.Event) {
		if e, ok := ev.(events.Error); ok {
			got = e
		}
	}
	emitTyped(context.Background(), cb, nil, []events.Event{
		events.Error{Err: errors.New("kaboom"), Message: "process exited"},
	})
	if got.Err == nil || got.Err.Error() != "kaboom" {
		t.Errorf("Err should carry underlying error, got %v", got.Err)
	}
	if got.Message != "process exited" {
		t.Errorf("Message should carry diagnostic, got %q", got.Message)
	}
}
