package provider

import (
	"encoding/json"
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
	if strings.Contains(settings, "approvedTools") || strings.Contains(settings, "mcpServers") {
		t.Errorf(".claude/settings.json must not emit the deprecated approvedTools/mcpServers keys\ngot:\n%s", settings)
	}
	var settingsObj map[string]any
	if err := json.Unmarshal([]byte(settings), &settingsObj); err != nil {
		t.Errorf(".claude/settings.json is not valid JSON: %v\ngot:\n%s", err, settings)
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
	// The deprecated approvedTools / mcpServers keys must NOT ride along.
	if strings.Contains(settings, "approvedTools") || strings.Contains(settings, "mcpServers") {
		t.Errorf("settings.json must not emit deprecated approvedTools/mcpServers keys\ngot:\n%s", settings)
	}
}

// TestClaudeSettingsStub_BypassPermissions pins that a SkipPermissions
// adapter plants `permissions.defaultMode: bypassPermissions` (current
// settings schema) and that the default adapter plants neither that nor
// the deprecated keys.
func TestClaudeSettingsStub_BypassPermissions(t *testing.T) {
	dev := NewClaudeAdapterDev()
	settings, err := dev.BootDirSpec().PlantedFiles[2].Render(PlantContext{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(settings), &got); err != nil {
		t.Fatalf("settings.json not valid JSON: %v\ngot:\n%s", err, settings)
	}
	perms, ok := got["permissions"].(map[string]any)
	if !ok || perms["defaultMode"] != "bypassPermissions" {
		t.Errorf("dev adapter settings.json: want permissions.defaultMode=bypassPermissions, got:\n%s", settings)
	}

	plain, _ := NewClaudeAdapter().BootDirSpec().PlantedFiles[2].Render(PlantContext{})
	if strings.Contains(plain, "permissions") {
		t.Errorf("non-skip adapter must not emit a permissions block, got:\n%s", plain)
	}
}

// TestClaudeSettingsStub_PermissionMode pins that ClaudeAdapter.PermissionMode
// threads into the planted .claude/settings.json as
// `permissions.defaultMode`, that it wins over the SkipPermissions
// back-compat default, and that an unrecognized value fails the Render.
func TestClaudeSettingsStub_PermissionMode(t *testing.T) {
	for _, mode := range []string{"default", "acceptEdits", "plan", "bypassPermissions"} {
		a := NewClaudeAdapter()
		a.PermissionMode = mode
		settings, err := a.BootDirSpec().PlantedFiles[2].Render(PlantContext{})
		if err != nil {
			t.Fatalf("PermissionMode=%q render: %v", mode, err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(settings), &got); err != nil {
			t.Fatalf("PermissionMode=%q: settings.json not valid JSON: %v\ngot:\n%s", mode, err, settings)
		}
		perms, ok := got["permissions"].(map[string]any)
		if !ok || perms["defaultMode"] != mode {
			t.Errorf("PermissionMode=%q: want permissions.defaultMode=%q, got:\n%s", mode, mode, settings)
		}
	}

	// PermissionMode wins over the SkipPermissions back-compat default.
	dev := NewClaudeAdapterDev() // SkipPermissions = true
	dev.PermissionMode = "plan"
	settings, err := dev.BootDirSpec().PlantedFiles[2].Render(PlantContext{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(settings), &got); err != nil {
		t.Fatalf("settings.json not valid JSON: %v\ngot:\n%s", err, settings)
	}
	if perms, ok := got["permissions"].(map[string]any); !ok || perms["defaultMode"] != "plan" {
		t.Errorf("PermissionMode must win over SkipPermissions, want defaultMode=plan, got:\n%s", settings)
	}

	// An unrecognized value fails the Render rather than planting junk.
	bad := NewClaudeAdapter()
	bad.PermissionMode = "yolo"
	if _, err := bad.BootDirSpec().PlantedFiles[2].Render(PlantContext{}); err == nil {
		t.Error("PermissionMode=\"yolo\" should fail Render, got nil error")
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
	got := renderMCPJSON("http://127.0.0.1:65535/mcp", muxEntry{})
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
	got := renderMCPJSON("", muxEntry{})
	if strings.TrimSpace(got) != `{"mcpServers":{}}` {
		t.Errorf("renderMCPJSON(\"\") should emit `{\"mcpServers\":{}}`, got %q", got)
	}
}

// TestRenderMCPJSON_LoopbackPlusMux pins the dual-entry shape emitted
// when both loopback and Mux are configured (CW-20260510-0110). Spawned
// task agents need access to the per-task loopback (role-aware
// clockwork surface) AND the portfolio-wide Mux aggregator (Vanta +
// cross-task clockwork + cerberus).
//
// stdio shape mirrors the canonical user-shell entry from
// ~/.claude.json: `{"type":"stdio","command":"...","args":[...],
// "env":{...}}`.
func TestRenderMCPJSON_LoopbackPlusMux(t *testing.T) {
	mux := muxEntry{
		Command: "/usr/local/bin/mux",
		Args: []string{
			"mcp", "--proxy",
			"--servers", "vanta,clockwork,cerberus",
			"--token", "local-dev",
			"--scopes", "session.write,message.write",
		},
	}
	got := renderMCPJSON("http://127.0.0.1:65535/mcp", mux)

	wants := []string{
		`"mcpServers"`,
		`"loopback"`,
		`"type": "http"`,
		`"url": "http://127.0.0.1:65535/mcp"`,
		`"mux"`,
		`"type": "stdio"`,
		`"command": "/usr/local/bin/mux"`,
		`"args"`,
		`"--proxy"`,
		`"--servers"`,
		`"vanta,clockwork,cerberus"`,
		`"env"`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("renderMCPJSON loopback+mux missing %q\ngot:\n%s", w, got)
		}
	}

	// Ensure the JSON is valid and round-trips. A regression here
	// (e.g. trailing comma, unbalanced brace) silently breaks bare-
	// mode claude with a noisy validator error.
	var parsed struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("renderMCPJSON loopback+mux JSON invalid: %v\ngot:\n%s", err, got)
	}
	if _, ok := parsed.MCPServers["loopback"]; !ok {
		t.Error("loopback entry absent after round-trip")
	}
	muxEntryParsed, ok := parsed.MCPServers["mux"]
	if !ok {
		t.Fatal("mux entry absent after round-trip")
	}
	if muxEntryParsed["type"] != "stdio" {
		t.Errorf("mux.type: want stdio, got %v", muxEntryParsed["type"])
	}
	if muxEntryParsed["command"] != "/usr/local/bin/mux" {
		t.Errorf("mux.command: want /usr/local/bin/mux, got %v", muxEntryParsed["command"])
	}
}

// TestRenderMCPJSON_MuxOnly pins the mux-only branch (loopback empty,
// Mux present). Rare in practice — clockwork always provisions the
// loopback — but the renderer must still emit a valid one-entry config
// so the field is forensically discoverable when an operator
// deliberately disables the loopback (e.g. by passing nil
// LoopbackBuilder in a unit test).
func TestRenderMCPJSON_MuxOnly(t *testing.T) {
	got := renderMCPJSON("", muxEntry{
		Command: "/path/to/mux",
		Args:    []string{"mcp", "--proxy"},
	})
	wants := []string{
		`"mcpServers"`,
		`"mux"`,
		`"type": "stdio"`,
		`"command": "/path/to/mux"`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("renderMCPJSON mux-only missing %q\ngot:\n%s", w, got)
		}
	}
	if strings.Contains(got, `"loopback"`) {
		t.Errorf("renderMCPJSON mux-only should NOT emit loopback entry, got:\n%s", got)
	}
}

// TestRenderMCPJSON_MuxEnv pins env passthrough into the planted Mux
// stdio entry. The KEY=VALUE shape lands as a JSON object under the
// `env` key. Operators use this for Mux-scoped overrides (e.g. a
// non-default token via env without recompiling the daemon).
func TestRenderMCPJSON_MuxEnv(t *testing.T) {
	got := renderMCPJSON("http://127.0.0.1:1/mcp", muxEntry{
		Command: "/bin/mux",
		Args:    []string{"mcp"},
		Env:     []string{"MUX_TOKEN=secret-xyz", "MUX_VERBOSE=1"},
	})
	wants := []string{
		`"env"`,
		`"MUX_TOKEN": "secret-xyz"`,
		`"MUX_VERBOSE": "1"`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("renderMCPJSON mux env missing %q\ngot:\n%s", w, got)
		}
	}
}

// TestMuxEnvMap_Edge pins the helper's behavior on edge cases: bare
// keys (no `=`), empty input, multiple equals signs (only first split).
func TestMuxEnvMap_Edge(t *testing.T) {
	if got := muxEnvMap(nil); len(got) != 0 {
		t.Errorf("muxEnvMap(nil) should be empty, got %v", got)
	}
	if got := muxEnvMap([]string{}); len(got) != 0 {
		t.Errorf("muxEnvMap([]) should be empty, got %v", got)
	}
	got := muxEnvMap([]string{"KEY=VALUE", "BARE", "EQ=A=B=C"})
	if got["KEY"] != "VALUE" {
		t.Errorf("KEY=VALUE: got %v", got["KEY"])
	}
	if got["BARE"] != "" {
		t.Errorf("BARE (no =): want empty value, got %v", got["BARE"])
	}
	if got["EQ"] != "A=B=C" {
		t.Errorf("EQ=A=B=C: should split on first '='; got %v", got["EQ"])
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
	if strings.Contains(configTOML, "[mcp_servers.mux]") {
		t.Errorf("config.toml should not contain mux block when MuxCommand is empty, got %q", configTOML)
	}
}

// TestCodexAdapter_ExecMode_BootDirSpec_HasProjectDirArg is a regression
// guard for the exec-mode (default) branch: BootDirSpec MUST keep emitting
// `--cd {{.ProjectDir}}` so single-turn `codex exec` invocations continue
// to receive the project root. Pins behavior alongside the app-server
// suppression below.
func TestCodexAdapter_ExecMode_BootDirSpec_HasProjectDirArg(t *testing.T) {
	a := NewCodexAdapter()
	spec := a.BootDirSpec()
	if spec.ProjectDirArg != "--cd {{.ProjectDir}}" {
		t.Errorf("exec mode ProjectDirArg: want %q, got %q",
			"--cd {{.ProjectDir}}", spec.ProjectDirArg)
	}
}

// TestCodexAdapter_AppServer_BootDirSpec_NoProjectDirArg pins that the
// app-server adapter suppresses ProjectDirArg. `codex app-server` rejects
// `--cd` (codex 0.130.0 exits 2 with "unexpected argument '--cd' found"),
// so the long-lived daemon must spawn without that flag. Project access
// is granted via JSON-RPC `thread/start` parameters at the consumer
// runtime layer (go-agent-sessions jsonRpcStdio kind), not via argv.
func TestCodexAdapter_AppServer_BootDirSpec_NoProjectDirArg(t *testing.T) {
	a := NewCodexAdapterAppServer()
	spec := a.BootDirSpec()
	if spec.ProjectDirArg != "" {
		t.Errorf("app-server ProjectDirArg: want \"\", got %q (codex app-server rejects --cd)",
			spec.ProjectDirArg)
	}
	// The rest of the spec must still match the exec adapter — CODEX_HOME
	// isolation, CwdBootDir, and the PlantedFiles set are all still
	// load-bearing for app-server.
	if spec.CwdPreference != CwdBootDir {
		t.Errorf("CwdPreference: want CwdBootDir, got %v", spec.CwdPreference)
	}
	foundEnv := false
	for _, e := range spec.EnvAmendments {
		if strings.Contains(e, "CODEX_HOME") && strings.Contains(e, "{{.BootDir}}") {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("expected CODEX_HOME env amendment in app-server spec, got %v",
			spec.EnvAmendments)
	}
}

// TestCodexBootDirSpec_EmptyMCP pins the empty-loopback path: with no
// MCPLoopbackURL the config.toml has no [mcp_servers.*] blocks, but it still
// carries the approval/sandbox policy header — the headless-codex deadlock
// fix means the policy is ALWAYS planted. The .mcp.json sidecar is exercised
// by TestRenderMCPJSON_Empty — this test only walks the config.toml branch.
func TestCodexBootDirSpec_EmptyMCP(t *testing.T) {
	a := NewCodexAdapter()
	spec := a.BootDirSpec()

	pctx := PlantContext{AgentName: "codex-exec"} // no MCPLoopbackURL

	configTOML, err := spec.PlantedFiles[2].Render(pctx)
	if err != nil {
		t.Fatalf("config.toml render: %v", err)
	}
	if strings.Contains(configTOML, "[mcp_servers.") {
		t.Errorf("empty MCPLoopbackURL should produce no mcp_servers blocks, got %q", configTOML)
	}
	// The policy header must be present even with no MCP servers — this is
	// the fix: a headless codex with no planted approval_policy falls back
	// to its interactive default and deadlocks.
	for _, want := range []string{`approval_policy = "never"`, `sandbox_mode = "workspace-write"`} {
		if !strings.Contains(configTOML, want) {
			t.Errorf("config.toml missing %q, got %q", want, configTOML)
		}
	}
}

// TestCodexBootDirSpec_InvalidApprovalPolicy pins that an unrecognized
// ApprovalPolicy fails the config.toml Render rather than planting a
// broken/ignored value.
func TestCodexBootDirSpec_InvalidApprovalPolicy(t *testing.T) {
	a := &CodexAdapter{ApprovalPolicy: "bogus"}
	spec := a.BootDirSpec()
	if _, err := spec.PlantedFiles[2].Render(PlantContext{AgentName: "codex-exec"}); err == nil {
		t.Error("invalid ApprovalPolicy should fail the config.toml Render")
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
		"AGENTS.md":   0,     // unset → 0o644 fallback
		"boot.md":     0,     // unset → 0o644 fallback
		"config.toml": 0o600, // loopback URL — secret-ish
		"auth.json":   0o600, // OAuth tokens / API keys
		".mcp.json":   0o600, // loopback URL sidecar
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

// TestRenderCodexConfigTOML pins the policy header (always emitted) plus the
// loopback and no-MCP branches of the codex TOML emitter.
func TestRenderCodexConfigTOML(t *testing.T) {
	got := renderCodexConfigTOML("never", "workspace-write", "http://127.0.0.1:65535/mcp", muxEntry{})
	want := "approval_policy = \"never\"\nsandbox_mode = \"workspace-write\"\n\n" +
		"[mcp_servers.loopback]\nurl = \"http://127.0.0.1:65535/mcp\"\n"
	if got != want {
		t.Errorf("populated shape mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}

	// No MCP servers: the config is no longer empty — the policy header is
	// always emitted so a headless codex never falls back to its
	// interactive approval default (the deadlock).
	noMCP := renderCodexConfigTOML("never", "workspace-write", "", muxEntry{})
	wantNoMCP := "approval_policy = \"never\"\nsandbox_mode = \"workspace-write\"\n"
	if noMCP != wantNoMCP {
		t.Errorf("no-MCP shape mismatch\nwant:\n%s\ngot:\n%s", wantNoMCP, noMCP)
	}
}

// TestResolveCodexExecPolicy pins the default-application and validation of
// CodexAdapter.ApprovalPolicy / SandboxMode.
func TestResolveCodexExecPolicy(t *testing.T) {
	approval, sandbox, err := resolveCodexExecPolicy("", "")
	if err != nil {
		t.Fatalf("default resolve: %v", err)
	}
	if approval != "never" || sandbox != "workspace-write" {
		t.Errorf("defaults = (%q, %q), want (never, workspace-write)", approval, sandbox)
	}

	approval, sandbox, err = resolveCodexExecPolicy("on-request", "danger-full-access")
	if err != nil {
		t.Fatalf("explicit resolve: %v", err)
	}
	if approval != "on-request" || sandbox != "danger-full-access" {
		t.Errorf("explicit = (%q, %q)", approval, sandbox)
	}

	if _, _, err := resolveCodexExecPolicy("yolo", ""); err == nil {
		t.Error("invalid ApprovalPolicy should error")
	}
	if _, _, err := resolveCodexExecPolicy("", "wide-open"); err == nil {
		t.Error("invalid SandboxMode should error")
	}
}

// TestRenderCodexConfigTOML_LoopbackPlusMux pins the load-bearing dual-entry
// shape for Codex: loopback HTTP plus stdio mux in config.toml.
func TestRenderCodexConfigTOML_LoopbackPlusMux(t *testing.T) {
	got := renderCodexConfigTOML("never", "workspace-write", "http://127.0.0.1:65535/mcp", muxEntry{
		Command: "/path/to/mux",
		Args: []string{
			"mcp",
			"--proxy",
			"--servers=vanta,clockwork,cerberus",
			"--token",
			"local-dev",
			"--scopes",
			"session.write,message.write",
		},
		Env: []string{
			"MUX_AUTH_MODE=stdio",
			"EMPTY",
		},
	})
	want := `approval_policy = "never"
sandbox_mode = "workspace-write"

[mcp_servers.loopback]
url = "http://127.0.0.1:65535/mcp"

[mcp_servers.mux]
command = "/path/to/mux"
args = ["mcp", "--proxy", "--servers=vanta,clockwork,cerberus", "--token", "local-dev", "--scopes", "session.write,message.write"]

[mcp_servers.mux.env]
"MUX_AUTH_MODE" = "stdio"
"EMPTY" = ""
`
	if got != want {
		t.Errorf("loopback+mux shape mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestRenderCodexConfigTOML_MuxOnly pins the mux-only branch.
func TestRenderCodexConfigTOML_MuxOnly(t *testing.T) {
	got := renderCodexConfigTOML("never", "workspace-write", "", muxEntry{
		Command: "/path/to/mux",
		Args:    []string{"mcp", "--proxy"},
	})
	want := `approval_policy = "never"
sandbox_mode = "workspace-write"

[mcp_servers.mux]
command = "/path/to/mux"
args = ["mcp", "--proxy"]
`
	if got != want {
		t.Errorf("mux-only shape mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestRenderCodexConfigTOML_MuxOnly_EmptyArgs pins that stdio mux entries emit
// a stable empty args array rather than omitting the line entirely.
func TestRenderCodexConfigTOML_MuxOnly_EmptyArgs(t *testing.T) {
	got := renderCodexConfigTOML("never", "workspace-write", "", muxEntry{
		Command: "/path/to/mux",
	})
	want := `approval_policy = "never"
sandbox_mode = "workspace-write"

[mcp_servers.mux]
command = "/path/to/mux"
args = []
`
	if got != want {
		t.Errorf("mux-only empty-args shape mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestRenderCodexConfigTOML_MuxEnvKeysQuoted pins that env keys are quoted in
// TOML and empty keys are skipped, preventing dotted keys or invalid syntax.
func TestRenderCodexConfigTOML_MuxEnvKeysQuoted(t *testing.T) {
	got := renderCodexConfigTOML("never", "workspace-write", "", muxEntry{
		Command: "/path/to/mux",
		Env: []string{
			"MUX.AUTH.MODE=stdio",
			"KEY WITH SPACE=value",
			"=drop-me",
		},
	})
	wants := []string{
		`args = []`,
		`[mcp_servers.mux.env]`,
		`"MUX.AUTH.MODE" = "stdio"`,
		`"KEY WITH SPACE" = "value"`,
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("quoted env TOML missing %q\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"" = "drop-me"`) {
		t.Errorf("empty env key should be skipped, got:\n%s", got)
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
	// With no MCP loopback URL, opencode.json should NOT carry an `mcp`
	// block. opencode merges per-dir config with global, so an empty
	// mcp map would be benign — but absent is the cleaner signal.
	if strings.Contains(opencodeJSON, `"mcp"`) {
		t.Errorf("opencode.json should omit mcp block when MCPLoopbackURL is empty, got %s", opencodeJSON)
	}
}

// TestOpencodeBootDirSpec_MCPBlock pins the mcp-block shape opencode
// expects. opencode 1.14.x reads `mcp:{<name>:{type,url,enabled}}`
// from opencode.json (NOT `.mcp.json` — that file is a claude
// convention opencode ignores). The transport keyword is "remote"
// (HTTP/SSE), not claude's "http". Probed empirically against
// opencode 1.14.20.
func TestOpencodeBootDirSpec_MCPBlock(t *testing.T) {
	a := NewOpencodeAdapter()
	a.Agent = "executor"
	spec := a.BootDirSpec()

	pctx := PlantContext{
		AgentName:      "executor",
		MCPLoopbackURL: "http://127.0.0.1:65500/mcp",
	}
	opencodeJSON, err := spec.PlantedFiles[2].Render(pctx)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wants := []string{
		`"mcp"`,
		`"loopback"`,
		`"type": "remote"`,
		`"url": "http://127.0.0.1:65500/mcp"`,
		`"enabled": true`,
	}
	for _, w := range wants {
		if !strings.Contains(opencodeJSON, w) {
			t.Errorf("opencode.json missing %q\ngot:\n%s", w, opencodeJSON)
		}
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

// TestClaudeBootDirSpec_MuxEntry pins that PlantContext.MuxCommand
// flows through the claude BootDirSpec into the planted .mcp.json as
// a stdio-shape "mux" server entry alongside the loopback HTTP entry.
//
// CW-20260510-0110: spawned task agents need access to Vanta + the
// portfolio-wide Mux-aggregated MCP surface, not just the per-task
// loopback. The Mux entry spawns the Mux aggregator as an stdio child
// per session (mirrors how the user's interactive claude reaches Mux
// via ~/.claude.json's `mcpServers.mux`).
func TestClaudeBootDirSpec_MuxEntry(t *testing.T) {
	a := NewClaudeAdapter()
	spec := a.BootDirSpec()

	pctx := PlantContext{
		SystemPrompt:   "you are an orchestrator",
		BootContent:    "boot",
		MCPLoopbackURL: "http://127.0.0.1:9000/mcp",
		MuxCommand:     "/Users/chrispian/go/bin/mux",
		MuxArgs: []string{
			"mcp", "--proxy",
			"--servers", "vanta,clockwork,cerberus",
			"--token", "local-dev",
			"--scopes", "session.write,message.write",
		},
	}
	mcpJSON, err := spec.PlantedFiles[3].Render(pctx)
	if err != nil {
		t.Fatalf(".mcp.json render: %v", err)
	}

	// Loopback entry preserved.
	if !strings.Contains(mcpJSON, `"loopback"`) {
		t.Error("loopback entry missing")
	}
	if !strings.Contains(mcpJSON, `"http://127.0.0.1:9000/mcp"`) {
		t.Error("loopback URL missing")
	}
	// Mux entry added with stdio shape.
	if !strings.Contains(mcpJSON, `"mux"`) {
		t.Error("mux entry missing")
	}
	if !strings.Contains(mcpJSON, `"type": "stdio"`) {
		t.Error("mux entry should declare type:stdio")
	}
	if !strings.Contains(mcpJSON, `"command": "/Users/chrispian/go/bin/mux"`) {
		t.Error("mux command missing")
	}
	if !strings.Contains(mcpJSON, `"vanta,clockwork,cerberus"`) {
		t.Error("mux servers arg missing")
	}

	// CLAUDE.md should mention both loopback URL and mux command so the
	// LLM has a textual cue about which MCP entry serves which surface.
	claudeMD, err := spec.PlantedFiles[0].Render(pctx)
	if err != nil {
		t.Fatalf("CLAUDE.md render: %v", err)
	}
	if !strings.Contains(claudeMD, "http://127.0.0.1:9000/mcp") {
		t.Error("CLAUDE.md should reference loopback URL")
	}
	if !strings.Contains(claudeMD, "/Users/chrispian/go/bin/mux") {
		t.Error("CLAUDE.md should reference Mux command when MuxCommand is set")
	}
}

// TestClaudeBootDirSpec_NoMux_BackCompat pins that PlantContext with
// no Mux fields preserves the pre-CW-20260510-0110 byte-for-byte
// shape — single "loopback" entry, no `mux` key. This is the back-
// compat guarantee for callers that haven't bumped to populate the
// new fields.
func TestClaudeBootDirSpec_NoMux_BackCompat(t *testing.T) {
	a := NewClaudeAdapter()
	spec := a.BootDirSpec()

	pctx := PlantContext{
		MCPLoopbackURL: "http://127.0.0.1:9000/mcp",
	}
	mcpJSON, err := spec.PlantedFiles[3].Render(pctx)
	if err != nil {
		t.Fatalf(".mcp.json render: %v", err)
	}
	if strings.Contains(mcpJSON, `"mux"`) {
		t.Errorf("no-mux back-compat: .mcp.json should NOT contain `\"mux\"` entry, got:\n%s", mcpJSON)
	}
	want := `{
  "mcpServers": {
    "loopback": {
      "type": "http",
      "url": "http://127.0.0.1:9000/mcp"
    }
  }
}
`
	if mcpJSON != want {
		t.Errorf("no-mux back-compat: shape drift\nwant:\n%s\ngot:\n%s", want, mcpJSON)
	}
}

// TestOpencodeBootDirSpec_MuxEntry pins that PlantContext.MuxCommand
// threads into opencode.json's `mcp` block as a "local" stdio entry
// alongside the existing "remote" loopback entry. Schema notes:
//
//   - opencode's stdio transport keyword is "local" (NOT claude's
//     "stdio").
//   - opencode's stdio entry uses a SINGLE `command` array (argv[0] is
//     the binary, the rest are args); it does NOT use the claude
//     command-string + args-array split.
//
// Probed against opencode 1.14.46 user config (~/.config/opencode/
// config.json).
func TestOpencodeBootDirSpec_MuxEntry(t *testing.T) {
	a := NewOpencodeAdapter()
	a.Agent = "executor"
	spec := a.BootDirSpec()

	pctx := PlantContext{
		AgentName:      "executor",
		MCPLoopbackURL: "http://127.0.0.1:65500/mcp",
		MuxCommand:     "/Users/chrispian/go/bin/mux",
		MuxArgs: []string{
			"mcp", "--proxy",
			"--servers", "vanta,clockwork,cerberus",
			"--token", "local-dev",
			"--scopes", "session.write,message.write",
		},
	}
	opencodeJSON, err := spec.PlantedFiles[2].Render(pctx)
	if err != nil {
		t.Fatalf("opencode.json render: %v", err)
	}

	// Loopback "remote" entry preserved.
	if !strings.Contains(opencodeJSON, `"loopback"`) {
		t.Error("loopback entry missing")
	}
	if !strings.Contains(opencodeJSON, `"type": "remote"`) {
		t.Error("loopback should still declare type:remote")
	}
	// Mux entry added with stdio shape.
	if !strings.Contains(opencodeJSON, `"mux"`) {
		t.Error("mux entry missing")
	}
	if !strings.Contains(opencodeJSON, `"type": "local"`) {
		t.Error("mux entry should declare type:local (opencode's stdio keyword)")
	}
	// command is a single array — argv[0] is the binary path.
	if !strings.Contains(opencodeJSON, `"/Users/chrispian/go/bin/mux"`) {
		t.Error("mux command (binary path) missing")
	}
	if !strings.Contains(opencodeJSON, `"--proxy"`) {
		t.Error("mux args should be inlined into the command array")
	}
	// enabled:true so opencode honors the entry.
	if !strings.Contains(opencodeJSON, `"enabled": true`) {
		t.Error("mux entry should declare enabled:true")
	}

	// Round-trip the JSON and verify command is an array (not a string).
	var parsed struct {
		MCP map[string]map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(opencodeJSON), &parsed); err != nil {
		t.Fatalf("opencode.json invalid JSON: %v", err)
	}
	muxBlock, ok := parsed.MCP["mux"]
	if !ok {
		t.Fatal("mux entry absent after round-trip")
	}
	cmd, ok := muxBlock["command"].([]any)
	if !ok {
		t.Fatalf("mux.command should be an array, got %T", muxBlock["command"])
	}
	if len(cmd) < 1 {
		t.Fatal("mux.command array empty")
	}
	if cmd[0] != "/Users/chrispian/go/bin/mux" {
		t.Errorf("mux.command[0]: want binary path, got %v", cmd[0])
	}
}

// TestCodexBootDirSpec_MuxEntry pins that the codex .mcp.json renderer
// produces both loopback and mux entries when MuxCommand is set.
//
// Codex itself reads config.toml, not .mcp.json. This test therefore pins
// both the load-bearing config.toml mux block and the legacy .mcp.json sidecar
// parity shape.
func TestCodexBootDirSpec_MuxEntry(t *testing.T) {
	a := NewCodexAdapter()
	spec := a.BootDirSpec()

	pctx := PlantContext{
		SystemPrompt:   "you are codex",
		AgentName:      "codex-exec",
		MCPLoopbackURL: "http://lp:1",
		MuxCommand:     "/path/to/mux",
		MuxArgs:        []string{"mcp", "--proxy"},
	}

	configTOML, err := spec.PlantedFiles[2].Render(pctx)
	if err != nil {
		t.Fatalf("config.toml render: %v", err)
	}
	for _, want := range []string{
		`[mcp_servers.loopback]`,
		`url = "http://lp:1"`,
		`[mcp_servers.mux]`,
		`command = "/path/to/mux"`,
		`args = ["mcp", "--proxy"]`,
	} {
		if !strings.Contains(configTOML, want) {
			t.Errorf("config.toml missing %q\ngot:\n%s", want, configTOML)
		}
	}

	// .mcp.json is at index 4 post-v0.15.0 (AGENTS.md, boot.md,
	// config.toml, auth.json, .mcp.json).
	mcpJSON, err := spec.PlantedFiles[4].Render(pctx)
	if err != nil {
		t.Fatalf(".mcp.json render: %v", err)
	}
	if !strings.Contains(mcpJSON, `"mux"`) {
		t.Error("mux entry missing")
	}
	if !strings.Contains(mcpJSON, `"type": "stdio"`) {
		t.Error("mux entry should declare type:stdio")
	}
	if !strings.Contains(mcpJSON, `"command": "/path/to/mux"`) {
		t.Error("mux command missing")
	}
}
