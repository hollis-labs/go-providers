package provider

import (
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

func TestOpencodeAdapter_Name(t *testing.T) {
	a := NewOpencodeAdapter()
	if a.Name() != "opencode" {
		t.Errorf("expected opencode, got %s", a.Name())
	}
}

func TestNewOpencodeAdapterServeHTTP(t *testing.T) {
	a := NewOpencodeAdapterServeHTTP()
	if a.Mode != "serve-http" {
		t.Fatalf("Mode = %q, want serve-http", a.Mode)
	}
	args := a.BuildArgs("ignored prompt", "ignored system", "ses_ignored")
	expected := []string{"serve", "--port", "0", "--hostname", "127.0.0.1"}
	assertArgsEqual(t, args, expected)
}

func TestOpencodeAdapter_BuildArgs(t *testing.T) {
	t.Run("agent plus prompt", func(t *testing.T) {
		a := &OpencodeAdapter{Agent: "code-review"}
		args := a.BuildArgs("fix the bug", "", "")
		expected := []string{"run", "--agent", "code-review", "fix the bug"}
		assertArgsEqual(t, args, expected)
	})

	t.Run("agent plus model", func(t *testing.T) {
		a := &OpencodeAdapter{Agent: "code-review", Model: "gpt-5"}
		args := a.BuildArgs("fix the bug", "", "")
		expected := []string{"run", "--agent", "code-review", "--model", "gpt-5", "fix the bug"}
		assertArgsEqual(t, args, expected)
	})

	t.Run("agent plus dir", func(t *testing.T) {
		a := &OpencodeAdapter{Agent: "code-review", Dir: "/tmp/work"}
		args := a.BuildArgs("fix the bug", "", "")
		expected := []string{"run", "--agent", "code-review", "--dir", "/tmp/work", "fix the bug"}
		assertArgsEqual(t, args, expected)
	})

	t.Run("system prompt", func(t *testing.T) {
		a := &OpencodeAdapter{Agent: "code-review"}
		args := a.BuildArgs("fix the bug", "Follow repo conventions", "")
		expected := []string{"run", "--agent", "code-review", "System: Follow repo conventions\n\nfix the bug"}
		assertArgsEqual(t, args, expected)
	})

	t.Run("empty agent preserves cli validation", func(t *testing.T) {
		a := NewOpencodeAdapter()
		args := a.BuildArgs("fix the bug", "", "")
		expected := []string{"run", "--agent", "", "fix the bug"}
		assertArgsEqual(t, args, expected)
	})

	t.Run("serve http ignores prompt and session params", func(t *testing.T) {
		a := &OpencodeAdapter{Mode: "serve-http", Agent: "ignored", Model: "ignored", Dir: "ignored"}
		args := a.BuildArgs("fix the bug", "system", "ses_123")
		expected := []string{"serve", "--port", "0", "--hostname", "127.0.0.1"}
		assertArgsEqual(t, args, expected)
	})
}

func TestOpencodeAdapter_ParseLine(t *testing.T) {
	a := NewOpencodeAdapter()

	t.Run("empty line", func(t *testing.T) {
		events, err := a.ParseLine([]byte(""))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events, got %d", len(events))
		}
	})

	t.Run("whitespace line", func(t *testing.T) {
		events, err := a.ParseLine([]byte("   \t"))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Errorf("expected 0 events, got %d", len(events))
		}
	})

	t.Run("content line emits delta", func(t *testing.T) {
		events, err := a.ParseLine([]byte("Applied the patch"))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].Type != llmtypes.EventDelta {
			t.Errorf("expected delta, got %s", events[0].Type)
		}
		if events[0].Content != "Applied the patch\n" {
			t.Errorf("expected content %q, got %q", "Applied the patch\n", events[0].Content)
		}
	})

	t.Run("serve http diagnostics are ignored", func(t *testing.T) {
		a := NewOpencodeAdapterServeHTTP()
		events, err := a.ParseLine([]byte("opencode server listening on http://127.0.0.1:4096"))
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Fatalf("expected 0 events, got %d", len(events))
		}
	})
}

func TestOpencodeAdapter_Detect(t *testing.T) {
	t.Run("env override", func(t *testing.T) {
		t.Setenv("OPENCODE_CLI_PATH", "/foo/bar")
		a := NewOpencodeAdapter()
		path, ok := a.Detect()
		if !ok {
			t.Fatal("expected Detect to succeed")
		}
		if path != "/foo/bar" {
			t.Errorf("expected /foo/bar, got %s", path)
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		t.Setenv("OPENCODE_CLI_PATH", "")
		emptyDir := t.TempDir()
		t.Setenv("PATH", emptyDir)
		t.Setenv("HOME", emptyDir)

		a := NewOpencodeAdapter()
		path, ok := a.Detect()
		if ok {
			t.Fatalf("expected Detect to fail, got path %q", path)
		}
		if path != "" {
			t.Errorf("expected empty path, got %q", path)
		}
	})
}

func assertArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d args, got %d: %v", len(want), len(got), got)
	}
	for i, exp := range want {
		if got[i] != exp {
			t.Errorf("arg[%d]: expected %q, got %q", i, exp, got[i])
		}
	}
}
