package provider

import (
	"os"
	"path/filepath"
	"runtime"
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
	// v0.9.1: populated loopback must declare `"type": "http"` so bare-
	// mode strict validation accepts the entry (non-bare auto-discovery
	// also accepts it). Without `type`, the validator defaults to the
	// stdio shape and rejects with `command: expected string, received
	// undefined`.
	if !strings.Contains(mcpJSON, `"type": "http"`) {
		t.Errorf(".mcp.json should declare type:\"http\" for the loopback entry, got %q", mcpJSON)
	}
}

// TestClaudeBootDirSpec_ApiKeyHelper_Absent pins that the planted
// .claude/settings.json omits the apiKeyHelper field when
// ClaudeAdapter.ApiKeyHelperPath is empty (default zero value). Bare
// mode then requires ANTHROPIC_API_KEY in env per the prior contract;
// this test guards against accidentally adding a stub helper path.
func TestClaudeBootDirSpec_ApiKeyHelper_Absent(t *testing.T) {
	a := NewClaudeAdapter()
	if a.ApiKeyHelperPath != "" {
		t.Fatalf("default ApiKeyHelperPath should be empty, got %q", a.ApiKeyHelperPath)
	}
	settings, err := a.BootDirSpec().PlantedFiles[2].Render(PlantContext{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(settings, "apiKeyHelper") {
		t.Errorf("settings.json should NOT contain apiKeyHelper when ApiKeyHelperPath is empty\ngot:\n%s", settings)
	}
}

// TestClaudeBootDirSpec_ApiKeyHelper_Set pins that ApiKeyHelperPath
// threads into the planted .claude/settings.json as a JSON-encoded
// `apiKeyHelper` field. Bare-mode subscription users (no
// ANTHROPIC_API_KEY in env) point apiKeyHelper at a small
// helper that resolves auth from the macOS keychain or another
// per-environment source.
func TestClaudeBootDirSpec_ApiKeyHelper_Set(t *testing.T) {
	a := NewClaudeAdapter()
	a.ApiKeyHelperPath = "/usr/local/bin/example-apikey-helper"
	settings, err := a.BootDirSpec().PlantedFiles[2].Render(PlantContext{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := `"apiKeyHelper": "/usr/local/bin/example-apikey-helper"`
	if !strings.Contains(settings, want) {
		t.Errorf("settings.json should contain %s\ngot:\n%s", want, settings)
	}
	// Sanity: the existing keys still ride along.
	if !strings.Contains(settings, `"mcpServers"`) {
		t.Errorf("settings.json lost mcpServers stub when apiKeyHelper added\ngot:\n%s", settings)
	}
	if !strings.Contains(settings, `"approvedTools"`) {
		t.Errorf("settings.json lost approvedTools stub when apiKeyHelper added\ngot:\n%s", settings)
	}
}

// TestClaudeBootDirSpec_ApiKeyHelper_BareAdapterRespects pins that the
// bare-mode constructors return adapters with the ApiKeyHelperPath
// field exposed (so consumers can populate it post-construction
// alongside the four BareInjectionPaths fields). Sanity check on the
// exposed surface; the field's threading is covered by the _Set test
// above.
func TestClaudeBootDirSpec_ApiKeyHelper_BareAdapterRespects(t *testing.T) {
	bare := NewClaudeAdapterBare()
	devBare := NewClaudeAdapterDevBare()
	if bare.ApiKeyHelperPath != "" || devBare.ApiKeyHelperPath != "" {
		t.Fatal("bare-mode constructors should default ApiKeyHelperPath to empty")
	}
	bare.ApiKeyHelperPath = "/tmp/akh"
	devBare.ApiKeyHelperPath = "/tmp/akh"
	for name, a := range map[string]*ClaudeAdapter{"bare": bare, "devBare": devBare} {
		settings, err := a.BootDirSpec().PlantedFiles[2].Render(PlantContext{})
		if err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
		if !strings.Contains(settings, `"apiKeyHelper": "/tmp/akh"`) {
			t.Errorf("%s adapter: settings.json missing apiKeyHelper\ngot:\n%s", name, settings)
		}
	}
}

func TestClaudeBootDirSpec_EmptyMCP(t *testing.T) {
	a := NewClaudeAdapter()
	spec := a.BootDirSpec()
	mcpJSON, _ := spec.PlantedFiles[3].Render(PlantContext{})
	// Pre-v0.9.0 emitted bare `{}`. That works for auto-discovery (non-
	// bare callers) but fails claude's MCP schema validation when the
	// file is referenced via --mcp-config (bare mode), which requires
	// `mcpServers` to be a record. `{"mcpServers":{}}` is valid for both.
	if strings.TrimSpace(mcpJSON) != `{"mcpServers":{}}` {
		t.Errorf("empty MCP loopback should produce minimal valid config, got %q", mcpJSON)
	}
}

// TestRenderMCPJSON_PopulatedShape pins the exact byte shape emitted for a
// non-empty loopback URL. Bare-mode (`--mcp-config <path>`) strict validation
// in claude 2.1.137 rejects entries without an explicit transport `type`
// (probed empirically against claude 2.1.137).
// `type: "http"` matches the `claude mcp add --transport http` CLI keyword and
// is accepted by both bare-mode validation and non-bare auto-discovery, so
// codex/opencode adapters that share renderMCPJSON inherit the same correct
// shape transparently.
func TestRenderMCPJSON_PopulatedShape(t *testing.T) {
	got := renderMCPJSON("http://127.0.0.1:65535/mcp")
	want := `{
  "mcpServers": {
    "loopback": {
      "type": "http",
      "url": "http://127.0.0.1:65535/mcp"
    }
  }
}
`
	if got != want {
		t.Errorf("renderMCPJSON populated shape mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestRenderMCPJSON_Empty pins the empty-loopback shape (mirrors
// TestClaudeBootDirSpec_EmptyMCP but at the function level for direct
// regression coverage of renderMCPJSON's two branches).
func TestRenderMCPJSON_Empty(t *testing.T) {
	got := renderMCPJSON("")
	if strings.TrimSpace(got) != `{"mcpServers":{}}` {
		t.Errorf("renderMCPJSON(\"\") should emit `{\"mcpServers\":{}}`, got %q", got)
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
		t.Error("codex spec Notes should document the TOML/JSON config split")
	}

	// CODEX_HOME env amendment isolates per-task config + auth from ~/.codex.
	foundEnv := false
	for _, e := range spec.EnvAmendments {
		if strings.Contains(e, "CODEX_HOME") && strings.Contains(e, "{{.BootDir}}") {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("expected CODEX_HOME env amendment, got %v", spec.EnvAmendments)
	}

	// AGENTS.md + boot.md + config.toml + auth.json + .mcp.json (sidecar).
	wantPaths := []string{"AGENTS.md", "boot.md", "config.toml", "auth.json", ".mcp.json"}
	if len(spec.PlantedFiles) != len(wantPaths) {
		t.Fatalf("PlantedFiles count: want %d, got %d", len(wantPaths), len(spec.PlantedFiles))
	}
	for i, want := range wantPaths {
		if spec.PlantedFiles[i].RelPath != want {
			t.Errorf("PlantedFiles[%d].RelPath: want %q, got %q", i, want, spec.PlantedFiles[i].RelPath)
		}
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

	// config.toml is the load-bearing MCP config.
	configTOML, err := spec.PlantedFiles[2].Render(pctx)
	if err != nil {
		t.Fatalf("config.toml render: %v", err)
	}
	if !strings.Contains(configTOML, "[mcp_servers.loopback]") {
		t.Errorf("config.toml should contain [mcp_servers.loopback] block, got %q", configTOML)
	}
	if !strings.Contains(configTOML, `"http://lp:1"`) {
		t.Errorf("config.toml should contain the loopback URL, got %q", configTOML)
	}
}

// TestCodexBootDirSpec_EmptyMCP pins the empty-loopback path: with no
// MCPLoopbackURL the config.toml renderer returns "" (codex falls back to
// defaults). The .mcp.json sidecar is exercised by TestRenderMCPJSON_Empty
// — this test only walks the codex-specific config.toml branch.
func TestCodexBootDirSpec_EmptyMCP(t *testing.T) {
	a := NewCodexAdapter()
	spec := a.BootDirSpec()

	pctx := PlantContext{AgentName: "codex-exec"} // no MCPLoopbackURL

	configTOML, err := spec.PlantedFiles[2].Render(pctx)
	if err != nil {
		t.Fatalf("config.toml render: %v", err)
	}
	if configTOML != "" {
		t.Errorf("empty MCPLoopbackURL should produce empty config.toml, got %q", configTOML)
	}
}

// TestOpencodeBootDirSpec_PlantedFileModes pins PlantedFile.Mode for the
// opencode spec: .mcp.json carries the per-task loopback URL and gets 0o600;
// other planted files leave Mode unset (caller falls back to 0o644).
func TestOpencodeBootDirSpec_PlantedFileModes(t *testing.T) {
	a := NewOpencodeAdapter()
	spec := a.BootDirSpec()

	want := map[string]os.FileMode{
		".mcp.json": 0o600, // loopback URL — secret-ish
	}
	for _, pf := range spec.PlantedFiles {
		w, pinned := want[pf.RelPath]
		if !pinned {
			if pf.Mode != 0 {
				t.Errorf("PlantedFile[%s].Mode: want unset (0), got %#o", pf.RelPath, pf.Mode)
			}
			continue
		}
		if pf.Mode != w {
			t.Errorf("PlantedFile[%s].Mode: want %#o, got %#o", pf.RelPath, w, pf.Mode)
		}
	}
}

// TestCodexBootDirSpec_PlantedFileModes pins PlantedFile.Mode for the codex
// spec: secret-ish files (config.toml, auth.json, .mcp.json — all carry the
// per-task loopback URL or OAuth tokens) get 0o600; AGENTS.md/boot.md leave
// Mode unset (caller falls back to 0o644 per the spec contract).
func TestCodexBootDirSpec_PlantedFileModes(t *testing.T) {
	a := NewCodexAdapter()
	spec := a.BootDirSpec()

	want := map[string]os.FileMode{
		"AGENTS.md":   0,      // unset → 0o644 fallback
		"boot.md":     0,      // unset → 0o644 fallback
		"config.toml": 0o600,  // loopback URL — secret-ish
		"auth.json":   0o600,  // OAuth tokens / API keys
		".mcp.json":   0o600,  // loopback URL sidecar
	}
	got := make(map[string]os.FileMode, len(spec.PlantedFiles))
	for _, pf := range spec.PlantedFiles {
		got[pf.RelPath] = pf.Mode
	}
	for rel, w := range want {
		if got[rel] != w {
			t.Errorf("PlantedFile[%s].Mode: want %#o, got %#o", rel, w, got[rel])
		}
	}
}

// TestReadCodexAuthSource_PermissionError pins the bubbles-up behavior for
// non-NotExist read errors. Set CODEX_HOME to a directory whose auth.json
// is unreadable (chmod 000) — the helper must return the error so callers
// can surface a real diagnostic instead of masking it as "not logged in".
//
// Skipped on Windows (no POSIX file modes) and when running as root (chmod
// 000 doesn't deny root).
func TestReadCodexAuthSource_PermissionError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not honored on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod 000")
	}

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatalf("seed auth.json: %v", err)
	}
	if err := os.Chmod(authPath, 0o000); err != nil {
		t.Fatalf("chmod 000: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(authPath, 0o600) })

	t.Setenv("CODEX_HOME", dir)
	content, ok, err := readCodexAuthSource()
	if err == nil {
		t.Fatalf("want permission error, got content=%q ok=%v", content, ok)
	}
	if ok {
		t.Errorf("ok should be false on error")
	}
	if content != "" {
		t.Errorf("content should be empty on error, got %q", content)
	}
}

// TestReadCodexAuthSource_Missing pins the silent-success path: source
// doesn't exist → ("", false, nil), no error. The bootdir gets an empty
// auth.json and codex's downstream "Not logged in" surfaces correctly.
func TestReadCodexAuthSource_Missing(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // empty dir, no auth.json
	content, ok, err := readCodexAuthSource()
	if err != nil {
		t.Fatalf("missing-source path should not error, got %v", err)
	}
	if ok {
		t.Errorf("ok should be false for missing source")
	}
	if content != "" {
		t.Errorf("content should be empty for missing source, got %q", content)
	}
}

// TestRenderCodexConfigTOML pins the populated and empty branches of the
// codex TOML emitter.
func TestRenderCodexConfigTOML(t *testing.T) {
	got := renderCodexConfigTOML("http://127.0.0.1:65535/mcp")
	want := "[mcp_servers.loopback]\nurl = \"http://127.0.0.1:65535/mcp\"\n"
	if got != want {
		t.Errorf("populated shape mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
	if got := renderCodexConfigTOML(""); got != "" {
		t.Errorf("empty URL should produce empty TOML, got %q", got)
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

func TestBootDirProvider_AssertedOnAllAdapters(t *testing.T) {
	adapters := []CLIAdapter{
		NewClaudeAdapter(),
		NewCodexAdapter(),
		NewOpencodeAdapter(),
	}
	for _, a := range adapters {
		if _, ok := a.(BootDirProvider); !ok {
			t.Errorf("%s adapter should implement BootDirProvider", a.Name())
		}
	}
}
