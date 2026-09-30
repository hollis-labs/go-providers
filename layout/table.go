package layout

// table is the one source of truth. Values were decided by the Step 0 probe
// (docs/HARNESS-DISCOVERY.md); the probe ids on each row exist in the newest
// golden under provider/testdata/harness-discovery, which a test enforces.
//
// Order matters only for skills rows of one (provider, mode): the first is the
// primary that SkillRoot returns.
var table = []Entry{
	// ---- Claude Code. Launch: cwd = boot, config discovered from cwd. ----
	{Provider: Claude, Concern: Instructions, Root: RootBoot, Rel: "CLAUDE.md", CWD: RootBoot,
		Unprobed: "instruction file auto-load is a model-visible effect Step 0 does not measure (no model call)"},
	{Provider: Claude, Mode: ModeClaudeBare, Concern: Instructions, Root: RootBoot, Rel: "CLAUDE.md", Flag: "--append-system-prompt-file", CWD: RootBoot,
		Unprobed: "--bare skips CLAUDE.md discovery; the flag delivers it (model-visible, not measured)"},
	{Provider: Claude, Concern: Boot, Root: RootBoot, Rel: "boot.md",
		Unprobed: "kick-off content read by the launcher, not discovered by the harness"},
	{Provider: Claude, Concern: MCP, Root: RootBoot, Rel: ".mcp.json", FileMode: 0o600, Flag: "--mcp-config", CWD: RootBoot,
		Probe: []string{"CFG1", "CFG4"},
		Note:  "read from cwd by convention (CFG1) and by --mcp-config <file> (CFG4)"},
	{Provider: Claude, Concern: NativeConfig, Root: RootBoot, Rel: ".claude/settings.json", CWD: RootBoot,
		Probe: []string{"CFG1"},
		Note:  "read from cwd .claude/settings.json in -p mode; --add-dir loads neither settings nor .mcp.json (CFG3)"},
	{Provider: Claude, Mode: ModeClaudeBare, Concern: NativeConfig, Root: RootBoot, Rel: ".claude/settings.json", Flag: "--settings", CWD: RootBoot,
		Probe: []string{"CFG2"},
		Note:  "--bare skips cwd discovery, so the file is passed explicitly"},
	{Provider: Claude, Concern: Skills, Root: RootBoot, Rel: ".claude/skills", Form: FormDir, CWD: RootBoot,
		Probe: []string{"C1", "C2"},
		Note:  "<cwd>/.claude/skills/<name>/SKILL.md; flat <name>.md is not read"},
	{Provider: Claude, Mode: ModeClaudeBare, Concern: Skills, Root: RootBoot, Rel: ".claude/skills", Form: FormDir, Flag: "--add-dir", CWD: RootBoot,
		Probe: []string{"C4", "C5"},
		Note:  "--bare reads no cwd or user skills (C4); only <--add-dir dir>/.claude/skills (C5), so the boot root must itself be --add-dir'ed when skills are projected"},
	{Provider: Claude, Concern: ProjectDir, Root: RootProject, Flag: "--add-dir", CWD: RootBoot,
		Probe: []string{"C3", "C5"},
		Note:  "extra directory, not a working root; accepted in every mode. The built-in launch convention passes it in claude-bare only (AK adds it for the other modes)"},

	// ---- Codex. Launch: cwd = boot, CODEX_HOME = boot. ----
	{Provider: Codex, Concern: Instructions, Root: RootBoot, Rel: "AGENTS.md", CWD: RootBoot,
		Probe: []string{"CFG2", "CFG3"},
		Note:  "AGENTS.md is read from cwd and from $CODEX_HOME"},
	{Provider: Codex, Concern: Boot, Root: RootBoot, Rel: "boot.md",
		Unprobed: "kick-off content read by the launcher, not discovered by the harness"},
	{Provider: Codex, Concern: NativeConfig, Root: RootBoot, Rel: "config.toml", FileMode: 0o600,
		Env: map[string]string{"CODEX_HOME": "boot"}, CWD: RootBoot,
		Probe: []string{"CFG2", "CFG3"},
		Note:  "$CODEX_HOME/config.toml. A .codex/config.toml under a redirected HOME also works (CFG4) but is not this convention; a project .codex/config.toml was not applied (untrusted, CFG1)"},
	{Provider: Codex, Concern: Auth, Root: RootBoot, Rel: "auth.json", FileMode: 0o600,
		Env: map[string]string{"CODEX_HOME": "boot"}, CWD: RootBoot,
		Unprobed: "credential placeholder resolved by runtime preparation; no credential is used by Step 0"},
	{Provider: Codex, Concern: MCP, Root: RootBoot, Rel: ".mcp.json", FileMode: 0o600,
		Unprobed: "mirror for operators; Codex reads MCP servers from config.toml, not this file"},
	{Provider: Codex, Concern: Skills, Root: RootBoot, Rel: "skills", Form: FormDir,
		Env: map[string]string{"CODEX_HOME": "boot"}, CWD: RootBoot,
		Probe: []string{"X2", "X3", "X4"},
		Note:  "$CODEX_HOME/skills/<name>/SKILL.md survives with and without --cd <project>; <boot>/.agents/skills does not survive --cd (X2 vs X3), and singular skill/ and flat .md are never read"},
	{Provider: Codex, Mode: ModeCodexExec, Concern: ProjectDir, Root: RootProject, Flag: "--cd",
		Probe: []string{"X3"},
		Note:  "--cd is the working root (codex -C), not an extra directory; app-server takes the project root over JSON-RPC instead"},

	// ---- OpenCode. Launch: cwd = project, OPENCODE_CONFIG_DIR = boot. ----
	{Provider: OpenCode, Concern: Instructions, Root: RootBoot, Rel: "agents/" + AgentPlaceholder + ".md",
		Env: map[string]string{"OPENCODE_CONFIG_DIR": "boot"}, CWD: RootProject,
		Unprobed: "agent prompt file is model-visible only; Step 0 does not measure it"},
	{Provider: OpenCode, Concern: NativeConfig, Root: RootBoot, Rel: "opencode.json",
		Env: map[string]string{"OPENCODE_CONFIG_DIR": "boot"}, CWD: RootProject,
		Probe: []string{"CFG2"},
		Note:  "$OPENCODE_CONFIG_DIR/opencode.json is merged with project and user config"},
	{Provider: OpenCode, Concern: Boot, Root: RootBoot, Rel: "boot.md",
		Unprobed: "kick-off content read by the launcher, not discovered by the harness"},
	{Provider: OpenCode, Concern: MCP, Root: RootBoot, Rel: ".mcp.json", FileMode: 0o600,
		Unprobed: "mirror for operators; OpenCode reads MCP servers from opencode.json"},
	{Provider: OpenCode, Concern: Skills, Root: RootBoot, Rel: "skills", Form: FormDir,
		Env: map[string]string{"OPENCODE_CONFIG_DIR": "boot"}, CWD: RootProject,
		Probe: []string{"O2"},
		Note:  "$OPENCODE_CONFIG_DIR/skills/<name>/SKILL.md (singular skill/ also read; flat .md is not)"},
	{Provider: OpenCode, Concern: Skills, Root: RootBoot, Rel: ".opencode/skills", Form: FormDir, CWD: RootBoot,
		Probe: []string{"O2", "O3"},
		Note:  "alias, valid only when cwd == boot (O3); NOT scanned under $OPENCODE_CONFIG_DIR when cwd is the project (O2)"},
	{Provider: OpenCode, Mode: ModeOpenCodeRun, Concern: ProjectDir, Root: RootProject, Flag: "--dir",
		Unprobed: "flag is exercised by a real run, which needs a model call"},
	{Provider: OpenCode, Mode: ModeOpenCodeServeHTTP, Concern: Runtime, Root: RootProject, Rel: "", Flag: "serve",
		Aliases:  []string{"serve-http", "http-sse"},
		Unprobed: "wire token for the HTTP runtime, not a harness discovery path; canonical spelling serve-http, http-sse is the public runtimeevents value and is never renamed here"},

	// ---- Antigravity (agy). Launch: cwd = boot, project via --add-dir. ----
	// agy has no config-dir variable: its global config is ~/.gemini/config,
	// shared with the Antigravity desktop app, and relocating HOME relocates
	// the credentials too. Everything projected therefore lives in the
	// workspace customization root <cwd>/.agents, which agy discovers by
	// walking from cwd (no .git needed). Global MCP servers and skills under
	// ~/.gemini/config still load alongside; a workspace skill shadows a
	// global one of the same name. Rows were verified live against agy 1.2.7
	// (go-providers testdata/antigravity; Tether CW-20260930-0107), not by the
	// Step 0 harness probe.
	{Provider: Antigravity, Concern: Instructions, Root: RootBoot, Rel: "AGENTS.md", CWD: RootBoot,
		Unprobed: "verified live against agy 1.2.7: <cwd>/AGENTS.md and the --add-dir project's own AGENTS.md both apply; not in the Step 0 golden"},
	{Provider: Antigravity, Concern: Boot, Root: RootBoot, Rel: "boot.md",
		Unprobed: "kick-off content read by the launcher, not discovered by the harness"},
	{Provider: Antigravity, Concern: NativeConfig, Root: RootBoot, Rel: ".agents/plugins/tether/plugin.json", CWD: RootBoot,
		Unprobed: "verified live against agy 1.2.7: a workspace plugin under <cwd>/.agents/plugins/<name>/ is discovered and enabled by default; plugin.json is its marker"},
	{Provider: Antigravity, Concern: MCP, Root: RootBoot, Rel: ".agents/plugins/tether/mcp_config.json", FileMode: 0o600, CWD: RootBoot,
		Unprobed: "verified live against agy 1.2.7: the plugin's servers are spawned (cwd = the plugin dir) and exposed as <plugin>_<server>; tool schemas are cached under ~/.gemini/antigravity-cli/mcp/<plugin>_<server>/, so names must be stable per server"},
	{Provider: Antigravity, Concern: Skills, Root: RootBoot, Rel: ".agents/skills", Form: FormDir, CWD: RootBoot,
		Unprobed: "verified live against agy 1.2.7: <cwd>/.agents/skills/<name>/SKILL.md loads and shadows a global ~/.gemini/config/skills/<name>"},
	{Provider: Antigravity, Mode: ModeAntigravityPrint, Concern: ProjectDir, Root: RootProject, Flag: "--add-dir",
		Unprobed: "verified live against agy 1.2.7: an --add-dir project is readable and its AGENTS.md applies"},
}
