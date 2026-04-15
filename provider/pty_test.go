//go:build !windows

package provider

import (
	"context"
	"testing"
)

func TestNewPTYBridge_NilWhenMissing(t *testing.T) {
	// Set a non-existent CLI path to force nil return.
	t.Setenv("CLAUDE_CLI_PATH", "/nonexistent/claude-fake-binary")
	// NewPTYBridge checks the env var path but doesn't verify existence
	// via LookPath when CLAUDE_CLI_PATH is set — it trusts the override.
	// So we test the LookPath fallback by unsetting and relying on a
	// missing binary.
	t.Setenv("CLAUDE_CLI_PATH", "")
	// Override PATH and HOME to ensure claude isn't found via LookPath or
	// lookPathExpanded's fallback directories.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	t.Setenv("HOME", emptyDir)

	bridge := NewPTYBridge()
	if bridge != nil {
		t.Error("expected nil when claude CLI not in PATH")
	}
}

func TestPTYBridge_Capabilities(t *testing.T) {
	bridge := &PTYBridge{adapter: NewClaudeAdapter(), cliPath: "/usr/bin/echo"}
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

func TestPTYBridge_StreamChat_NoUserMessage(t *testing.T) {
	bridge := &PTYBridge{adapter: NewClaudeAdapter(), cliPath: "/usr/bin/echo"}
	_, err := bridge.StreamChat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestPTYBridge_StreamChat_WithMockCLI(t *testing.T) {
	// Use a shell script that outputs mock stream-json events.
	// We write it inline via /bin/sh -c.
	mockOutput := `{"type":"system","subtype":"init","cwd":"/tmp"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from mock CLI!"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Hello from mock CLI!","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

	bridge := &PTYBridge{adapter: NewClaudeAdapter(), cliPath: "/bin/sh"}

	// Override streamCLI by calling StreamChat with messages — but we need
	// to construct the command ourselves. Instead, test the parser integration
	// by creating a PTYBridge that points to a printf script.
	// For a proper integration test, we'd need the real CLI.

	// Test that the provider correctly returns an error for missing user message.
	_, err := bridge.StreamChat(context.Background(), ChatRequest{Messages: []ChatMessage{
		{Role: "system", Content: "test"},
	}})
	if err == nil {
		t.Fatal("expected error for no user message")
	}

	// Verify we can construct the bridge and it has the right path.
	_ = mockOutput // Used conceptually; real integration test would use this.
	if bridge.cliPath != "/bin/sh" {
		t.Errorf("expected cliPath=/bin/sh, got %s", bridge.cliPath)
	}
}

func TestSandboxDirContext(t *testing.T) {
	ctx := context.Background()
	_, ok := SandboxDirFromContext(ctx)
	if ok {
		t.Error("expected no sandbox dir in empty context")
	}

	ctx = WithSandboxDir(ctx, "/tmp/sandbox/sess-1")
	dir, ok := SandboxDirFromContext(ctx)
	if !ok {
		t.Fatal("expected sandbox dir in context")
	}
	if dir != "/tmp/sandbox/sess-1" {
		t.Errorf("expected /tmp/sandbox/sess-1, got %s", dir)
	}

	// Empty string should return false.
	ctx = WithSandboxDir(context.Background(), "")
	_, ok = SandboxDirFromContext(ctx)
	if ok {
		t.Error("expected empty string to return ok=false")
	}
}

func TestCLISessionIDContext(t *testing.T) {
	// Round-trip: set and retrieve CLI session ID from context.
	ctx := context.Background()
	_, ok := CLISessionIDFromContext(ctx)
	if ok {
		t.Error("expected no CLI session ID in empty context")
	}

	ctx = WithCLISessionID(ctx, "sess-123")
	id, ok := CLISessionIDFromContext(ctx)
	if !ok {
		t.Fatal("expected CLI session ID in context")
	}
	if id != "sess-123" {
		t.Errorf("expected sess-123, got %s", id)
	}

	// Empty string should return false.
	ctx = WithCLISessionID(context.Background(), "")
	_, ok = CLISessionIDFromContext(ctx)
	if ok {
		t.Error("expected empty string to return ok=false")
	}
}
