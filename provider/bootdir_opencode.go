package provider

import (
	"encoding/json"
	"strings"
)

// BootDirSpec for the opencode CLI.
//
// Layout:
//
//	<bootDir>/
//	├── agents/
//	│   └── <agentName>.md    # system context referenced by agents.json + opencode.json
//	├── agents.json            # {"agents":[{"name","instructions_file"}]}
//	├── opencode.json          # {"agent":{...},"mcp":{"loopback":{"type":"remote","url":...,"enabled":true}}}
//	├── boot.md                # task kickoff content
//	└── .mcp.json              # claude-shape mirror (kept for cross-tool sanity, ignored by opencode)
//
// Spawn invariants: cwd = projectDir (opencode treats the *config*
// dir as the boot dir, not cwd); project access is implicit via cwd
// or via "--dir <projectDir>"; OPENCODE_CONFIG_DIR={{.BootDir}} env
// var must be set so opencode loads agents.json + opencode.json from
// the boot dir.
//
// MCP loopback: opencode's MCP server config lives INSIDE
// opencode.json under the top-level "mcp" key (opencode 1.14.x
// schema; verified empirically against opencode 1.14.20). The
// shape is:
//
//	{"mcp":{"<name>":{"type":"remote","url":"http://...","enabled":true}}}
//
// `type:"remote"` is opencode's HTTP-transport keyword (NOT
// "http" — that's claude's keyword). opencode does NOT read the
// claude-shape `.mcp.json` at all, so prior versions of this spec
// that planted only `.mcp.json` left opencode with no loopback
// access. The `.mcp.json` file is still planted as a cross-tool
// sanity mirror but carries no weight for opencode itself.
//
// Notes: the agent name in agents.json must match the value passed
// to OpencodeAdapter.Agent at construction time.
func (a *OpencodeAdapter) BootDirSpec() BootDirSpec {
	agentName := a.Agent
	if agentName == "" {
		agentName = "default"
	}
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: "agents/" + agentName + ".md",
				Render: func(ctx PlantContext) (string, error) {
					name := ctx.AgentName
					if name == "" {
						name = agentName
					}
					return renderOpencodeAgentMD(name, ctx), nil
				},
			},
			{
				RelPath: "agents.json",
				Render: func(ctx PlantContext) (string, error) {
					name := ctx.AgentName
					if name == "" {
						name = agentName
					}
					return renderOpencodeAgentsJSON(name), nil
				},
			},
			{
				RelPath: "opencode.json",
				Render: func(ctx PlantContext) (string, error) {
					name := ctx.AgentName
					if name == "" {
						name = agentName
					}
					return renderOpencodeJSON(name, ctx.MCPLoopbackURL, muxEntryFromContext(ctx)), nil
				},
			},
			{
				RelPath: "boot.md",
				Render: func(ctx PlantContext) (string, error) {
					return ctx.BootContent, nil
				},
			},
			{
				RelPath: ".mcp.json",
				Mode:    0o600,
				Render: func(ctx PlantContext) (string, error) {
					return renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx)), nil
				},
			},
		},
		EnvAmendments: []string{"OPENCODE_CONFIG_DIR={{.BootDir}}"},
		CwdPreference: CwdProjectDir,
		ProjectDirArg: "--dir {{.ProjectDir}}",
		Notes:         "verify opencode MCP config convention; agents.json agent name must match OpencodeAdapter.Agent",
	}
}

func renderOpencodeAgentMD(agentName string, ctx PlantContext) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(agentName)
	b.WriteString("\n\n")
	if ctx.SystemPrompt != "" {
		b.WriteString(ctx.SystemPrompt)
		b.WriteString("\n")
	}
	if ctx.MCPLoopbackURL != "" {
		b.WriteString("\nMCP server: ")
		b.WriteString(ctx.MCPLoopbackURL)
		b.WriteString("\n")
	}
	return b.String()
}

func renderOpencodeAgentsJSON(agentName string) string {
	cfg := map[string]any{
		"agents": []map[string]any{{
			"name":              agentName,
			"instructions_file": "./agents/" + agentName + ".md",
		}},
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}

func renderOpencodeJSON(agentName, mcpLoopbackURL string, mux muxEntry) string {
	cfg := map[string]any{
		"agent": map[string]any{
			agentName: map[string]any{
				"prompt": "{file:./agents/" + agentName + ".md}",
			},
		},
	}
	// opencode's MCP config lives under the top-level "mcp" key in
	// opencode.json (opencode 1.14.x). The transport keywords differ
	// from claude's:
	//   - HTTP/SSE: "remote" (NOT claude's "http"). Carries `url`.
	//   - stdio:    "local"  (NOT claude's "stdio"). Carries `command`
	//               as a SINGLE array — argv[0] is the binary, the
	//               rest are args. opencode does NOT use claude's
	//               separate command-string + args-array shape.
	//
	// Probed against opencode 1.14.46 user config (~/.config/opencode/
	// config.json) where the canonical mux entry is:
	//
	//	"mux": {
	//	  "type": "local",
	//	  "command": ["/path/to/mux", "mcp", "--proxy", ...],
	//	  "enabled": true
	//	}
	//
	// When the caller leaves both MCPLoopbackURL and Mux empty, omit
	// the mcp block entirely — opencode merges per-dir configs with
	// global so an empty map would still be valid, but a missing key
	// is the cleaner signal-of-absence.
	mcp := map[string]any{}
	if mcpLoopbackURL != "" {
		mcp["loopback"] = map[string]any{
			"type":    "remote",
			"url":     mcpLoopbackURL,
			"enabled": true,
		}
	}
	if mux.present() {
		// Collapse command + args into the single-array shape opencode
		// expects. Nil-safe; the resulting array always starts with the
		// binary path even when MuxArgs is nil/empty.
		command := make([]string, 0, 1+len(mux.Args))
		command = append(command, mux.Command)
		command = append(command, mux.Args...)
		entry := map[string]any{
			"type":    "local",
			"command": command,
			"enabled": true,
		}
		// opencode's stdio entries support an optional `environment`
		// object (probed via the schema; schema URL is in the user's
		// config). Emit only when non-empty so the planted config stays
		// minimal in the common case.
		if env := muxEnvMap(mux.Env); len(env) > 0 {
			entry["environment"] = env
		}
		mcp["mux"] = entry
	}
	if len(mcp) > 0 {
		cfg["mcp"] = mcp
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}
