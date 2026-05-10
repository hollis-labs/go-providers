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
//	├── opencode.json          # {"agent":{...},"mcp":{"loopback":{"type":"remote","url":...}}}
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
					return renderOpencodeJSON(name, ctx.MCPLoopbackURL), nil
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
				Render: func(ctx PlantContext) (string, error) {
					return renderMCPJSON(ctx.MCPLoopbackURL), nil
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

func renderOpencodeJSON(agentName, mcpLoopbackURL string) string {
	cfg := map[string]any{
		"agent": map[string]any{
			agentName: map[string]any{
				"prompt": "{file:./agents/" + agentName + ".md}",
			},
		},
	}
	// opencode's MCP config lives under the top-level "mcp" key in
	// opencode.json (opencode 1.14.x). The transport keyword is
	// "remote" (HTTP/SSE) rather than claude's "http". When the
	// caller leaves MCPLoopbackURL empty, omit the mcp block entirely
	// — opencode merges per-dir configs with global so an empty map
	// would still be valid, but a missing key is the cleaner
	// signal-of-absence.
	if mcpLoopbackURL != "" {
		cfg["mcp"] = map[string]any{
			"loopback": map[string]any{
				"type":    "remote",
				"url":     mcpLoopbackURL,
				"enabled": true,
			},
		}
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}
