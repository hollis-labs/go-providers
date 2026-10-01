package provider

import "fmt"

// PlantContext.MCPServers reach every runtime that plants an MCP config, each
// in its CLI's own form and at the location its layout row names (the MCP
// concern, or Codex's and OpenCode's native config):
//
//	runtime      file                                      stdio                         http
//	claude       .mcp.json                                 {type:stdio,command,args,env} {type:http,url}
//	codex        config.toml [mcp_servers.<name>]          command, args, [.env]         url
//	opencode     opencode.json "mcp"                       {type:local,command:[...]}    {type:remote,url}
//	antigravity  .agents/plugins/tether/mcp_config.json    {command,args,env}            {serverUrl}
//
// The .mcp.json mirrors Codex and OpenCode plant for operators carry the same
// servers in Claude's form. Copilot and Pi have no boot-dir MCP config: they
// receive servers over ACP session/new.

// validateMCPServers checks a PlantContext.MCPServers list the same way for
// every renderer: each name is a non-empty [A-Za-z0-9_-]+, not reserved
// (loopback and mux come from MCPLoopbackURL and the Mux* fields), and
// unique, and each server sets exactly one transport.
func validateMCPServers(servers []MCPServerSpec) error {
	seen := make(map[string]bool, len(servers))
	for _, s := range servers {
		switch {
		case !validMCPServerName(s.Name):
			return fmt.Errorf("MCPServerSpec: invalid name %q (want non-empty [A-Za-z0-9_-]+)", s.Name)
		case reservedMCPNames[s.Name]:
			return fmt.Errorf("MCPServerSpec %q: name is reserved — loopback/mux come from MCPLoopbackURL/Mux*", s.Name)
		case seen[s.Name]:
			return fmt.Errorf("MCPServerSpec %q: duplicate name", s.Name)
		}
		seen[s.Name] = true
		if (s.HTTPURL != "") == (s.Command != "") {
			return fmt.Errorf("MCPServerSpec %q: set exactly one of HTTPURL or Command", s.Name)
		}
	}
	return nil
}

// stdioArgs coerces a nil argv to an empty one so planted entries always
// carry an args array.
func stdioArgs(args []string) []string {
	if args == nil {
		return []string{}
	}
	return args
}

// claudeMCPEntry renders one server in Claude Code's .mcp.json form.
func claudeMCPEntry(s MCPServerSpec) map[string]any {
	if s.HTTPURL != "" {
		return map[string]any{"type": "http", "url": s.HTTPURL}
	}
	entry := map[string]any{"type": "stdio", "command": s.Command, "args": stdioArgs(s.Args)}
	if env := muxEnvMap(s.Env); len(env) > 0 {
		entry["env"] = env
	}
	return entry
}

// opencodeMCPEntry renders one server in opencode.json's "mcp" form: http is
// "remote", stdio is "local" with command and args in one array.
func opencodeMCPEntry(s MCPServerSpec) map[string]any {
	if s.HTTPURL != "" {
		return map[string]any{"type": "remote", "url": s.HTTPURL, "enabled": true}
	}
	command := append([]string{s.Command}, s.Args...)
	entry := map[string]any{"type": "local", "command": command, "enabled": true}
	if env := muxEnvMap(s.Env); len(env) > 0 {
		entry["environment"] = env
	}
	return entry
}

// antigravityMCPEntry renders one server in the plugin mcp_config.json form
// `agy mcp add` writes.
func antigravityMCPEntry(s MCPServerSpec) map[string]any {
	if s.HTTPURL != "" {
		return map[string]any{"serverUrl": s.HTTPURL}
	}
	entry := map[string]any{"command": s.Command, "args": stdioArgs(s.Args)}
	if env := muxEnvMap(s.Env); len(env) > 0 {
		entry["env"] = env
	}
	return entry
}
