package provider

import (
	"slices"
	"testing"
)

// A pinned Binary wins over the env override and PATH, so a host pins a
// binary by setting a field rather than wrapping the adapter.
func TestAdapterBinaryPinsDetect(t *testing.T) {
	t.Setenv("CLAUDE_CLI_PATH", "/env/claude")
	t.Setenv("CODEX_CLI_PATH", "/env/codex")
	t.Setenv("OPENCODE_CLI_PATH", "/env/opencode")
	t.Setenv("AGY_CLI_PATH", "/env/agy")
	for _, c := range []struct {
		pinned, unpinned CLIAdapter
		pin, env         string
	}{
		{&ClaudeAdapter{Binary: "/pin/claude"}, &ClaudeAdapter{}, "/pin/claude", "/env/claude"},
		{&CodexAdapter{Binary: "/pin/codex"}, &CodexAdapter{}, "/pin/codex", "/env/codex"},
		{&OpencodeAdapter{Binary: "/pin/opencode"}, &OpencodeAdapter{}, "/pin/opencode", "/env/opencode"},
		{&AntigravityAdapter{Binary: "/pin/agy"}, &AntigravityAdapter{}, "/pin/agy", "/env/agy"},
	} {
		if got, ok := c.pinned.Detect(); !ok || got != c.pin {
			t.Errorf("%s pinned Detect = %q, %v; want %q", c.pinned.Name(), got, ok, c.pin)
		}
		if got, ok := c.unpinned.Detect(); !ok || got != c.env {
			t.Errorf("%s unpinned Detect = %q, %v; want the env override %q", c.unpinned.Name(), got, ok, c.env)
		}
	}
}

// ExtraArgs land at each convention's extra slot, not blindly at the end:
// before the variadic --add-dir (Claude, agy), before --json (codex exec),
// before the trailing message (opencode run), and last where the slot is
// last (codex app-server, opencode serve). Removing them leaves the argv
// the adapter builds without them.
func TestAdapterExtraArgsLandAtTheSlot(t *testing.T) {
	extra := []string{"--x-extra", "value with spaces"}
	cases := []struct {
		name     string
		adapter  func(extra []string) CLIAdapter
		followed string // the argument right after the extras; "" = extras are last
	}{
		{"claude print", func(x []string) CLIAdapter { return &ClaudeAdapter{ProjectDir: "/p", ExtraArgs: x} }, "--add-dir"},
		{"claude streaming", func(x []string) CLIAdapter {
			return &ClaudeAdapter{InputMode: "stream-json", ProjectDir: "/p", ExtraArgs: x}
		}, "--add-dir"},
		{"claude pty", func(x []string) CLIAdapter { return &ClaudeAdapter{PTY: true, ProjectDir: "/p", ExtraArgs: x} }, "--add-dir"},
		{"claude bare", func(x []string) CLIAdapter { return &ClaudeAdapter{Bare: true, ProjectDir: "/p", ExtraArgs: x} }, "--add-dir"},
		{"codex exec", func(x []string) CLIAdapter { return &CodexAdapter{ExtraArgs: x} }, "--json"},
		{"codex app-server", func(x []string) CLIAdapter { return &CodexAdapter{Mode: "app-server", ExtraArgs: x} }, ""},
		{"opencode run", func(x []string) CLIAdapter { return &OpencodeAdapter{ExtraArgs: x} }, "PROMPT"},
		{"opencode serve", func(x []string) CLIAdapter { return &OpencodeAdapter{Mode: "serve-http", ExtraArgs: x} }, ""},
		{"agy", func(x []string) CLIAdapter { return &AntigravityAdapter{AddDirs: []string{"/p"}, ExtraArgs: x} }, "--add-dir"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			with := c.adapter(extra).BuildArgs("PROMPT", "", "")
			without := c.adapter(nil).BuildArgs("PROMPT", "", "")
			i := slices.Index(with, extra[0])
			if i < 0 || i+1 >= len(with) || with[i+1] != extra[1] {
				t.Fatalf("extras missing or split: %q", with)
			}
			if got := slices.Delete(slices.Clone(with), i, i+2); !slices.Equal(got, without) {
				t.Fatalf("argv without the extras = %q, want %q", got, without)
			}
			if c.followed == "" {
				if i+2 != len(with) {
					t.Errorf("extras not last: %q", with)
				}
			} else if i+2 >= len(with) || with[i+2] != c.followed {
				t.Errorf("extras followed by %q, want %q: %q", with[min(i+2, len(with)-1)], c.followed, with)
			}
		})
	}
}
