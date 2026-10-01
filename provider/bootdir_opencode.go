package provider

import (
	"encoding/json"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
)

// BootDirSpec for the opencode CLI.
//
// Layout:
//
//	<bootDir>/
//	├── agents/
//	│   └── <agentName>.md    # markdown agent: frontmatter (description, mode: primary) + system context
//	├── opencode.json          # {"mcp":{"loopback":{"type":"remote","url":...,"enabled":true}}}
//	├── boot.md                # task kickoff content
//	└── .mcp.json              # claude-shape mirror (kept for cross-tool sanity, ignored by opencode)
//
// Spawn invariants: cwd = projectDir (opencode treats the *config*
// dir as the boot dir, not cwd); project access is implicit via cwd
// or via "--dir <projectDir>"; OPENCODE_CONFIG_DIR={{.BootDir}} env
// var must be set so opencode loads agents/*.md + opencode.json from
// the boot dir.
//
// The agent is defined once, by agents/<agentName>.md: opencode 1.18.30
// registers every <config>/agents/*.md as an agent named after the file,
// taking description and mode from its frontmatter and the body as the
// prompt. Earlier specs also planted agents.json (which opencode does not
// read) and an opencode.json "agent" entry pointing its prompt at the same
// file with {file:...}, which would now pull the frontmatter into the
// prompt text.
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
// Notes: the planted agent file is named after OpencodeAdapter.Agent (or
// PlantContext.AgentName), which is what `run --agent` selects.
func (a *OpencodeAdapter) BootDirSpec() BootDirSpec {
	agentName := a.Agent
	if agentName == "" {
		agentName = "default"
	}
	// The legacy spec is mode-independent and keeps --dir for serve-http too,
	// so it reads the subprocess-per-turn (run) rows.
	const pid = runtimes.OpenCode
	shape := shapePerTurn
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: layoutRel(pid, shape, layout.Instructions, agentName),
				Render: func(ctx PlantContext) (string, error) {
					name := ctx.AgentName
					if name == "" {
						name = agentName
					}
					return renderOpencodeAgentMD(name, ctx), nil
				},
			},
			{
				// opencode.json carries MCP servers and their env, so it is
				// owner-only like every other MCP-bearing file.
				RelPath: layoutRel(pid, shape, layout.NativeConfig, agentName),
				Mode:    layoutFileMode(pid, shape, layout.NativeConfig),
				Render: func(ctx PlantContext) (string, error) {
					return renderOpencodeJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx), ctx.MCPServers)
				},
			},
			{
				RelPath: layoutRel(pid, shape, layout.Boot, agentName),
				Render: func(ctx PlantContext) (string, error) {
					return ctx.BootContent, nil
				},
			},
			{
				RelPath: layoutRel(pid, shape, layout.MCP, agentName),
				Mode:    0o600,
				Render: func(ctx PlantContext) (string, error) {
					return renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx), ctx.MCPServers)
				},
			},
		},
		EnvAmendments: layoutLegacyEnv(pid, shape),
		CwdPreference: layoutLegacyCwd(pid, shape),
		ProjectDirArg: layoutLegacyProjectDirArg(pid, shape),
		Notes:         "agents/<name>.md is the agent definition; its name must match OpencodeAdapter.Agent",
	}
}

func renderOpencodeAgentMD(agentName string, ctx PlantContext) string {
	var b strings.Builder
	// mode: primary keeps the agent selectable by `run --agent` without
	// also offering it to itself as a subagent (a file with no mode is
	// registered as "all").
	b.WriteString("---\ndescription: Launch agent ")
	b.WriteString(agentName)
	b.WriteString("\nmode: primary\n---\n\n# ")
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

func renderOpencodeJSON(mcpLoopbackURL string, mux muxEntry, extra []MCPServerSpec) (string, error) {
	if err := validateMCPServers(extra); err != nil {
		return "", err
	}
	cfg := map[string]any{}
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
	for _, s := range extra {
		mcp[s.Name] = opencodeMCPEntry(s)
	}
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
	return string(out) + "\n", nil
}
