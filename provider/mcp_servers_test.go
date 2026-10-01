package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"

	"github.com/hollis-labs/go-providers/layout"
)

// CW-20260930-0136: the same PlantContext.MCPServers reach every runtime's
// boot-dir MCP config, stdio and http, in each CLI's own form, at the path the
// layout table names.

var testMCPServers = []MCPServerSpec{
	{Name: "hadron", HTTPURL: "http://127.0.0.1:7777/mcp"},
	{Name: "nanite", Command: "/usr/local/bin/nanite", Args: []string{"mcp", "--stdio"}, Env: []string{"NANITE_TOKEN=t0k"}},
}

func testMCPContext() PlantContext {
	return PlantContext{AgentName: "worker", MCPLoopbackURL: "http://127.0.0.1:65535/mcp", MCPServers: testMCPServers}
}

// renderPlanted renders the planted file at rel from a BootDirSpec.
func renderPlanted(t *testing.T, spec BootDirSpec, rel string, ctx PlantContext) string {
	t.Helper()
	for _, f := range spec.PlantedFiles {
		if f.RelPath == rel {
			out, err := f.Render(ctx)
			if err != nil {
				t.Fatalf("render %s: %v", rel, err)
			}
			return out
		}
	}
	t.Fatalf("no planted file %s", rel)
	return ""
}

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return m
}

func servers(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	m, ok := doc[key].(map[string]any)
	if !ok {
		t.Fatalf("no %q object in %v", key, doc)
	}
	return m
}

func TestClaudePlantsMCPServers(t *testing.T) {
	spec := (&ClaudeAdapter{}).BootDirSpec()
	got := decodeJSON(t, renderPlanted(t, spec, layoutRel(runtimes.Claude, shapePerTurn, layout.MCP, ""), testMCPContext()))
	s := servers(t, got, "mcpServers")
	if h, _ := s["hadron"].(map[string]any); h["type"] != "http" || h["url"] != "http://127.0.0.1:7777/mcp" {
		t.Errorf("hadron = %v", s["hadron"])
	}
	n, _ := s["nanite"].(map[string]any)
	if n["type"] != "stdio" || n["command"] != "/usr/local/bin/nanite" {
		t.Errorf("nanite = %v", n)
	}
	if args, _ := n["args"].([]any); len(args) != 2 || args[0] != "mcp" {
		t.Errorf("nanite args = %v", n["args"])
	}
	if env, _ := n["env"].(map[string]any); env["NANITE_TOKEN"] != "t0k" {
		t.Errorf("nanite env = %v", n["env"])
	}
	if _, ok := s["loopback"]; !ok {
		t.Error("loopback entry lost alongside extra servers")
	}
}

func TestOpencodePlantsMCPServers(t *testing.T) {
	a := &OpencodeAdapter{}
	spec := a.BootDirSpec()
	shape := opencodeShape(a)
	got := decodeJSON(t, renderPlanted(t, spec, layoutRel(runtimes.OpenCode, shape, layout.NativeConfig, "worker"), testMCPContext()))
	s := servers(t, got, "mcp")
	if h, _ := s["hadron"].(map[string]any); h["type"] != "remote" || h["url"] != "http://127.0.0.1:7777/mcp" || h["enabled"] != true {
		t.Errorf("hadron = %v", s["hadron"])
	}
	n, _ := s["nanite"].(map[string]any)
	cmd, _ := n["command"].([]any)
	if n["type"] != "local" || len(cmd) != 3 || cmd[0] != "/usr/local/bin/nanite" || cmd[2] != "--stdio" {
		t.Errorf("nanite = %v", n)
	}
	if env, _ := n["environment"].(map[string]any); env["NANITE_TOKEN"] != "t0k" {
		t.Errorf("nanite environment = %v", n["environment"])
	}
	// The operator mirror carries the same servers in Claude's form.
	mirror := decodeJSON(t, renderPlanted(t, spec, layoutRel(runtimes.OpenCode, shape, layout.MCP, "worker"), testMCPContext()))
	if _, ok := servers(t, mirror, "mcpServers")["nanite"]; !ok {
		t.Errorf("mirror = %v", mirror)
	}
}

func TestAntigravityPlantsMCPServers(t *testing.T) {
	spec := (&AntigravityAdapter{}).BootDirSpec()
	rel := layoutRel(runtimes.Antigravity, shapePerTurn, layout.MCP, "")
	if rel != ".agents/plugins/tether/mcp_config.json" {
		t.Fatalf("agy MCP path = %q; want the tether plugin's mcp_config.json from the layout table", rel)
	}
	got := decodeJSON(t, renderPlanted(t, spec, rel, testMCPContext()))
	s := servers(t, got, "mcpServers")
	if h, _ := s["hadron"].(map[string]any); h["serverUrl"] != "http://127.0.0.1:7777/mcp" {
		t.Errorf("hadron = %v", s["hadron"])
	}
	if n, _ := s["nanite"].(map[string]any); n["command"] != "/usr/local/bin/nanite" || n["env"] == nil {
		t.Errorf("nanite = %v", s["nanite"])
	}
}

func TestCodexMirrorPlantsMCPServers(t *testing.T) {
	a := &CodexAdapter{}
	spec := a.BootDirSpec()
	shape := codexShape(a)
	mirror := decodeJSON(t, renderPlanted(t, spec, layoutRel(runtimes.Codex, shape, layout.MCP, ""), testMCPContext()))
	if _, ok := servers(t, mirror, "mcpServers")["hadron"]; !ok {
		t.Errorf("codex mirror = %v", mirror)
	}
	cfg, err := a.ConfigDocument(testMCPContext())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "[mcp_servers.hadron]") || !strings.Contains(cfg, "[mcp_servers.nanite]") {
		t.Errorf("config.toml = %s", cfg)
	}
}

// The projection path (agentkit's prepared launches) carries the same
// servers.
func TestProjectionsCarryMCPServers(t *testing.T) {
	for name, project := range map[string]func() (ProviderProjection, error){
		"claude": func() (ProviderProjection, error) {
			return (&ClaudeAdapter{}).ProviderProjection(testMCPContext(), ProjectionOptions{})
		},
		"codex": func() (ProviderProjection, error) {
			return (&CodexAdapter{}).ProviderProjection(testMCPContext(), ProjectionOptions{})
		},
		"opencode": func() (ProviderProjection, error) {
			return (&OpencodeAdapter{}).ProviderProjection(testMCPContext(), ProjectionOptions{})
		},
		"antigravity": func() (ProviderProjection, error) {
			return (&AntigravityAdapter{}).ProviderProjection(testMCPContext(), ProjectionOptions{})
		},
	} {
		proj, err := project()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var found bool
		for _, f := range proj.Files {
			if strings.Contains(string(f.Content), "nanite") && strings.Contains(string(f.Content), "7777") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s projection carries no MCPServers", name)
		}
	}
}

// A bad spec fails every renderer, never a silently broken config.
func TestInvalidMCPServersFailEveryRenderer(t *testing.T) {
	bad := map[string][]MCPServerSpec{
		"reserved name":  {{Name: "loopback", HTTPURL: "http://x"}},
		"duplicate":      {{Name: "a", HTTPURL: "http://x"}, {Name: "a", Command: "/bin/a"}},
		"two transports": {{Name: "a", HTTPURL: "http://x", Command: "/bin/a"}},
		"no transport":   {{Name: "a"}},
		"bad name":       {{Name: "has space", HTTPURL: "http://x"}},
	}
	for name, specs := range bad {
		ctx := PlantContext{MCPServers: specs}
		if _, err := renderMCPJSON("", muxEntry{}, specs); err == nil {
			t.Errorf("%s: claude .mcp.json rendered", name)
		}
		if _, err := renderOpencodeJSON("", muxEntry{}, specs); err == nil {
			t.Errorf("%s: opencode.json rendered", name)
		}
		if _, err := renderAntigravityMCPConfig("", muxEntry{}, specs); err == nil {
			t.Errorf("%s: agy mcp_config.json rendered", name)
		}
		if _, err := (&CodexAdapter{}).ConfigDocument(ctx); err == nil {
			t.Errorf("%s: codex config.toml rendered", name)
		}
		if _, err := (&ClaudeAdapter{}).ProviderProjection(ctx, ProjectionOptions{}); err == nil {
			t.Errorf("%s: claude projection succeeded", name)
		}
	}
}

// No servers: every config is byte-identical to before (back-compat).
func TestNoMCPServersLeavesConfigsUnchanged(t *testing.T) {
	a, _ := renderMCPJSON("http://l", muxEntry{}, nil)
	if a != "{\n  \"mcpServers\": {\n    \"loopback\": {\n      \"type\": \"http\",\n      \"url\": \"http://l\"\n    }\n  }\n}\n" {
		t.Errorf("claude .mcp.json changed: %q", a)
	}
	o, _ := renderOpencodeJSON("", muxEntry{}, nil)
	if o != "{}\n" {
		t.Errorf("opencode.json without MCP = %q", o)
	}
}
