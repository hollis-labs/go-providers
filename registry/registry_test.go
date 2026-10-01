package registry_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
	"github.com/hollis-labs/go-providers/registry"
)

func mustLookup(t *testing.T, name string) registry.Descriptor {
	t.Helper()
	d, ok := registry.Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) found nothing", name)
	}
	return d
}

// One descriptor per runtime in the leaf vocabulary, in its order: the
// registry is the one list.
func TestEveryRuntimeHasOneDescriptor(t *testing.T) {
	var got []runtimes.ID
	for _, d := range registry.All() {
		got = append(got, d.ID)
	}
	if !slices.Equal(got, runtimes.IDs()) {
		t.Fatalf("All() ids = %v, want %v", got, runtimes.IDs())
	}
}

func TestLookupByIDOrAlias(t *testing.T) {
	for name, want := range map[string]runtimes.ID{
		"claude": runtimes.Claude, "claude-code": runtimes.Claude, " Claude-Code ": runtimes.Claude,
		"codex": runtimes.Codex, "opencode": runtimes.OpenCode, "open-code": runtimes.OpenCode,
		"copilot": runtimes.Copilot, "pi": runtimes.Pi, "pi-acp": runtimes.Pi,
		"antigravity": runtimes.Antigravity, "agy": runtimes.Antigravity,
	} {
		if d := mustLookup(t, name); d.ID != want {
			t.Errorf("Lookup(%q).ID = %s, want %s", name, d.ID, want)
		}
	}
	for _, name := range []string{"", "gemini", "claude-print", "app-server"} {
		if _, ok := registry.Lookup(name); ok {
			t.Errorf("Lookup(%q) found a descriptor", name)
		}
	}
}

func TestDefaultModes(t *testing.T) {
	for id, want := range map[runtimes.ID]runtimes.Mode{
		runtimes.Claude: runtimes.ModeStreamingStdio,
		// D-74: Codex defaults to app-server.
		runtimes.Codex:       runtimes.ModeJSONRPCStdio,
		runtimes.OpenCode:    runtimes.ModeSubprocessPerTurn,
		runtimes.Copilot:     runtimes.ModeACPStdio,
		runtimes.Pi:          runtimes.ModeACPStdio,
		runtimes.Antigravity: runtimes.ModeSubprocessPerTurn,
	} {
		if d := mustLookup(t, string(id)); d.DefaultMode != want {
			t.Errorf("%s default = %s, want %s", id, d.DefaultMode, want)
		}
	}
}

// Copilot and Pi are ACP-only: no native mode, no layout, no boot dir. Every
// other runtime has a native mode and layout rows.
func TestACPOnlyRuntimesHaveNoLayout(t *testing.T) {
	for _, d := range registry.All() {
		acpOnly := d.ID == runtimes.Copilot || d.ID == runtimes.Pi
		if acpOnly != (len(d.NativeModes()) == 0) || acpOnly == d.HasLayout() {
			t.Errorf("%s: native modes %v, HasLayout %v; want ACP-only = %v", d.ID, d.NativeModes(), d.HasLayout(), acpOnly)
		}
	}
	if d := mustLookup(t, "copilot"); !d.Supports(runtimes.ModeACPTCP) {
		t.Error("copilot must support acp-tcp (copilot --acp --port N)")
	}
}

// Layout is read from the layout table, not copied: it is exactly the
// runtime's rows.
func TestLayoutIsTheTable(t *testing.T) {
	for _, d := range registry.All() {
		var want []layout.Entry
		for _, e := range layout.Table() {
			if e.Provider == d.ID {
				want = append(want, e)
			}
		}
		got := d.Layout()
		if len(got) != len(want) {
			t.Fatalf("%s: %d layout rows, table has %d", d.ID, len(got), len(want))
		}
		for i := range want {
			if got[i].Shape() != want[i].Shape() || got[i].Concern != want[i].Concern || got[i].Rel != want[i].Rel {
				t.Errorf("%s row %d = %+v, want %+v", d.ID, i, got[i], want[i])
			}
		}
	}
}

func TestCapabilitiesPerMode(t *testing.T) {
	codex := mustLookup(t, "codex")
	if !codex.Has(runtimes.ModeJSONRPCStdio, runtimes.CapApprovals) || codex.Has(runtimes.ModeSubprocessPerTurn, runtimes.CapApprovals) {
		t.Error("codex asks for approval in app-server mode only")
	}
	claude := mustLookup(t, "claude")
	if claude.Has(runtimes.ModePTY, runtimes.CapTypedEvents) || !claude.Has(runtimes.ModeStreamingStdio, runtimes.CapTypedEvents) {
		t.Error("claude's TUI emits no typed events; stream-json does")
	}
	if claude.Capabilities(runtimes.ModeHTTPSSE) != nil || claude.Supports(runtimes.ModeHTTPSSE) {
		t.Error("an unsupported mode has no capabilities")
	}
	// ACP claims are what go-agent-wrapper measured live: Claude's and
	// OpenCode's bridges ran shell tools without asking, so they declare no
	// approvals; OpenCode's session/load resume and Copilot's permission
	// request were observed.
	if claude.Has(runtimes.ModeACPStdio, runtimes.CapApprovals) {
		t.Error("claude acp-stdio must not claim approvals")
	}
	opencode := mustLookup(t, "opencode")
	if opencode.Has(runtimes.ModeACPStdio, runtimes.CapApprovals) || !opencode.Has(runtimes.ModeACPStdio, runtimes.CapResume) {
		t.Error("opencode acp-stdio: resume yes, approvals no")
	}
	if copilot := mustLookup(t, "copilot"); !copilot.Has(runtimes.ModeACPStdio, runtimes.CapApprovals) || copilot.Has(runtimes.ModeACPStdio, runtimes.CapResume) {
		t.Error("copilot acp-stdio: approvals measured, resume not")
	}
	agy := mustLookup(t, "agy")
	if !agy.Has(runtimes.ModeSubprocessPerTurn, runtimes.CapResumeKeepsID) {
		t.Error("antigravity resume keeps the session id")
	}
}

// The posture enum lands with CW-20260930-0138; until then no descriptor maps
// one.
func TestNoPostureMappingYet(t *testing.T) {
	for _, d := range registry.All() {
		if d.Posture != nil {
			t.Errorf("%s has a posture mapping before the posture enum exists", d.ID)
		}
	}
}

func TestDescriptorsAreCopies(t *testing.T) {
	d := mustLookup(t, "claude")
	d.Aliases[0] = "mutated"
	d.Modes[0].Capabilities[0] = "mutated"
	d.Modes[0].Mode = "mutated"
	again := mustLookup(t, "claude")
	if again.Aliases[0] == "mutated" || again.Modes[0].Mode == "mutated" || again.Modes[0].Capabilities[0] == "mutated" {
		t.Fatal("a caller's edit reached the registry")
	}
	if _, ok := registry.Lookup("mutated"); ok {
		t.Fatal("an edited alias became resolvable")
	}
}

type fakeTB struct {
	cleanups []func()
	failed   string
}

func (f *fakeTB) Helper()                   {}
func (f *fakeTB) Cleanup(fn func())         { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Fatalf(s string, a ...any) { f.failed = fmt.Sprintf(s, a...) }
func (f *fakeTB) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func fake() registry.Descriptor {
	return registry.Descriptor{
		ID:          "fake-cli",
		Aliases:     []string{"fake"},
		Binary:      "fake-cli",
		EnvOverride: "FAKE_CLI_PATH",
		Modes:       []registry.ModeSupport{{Mode: runtimes.ModeSubprocessPerTurn, Capabilities: []runtimes.Capability{runtimes.CapResume}}},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
	}
}

func TestRegisterForTest(t *testing.T) {
	tb := &fakeTB{}
	registry.RegisterForTest(tb, fake())
	if tb.failed != "" {
		t.Fatal(tb.failed)
	}
	if d := mustLookup(t, "FAKE"); d.ID != "fake-cli" || d.HasLayout() {
		t.Errorf("Lookup(FAKE) = %+v", d)
	}
	if all := registry.All(); all[len(all)-1].ID != "fake-cli" || len(all) != len(runtimes.IDs())+1 {
		t.Errorf("All() does not end with the fake: %d descriptors", len(all))
	}
	tb.runCleanups()
	if _, ok := registry.Lookup("fake"); ok {
		t.Error("cleanup did not remove the fake")
	}
	if len(registry.All()) != len(runtimes.IDs()) {
		t.Error("cleanup left the registry larger than the built-ins")
	}
	if mustLookup(t, "claude-code").ID != runtimes.Claude {
		t.Error("removing the fake broke the built-in index")
	}
}

func TestRegisterForTestRefusesCollisionsAndBadDescriptors(t *testing.T) {
	cases := map[string]func(*registry.Descriptor){
		"id collides with a built-in":    func(d *registry.Descriptor) { d.ID = "claude" },
		"alias collides with a built-in": func(d *registry.Descriptor) { d.Aliases = []string{"Claude-Code"} },
		"default not supported":          func(d *registry.Descriptor) { d.DefaultMode = runtimes.ModePTY },
		"unknown mode":                   func(d *registry.Descriptor) { d.Modes[0].Mode = "app-server" },
		"unknown capability":             func(d *registry.Descriptor) { d.Modes[0].Capabilities = []runtimes.Capability{"mcp"} },
		"no binary":                      func(d *registry.Descriptor) { d.Binary = "" },
		"uppercase id":                   func(d *registry.Descriptor) { d.ID = "Fake" },
		"ACP default beside a native mode": func(d *registry.Descriptor) {
			d.Modes = append(d.Modes, registry.ModeSupport{Mode: runtimes.ModeACPStdio})
			d.DefaultMode = runtimes.ModeACPStdio
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := fake()
			mutate(&d)
			tb := &fakeTB{}
			registry.RegisterForTest(tb, d)
			defer tb.runCleanups()
			if tb.failed == "" {
				t.Errorf("RegisterForTest accepted %+v", d)
			}
		})
	}
}

func TestLookPath(t *testing.T) {
	home, pathDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", pathDir)
	t.Setenv("OPENCODE_CLI_PATH", "")
	oc := mustLookup(t, "opencode")
	if _, err := oc.LookPath(); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("LookPath with nothing installed = %v, want ErrNotFound", err)
	}

	own := filepath.Join(home, ".opencode", "bin", "opencode")
	writeExe(t, own)
	if p, err := oc.LookPath(); err != nil || p != own {
		t.Errorf("LookPath = %q, %v; want the runtime's own install dir %q", p, err, own)
	}

	common := filepath.Join(home, ".local", "bin", "opencode")
	writeExe(t, common)
	if p, _ := oc.LookPath(); p != common {
		t.Errorf("LookPath = %q; the common install dirs come before the runtime's own (%q)", p, common)
	}

	onPath := filepath.Join(pathDir, "opencode")
	writeExe(t, onPath)
	if p, _ := oc.LookPath(); p != onPath {
		t.Errorf("LookPath = %q; PATH comes first (%q)", p, onPath)
	}

	t.Setenv("OPENCODE_CLI_PATH", "/pinned/opencode")
	if p, _ := oc.LookPath(); p != "/pinned/opencode" {
		t.Errorf("LookPath = %q; the env override wins and is used as-is", p)
	}

	// Another runtime does not search OpenCode's install dir.
	t.Setenv("CLAUDE_CLI_PATH", "")
	writeExe(t, filepath.Join(home, ".opencode", "bin", "claude"))
	if _, err := mustLookup(t, "claude").LookPath(); err == nil {
		t.Error("claude was found in OpenCode's install dir")
	}
}

func writeExe(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
