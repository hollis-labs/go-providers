package provider

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

// testdata/antigravity/print_*.jsonl is verbatim `agy -p=<prompt>
// --output-format stream-json` stdout from agy 1.2.7 (paths and names
// scrubbed):
//
//   - print_turn1.jsonl            — first turn, text reply
//   - print_turn2_resume.jsonl     — --conversation <turn 1 id>, recalled turn 1
//   - print_tool_run.jsonl         — run_command under --dangerously-skip-permissions
//   - print_tool_denied.jsonl      — the same command under request-review: auto-denied
//   - print_mcp_tool.jsonl         — a workspace-plugin MCP call (call_mcp_tool)
//   - print_resume_unknown_id.jsonl/.stderr — --conversation with an unknown id:
//     a new conversation, a stderr warning, exit 0

func agyFixture(t *testing.T, name string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "antigravity", name))
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	for _, l := range splitLines(data) {
		if len(l) > 0 {
			lines = append(lines, l)
		}
	}
	return lines
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return append(out, b[start:])
}

func parseAgyFixture(t *testing.T, name string) []llmtypes.StreamEvent {
	t.Helper()
	a := NewAntigravityAdapter()
	var out []llmtypes.StreamEvent
	for _, l := range agyFixture(t, name) {
		evs, err := a.ParseLine(l)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, evs...)
	}
	return out
}

func agyTypes(evs []llmtypes.StreamEvent) []llmtypes.EventType {
	out := make([]llmtypes.EventType, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func TestAntigravityBuildArgs(t *testing.T) {
	cases := []struct {
		name string
		a    *AntigravityAdapter
		sid  string
		want []string
	}{
		{"defaults", NewAntigravityAdapter(), "",
			[]string{"--output-format", "stream-json", "-p=hi"}},
		{"resume", NewAntigravityAdapter(), "conv-1",
			[]string{"--output-format", "stream-json", "--conversation", "conv-1", "-p=hi"}},
		{"bypass and options", &AntigravityAdapter{Permission: "bypass", Model: "gemini-3.1-pro-high", Effort: "high", Agent: "ops", AddDirs: []string{"/p"}}, "",
			[]string{"--output-format", "stream-json", "--dangerously-skip-permissions", "--model", "gemini-3.1-pro-high", "--effort", "high", "--agent", "ops", "--add-dir", "/p", "-p=hi"}},
		{"accept edits", &AntigravityAdapter{Permission: "accept-edits"}, "",
			[]string{"--output-format", "stream-json", "--mode", "accept-edits", "-p=hi"}},
	}
	for _, c := range cases {
		if got := c.a.BuildArgs("hi", "", c.sid); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q; want %q", c.name, got, c.want)
		}
	}
	// A prompt that starts with a dash stays the -p value.
	if got := NewAntigravityAdapter().BuildArgs("--help me", "", ""); got[len(got)-1] != "-p=--help me" {
		t.Errorf("dash prompt: %q", got)
	}
}

func TestAntigravityParseLine_Fixtures(t *testing.T) {
	t.Run("single turn", func(t *testing.T) {
		evs := parseAgyFixture(t, "print_turn1.jsonl")
		want := []llmtypes.EventType{llmtypes.EventSessionID, llmtypes.EventDelta, llmtypes.EventDelta, llmtypes.EventUsage, llmtypes.EventDone}
		if got := agyTypes(evs); !reflect.DeepEqual(got, want) {
			t.Fatalf("types = %v; want %v", got, want)
		}
		if evs[0].SessionID != "a62adc17-9c92-4d24-8b97-65226dfa2967" || evs[1].Content+evs[2].Content != "OK\n" {
			t.Errorf("session %q, text %q", evs[0].SessionID, evs[1].Content+evs[2].Content)
		}
		// output_tokens already include thinking tokens.
		if u := *evs[3].Usage; u != (llmtypes.Usage{InputTokens: 17138, OutputTokens: 276}) {
			t.Errorf("usage = %+v", u)
		}
	})

	t.Run("resume keeps the id and recalls", func(t *testing.T) {
		evs := parseAgyFixture(t, "print_turn2_resume.jsonl")
		var text string
		for _, e := range evs {
			if e.Type == llmtypes.EventDelta {
				text += e.Content
			}
		}
		if evs[0].SessionID != "a62adc17-9c92-4d24-8b97-65226dfa2967" || text != "TANGERINE-58\n" {
			t.Errorf("session %q, text %q", evs[0].SessionID, text)
		}
	})

	t.Run("tool use, one usage per agent step, one done", func(t *testing.T) {
		evs := parseAgyFixture(t, "print_tool_run.jsonl")
		var uses, usages, dones int
		for _, e := range evs {
			switch e.Type {
			case llmtypes.EventToolUse:
				uses++
				if e.ToolUse.Name != "run_command" || e.ToolUse.Input["CommandLine"] != "cat note.txt > copy.txt && echo done" {
					t.Errorf("tool use = %+v", e.ToolUse)
				}
			case llmtypes.EventUsage:
				usages++
			case llmtypes.EventDone:
				dones++
			}
		}
		if uses != 1 || usages != 2 || dones != 1 || evs[len(evs)-1].Type != llmtypes.EventDone {
			t.Errorf("uses=%d usages=%d dones=%d last=%s", uses, usages, dones, evs[len(evs)-1].Type)
		}
	})

	t.Run("unknown resume id starts a new conversation", func(t *testing.T) {
		evs := parseAgyFixture(t, "print_resume_unknown_id.jsonl")
		if evs[0].SessionID == "00000000-0000-0000-0000-000000000000" || evs[len(evs)-1].Type != llmtypes.EventDone {
			t.Errorf("events = %+v", evs)
		}
	})

	t.Run("tolerant", func(t *testing.T) {
		for _, l := range []string{"", "not json", `{"event":"new_kind","new_kind":{}}`, `{"event":"step_update","step_update":{"step_type":"system_message","state":"DONE"}}`} {
			if evs, err := NewAntigravityAdapter().ParseLine([]byte(l)); err != nil || len(evs) != 0 {
				t.Errorf("%q: %v %v", l, evs, err)
			}
		}
	})

	t.Run("error result", func(t *testing.T) {
		evs, _ := NewAntigravityAdapter().ParseLine([]byte(`{"event":"result","result":{"conversation_id":"","status":"ERROR","error":"authentication failed or timed out"}}`))
		if len(evs) != 1 || evs[0].Type != llmtypes.EventError || evs[0].Error != "authentication failed or timed out" {
			t.Errorf("events = %+v", evs)
		}
	})
}

func TestAntigravityParseLineEvents_Fixtures(t *testing.T) {
	collect := func(name string) []events.Event {
		var out []events.Event
		for _, l := range agyFixture(t, name) {
			evs, err := NewAntigravityAdapter().ParseLineEvents(l)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, evs...)
		}
		return out
	}

	denied := collect("print_tool_denied.jsonl")
	var gotDenied []events.PermissionDenied
	var errResult bool
	for _, e := range denied {
		switch v := e.(type) {
		case events.PermissionDenied:
			gotDenied = append(gotDenied, v)
		case events.ToolResult:
			errResult = v.IsError && v.ContentPreview != ""
		}
	}
	if !reflect.DeepEqual(gotDenied, []events.PermissionDenied{{Action: "command", DisplayName: "RunCommand"}}) || !errResult {
		t.Errorf("denied = %+v, error tool result = %v", gotDenied, errResult)
	}
	if _, ok := denied[len(denied)-1].(events.Done); !ok {
		t.Errorf("a turn with denials still ends in Done, got %#v", denied[len(denied)-1])
	}

	mcp := collect("print_mcp_tool.jsonl")
	var mcpResult string
	for _, e := range mcp {
		if r, ok := e.(events.ToolResult); ok && !r.IsError && r.ContentPreview == "SECRET-WORD-MAGNOLIA" {
			mcpResult = r.ContentPreview
		}
	}
	if mcpResult == "" {
		t.Errorf("mcp tool result not surfaced: %#v", mcp)
	}
}

func TestAntigravityClassifiers(t *testing.T) {
	a := NewAntigravityAdapter()
	var _ SessionResumeVerifier = a
	var _ SessionLostClassifier = a
	var _ AuthFailureClassifier = a
	var _ EventParser = a

	stderr, err := os.ReadFile(filepath.Join("testdata", "antigravity", "print_resume_unknown_id.stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if !a.ResumeKeepsSessionID() || !a.IsSessionLost(stderr) || a.IsSessionLost([]byte("warning: something else")) {
		t.Error("session-lost classification")
	}
	if !a.IsNotAuthenticated([]byte("Waiting for authentication (timeout 60s)...\nError: authentication timed out.\nerror: authentication failed or timed out\n")) || a.IsNotAuthenticated(stderr) {
		t.Error("auth classification")
	}
}

func TestAntigravityHasNoPreflight(t *testing.T) {
	// agy reads the Keychain, not ~/.gemini/oauth_creds.json; a stat of that
	// file is wrong both ways, so the adapter must not implement Preflighter.
	var a any = NewAntigravityAdapter()
	if _, ok := a.(Preflighter); ok {
		t.Fatal("AntigravityAdapter must not implement Preflighter")
	}
}

func TestAntigravityIsNotAuthenticatedMarkers(t *testing.T) {
	a := NewAntigravityAdapter()
	for name, tail := range map[string]string{
		"sign-in prompt":     "Authentication required. Please visit the URL https://accounts.example/o/oauth2 to sign in\n",
		"no stored creds":    "error: not authenticated: no stored credentials found\n",
		"wait then timeout":  "Waiting for authentication (timeout 60s)...\nerror: authentication failed or timed out\n",
		"marker after noise": "ChainedAuth: trying silent auth\nAuthentication required. Please visit the URL x\n",
	} {
		if !a.IsNotAuthenticated([]byte(tail)) {
			t.Errorf("%s: want not-authenticated", name)
		}
	}
	for name, tail := range map[string]string{
		"healthy run":   "ChainedAuth: trying silent auth\nChainedAuth: authenticated via keyring\n",
		"empty":         "",
		"unknown convo": "warning: conversation \"x\" not found\n",
	} {
		if a.IsNotAuthenticated([]byte(tail)) {
			t.Errorf("%s: must not be classified as not-authenticated", name)
		}
	}
}

func TestAntigravityBootDirSpec(t *testing.T) {
	spec := NewAntigravityAdapter().BootDirSpec()
	want := []string{"AGENTS.md", "boot.md", ".agents/plugins/tether/plugin.json", ".agents/plugins/tether/mcp_config.json"}
	var got []string
	for _, f := range spec.PlantedFiles {
		got = append(got, f.RelPath)
	}
	if !reflect.DeepEqual(got, want) || spec.CwdPreference != CwdBootDir || spec.ProjectDirArg != "--add-dir {{.ProjectDir}}" || len(spec.EnvAmendments) != 0 {
		t.Fatalf("spec = %+v", spec)
	}
	mcp, err := spec.PlantedFiles[3].Render(PlantContext{
		MCPLoopbackURL: "http://127.0.0.1:1/mcp",
		MuxCommand:     "/bin/mux", MuxArgs: []string{"mcp"}, MuxEnv: []string{"TOKEN=x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMCP := `{
  "mcpServers": {
    "loopback": {
      "serverUrl": "http://127.0.0.1:1/mcp"
    },
    "mux": {
      "args": [
        "mcp"
      ],
      "command": "/bin/mux",
      "env": {
        "TOKEN": "x"
      }
    }
  }
}
`
	if mcp != wantMCP || spec.PlantedFiles[3].Mode != 0o600 {
		t.Errorf("mcp_config.json mode %o:\n%s", spec.PlantedFiles[3].Mode, mcp)
	}
}
