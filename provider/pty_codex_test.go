package provider

import "testing"

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

func TestParseCodexStreamLine_ThreadStarted(t *testing.T) {
	line := []byte(`{"type":"thread.started","thread_id":"xyz"}`)
	events, err := parseCodexStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for thread.started, got %d", len(events))
	}
}

func TestCodexAdapter_BuildArgs(t *testing.T) {
	a := NewCodexAdapter()
	args := a.BuildArgs("fix bug", "system prompt", "")
	// Codex doesn't use system prompt flag or resume
	if args[0] != "exec" {
		t.Errorf("expected first arg=exec, got %s", args[0])
	}
	if args[1] != "fix bug" {
		t.Errorf("expected second arg=prompt, got %s", args[1])
	}
	if args[2] != "--json" {
		t.Errorf("expected --json flag, got %s", args[2])
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
			"exec", "--json", "--resume":
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
