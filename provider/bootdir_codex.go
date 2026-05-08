package provider

// BootDirSpec for the OpenAI Codex CLI.
//
// Layout:
//
//	<bootDir>/
//	├── AGENTS.md           # system context (auto-loaded by Codex on cwd)
//	├── boot.md             # task kickoff content
//	└── .mcp.json           # MCP loopback config (codex MCP convention TBD)
//
// Spawn invariants: cwd = bootDir; project access via Codex's
// project-dir flag — empirically observed via `codex --help` to be
// `--cd <dir>` in current versions but apps should re-verify per
// their installed codex revision. AGENTS.md is auto-loaded by the
// CLI as the system prompt.
//
// Notes: the Codex MCP convention has not been fully probed at the
// time of this spec; apps planting .mcp.json should verify the
// installed Codex respects it. The spec is otherwise stable.
func (a *CodexAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: "AGENTS.md",
				Render: func(ctx PlantContext) (string, error) {
					return AgentsMD(AgentInfo{
						Name:         ctx.AgentName,
						SystemPrompt: ctx.SystemPrompt,
					}, ctx.MCPLoopbackURL), nil
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
		CwdPreference: CwdBootDir,
		ProjectDirArg: "--cd {{.ProjectDir}}",
		Notes:         "verify codex --help for the project-dir flag and MCP config convention against your installed version",
	}
}
