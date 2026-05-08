package provider

import (
	"encoding/json"
	"strings"
)

// BootDirSpec for the Claude Code CLI.
//
// Layout:
//
//	<bootDir>/
//	├── CLAUDE.md           # system context (auto-loaded by Claude on cwd)
//	├── boot.md             # task kickoff content (referenced via Boot @./boot.md)
//	├── .claude/settings.json   # stub to avoid global ~/.claude.json bleed
//	└── .mcp.json           # MCP loopback config
//
// Spawn invariants: cwd = bootDir; project access via
// "--add-dir <projectDir>"; CLAUDE.md is auto-loaded by the CLI.
func (a *ClaudeAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: "CLAUDE.md",
				Render: func(ctx PlantContext) (string, error) {
					return renderClaudeMD(ctx), nil
				},
			},
			{
				RelPath: "boot.md",
				Render: func(ctx PlantContext) (string, error) {
					return ctx.BootContent, nil
				},
			},
			{
				RelPath: ".claude/settings.json",
				Render: func(ctx PlantContext) (string, error) {
					return claudeSettingsStub(), nil
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
		ProjectDirArg: "--add-dir {{.ProjectDir}}",
	}
}

func renderClaudeMD(ctx PlantContext) string {
	var b strings.Builder
	if ctx.SystemPrompt != "" {
		b.WriteString(ctx.SystemPrompt)
		b.WriteString("\n\n")
	}
	if ctx.MCPLoopbackURL != "" {
		b.WriteString("## MCP\n\nTools for this task are available via the MCP server configured in .mcp.json (")
		b.WriteString(ctx.MCPLoopbackURL)
		b.WriteString(").\n")
	}
	return b.String()
}

func claudeSettingsStub() string {
	// Minimal stub that prevents the CLI from reading the user's global
	// ~/.claude.json or ~/.claude/settings.json. Apps that need a
	// custom approvedTools list or mcpServers map can post-process
	// the planted file before spawn.
	stub := map[string]any{
		"mcpServers":    map[string]any{},
		"approvedTools": []string{},
	}
	out, _ := json.MarshalIndent(stub, "", "  ")
	return string(out) + "\n"
}

func renderMCPJSON(loopbackURL string) string {
	if loopbackURL == "" {
		// Empty loopback — emit an empty object so the file exists
		// but no MCP servers are configured.
		return "{}\n"
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"loopback": map[string]any{
				"url": loopbackURL,
			},
		},
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}
