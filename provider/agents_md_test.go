package provider

import (
	"strings"
	"testing"
)

func TestAgentsMD_AllFields(t *testing.T) {
	out := AgentsMD(AgentInfo{
		Name:         "orchestrator",
		Role:         "orchestrator",
		Description:  "drives multi-step agent loops",
		SystemPrompt: "You delegate tasks to executors.",
	}, "http://localhost:9999/mcp", "## Extra\n\nbonus content")

	if !strings.HasPrefix(out, "---\n") {
		t.Error("output should start with frontmatter")
	}
	if !strings.Contains(out, "name: orchestrator") {
		t.Error("frontmatter should contain name")
	}
	if !strings.Contains(out, "role: orchestrator") {
		t.Error("frontmatter should contain role")
	}
	if !strings.Contains(out, "description: drives multi-step agent loops") {
		t.Error("frontmatter should contain description")
	}
	if !strings.Contains(out, "# orchestrator") {
		t.Error("body should have H1 title")
	}
	if !strings.Contains(out, "You delegate tasks to executors.") {
		t.Error("body should contain SystemPrompt")
	}
	if !strings.Contains(out, "http://localhost:9999/mcp") {
		t.Error("MCP section should contain loopback URL")
	}
	if !strings.Contains(out, "bonus content") {
		t.Error("extras should be appended")
	}
}

func TestAgentsMD_MinimalFields(t *testing.T) {
	out := AgentsMD(AgentInfo{Name: "exec"}, "")
	if !strings.Contains(out, "# exec") {
		t.Error("title should fall back to Name")
	}
	if strings.Contains(out, "## MCP") {
		t.Error("no MCP section when loopback URL is empty")
	}
	if strings.Contains(out, "role:") {
		t.Error("no role line when Role is empty")
	}
}

func TestAgentsMD_EmptyName(t *testing.T) {
	out := AgentsMD(AgentInfo{SystemPrompt: "be terse"}, "")
	if !strings.Contains(out, "# Agent") {
		t.Error("title should fall back to 'Agent' when Name is empty")
	}
	if !strings.Contains(out, "be terse") {
		t.Error("body should still contain SystemPrompt")
	}
}

func TestAgentsMD_DescriptionNewlinesFlattened(t *testing.T) {
	out := AgentsMD(AgentInfo{
		Name:        "x",
		Description: "line1\nline2",
	}, "")
	// Frontmatter should have description on a single line.
	frontmatter := strings.SplitN(out, "---\n\n", 2)[0]
	if strings.Count(frontmatter, "description: line1 line2") != 1 {
		t.Errorf("description newlines should collapse to spaces in frontmatter, got: %s", frontmatter)
	}
}
