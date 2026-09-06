package provider

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProviderCapabilityMatrix_M06(t *testing.T) {
	var _ ProjectionProvider = NewClaudeAdapter()
	var _ ProjectionProvider = NewCodexAdapter()
	var _ ProjectionProvider = NewOpencodeAdapter()

	rows := ProviderCapabilityMatrix()
	if len(rows) != 8 {
		t.Fatalf("matrix row count: want 8, got %d", len(rows))
	}
	want := map[ProviderID][]ProviderMode{
		ProviderClaude:   {ModeClaudePrint, ModeClaudeBare, ModeClaudePTY, ModeClaudeStreamingStdio},
		ProviderCodex:    {ModeCodexExec, ModeCodexAppServer},
		ProviderOpencode: {ModeOpencodeRun, ModeOpencodeServeHTTP},
	}
	for provider, modes := range want {
		for _, mode := range modes {
			row, ok := capabilityRow(provider, mode)
			if !ok {
				t.Fatalf("missing matrix row for %s/%s", provider, mode)
			}
			if row.TestedVersion == "" {
				t.Errorf("%s/%s: TestedVersion is empty", provider, mode)
			}
			for _, feature := range []ProviderFeature{
				FeatureInstructions,
				FeatureNativeConfig,
				FeatureMCP,
				FeatureSkillTrees,
				FeatureCredential,
			} {
				if row.Features[string(feature)] == "" {
					t.Errorf("%s/%s: missing feature %q", provider, mode, feature)
				}
			}
		}
	}
}

func TestProviderProjection_GoldenLayoutsWithSkillTrees(t *testing.T) {
	ctx := PlantContext{
		SystemPrompt:   "Follow fixture instructions.",
		BootContent:    "Read @./boot.md and continue.",
		AgentName:      "fixture-agent",
		MCPLoopbackURL: "http://127.0.0.1:60000/mcp",
		MuxCommand:     "/tmp/fixture bin/mux",
		MuxArgs:        []string{"mcp", "--proxy"},
		MuxEnv:         []string{"TOKEN=fixture"},
		MCPServers:     []MCPServerSpec{{Name: "extra", Command: "/tmp/fixture bin/server", Args: []string{"serve"}}},
	}
	skills := []SkillPackage{{
		Name: "fixture-skill",
		Files: []SkillFile{
			{RelPath: "SKILL.md", Content: []byte("---\nname: fixture-skill\ndescription: Fixture skill\n---\n\nUse the fixture.\n")},
			{RelPath: "references/info.md", Content: []byte("reference\n")},
			{RelPath: "scripts/run.sh", Content: []byte("#!/bin/sh\nprintf fixture\n"), Mode: 0o755},
		},
	}}
	opts := ProjectionOptions{
		Version:          "fixture-version",
		Skills:           skills,
		RequiredFeatures: []ProviderFeature{FeatureInstructions, FeatureNativeConfig, FeatureMCP, FeatureSkillTrees},
	}

	cases := []struct {
		name      string
		project   func() (ProviderProjection, error)
		wantFiles []string
		wants     []string
	}{
		{
			name: "claude",
			project: func() (ProviderProjection, error) {
				return (&ClaudeAdapter{Bare: true, PermissionMode: "plan"}).ProviderProjection(ctx, opts)
			},
			wantFiles: []string{
				".claude/settings.json",
				".claude/skills/fixture-skill/SKILL.md",
				".claude/skills/fixture-skill/references/info.md",
				".claude/skills/fixture-skill/scripts/run.sh",
				".mcp.json",
				"CLAUDE.md",
				"boot.md",
			},
			wants: []string{`"type": "http"`, `"type": "stdio"`, `"permissions"`},
		},
		{
			name:    "codex",
			project: func() (ProviderProjection, error) { return NewCodexAdapter().ProviderProjection(ctx, opts) },
			wantFiles: []string{
				".agents/skills/fixture-skill/SKILL.md",
				".agents/skills/fixture-skill/references/info.md",
				".agents/skills/fixture-skill/scripts/run.sh",
				".mcp.json",
				"AGENTS.md",
				"auth.json",
				"boot.md",
				"config.toml",
			},
			wants: []string{`[mcp_servers.loopback]`, `command = "/tmp/fixture bin/mux"`, `[mcp_servers.extra]`},
		},
		{
			name: "opencode",
			project: func() (ProviderProjection, error) {
				a := &OpencodeAdapter{Agent: "fixture-agent"}
				return a.ProviderProjection(ctx, opts)
			},
			wantFiles: []string{
				".mcp.json",
				".opencode/skills/fixture-skill/SKILL.md",
				".opencode/skills/fixture-skill/references/info.md",
				".opencode/skills/fixture-skill/scripts/run.sh",
				"agents.json",
				"agents/fixture-agent.md",
				"boot.md",
				"opencode.json",
			},
			wants: []string{`"type": "remote"`, `"type": "local"`, `"{file:./agents/fixture-agent.md}"`},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj, err := c.project()
			if err != nil {
				t.Fatalf("projection: %v", err)
			}
			gotFiles := make([]string, 0, len(proj.Files))
			fixture := MarshalProjectionFixture(proj)
			for _, f := range proj.Files {
				gotFiles = append(gotFiles, f.RelPath)
				if strings.HasSuffix(f.RelPath, "scripts/run.sh") && f.Mode != 0o755 {
					t.Errorf("script mode: want 0755, got %#o", f.Mode)
				}
			}
			if !reflect.DeepEqual(gotFiles, c.wantFiles) {
				t.Errorf("file list mismatch\nwant: %#v\ngot:  %#v", c.wantFiles, gotFiles)
			}
			payload := joinedProjectedFileContent(proj)
			for _, want := range c.wants {
				if !strings.Contains(payload, want) {
					t.Errorf("fixture missing %q\n%s", want, fixture)
				}
			}
		})
	}
}

func joinedProjectedFileContent(proj ProviderProjection) string {
	var b strings.Builder
	for _, f := range proj.Files {
		b.WriteString("\n--- ")
		b.WriteString(f.RelPath)
		b.WriteString(" ---\n")
		b.Write(f.Content)
	}
	return b.String()
}

func TestProviderProjection_PureSyntheticHome(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	codexHome := filepath.Join(home, "codex home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"token":"must-not-read"}`), 0o000); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatalf("make synthetic HOME read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	ctx := PlantContext{
		SystemPrompt:   "pure",
		BootContent:    "pure boot",
		AgentName:      "pure-agent",
		MCPLoopbackURL: "http://127.0.0.1:1/mcp",
		BootDir:        filepath.Join(home, "read only boot"),
	}
	if _, err := NewClaudeAdapterBare().ProviderProjection(ctx, ProjectionOptions{}); err != nil {
		t.Fatalf("claude pure projection: %v", err)
	}
	if _, err := NewCodexAdapter().ProviderProjection(ctx, ProjectionOptions{}); err != nil {
		t.Fatalf("codex pure projection read credentials or failed: %v", err)
	}
	if _, err := (&OpencodeAdapter{Agent: "pure-agent"}).ProviderProjection(ctx, ProjectionOptions{}); err != nil {
		t.Fatalf("opencode pure projection: %v", err)
	}
	names, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("readdir home: %v", err)
	}
	got := make([]string, 0, len(names))
	for _, n := range names {
		got = append(got, n.Name())
	}
	if !reflect.DeepEqual(got, []string{"codex home"}) {
		t.Fatalf("pure projection touched synthetic HOME: %v", got)
	}
}

func TestProviderProjection_ResolveLaunchStructuredRootsAndEnvPrecedence(t *testing.T) {
	roots := ProjectionRoots{
		ProjectRoot: "/tmp/Project Root Ω",
		BootRoot:    "/tmp/Boot Root ü",
		StateRoot:   "/tmp/State Root",
		ScratchRoot: "/tmp/Scratch Root",
	}
	proj, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	binding, err := proj.ResolveLaunch(roots, "say hello to Ω")
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	wantArgv := []string{"exec", "say hello to Ω", "--json", "--skip-git-repo-check", "--cd", "/tmp/Project Root Ω"}
	if !reflect.DeepEqual(binding.Argv, wantArgv) {
		t.Fatalf("argv mismatch\nwant %#v\ngot  %#v", wantArgv, binding.Argv)
	}
	if binding.CWD != "/tmp/Boot Root ü" || binding.ConfigDir != "/tmp/Boot Root ü" {
		t.Fatalf("roots not distinct in binding: %#v", binding)
	}
	env := ApplyEnvDeltas([]string{"CODEX_HOME=/operator/codex", "PATH=/bin"}, binding.Env)
	if !reflect.DeepEqual(env, []string{"CODEX_HOME=/tmp/Boot Root ü", "PATH=/bin"}) {
		t.Fatalf("provider env precedence mismatch: %#v", env)
	}

	opencode, err := (&OpencodeAdapter{Agent: "worker", Model: "opencode/model"}).ProviderProjection(PlantContext{AgentName: "worker"}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("opencode projection: %v", err)
	}
	ob, err := opencode.ResolveLaunch(roots, "work in /tmp/Project Root Ω")
	if err != nil {
		t.Fatalf("opencode ResolveLaunch: %v", err)
	}
	if ob.CWD != roots.ProjectRoot || ob.ConfigDir != roots.BootRoot {
		t.Fatalf("opencode cwd/config roots mismatch: %#v", ob)
	}
	if !containsExact(ob.Argv, "/tmp/Project Root Ω") {
		t.Fatalf("argv should carry spaced/unicode root as one arg: %#v", ob.Argv)
	}
}

func TestProviderProjection_UnsupportedFeatureDiagnostics(t *testing.T) {
	_, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{
		RequiredFeatures: []ProviderFeature{FeatureCommands},
	})
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("want UnsupportedFeatureError, got %T %v", err, err)
	}
	if got := unsupported.Diagnostics[0].Feature; got != FeatureCommands {
		t.Fatalf("diagnostic feature: want %q, got %q", FeatureCommands, got)
	}
	if !strings.Contains(unsupported.Error(), "codex") || !strings.Contains(unsupported.Error(), "commands") {
		t.Fatalf("diagnostic is not actionable: %v", unsupported)
	}

	_, err = NewClaudeAdapterBare().ProviderProjection(PlantContext{}, ProjectionOptions{
		RequiredFeatures: []ProviderFeature{FeatureTrust},
	})
	if !errors.As(err, &unsupported) {
		t.Fatalf("want explicit-effect UnsupportedFeatureError, got %T %v", err, err)
	}
	if !strings.Contains(unsupported.Error(), "explicit runtime preparation") {
		t.Fatalf("explicit-effect diagnostic missing preparation hint: %v", unsupported)
	}
}

func TestProviderProjection_SkillPackageValidation(t *testing.T) {
	badCases := []SkillPackage{
		{Name: "Bad", Files: []SkillFile{{RelPath: "SKILL.md"}}},
		{Name: "ok", Files: []SkillFile{{RelPath: "../escape"}}},
		{Name: "ok", Files: []SkillFile{{RelPath: "nested\\bad"}}},
		{Name: "ok", Files: []SkillFile{{RelPath: "README.md"}}},
	}
	for _, c := range badCases {
		_, err := NewClaudeAdapterBare().ProviderProjection(PlantContext{}, ProjectionOptions{Skills: []SkillPackage{c}})
		if err == nil {
			t.Fatalf("expected validation error for %#v", c)
		}
	}
}

func containsExact(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
