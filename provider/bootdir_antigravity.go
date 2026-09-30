package provider

import (
	"encoding/json"

	"github.com/hollis-labs/go-providers/layout"
)

// BootDirSpec for the Antigravity CLI (agy).
//
// Layout (cwd = boot dir, project attached with --add-dir):
//
//	<bootDir>/
//	├── AGENTS.md                              # agent instructions (rules)
//	├── boot.md                                # task kickoff content
//	└── .agents/plugins/tether/
//	    ├── plugin.json                        # marks the workspace plugin
//	    └── mcp_config.json                    # MCP servers for this launch
//
// agy has no config-dir variable, and its global config (~/.gemini/config)
// is shared with the Antigravity desktop app, so nothing is written there.
// The workspace customization root <cwd>/.agents is discovered without a
// .git; skills go to .agents/skills/<name>/SKILL.md (layout Skills row).
// Global MCP servers and skills still load alongside the projected ones.
//
// The plugin is always named "tether" and the servers keep stable names:
// agy caches each plugin server's tool schemas under
// ~/.gemini/antigravity-cli/mcp/<plugin>_<server>/, so a name must always
// mean the same server.
func (a *AntigravityAdapter) BootDirSpec() BootDirSpec {
	const pid, mode = ProviderAntigravity, ModeAntigravityPrint
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: layoutRel(pid, mode, layout.Instructions, ""),
				Render: func(ctx PlantContext) (string, error) {
					return AgentsMD(AgentInfo{Name: ctx.AgentName, SystemPrompt: ctx.SystemPrompt}, ctx.MCPLoopbackURL), nil
				},
			},
			{
				RelPath: layoutRel(pid, mode, layout.Boot, ""),
				Render: func(ctx PlantContext) (string, error) {
					return ctx.BootContent, nil
				},
			},
			{
				RelPath: layoutRel(pid, mode, layout.NativeConfig, ""),
				Render: func(PlantContext) (string, error) {
					return renderAntigravityPluginJSON(), nil
				},
			},
			{
				RelPath: layoutRel(pid, mode, layout.MCP, ""),
				Mode:    layoutFileMode(pid, mode, layout.MCP),
				Render: func(ctx PlantContext) (string, error) {
					return renderAntigravityMCPConfig(ctx.MCPLoopbackURL, muxEntryFromContext(ctx)), nil
				},
			},
		},
		CwdPreference: CwdBootDir,
		ProjectDirArg: layoutEntry(pid, mode, layout.ProjectDir).Flag + " {{.ProjectDir}}",
		Notes:         "workspace-only projection: agy's global ~/.gemini/config is shared with the desktop app and never written",
	}
}

func renderAntigravityPluginJSON() string {
	return `{"name":"tether"}` + "\n"
}

// renderAntigravityMCPConfig renders a plugin mcp_config.json in the shape
// `agy mcp add` writes: an http server is {"serverUrl"}, a stdio server is
// {"command","args","env"}.
func renderAntigravityMCPConfig(loopbackURL string, mux muxEntry) string {
	servers := map[string]any{}
	if loopbackURL != "" {
		servers["loopback"] = map[string]any{"serverUrl": loopbackURL}
	}
	if mux.present() {
		args := mux.Args
		if args == nil {
			args = []string{}
		}
		entry := map[string]any{"command": mux.Command, "args": args}
		if env := muxEnvMap(mux.Env); len(env) > 0 {
			entry["env"] = env
		}
		servers["mux"] = entry
	}
	out, _ := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	return string(out) + "\n"
}
