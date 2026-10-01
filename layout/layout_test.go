package layout_test

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
)

// newestGolden returns the lexically newest TSV under the harness-discovery
// golden directory (file names start with an ISO date).
func newestGolden(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "provider", "testdata", "harness-discovery", "*.tsv"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no harness-discovery golden found: %v", err)
	}
	sort.Strings(files)
	return files[len(files)-1]
}

// goldenIDs returns provider -> set of probe ids from a golden TSV.
func goldenIDs(t *testing.T, file string) map[runtimes.ID][]string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[runtimes.ID][]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) >= 3 && cols[0] == "R" {
			p := runtimes.ID(cols[1])
			out[p] = append(out[p], cols[2])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// hasProbe reports whether id names a golden row: an exact match, or the id
// followed by "-" (rows are named like "C2-cwd=boot").
func hasProbe(golden []string, id string) bool {
	for _, g := range golden {
		if g == id || strings.HasPrefix(g, id+"-") {
			return true
		}
	}
	return false
}

func TestTableNoDuplicateRows(t *testing.T) {
	seen := map[[6]string]int{}
	for i, e := range layout.Table() {
		k := [6]string{string(e.Provider), string(e.Mode), string(e.Variant), string(e.Concern), string(e.Root), e.Rel}
		if j, dup := seen[k]; dup {
			t.Errorf("row %d duplicates row %d: %v", i, j, k)
		}
		seen[k] = i
	}
}

func TestTableEveryEntryIsJustified(t *testing.T) {
	golden := goldenIDs(t, newestGolden(t))
	for i, e := range layout.Table() {
		name := string(e.Provider) + "/" + e.Shape().String() + "/" + string(e.Concern) + "/" + e.Rel
		if (len(e.Probe) == 0) == (e.Unprobed == "") {
			t.Errorf("row %d %s: want exactly one of Probe and Unprobed", i, name)
		}
		for _, id := range e.Probe {
			if !hasProbe(golden[e.Provider], id) {
				t.Errorf("row %d %s: probe id %q not in newest golden for %s", i, name, id, e.Provider)
			}
		}
	}
}

func TestTableShape(t *testing.T) {
	for i, e := range layout.Table() {
		if !e.Provider.Valid() {
			t.Errorf("row %d: unknown runtime %q", i, e.Provider)
		}
		if e.Mode != "" && !e.Mode.Valid() {
			t.Errorf("row %d: unknown mode %q", i, e.Mode)
		}
		if e.Mode.ACP() {
			t.Errorf("row %d: an ACP mode has no boot-dir layout", i)
		}
		if e.Variant != "" && e.Variant != layout.VariantBare {
			t.Errorf("row %d: unknown variant %q", i, e.Variant)
		}
		if e.Concern == layout.Skills {
			if e.Form != layout.FormDir {
				t.Errorf("row %d: skills row must use the directory form, got %q", i, e.Form)
			}
			if e.Rel == "" {
				t.Errorf("row %d: skills row without a Rel", i)
			}
		} else if e.Form != "" {
			t.Errorf("row %d: Form set on a %s row", i, e.Concern)
		}
		if e.Root == "" {
			t.Errorf("row %d: empty Root", i)
		}
		if strings.HasPrefix(e.Rel, "/") || strings.Contains(e.Rel, "..") {
			t.Errorf("row %d: Rel %q must be a clean relative path", i, e.Rel)
		}
	}
}

func TestTableReturnsACopy(t *testing.T) {
	a := layout.Table()
	a[0].Rel = "mutated"
	for _, e := range layout.Table() {
		if e.Rel == "mutated" {
			t.Fatal("Table exposes its backing slice")
		}
	}
	e, _ := layout.SkillRoot(runtimes.Codex, perTurn)
	e.Env["CODEX_HOME"] = "mutated"
	e2, _ := layout.SkillRoot(runtimes.Codex, perTurn)
	if e2.Env["CODEX_HOME"] != "boot" {
		t.Fatal("Env map is shared with the table")
	}
}

func TestSkillRoot(t *testing.T) {
	cases := []struct {
		p    runtimes.ID
		m    layout.Shape
		root layout.Root
		rel  string
		flag string
	}{
		{runtimes.Claude, perTurn, layout.RootBoot, ".claude/skills", ""},
		{runtimes.Claude, pty, layout.RootBoot, ".claude/skills", ""},
		{runtimes.Claude, bare, layout.RootBoot, ".claude/skills", "--add-dir"},
		{runtimes.Codex, perTurn, layout.RootBoot, "skills", ""},
		{runtimes.Codex, jsonRPC, layout.RootBoot, "skills", ""},
		{runtimes.OpenCode, perTurn, layout.RootBoot, "skills", ""},
		{runtimes.OpenCode, httpSSE, layout.RootBoot, "skills", ""},
	}
	for _, c := range cases {
		e, ok := layout.SkillRoot(c.p, c.m)
		if !ok || e.Root != c.root || e.Rel != c.rel || e.Flag != c.flag || e.Form != layout.FormDir {
			t.Errorf("SkillRoot(%s,%s) = %+v, %v", c.p, c.m, e, ok)
		}
	}
	if _, ok := layout.SkillRoot("nope", layout.Shape{}); ok {
		t.Error("SkillRoot of an unknown provider must report false")
	}
}

func TestForIncludesEveryModeRows(t *testing.T) {
	var sawBare, sawAny bool
	for _, e := range layout.For(runtimes.Claude, perTurn) {
		if e.Variant == layout.VariantBare {
			sawBare = true
		}
		if e.Shape() == (layout.Shape{}) {
			sawAny = true
		}
	}
	if sawBare || !sawAny {
		t.Errorf("For(claude, print): bare rows leaked=%v, every-mode rows present=%v", sawBare, sawAny)
	}
}

func TestFindPrefersTheMostSpecificRow(t *testing.T) {
	cases := []struct {
		name  string
		s     layout.Shape
		c     layout.Concern
		flag  string
		found bool
	}{
		{"every-mode row for plain print", perTurn, layout.Instructions, "", true},
		{"mode+variant row beats every-mode row", bare, layout.Instructions, "--append-system-prompt-file", true},
		{"bare only applies in its own mode", layout.Shape{Mode: runtimes.ModeStreamingStdio, Variant: layout.VariantBare}, layout.NativeConfig, "", true},
		{"zero shape sees every-mode rows", layout.Shape{}, layout.MCP, "--mcp-config", true},
	}
	for _, c := range cases {
		e, ok := layout.Find(runtimes.Claude, c.s, c.c)
		if ok != c.found || e.Flag != c.flag {
			t.Errorf("%s: Find(claude, %s, %s) = flag %q ok %v, want %q %v", c.name, c.s, c.c, e.Flag, ok, c.flag, c.found)
		}
	}
	if _, ok := layout.Find(runtimes.Codex, jsonRPC, layout.ProjectDir); ok {
		t.Error("codex app-server (jsonrpc-stdio) has no project-dir row; the subprocess-per-turn row must not leak")
	}
	if e, ok := layout.Find(runtimes.Codex, perTurn, layout.ProjectDir); !ok || e.Flag != "--cd" {
		t.Errorf("codex exec project-dir = %+v %v, want --cd", e, ok)
	}
}

// Copilot and Pi are ACP-only: no boot-dir rows.
func TestRuntimesWithLayout(t *testing.T) {
	got := layout.Runtimes()
	want := []runtimes.ID{runtimes.Claude, runtimes.Codex, runtimes.OpenCode, runtimes.Antigravity}
	if len(got) != len(want) {
		t.Fatalf("Runtimes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Runtimes() = %v, want %v", got, want)
		}
	}
}

func TestShapeString(t *testing.T) {
	for s, want := range map[layout.Shape]string{
		{}:                            "all",
		perTurn:                       "subprocess-per-turn",
		bare:                          "subprocess-per-turn+bare",
		{Variant: layout.VariantBare}: "+bare",
	} {
		if s.String() != want {
			t.Errorf("%#v.String() = %q, want %q", s, s.String(), want)
		}
	}
}
