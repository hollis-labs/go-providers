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
//	├── opencode.json          # {"agent":{"<agentName>":{"prompt":"{file:./agents/<agentName>.md}"}}}
//	├── boot.md                # task kickoff content
//	└── .mcp.json              # MCP loopback config (opencode MCP convention TBD)
//
// Spawn invariants: cwd = projectDir (opencode treats the *config*
// dir as the boot dir, not cwd); project access is implicit via cwd
// or via "--dir <projectDir>"; OPENCODE_CONFIG_DIR={{.BootDir}} env
// var must be set so opencode loads agents.json + opencode.json from
// the boot dir.
//
// Notes: the agent name in agents.json must match the value passed
// to OpencodeAdapter.Agent at construction time. The opencode MCP
// convention is not fully probed; apps should verify against the
// installed opencode revision.
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
					return renderOpencodeJSON(name), nil
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

func renderOpencodeJSON(agentName string) string {
	cfg := map[string]any{
		"agent": map[string]any{
			agentName: map[string]any{
				"prompt": "{file:./agents/" + agentName + ".md}",
			},
		},
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}
