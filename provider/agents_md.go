package provider

import (
	"encoding/json"
	"strings"
)

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
		writeFrontmatterScalar(&b, "name", agent.Name)
	}
	if agent.Role != "" {
		writeFrontmatterScalar(&b, "role", agent.Role)
	}
	if agent.Description != "" {
		writeFrontmatterScalar(&b, "description", strings.ReplaceAll(agent.Description, "\n", " "))
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

// writeFrontmatterScalar emits a YAML key/value where the value is a
// JSON-marshalled string. JSON's flow-scalar form is valid YAML
// (YAML is a JSON superset), and json.Marshal handles all the
// escaping we'd otherwise need to write by hand: embedded ':', '#',
// quotes, leading/trailing whitespace, control characters, etc.
func writeFrontmatterScalar(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	encoded, err := json.Marshal(value)
	if err != nil {
		// Marshalling a string never fails for any in-memory string;
		// fall back to a quoted empty value so the YAML stays valid.
		b.WriteString(`""`)
	} else {
		b.Write(encoded)
	}
	b.WriteString("\n")
}
