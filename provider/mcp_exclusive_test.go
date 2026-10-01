package provider

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

// CW-20261001-0225: a typed option that keeps a launch to the MCP servers it
// plants. For Claude it is --strict-mcp-config, owned by the convention in
// argv.go; the registry says per mode where it is measured.

const strictMCPFlag = "--strict-mcp-config"

// With MCPExclusive, every Claude shape adds --strict-mcp-config right after
// the --mcp-config pair, before extras, directories and the "--" prompt; with
// nothing planted it is added alone; off, the argv has no trace of it.
func TestClaudeMCPExclusiveArgv(t *testing.T) {
	cases := []struct {
		name    string
		a       ClaudeAdapter
		off, on []string
	}{
		{"print", ClaudeAdapter{MCPConfigPath: "mcp.json", ProjectDir: "/p"},
			[]string{"-p", "--output-format", "stream-json", "--verbose", "--mcp-config", "mcp.json", "--add-dir", "/p", "--", "hi"},
			[]string{"-p", "--output-format", "stream-json", "--verbose", "--mcp-config", "mcp.json", strictMCPFlag, "--add-dir", "/p", "--", "hi"}},
		{"print, nothing planted", ClaudeAdapter{ProjectDir: "/p"},
			[]string{"-p", "--output-format", "stream-json", "--verbose", "--add-dir", "/p", "--", "hi"},
			[]string{"-p", "--output-format", "stream-json", "--verbose", strictMCPFlag, "--add-dir", "/p", "--", "hi"}},
		{"bare", ClaudeAdapter{Bare: true, MCPConfigPath: "mcp.json"},
			[]string{"-p", "--output-format", "stream-json", "--verbose", "--bare", "--mcp-config", "mcp.json", "--", "hi"},
			[]string{"-p", "--output-format", "stream-json", "--verbose", "--bare", "--mcp-config", "mcp.json", strictMCPFlag, "--", "hi"}},
		{"streaming", ClaudeAdapter{InputMode: "stream-json", MCPConfigPath: "mcp.json", ProjectDir: "/p"},
			[]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--mcp-config", "mcp.json", "--add-dir", "/p"},
			[]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--mcp-config", "mcp.json", strictMCPFlag, "--add-dir", "/p"}},
		{"pty", ClaudeAdapter{PTY: true, MCPConfigPath: "mcp.json"},
			[]string{"--mcp-config", "mcp.json"},
			[]string{"--mcp-config", "mcp.json", strictMCPFlag}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			off := tc.a
			if got := off.BuildArgs("hi", "", ""); !slices.Equal(got, tc.off) {
				t.Errorf("off:\n got %q\nwant %q", got, tc.off)
			}
			on := tc.a
			on.MCPExclusive = true
			if got := on.BuildArgs("hi", "", ""); !slices.Equal(got, tc.on) {
				t.Errorf("on:\n got %q\nwant %q", got, tc.on)
			}
		})
	}
}

// The flag ends the variadic --mcp-config list, so a caller's extra that
// starts with a non-flag token cannot join it as another config file; without
// the option that is where it lands (CW-20261001-0219).
func TestClaudeMCPExclusiveEndsTheVariadicMCPConfigList(t *testing.T) {
	a := &ClaudeAdapter{MCPConfigPath: "mcp.json", ProjectDir: "/p"}
	at := func(argv []string) int { return slices.Index(argv, "plainword") - slices.Index(argv, "--mcp-config") }
	if got := a.BuildArgsWithExtras("hi", "", "", []string{"plainword"}); at(got) != 2 {
		t.Fatalf("without the option the extra is expected to follow --mcp-config's value: %q", got)
	}
	a.MCPExclusive = true
	got := a.BuildArgsWithExtras("hi", "", "", []string{"plainword"})
	if i := slices.Index(got, "plainword"); i < 0 || got[i-1] != strictMCPFlag {
		t.Errorf("with the option the extra must follow %s, not --mcp-config's list: %q", strictMCPFlag, got)
	}
}

// What each (runtime, mode) must declare. Adding a claim means adding the
// evidence for it to the golden and a line here: nothing else is declared.
var wantMCPExclusivity = map[runtimes.ID]map[runtimes.Mode]registry.MCPExclusivity{
	runtimes.Claude: {
		runtimes.ModeStreamingStdio:    registry.MCPExclusivityFlag,
		runtimes.ModeSubprocessPerTurn: registry.MCPExclusivityFlag,
		runtimes.ModePTY:               registry.MCPExclusivityFlag,
	},
	runtimes.Codex: {
		runtimes.ModeJSONRPCStdio:      registry.MCPExclusivityLayout,
		runtimes.ModeSubprocessPerTurn: registry.MCPExclusivityLayout,
	},
}

// The registry's claim for each native mode is what the code does: a "flag"
// mode's adapter adds the flag when asked and not otherwise, a "layout" mode's
// projection sets the config root that excludes the user's servers, and no
// other mode claims anything.
func TestMCPExclusivityMatchesTheAdapters(t *testing.T) {
	roots := ProjectionRoots{ProjectRoot: "/p/project", BootRoot: "/p/boot"}
	for _, d := range registry.All() {
		for _, m := range d.NativeModes() {
			got := d.MCPExclusivity(m)
			if want := wantMCPExclusivity[d.ID][m]; got != want {
				t.Errorf("%s/%s declares MCP exclusivity %q, want %q", d.ID, m, got, want)
			}
			a, err := NewAdapter(d.ID, m)
			if err != nil {
				t.Fatalf("%s/%s: %v", d.ID, m, err)
			}
			switch got {
			case registry.MCPExclusivityNone:
				// No claim, so nothing to hold the code to; the table above
				// already fails a mode that claims without evidence.
			case registry.MCPExclusivityFlag:
				c, ok := a.(*ClaudeAdapter)
				if !ok {
					t.Errorf("%s/%s declares a flag, but %T has no MCPExclusive field", d.ID, m, a)
					continue
				}
				if slices.Contains(c.BuildArgs("hi", "", ""), strictMCPFlag) {
					t.Errorf("%s/%s adds %s without MCPExclusive", d.ID, m, strictMCPFlag)
				}
				c.MCPExclusive = true
				if n := countOf(c.BuildArgs("hi", "", ""), strictMCPFlag); n != 1 {
					t.Errorf("%s/%s with MCPExclusive has %s %d times, want once", d.ID, m, strictMCPFlag, n)
				}
			case registry.MCPExclusivityLayout:
				p, ok := a.(ProjectionProvider)
				if !ok {
					t.Fatalf("%s/%s: %T is not a ProjectionProvider", d.ID, m, a)
				}
				proj, err := p.ProviderProjection(PlantContext{AgentName: "agent"}, ProjectionOptions{})
				if err != nil {
					t.Fatalf("%s/%s: %v", d.ID, m, err)
				}
				b, err := proj.ResolveLaunch(roots, "x")
				if err != nil {
					t.Fatalf("%s/%s: %v", d.ID, m, err)
				}
				if !envIs(b, "CODEX_HOME", roots.BootRoot) {
					t.Errorf("%s/%s declares layout exclusivity, but the launch does not set CODEX_HOME to the boot root: %v", d.ID, m, b.Env)
				}
			}
		}
	}
}

func countOf(args []string, s string) int {
	n := 0
	for _, a := range args {
		if a == s {
			n++
		}
	}
	return n
}

// mcpGolden reads the newest hack/probe-mcp-exclusive.sh golden:
// runtime -> probe id -> result.
func mcpGolden(t *testing.T) map[string]map[string]string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("testdata", "mcp-exclusive", "*.tsv"))
	if len(files) == 0 {
		t.Fatal("no mcp-exclusive golden")
	}
	sort.Strings(files)
	f, err := os.Open(files[len(files)-1])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) < 4 || c[0] != "R" {
			continue
		}
		if out[c[1]] == nil {
			out[c[1]] = map[string]string{}
		}
		id, _, _ := strings.Cut(c[2], "-")
		out[c[1]][id] = c[3]
	}
	return out
}

// field returns the server names a probe result lists under key ("mcp" or
// "spawned").
func field(result, key string) []string {
	for _, part := range strings.Fields(result) {
		if v, ok := strings.CutPrefix(part, key+"="); ok {
			if v == "" {
				return nil
			}
			return strings.Split(v, ",")
		}
	}
	return nil
}

// Every claim the registry makes is backed by what the probe recorded: the
// user's server loaded without the mechanism, and only the planted one with
// it. The probe launches each CLI in a scratch HOME (hack/probe-mcp-exclusive.sh).
func TestMCPExclusivityClaimsAreMeasured(t *testing.T) {
	g := mcpGolden(t)
	const user, planted, project = "user-probe", "planted-probe", "project-probe"
	both := []string{planted, user}
	only := []string{planted}
	eq := func(t *testing.T, runtime, probe, key string, want []string) {
		t.Helper()
		res, ok := g[runtime][probe]
		if !ok {
			t.Errorf("golden has no %s %s", runtime, probe)
			return
		}
		got := slices.Clone(field(res, key))
		slices.Sort(got)
		w := slices.Clone(want)
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s %s %s = %v, want %v", runtime, probe, key, got, w)
		}
	}
	t.Run("claude", func(t *testing.T) {
		for _, c := range []struct {
			shape, leak, fix string
			key              string
			leaks            bool // does the shape load the user's server without the flag?
		}{
			{"per-turn", "MCP1", "MCP2", "mcp", true},
			{"bare", "MCP5", "MCP6", "mcp", false}, // --bare already skips the user's servers
			{"streaming", "MCP7", "MCP8", "mcp", true},
			{"pty", "MCP9", "MCP10", "spawned", true},
		} {
			if c.leaks {
				eq(t, "claude", c.leak, c.key, both)
			} else {
				eq(t, "claude", c.leak, c.key, only)
			}
			eq(t, "claude", c.fix, c.key, only)
		}
		// Nothing passed and the flag set: no MCP server loads at all, a
		// .mcp.json in the working directory included.
		eq(t, "claude", "MCP3", "mcp", nil)
		eq(t, "claude", "MCP3", "spawned", nil)
		eq(t, "claude", "MCP4", "spawned", both)
	})
	t.Run("codex", func(t *testing.T) {
		eq(t, "codex", "MCP1", "mcp", []string{user}) // CODEX_HOME unset: the user's
		eq(t, "codex", "MCP2", "mcp", only)           // CODEX_HOME = boot
		eq(t, "codex", "MCP3", "mcp", only)           // ... and a project .codex/config.toml is not applied
		eq(t, "codex", "MCP4", "mcp", []string{user}) // CODEX_HOME unset, project .codex ignored
		eq(t, "codex", "MCP5", "spawned", []string{user})
		eq(t, "codex", "MCP6", "spawned", only)
	})
	t.Run("opencode has no MCP-only switch", func(t *testing.T) {
		eq(t, "opencode", "MCP1", "mcp", []string{planted, project, user}) // OPENCODE_CONFIG_DIR alone merges all three
		eq(t, "opencode", "MCP2", "mcp", []string{planted, project})       // XDG_CONFIG_HOME: the user's, but also every other global setting
		eq(t, "opencode", "MCP3", "mcp", []string{planted, user})          // OPENCODE_DISABLE_PROJECT_CONFIG
		eq(t, "opencode", "MCP4", "mcp", only)
		d, _ := registry.Lookup("opencode")
		for _, m := range d.NativeModes() {
			if x := d.MCPExclusivity(m); x != registry.MCPExclusivityNone {
				t.Errorf("opencode/%s declares %q; no MCP-only mechanism was found", m, x)
			}
		}
	})
}
