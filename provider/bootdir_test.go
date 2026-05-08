package provider

import (
	"strings"
	"testing"
)

func TestClaudeBootDirSpec(t *testing.T) {
	a := NewClaudeAdapter()
	spec := a.BootDirSpec()

	if spec.CwdPreference != CwdBootDir {
		t.Errorf("CwdPreference: want CwdBootDir, got %v", spec.CwdPreference)
	}
	if spec.ProjectDirArg != "--add-dir {{.ProjectDir}}" {
		t.Errorf("ProjectDirArg: %q", spec.ProjectDirArg)
	}
	if spec.SpawnWorkdir("/boot", "/proj") != "/boot" {
		t.Error("CwdBootDir should select boot dir")
	}

	wantPaths := []string{"CLAUDE.md", "boot.md", ".claude/settings.json", ".mcp.json"}
	if len(spec.PlantedFiles) != len(wantPaths) {
		t.Fatalf("PlantedFiles count: want %d, got %d", len(wantPaths), len(spec.PlantedFiles))
	}
	for i, want := range wantPaths {
		if spec.PlantedFiles[i].RelPath != want {
			t.Errorf("PlantedFiles[%d].RelPath: want %q, got %q", i, want, spec.PlantedFiles[i].RelPath)
		}
	}

	pctx := PlantContext{
		SystemPrompt:   "you are an orchestrator",
		BootContent:    "Read @./instructions.md and start.",
		AgentName:      "orchestrator",
		MCPLoopbackURL: "http://localhost:9999",
		ProjectDir:     "/work/project",
	}

	claudeMD, err := spec.PlantedFiles[0].Render(pctx)
	if err != nil {
		t.Fatalf("CLAUDE.md render: %v", err)
	}
	if !strings.Contains(claudeMD, "you are an orchestrator") {
		t.Error("CLAUDE.md should contain SystemPrompt")
	}
	if !strings.Contains(claudeMD, "http://localhost:9999") {
		t.Error("CLAUDE.md should contain MCP loopback URL")
	}

	bootMD, _ := spec.PlantedFiles[1].Render(pctx)
	if bootMD != "Read @./instructions.md and start." {
		t.Errorf("boot.md content: %q", bootMD)
	}

	settings, _ := spec.PlantedFiles[2].Render(pctx)
	if !strings.Contains(settings, `"mcpServers"`) {
		t.Error(".claude/settings.json should contain mcpServers stub")
	}

	mcpJSON, _ := spec.PlantedFiles[3].Render(pctx)
	if !strings.Contains(mcpJSON, "http://localhost:9999") {
		t.Error(".mcp.json should contain loopback URL")
	}
}

func TestClaudeBootDirSpec_EmptyMCP(t *testing.T) {
	a := NewClaudeAdapter()
	spec := a.BootDirSpec()
	mcpJSON, _ := spec.PlantedFiles[3].Render(PlantContext{})
	if strings.TrimSpace(mcpJSON) != "{}" {
		t.Errorf("empty MCP loopback should produce {}, got %q", mcpJSON)
	}
}

func TestCodexBootDirSpec(t *testing.T) {
	a := NewCodexAdapter()
	spec := a.BootDirSpec()

	if spec.CwdPreference != CwdBootDir {
		t.Errorf("CwdPreference: want CwdBootDir, got %v", spec.CwdPreference)
	}
	if !strings.Contains(spec.ProjectDirArg, "{{.ProjectDir}}") {
		t.Errorf("ProjectDirArg should template ProjectDir, got %q", spec.ProjectDirArg)
	}
	if spec.Notes == "" {
		t.Error("codex spec Notes should describe TBD probes")
	}

	wantPaths := []string{"AGENTS.md", "boot.md", ".mcp.json"}
	if len(spec.PlantedFiles) != len(wantPaths) {
		t.Fatalf("PlantedFiles count: want %d, got %d", len(wantPaths), len(spec.PlantedFiles))
	}

	pctx := PlantContext{
		SystemPrompt:   "you are codex",
		AgentName:      "codex-exec",
		MCPLoopbackURL: "http://lp:1",
	}
	agentsMD, err := spec.PlantedFiles[0].Render(pctx)
	if err != nil {
		t.Fatalf("AGENTS.md render: %v", err)
	}
	if !strings.Contains(agentsMD, "you are codex") {
		t.Error("AGENTS.md should contain SystemPrompt")
	}
	if !strings.Contains(agentsMD, `name: "codex-exec"`) {
		t.Error("AGENTS.md frontmatter should contain JSON-quoted name")
	}
}

func TestOpencodeBootDirSpec(t *testing.T) {
	a := NewOpencodeAdapter()
	a.Agent = "executor"
	spec := a.BootDirSpec()

	if spec.CwdPreference != CwdProjectDir {
		t.Errorf("CwdPreference: want CwdProjectDir, got %v", spec.CwdPreference)
	}
	if spec.SpawnWorkdir("/boot", "/proj") != "/proj" {
		t.Error("CwdProjectDir should select project dir")
	}
	if spec.SpawnWorkdir("/boot", "") != "/boot" {
		t.Error("empty projectDir should fall back to bootDir")
	}

	foundEnv := false
	for _, e := range spec.EnvAmendments {
		if strings.Contains(e, "OPENCODE_CONFIG_DIR") && strings.Contains(e, "{{.BootDir}}") {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("expected OPENCODE_CONFIG_DIR env amendment, got %v", spec.EnvAmendments)
	}

	wantPaths := []string{
		"agents/executor.md",
		"agents.json",
		"opencode.json",
		"boot.md",
		".mcp.json",
	}
	if len(spec.PlantedFiles) != len(wantPaths) {
		t.Fatalf("PlantedFiles count: want %d, got %d", len(wantPaths), len(spec.PlantedFiles))
	}
	for i, want := range wantPaths {
		if spec.PlantedFiles[i].RelPath != want {
			t.Errorf("PlantedFiles[%d].RelPath: want %q, got %q", i, want, spec.PlantedFiles[i].RelPath)
		}
	}

	pctx := PlantContext{
		SystemPrompt: "you orchestrate",
		AgentName:    "executor",
	}
	agentsJSON, _ := spec.PlantedFiles[1].Render(pctx)
	if !strings.Contains(agentsJSON, `"name": "executor"`) {
		t.Errorf("agents.json should contain name=executor, got %s", agentsJSON)
	}
	if !strings.Contains(agentsJSON, "./agents/executor.md") {
		t.Errorf("agents.json should reference agents/executor.md, got %s", agentsJSON)
	}

	opencodeJSON, _ := spec.PlantedFiles[2].Render(pctx)
	if !strings.Contains(opencodeJSON, "{file:./agents/executor.md}") {
		t.Errorf("opencode.json should reference agent file, got %s", opencodeJSON)
	}
}

func TestOpencodeBootDirSpec_AgentNameDefault(t *testing.T) {
	a := NewOpencodeAdapter()
	// Agent left empty — spec should fall back to "default".
	spec := a.BootDirSpec()
	if spec.PlantedFiles[0].RelPath != "agents/default.md" {
		t.Errorf("expected agents/default.md when Agent is empty, got %q", spec.PlantedFiles[0].RelPath)
	}
}

func TestStubBootDirSpecs(t *testing.T) {
	stubs := map[string]CLIAdapter{
		"gemini":  NewGeminiAdapter(),
		"copilot": NewCopilotAdapter(),
		"aider":   NewAiderAdapter(),
		"junie":   NewJunieAdapter(),
		"kiro":    NewKiroAdapter(),
		"qwen":    NewQwenAdapter(),
	}
	for name, a := range stubs {
		bp, ok := a.(BootDirProvider)
		if !ok {
			t.Errorf("%s adapter should implement BootDirProvider", name)
			continue
		}
		spec := bp.BootDirSpec()
		if len(spec.PlantedFiles) != 0 {
			t.Errorf("%s stub should have no PlantedFiles, got %d", name, len(spec.PlantedFiles))
		}
		if spec.Notes == "" {
			t.Errorf("%s stub Notes should describe TBD probes", name)
		}
		if !strings.Contains(strings.ToLower(spec.Notes), "tbd") {
			t.Errorf("%s stub Notes should signal TBD, got %q", name, spec.Notes)
		}
	}
}

func TestBootDirProvider_AssertedOnAllAdapters(t *testing.T) {
	adapters := []CLIAdapter{
		NewClaudeAdapter(),
		NewCodexAdapter(),
		NewOpencodeAdapter(),
		NewGeminiAdapter(),
		NewCopilotAdapter(),
		NewAiderAdapter(),
		NewJunieAdapter(),
		NewKiroAdapter(),
		NewQwenAdapter(),
	}
	for _, a := range adapters {
		if _, ok := a.(BootDirProvider); !ok {
			t.Errorf("%s adapter should implement BootDirProvider", a.Name())
		}
	}
}
