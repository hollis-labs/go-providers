package provider

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
		runtimes.ModeJSONRPCStdio:      registry.MCPExclusivityProjectedLayout,
		runtimes.ModeSubprocessPerTurn: registry.MCPExclusivityProjectedLayout,
	},
	// Measured to have no MCP-only switch (the golden's opencode rows).
	runtimes.OpenCode: {
		runtimes.ModeSubprocessPerTurn: registry.MCPExclusivityAbsent,
		runtimes.ModeHTTPSSE:           registry.MCPExclusivityAbsent,
	},
}

// The registry's claim for each native mode is what the code does: a "flag"
// mode's adapter adds the flag when asked and not otherwise, a
// "projected-layout" mode's projection sets the config root that excludes the
// user's servers, and no other mode claims a mechanism.
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
			case registry.MCPExclusivityNone, registry.MCPExclusivityAbsent:
				// No mechanism claimed, so nothing to hold the code to; the
				// table above already fails a mode that claims without evidence.
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
			case registry.MCPExclusivityProjectedLayout:
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
					t.Errorf("%s/%s declares projected-layout exclusivity, but the launch does not set CODEX_HOME to the boot root: %v", d.ID, m, b.Env)
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
		// The app-server (jsonrpc-stdio) starts its MCP servers on thread/start,
		// without a turn: the same split, by the servers it spawned.
		eq(t, "codex", "MCP7", "spawned", []string{user})
		eq(t, "codex", "MCP8", "spawned", only)
	})
	t.Run("opencode has no MCP-only switch", func(t *testing.T) {
		eq(t, "opencode", "MCP1", "mcp", []string{planted, project, user}) // OPENCODE_CONFIG_DIR alone merges all three
		eq(t, "opencode", "MCP2", "mcp", []string{planted, project})       // XDG_CONFIG_HOME: the user's, but also every other global setting
		eq(t, "opencode", "MCP3", "mcp", []string{planted, user})          // OPENCODE_DISABLE_PROJECT_CONFIG
		eq(t, "opencode", "MCP4", "mcp", only)
		d, _ := registry.Lookup("opencode")
		for _, m := range d.NativeModes() {
			if x := d.MCPExclusivity(m); x != registry.MCPExclusivityAbsent {
				t.Errorf("opencode/%s declares %q; no MCP-only mechanism was found, which is the absent value", m, x)
			}
		}
	})
}

// ProjectionOptions.MCPExclusive is the plan-level request: every native
// shape either gets its mechanism or is refused, so a host never launches
// non-exclusive without knowing. Claude's convention gains the flag, a layout
// mode needs nothing added, and a mode with no measured mechanism fails.
func TestProjectionMCPExclusive(t *testing.T) {
	roots := ProjectionRoots{ProjectRoot: "/p/project", BootRoot: "/p/boot"}
	ctx := PlantContext{AgentName: "agent"}
	for _, c := range builtinModes {
		t.Run(string(c.provider)+"/"+c.mode.String(), func(t *testing.T) {
			d, ok := registry.Lookup(string(c.provider))
			if !ok {
				t.Fatalf("no registry descriptor for %s", c.provider)
			}
			how := d.MCPExclusivity(c.mode.Mode)
			plain, err := c.adapter().ProviderProjection(ctx, ProjectionOptions{})
			if err != nil {
				t.Fatalf("plain projection: %v", err)
			}
			plainLaunch, err := plain.ResolveLaunch(roots, "x")
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(plainLaunch.Argv, strictMCPFlag) {
				t.Fatalf("a launch that did not ask for exclusivity carries %s: %q", strictMCPFlag, plainLaunch.Argv)
			}
			adapter := c.adapter()
			proj, err := adapter.ProviderProjection(ctx, ProjectionOptions{MCPExclusive: true})
			switch how {
			case registry.MCPExclusivityNone, registry.MCPExclusivityAbsent:
				if !errors.Is(err, ErrMCPExclusiveUnsupported) {
					t.Fatalf("a mode with no mechanism: err = %v, want ErrMCPExclusiveUnsupported", err)
				}
				wantNamed(t, err, string(c.provider), string(c.mode.Mode))
			case registry.MCPExclusivityFlag:
				if err != nil {
					t.Fatalf("projection: %v", err)
				}
				got, resolveErr := proj.ResolveLaunch(roots, "x")
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				if n := countOf(got.Argv, strictMCPFlag); n != 1 {
					t.Fatalf("%s %d times in %q, want once", strictMCPFlag, n, got.Argv)
				}
				// Exactly the flag is added: without it the argv is the plain one.
				if without := slices.DeleteFunc(slices.Clone(got.Argv), func(a string) bool { return a == strictMCPFlag }); !slices.Equal(without, plainLaunch.Argv) {
					t.Errorf("the option changed more than the flag:\n  without the flag %q\n  plain            %q", without, plainLaunch.Argv)
				}
				// The same request through the adapter's own field gives the
				// same convention.
				if ca, ok := c.adapter().(*ClaudeAdapter); ok {
					ca.MCPExclusive = true
					byField, fieldErr := ca.ProviderProjection(ctx, ProjectionOptions{})
					if fieldErr != nil {
						t.Fatal(fieldErr)
					}
					if !reflect.DeepEqual(byField.Launch, proj.Launch) {
						t.Errorf("option and field give different conventions:\n  option %+v\n  field  %+v", proj.Launch, byField.Launch)
					}
					// Both at once is still one flag.
					both, bothErr := ca.ProviderProjection(ctx, ProjectionOptions{MCPExclusive: true})
					if bothErr != nil {
						t.Fatal(bothErr)
					}
					b, _ := both.ResolveLaunch(roots, "x")
					if n := countOf(b.Argv, strictMCPFlag); n != 1 {
						t.Errorf("field and option together: %s %d times, want once", strictMCPFlag, n)
					}
				}
				// The caller's adapter is not changed to satisfy the request.
				if ca, ok := adapter.(*ClaudeAdapter); ok && ca.MCPExclusive {
					t.Error("ProviderProjection set MCPExclusive on the caller's adapter")
				}
			case registry.MCPExclusivityProjectedLayout:
				if err != nil {
					t.Fatalf("projection: %v", err)
				}
				got, resolveErr := proj.ResolveLaunch(roots, "x")
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				if !slices.Equal(got.Argv, plainLaunch.Argv) {
					t.Errorf("a layout mode adds nothing to argv: got %q, plain %q", got.Argv, plainLaunch.Argv)
				}
				if !envIs(got, "CODEX_HOME", roots.BootRoot) {
					t.Errorf("the launch does not set the config root: %v", got.Env)
				}
			}
		})
	}
}

// The projection refuses, not the caller's luck: with the registry's claim
// removed the flag and the layout checks fail on their own.
func TestRequireMCPExclusiveChecksTheConvention(t *testing.T) {
	// Fail closed when the registry's claim and the projected convention
	// disagree, and say which provider and mode, so a host can tell which
	// launch it asked for and did not get.
	flagless := ProviderProjection{Provider: runtimes.Claude, Mode: runtimes.ModeSubprocessPerTurn, Launch: LaunchConvention{Argv: lits("-p")}}
	err := requireMCPExclusive(flagless, ProjectionOptions{MCPExclusive: true})
	if !errors.Is(err, ErrMCPExclusiveUnsupported) {
		t.Errorf("a claude convention without the flag: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	wantNamed(t, err, "claude", string(runtimes.ModeSubprocessPerTurn))
	rootless := ProviderProjection{Provider: runtimes.Codex, Mode: runtimes.ModeSubprocessPerTurn}
	err = requireMCPExclusive(rootless, ProjectionOptions{MCPExclusive: true})
	if !errors.Is(err, ErrMCPExclusiveUnsupported) {
		t.Errorf("a codex convention that sets no CODEX_HOME: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	wantNamed(t, err, "codex", string(runtimes.ModeSubprocessPerTurn))
	// A codex convention that unsets the root, or only prepends to it, does not
	// hold the claim either: the root must be set.
	prepends := ProviderProjection{Provider: runtimes.Codex, Mode: runtimes.ModeSubprocessPerTurn, Launch: LaunchConvention{Env: []EnvDelta{{Name: "CODEX_HOME", Operation: EnvPrepend, Value: "/x"}}}}
	err = requireMCPExclusive(prepends, ProjectionOptions{MCPExclusive: true})
	if !errors.Is(err, ErrMCPExclusiveUnsupported) {
		t.Errorf("a codex convention that only prepends to CODEX_HOME: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	// A mode with no mechanism at all is refused whatever the convention holds.
	none := ProviderProjection{Provider: runtimes.OpenCode, Mode: runtimes.ModeSubprocessPerTurn, Launch: LaunchConvention{Argv: lits(claudeStrictMCPConfigFlag)}}
	err = requireMCPExclusive(none, ProjectionOptions{MCPExclusive: true})
	if !errors.Is(err, ErrMCPExclusiveUnsupported) {
		t.Errorf("a mode with no mechanism: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	wantNamed(t, err, "opencode", string(runtimes.ModeSubprocessPerTurn))
	// The refusal says which kind of "no": measured absent, not measured, a
	// mode the registry does not measure, or a runtime it does not know.
	for _, c := range []struct {
		name string
		proj ProviderProjection
		want string
	}{
		{"measured absent", ProviderProjection{Provider: runtimes.OpenCode, Mode: runtimes.ModeHTTPSSE}, "was measured and has no switch"},
		{"native but not measured", ProviderProjection{Provider: runtimes.Antigravity, Mode: runtimes.ModeSubprocessPerTurn}, "was not measured"},
		{"not a native mode", ProviderProjection{Provider: runtimes.Claude, Mode: runtimes.ModeACPStdio}, "not a native mode"},
		{"not in the registry", ProviderProjection{Provider: "no-such-runtime", Mode: runtimes.ModeSubprocessPerTurn}, "not a runtime in the registry"},
	} {
		kindErr := requireMCPExclusive(c.proj, ProjectionOptions{MCPExclusive: true})
		if !errors.Is(kindErr, ErrMCPExclusiveUnsupported) {
			t.Errorf("%s: err = %v, want ErrMCPExclusiveUnsupported", c.name, kindErr)
			continue
		}
		wantNamed(t, kindErr, string(c.proj.Provider), string(c.proj.Mode))
		if !strings.Contains(kindErr.Error(), c.want) {
			t.Errorf("%s: error %q does not say %q", c.name, kindErr, c.want)
		}
	}
	if err = requireMCPExclusive(flagless, ProjectionOptions{}); err != nil {
		t.Errorf("no request, no check: %v", err)
	}
}

// CheckMCPExclusive is what a host runs on a projection from an adapter that
// may ignore ProjectionOptions.MCPExclusive: it passes the projection the
// built-in adapter renders when asked, and refuses the one it renders when not.
func TestCheckMCPExclusiveJudgesAProjectionNotTheRequest(t *testing.T) {
	ctx := PlantContext{AgentName: "agent"}
	asked, err := NewClaudeAdapter().ProviderProjection(ctx, ProjectionOptions{MCPExclusive: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckMCPExclusive(asked); err != nil {
		t.Errorf("a claude projection rendered with MCPExclusive: %v", err)
	}
	ignored, err := NewClaudeAdapter().ProviderProjection(ctx, ProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = CheckMCPExclusive(ignored)
	if !errors.Is(err, ErrMCPExclusiveUnsupported) {
		t.Fatalf("a claude projection that did not ask: err = %v, want ErrMCPExclusiveUnsupported", err)
	}
	wantNamed(t, err, "claude", string(ignored.Mode))
	codex, err := NewCodexAdapter().ProviderProjection(ctx, ProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckMCPExclusive(codex); err != nil {
		t.Errorf("a codex projection sets its own config root: %v", err)
	}
}

// wantNamed fails unless err names the provider and the mode it refused.
func wantNamed(t *testing.T, err error, provider, mode string) {
	t.Helper()
	if err == nil {
		t.Error("no error to name the provider and mode")
		return
	}
	if want := provider + "/" + mode; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name %q", err, want)
	}
}
