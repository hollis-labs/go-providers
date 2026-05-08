package provider

import (
	"encoding/json"
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
	// Frontmatter scalars are JSON-quoted (valid YAML flow scalars).
	if !strings.Contains(out, `name: "orchestrator"`) {
		t.Error("frontmatter should contain JSON-quoted name")
	}
	if !strings.Contains(out, `role: "orchestrator"`) {
		t.Error("frontmatter should contain JSON-quoted role")
	}
	if !strings.Contains(out, `description: "drives multi-step agent loops"`) {
		t.Error("frontmatter should contain JSON-quoted description")
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
	// JSON-quoted: description: "line1 line2"
	if !strings.Contains(frontmatter, `description: "line1 line2"`) {
		t.Errorf("description newlines should collapse to spaces in frontmatter, got: %s", frontmatter)
	}
}

// TestAgentsMD_FrontmatterYAMLSafe verifies that values containing
// YAML-sensitive characters (':', '#', leading/trailing space, double
// quote, backslash) are emitted as JSON-quoted scalars so they remain
// valid YAML even when the agent's identity contains free-form text.
// Regression: PR #9 review (Copilot) flagged bare-scalar emission as
// breakage-prone for downstream parsers.
func TestAgentsMD_FrontmatterYAMLSafe(t *testing.T) {
	cases := []struct {
		field string
		value string
	}{
		{"name", `agent: with: colons`},
		{"role", `# starts with hash`},
		{"description", `has "double quotes" and \backslashes`},
		{"description", `   leading and trailing whitespace   `},
		{"name", "tab\there"},
	}
	for _, c := range cases {
		var info AgentInfo
		switch c.field {
		case "name":
			info.Name = c.value
		case "role":
			info.Role = c.value
		case "description":
			info.Description = c.value
		}
		out := AgentsMD(info, "")
		// The emitted value should be JSON-quoted (which is valid YAML
		// flow scalar). Verify the line opens with a double-quote and
		// the value can round-trip via JSON unmarshalling.
		fm := strings.SplitN(out, "---\n\n", 2)[0]
		expectedPrefix := c.field + ": \""
		if !strings.Contains(fm, expectedPrefix) {
			t.Errorf("case %q=%q: frontmatter should JSON-quote value (prefix %q), got: %s", c.field, c.value, expectedPrefix, fm)
			continue
		}
		// Extract the line and verify round-trip.
		for _, line := range strings.Split(fm, "\n") {
			if strings.HasPrefix(line, c.field+": ") {
				rhs := strings.TrimPrefix(line, c.field+": ")
				var got string
				if err := json.Unmarshal([]byte(rhs), &got); err != nil {
					t.Errorf("case %q=%q: emitted scalar %q is not valid JSON/YAML: %v", c.field, c.value, rhs, err)
				} else if got != strings.ReplaceAll(c.value, "\n", " ") {
					t.Errorf("case %q=%q: round-trip mismatch, got %q", c.field, c.value, got)
				}
				break
			}
		}
	}
}
