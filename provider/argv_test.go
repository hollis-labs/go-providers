package provider

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

var argvRoots = ProjectionRoots{
	ProjectRoot: "/work/Project Root Ω",
	BootRoot:    "/run/Boot Root ü",
	StateRoot:   "/run/state",
}

// argvCase is one launch both ways: the prepared path resolves the
// projection's convention against argvRoots; the adapter path calls
// BuildArgs on an adapter whose path fields hold what that projection
// resolves to.
type argvCase struct {
	name    string
	project func(t *testing.T) ProviderProjection
	adapter CLIAdapter
}

func argvCases(t *testing.T) []argvCase {
	boot, proj := argvRoots.BootRoot, argvRoots.ProjectRoot
	project := func(p ProjectionProvider, ctx PlantContext, opts ProjectionOptions) func(*testing.T) ProviderProjection {
		return func(t *testing.T) ProviderProjection {
			t.Helper()
			pp, err := p.ProviderProjection(ctx, opts)
			if err != nil {
				t.Fatalf("projection: %v", err)
			}
			return pp
		}
	}
	claudeFields := func(a ClaudeAdapter) *ClaudeAdapter {
		a.MCPConfigPath = filepath.Join(boot, ".mcp.json")
		a.ProjectDir = proj
		return &a
	}
	bareFields := func(a ClaudeAdapter, skills bool) *ClaudeAdapter {
		inj := a.BareInjectionPaths(boot, proj)
		a.MCPConfigPath, a.AppendSystemPromptFile, a.SettingsPath, a.ProjectDir = inj.MCPConfigPath, inj.AppendSystemPromptFile, inj.SettingsPath, inj.ProjectDir
		if skills {
			a.SkillsDir = boot
		}
		return &a
	}
	skills := ProjectionOptions{Skills: []SkillPackage{{Name: "probe", Files: []SkillFile{{RelPath: "SKILL.md", Content: []byte("---\nname: probe\n---\n")}}}}}

	var cases []argvCase
	for _, c := range []struct {
		name string
		a    ClaudeAdapter
	}{
		{"claude/print", ClaudeAdapter{}},
		{"claude/print dev", ClaudeAdapter{SkipPermissions: true}},
		{"claude/print text input", ClaudeAdapter{InputMode: "text"}},
		{"claude/streaming", ClaudeAdapter{InputMode: "stream-json"}},
		{"claude/streaming dev", ClaudeAdapter{InputMode: "stream-json", SkipPermissions: true}},
		{"claude/pty", ClaudeAdapter{PTY: true}},
		{"claude/pty dev", ClaudeAdapter{PTY: true, SkipPermissions: true}},
		{"claude/print model", ClaudeAdapter{Model: "sonnet"}},
		{"claude/streaming model", ClaudeAdapter{InputMode: "stream-json", Model: "sonnet"}},
		{"claude/pty model", ClaudeAdapter{PTY: true, Model: "sonnet"}},
	} {
		a := c.a
		cases = append(cases, argvCase{c.name, project(&a, PlantContext{}, ProjectionOptions{}), claudeFields(a)})
	}
	cases = append(cases,
		argvCase{"claude/bare", project(&ClaudeAdapter{Bare: true}, PlantContext{}, ProjectionOptions{}), bareFields(ClaudeAdapter{Bare: true}, false)},
		argvCase{"claude/bare model", project(&ClaudeAdapter{Bare: true, Model: "sonnet"}, PlantContext{}, ProjectionOptions{}), bareFields(ClaudeAdapter{Bare: true, Model: "sonnet"}, false)},
		argvCase{"claude/bare dev with skills", project(&ClaudeAdapter{Bare: true, SkipPermissions: true}, PlantContext{}, skills), bareFields(ClaudeAdapter{Bare: true, SkipPermissions: true}, true)},
		argvCase{"codex/exec", project(NewCodexAdapter(), PlantContext{}, ProjectionOptions{}), &CodexAdapter{ProjectDir: proj}},
		argvCase{"codex/app-server", project(NewCodexAdapterAppServer(), PlantContext{}, ProjectionOptions{}), NewCodexAdapterAppServer()},
		argvCase{"codex/exec model", project(&CodexAdapter{Model: "gpt-6-luna"}, PlantContext{}, ProjectionOptions{}), &CodexAdapter{Model: "gpt-6-luna", ProjectDir: proj}},
		argvCase{"codex/app-server model", project(&CodexAdapter{Mode: "app-server", Model: "gpt-6-luna"}, PlantContext{}, ProjectionOptions{}), &CodexAdapter{Mode: "app-server", Model: "gpt-6-luna"}},
		argvCase{"opencode/run", project(&OpencodeAdapter{}, PlantContext{AgentName: "worker"}, ProjectionOptions{}), &OpencodeAdapter{Agent: "worker", Dir: proj}},
		argvCase{"opencode/run model", project(&OpencodeAdapter{Agent: "worker", Model: "openai/gpt-x"}, PlantContext{}, ProjectionOptions{}), &OpencodeAdapter{Agent: "worker", Model: "openai/gpt-x", Dir: proj}},
		argvCase{"opencode/serve", project(NewOpencodeAdapterServeHTTP(), PlantContext{AgentName: "worker"}, ProjectionOptions{}), NewOpencodeAdapterServeHTTP()},
		argvCase{"antigravity/print", project(&AntigravityAdapter{}, PlantContext{}, ProjectionOptions{}), &AntigravityAdapter{AddDirs: []string{proj}}},
		argvCase{"antigravity/print flags", project(&AntigravityAdapter{Model: "m", Effort: "low", Agent: "a", Permission: "bypass"}, PlantContext{}, ProjectionOptions{}), &AntigravityAdapter{Model: "m", Effort: "low", Agent: "a", Permission: "bypass", AddDirs: []string{proj}}},
		argvCase{"antigravity/print plan", project(&AntigravityAdapter{Permission: "plan"}, PlantContext{}, ProjectionOptions{}), &AntigravityAdapter{Permission: "plan", AddDirs: []string{proj}}},
	)
	return cases
}

var argvTurns = []struct {
	name string
	in   TurnInput
}{
	{"first turn", TurnInput{Prompt: "say hi to Ω", SystemPrompt: "be terse"}},
	{"first turn, no system prompt", TurnInput{Prompt: "say hi"}},
	{"resume", TurnInput{Prompt: "say bye", SystemPrompt: "be terse", ResumeID: "00000000-0000-4000-8000-000000000001"}},
	{"empty prompt", TurnInput{ResumeID: "sess-2"}},
}

// The acceptance check for one argv owner: for every runtime and mode the
// adapter path (BuildArgs) and the prepared path (the projection's convention
// resolved against roots) produce the same argv, turn by turn.
func TestBuildArgsMatchesProjectionResolveTurn(t *testing.T) {
	for _, c := range argvCases(t) {
		t.Run(c.name, func(t *testing.T) {
			proj := c.project(t)
			d, ok := registry.Lookup(string(proj.Provider))
			if !ok || proj.Launch.Executable != d.Binary {
				t.Errorf("executable = %q, want the registry binary %q", proj.Launch.Executable, d.Binary)
			}
			for _, turn := range argvTurns {
				b, err := proj.ResolveTurn(argvRoots, turn.in, nil)
				if err != nil {
					t.Fatalf("%s: ResolveTurn: %v", turn.name, err)
				}
				got := c.adapter.BuildArgs(turn.in.Prompt, turn.in.SystemPrompt, turn.in.ResumeID)
				if !reflect.DeepEqual(got, b.Argv) {
					t.Errorf("%s:\n  BuildArgs   %q\n  ResolveTurn %q", turn.name, got, b.Argv)
				}
			}
		})
	}
}

// Extra arguments land where the convention puts ArgExtra, never where they
// could swallow the prompt or be read as a variadic flag's value.
func TestResolveTurnPlacesExtraArgs(t *testing.T) {
	extra := []string{"--extra-flag", "extra-value"}
	in := TurnInput{Prompt: "PROMPT", ResumeID: "SID"}
	for _, c := range argvCases(t) {
		t.Run(c.name, func(t *testing.T) {
			proj := c.project(t)
			b, err := proj.ResolveTurn(argvRoots, in, extra)
			if err != nil {
				t.Fatal(err)
			}
			argv := b.Argv
			at := slices.Index(argv, "--extra-flag")
			if at < 0 || at+1 >= len(argv) || argv[at+1] != "extra-value" || slices.Index(argv[at+1:], "--extra-flag") >= 0 {
				t.Fatalf("extra args not placed once, contiguously: %q", argv)
			}
			prompt := slices.IndexFunc(argv, func(s string) bool { return strings.HasSuffix(s, "PROMPT") })
			// The prompt is last and the extras come before it: after
			// "--" for a positional prompt (claude, codex, opencode), or
			// inline as -p= (agy).
			if prompt >= 0 && (prompt != len(argv)-1 || prompt < at) {
				t.Errorf("prompt is not last, after the extras: %q", argv)
			}
			if prompt >= 0 && proj.Provider != runtimes.Antigravity && argv[prompt-1] != "--" {
				t.Errorf("positional prompt not preceded by --: %q", argv)
			}
			if add := slices.Index(argv, "--add-dir"); add >= 0 && add < at {
				t.Errorf("extra args after --add-dir would be read as directories: %q", argv)
			}
		})
	}
}

// claude's --add-dir (and agy's) is variadic: a positional argument after its
// directory would be read as another directory. No resolved argv may put a
// non-flag argument after an --add-dir value.
func TestNoPositionalAfterAddDir(t *testing.T) {
	for _, c := range argvCases(t) {
		for _, turn := range argvTurns {
			b, err := c.project(t).ResolveTurn(argvRoots, turn.in, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, argv := range [][]string{b.Argv, c.adapter.BuildArgs(turn.in.Prompt, turn.in.SystemPrompt, turn.in.ResumeID)} {
				for i, a := range argv {
					if a == "--add-dir" && i+2 < len(argv) && !strings.HasPrefix(argv[i+2], "-") {
						t.Errorf("%s/%s: %q follows --add-dir %q: %q", c.name, turn.name, argv[i+2], argv[i+1], argv)
					}
				}
			}
		}
	}
}

func TestResolveTurnTemplateKinds(t *testing.T) {
	conv := LaunchConvention{Argv: []ArgTemplate{
		{Kind: ArgResume, Value: "--resume"},
		{Kind: ArgResume},
		{Kind: ArgSystemPrompt, Value: "--system-prompt", FirstTurnOnly: true},
		{Kind: ArgSystemPrompt, Value: "--system-prompt=", FirstTurnOnly: true},
		{Kind: ArgLiteral, Value: "--first-only", FirstTurnOnly: true},
		{Kind: ArgPromptInline, Value: "-p="},
		{Kind: ArgPromptInline, Value: "-p=", WithSystem: true},
		{Kind: ArgPrompt, WithSystem: true},
	}}
	for _, tc := range []struct {
		in   TurnInput
		want []string
	}{
		{TurnInput{Prompt: "hi", SystemPrompt: "sys"}, []string{"--system-prompt", "sys", "--system-prompt=sys", "--first-only", "-p=hi", "-p=System: sys\n\nhi", "--", "System: sys\n\nhi"}},
		{TurnInput{Prompt: "hi"}, []string{"--first-only", "-p=hi", "-p=hi", "--", "hi"}},
		{TurnInput{Prompt: "hi", SystemPrompt: "sys", ResumeID: "s1"}, []string{"--resume", "s1", "s1", "-p=hi", "-p=System: sys\n\nhi", "--", "System: sys\n\nhi"}},
	} {
		b, err := conv.ResolveTurn(ProjectionRoots{}, tc.in, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(b.Argv, tc.want) {
			t.Errorf("ResolveTurn(%+v)\n got %q\nwant %q", tc.in, b.Argv, tc.want)
		}
	}

	// Without an ArgExtra slot, extra args go last.
	b, err := LaunchConvention{Argv: []ArgTemplate{{Kind: ArgLiteral, Value: "run"}}}.ResolveTurn(ProjectionRoots{}, TurnInput{}, []string{"x", "y"})
	if err != nil || !reflect.DeepEqual(b.Argv, []string{"run", "x", "y"}) {
		t.Errorf("extra without a slot: %q, %v", b.Argv, err)
	}
}

// The registry is the one list of runtimes: every projection's executable is
// the descriptor's binary, and the capability matrix carries the descriptor's
// projection facts.
func TestProjectionFactsComeFromTheRegistry(t *testing.T) {
	for _, row := range ProviderCapabilityMatrix() {
		d, _ := registry.Lookup(string(row.Provider))
		if d.Projection == nil || row.TestedVersion != d.Projection.TestedVersion || row.Notes != d.Projection.Note(row.Mode) {
			t.Errorf("%s/%s: row %+v does not match the descriptor's projection facts", row.Provider, row.Shape(), row)
		}
		for f, s := range d.Projection.Features {
			if row.Features[string(f)] != string(s) {
				t.Errorf("%s/%s: feature %s = %q, descriptor says %q", row.Provider, row.Shape(), f, row.Features[string(f)], s)
			}
		}
	}
}
