package provider

import "strings"

// AgentInfo describes the agent identity used to render an AGENTS.md
// file. Fields beyond Name and SystemPrompt are optional; renderers
// include them when set.
type AgentInfo struct {
	// Name is the agent identifier (e.g. "orchestrator", "executor").
	// Used in the file title and frontmatter.
	Name string
	// Role is the agent's functional role (e.g. "orchestrator",
	// "reviewer", "planner", "executor", "chat"). Optional.
	Role string
	// Description is a one-line summary shown near the top.
	Description string
	// SystemPrompt is the agent's identity / invariants / role
	// framing. Becomes the body of the document.
	SystemPrompt string
}

// AgentsMD assembles AGENTS.md content with a standard structure.
//
// Used by adapters whose CLIs auto-load AGENTS.md from cwd as the
// system context (codex, opencode, possibly gemini). The output has
// YAML frontmatter (name, role, description), an H1 title, the
// system prompt body, and an optional "## MCP" section if a loopback
// URL is provided. Extras are appended verbatim after the body.
//
// Apps that want a custom layout can ignore this helper and render
// their own content from the PlantedFile.Render closure.
func AgentsMD(agent AgentInfo, mcpLoopbackURL string, extras ...string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if agent.Name != "" {
		b.WriteString("name: ")
		b.WriteString(agent.Name)
		b.WriteString("\n")
	}
	if agent.Role != "" {
		b.WriteString("role: ")
		b.WriteString(agent.Role)
		b.WriteString("\n")
	}
	if agent.Description != "" {
		b.WriteString("description: ")
		b.WriteString(strings.ReplaceAll(agent.Description, "\n", " "))
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")

	title := agent.Name
	if title == "" {
		title = "Agent"
	}
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")

	if agent.SystemPrompt != "" {
		b.WriteString(agent.SystemPrompt)
		if !strings.HasSuffix(agent.SystemPrompt, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if mcpLoopbackURL != "" {
		b.WriteString("## MCP\n\nTools for this task are available via the MCP server at ")
		b.WriteString(mcpLoopbackURL)
		b.WriteString(" (configured in .mcp.json).\n\n")
	}

	for _, extra := range extras {
		if extra == "" {
			continue
		}
		b.WriteString(extra)
		if !strings.HasSuffix(extra, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}
