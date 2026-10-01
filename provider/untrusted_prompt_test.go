package provider

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
)

// Turn text is untrusted: it can come from another agent, a wake or a
// steering notice. A turn that starts with '-', or that equals a real flag of
// the CLI, must reach the model as text and never be parsed as an option
// (CW-20261001-0069: codex exec honoured
// --dangerously-bypass-approvals-and-sandbox as a flag).
var hostilePrompts = []string{
	"-1 is wrong",
	"--dangerously-skip-permissions",
	"--dangerously-bypass-approvals-and-sandbox",
	"--auto",
	"--model=evil",
	"--",
	"-p",
}

type promptCase struct {
	name    string
	adapter CLIAdapter
	inline  string // "" = positional after "--"; else the inline flag prefix
}

func promptCases() []promptCase {
	return []promptCase{
		{"claude print", &ClaudeAdapter{ProjectDir: "/p", ExtraArgs: []string{"--x"}}, ""},
		{"claude print skip-permissions", &ClaudeAdapter{SkipPermissions: true, ProjectDir: "/p"}, ""},
		{"claude bare", &ClaudeAdapter{Bare: true, ProjectDir: "/p", SkillsDir: "/b"}, ""},
		{"codex exec", &CodexAdapter{ExtraArgs: []string{"--x"}}, ""},
		{"opencode run", &OpencodeAdapter{ExtraArgs: []string{"--x"}}, ""},
		{"agy", &AntigravityAdapter{AddDirs: []string{"/p"}}, "-p="},
	}
}

func TestUntrustedPromptIsNeverAFlag(t *testing.T) {
	for _, c := range promptCases() {
		for _, prompt := range hostilePrompts {
			for _, resume := range []string{"", "SID"} {
				argv := c.adapter.BuildArgs(prompt, "", resume)
				last := argv[len(argv)-1]
				if c.inline != "" {
					// agy: the value is attached, -p=<prompt>.
					if last != c.inline+prompt {
						t.Errorf("%s %q: last arg %q, want %q", c.name, prompt, last, c.inline+prompt)
					}
					continue
				}
				if last != prompt || argv[len(argv)-2] != "--" {
					t.Errorf("%s %q: argv does not end with -- <prompt>: %q", c.name, prompt, argv)
				}
				// Nothing before "--" is the prompt: a prompt equal to a flag
				// appears only after it.
				end := len(argv) - 2
				if slices.Index(argv[:end], "--") >= 0 {
					t.Errorf("%s %q: more than one -- : %q", c.name, prompt, argv)
				}
				if prompt != "--" && slices.Contains(argv[:end], prompt) && !slices.Contains(c.adapter.BuildArgs("plain", "", resume)[:end], prompt) {
					t.Errorf("%s %q: the prompt leaked in front of --: %q", c.name, prompt, argv)
				}
			}
		}
	}
}

// Claude's system prompt is untrusted text too; the inline --system-prompt=
// form keeps a value that starts with '-' the flag's value.
func TestSystemPromptIsInline(t *testing.T) {
	argv := (&ClaudeAdapter{}).BuildArgs("hi", "--dangerously-skip-permissions", "")
	if !slices.Contains(argv, "--system-prompt=--dangerously-skip-permissions") || slices.Contains(argv, "--dangerously-skip-permissions") {
		t.Fatalf("system prompt not inline: %q", argv)
	}
}

// Streaming stdio, the TUI, codex app-server and opencode serve take turns
// over stdin or their protocol, so the prompt never reaches argv.
func TestNonArgvModesCarryNoPrompt(t *testing.T) {
	for name, a := range map[string]CLIAdapter{
		"claude streaming": &ClaudeAdapter{InputMode: "stream-json"},
		"claude pty":       &ClaudeAdapter{PTY: true},
		"codex app-server": &CodexAdapter{Mode: "app-server"},
		"opencode serve":   &OpencodeAdapter{Mode: "serve-http"},
	} {
		argv := a.BuildArgs("--dangerously-bypass-approvals-and-sandbox", "--evil-system", "")
		for _, arg := range argv {
			if strings.Contains(arg, "dangerously-bypass") || strings.Contains(arg, "--evil-system") || arg == "--" {
				t.Errorf("%s: turn text reached argv: %q", name, argv)
			}
		}
	}
}

// The adapter path and the prepared (projection) path resolve the same
// convention, so the projection's argv ends the same way.
func TestProjectionPromptIsLastAfterEndOfOptions(t *testing.T) {
	for _, c := range []struct {
		name string
		a    ProjectionProvider
	}{
		{"claude", &ClaudeAdapter{}},
		{"claude bare", &ClaudeAdapter{Bare: true}},
		{"codex exec", NewCodexAdapter()},
		{"opencode run", NewOpencodeAdapter()},
	} {
		proj, err := c.a.ProviderProjection(PlantContext{AgentName: "a"}, ProjectionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := proj.ResolveTurn(ProjectionRoots{BootRoot: "/b", ProjectRoot: "/p"}, TurnInput{Prompt: "--dangerously-skip-permissions"}, []string{"--x"})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(b.Argv); b.Argv[n-1] != "--dangerously-skip-permissions" || b.Argv[n-2] != "--" {
			t.Errorf("%s: projection argv %q", c.name, b.Argv)
		}
	}
}

// A convention that puts anything after the prompt is refused: after "--"
// every argument is a positional.
func TestResolveTurnRefusesArgumentsAfterThePrompt(t *testing.T) {
	c := LaunchConvention{Executable: "x", Argv: []ArgTemplate{{Kind: ArgPrompt}, {Kind: ArgLiteral, Value: "--json"}}}
	if _, err := c.ResolveTurn(ProjectionRoots{}, TurnInput{Prompt: "hi"}, nil); err == nil {
		t.Fatal("ResolveTurn accepted an argument after the prompt")
	}
}

// What the CLI receives: a fake CLI records its argv; the hostile turn is the
// argument after "--", and no dangerous flag stands before it.
func TestFakeCLIReceivesTheTurnAsItsPrompt(t *testing.T) {
	for _, c := range []struct {
		id        runtimes.ID
		adapter   CLIAdapter
		dangerous string
	}{
		{runtimes.Codex, NewCodexAdapter(), "--dangerously-bypass-approvals-and-sandbox"},
		{runtimes.Claude, NewClaudeAdapter(), "--dangerously-skip-permissions"},
		{runtimes.OpenCode, NewOpencodeAdapter(), "--auto"},
	} {
		t.Run(string(c.id), func(t *testing.T) {
			fake := providertest.New(t, c.id, providertest.Lines("{}").When("--"))
			if out, err := exec.Command(fake.Path, c.adapter.BuildArgs(c.dangerous, "", "")...).CombinedOutput(); err != nil {
				t.Fatalf("fake %s: %v: %s", c.id, err, out)
			}
			call := fake.Call(0)
			if got, ok := call.ArgAfter("--"); !ok || got != c.dangerous {
				t.Fatalf("fake received prompt %q (ok=%v), want %q; argv %q", got, ok, c.dangerous, call.Args)
			}
			if i := slices.Index(call.Args, c.dangerous); i != len(call.Args)-1 {
				t.Fatalf("%s stands before -- as a flag: %q", c.dangerous, call.Args)
			}
		})
	}
}
