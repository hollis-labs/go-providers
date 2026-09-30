package layout_test

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
func goldenIDs(t *testing.T, file string) map[layout.Provider][]string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[layout.Provider][]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		cols := strings.Split(sc.Text(), "\t")
		if len(cols) >= 3 && cols[0] == "R" {
			p := layout.Provider(cols[1])
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
	seen := map[[5]string]int{}
	for i, e := range layout.Table() {
		k := [5]string{string(e.Provider), string(e.Mode), string(e.Concern), string(e.Root), e.Rel}
		if j, dup := seen[k]; dup {
			t.Errorf("row %d duplicates row %d: %v", i, j, k)
		}
		seen[k] = i
	}
}

func TestTableEveryEntryIsJustified(t *testing.T) {
	golden := goldenIDs(t, newestGolden(t))
	for i, e := range layout.Table() {
		name := string(e.Provider) + "/" + string(e.Mode) + "/" + string(e.Concern) + "/" + e.Rel
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
		switch e.Provider {
		case layout.Claude, layout.Codex, layout.OpenCode, layout.Antigravity:
		default:
			t.Errorf("row %d: unknown provider %q", i, e.Provider)
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
	e, _ := layout.SkillRoot(layout.Codex, layout.ModeCodexExec)
	e.Env["CODEX_HOME"] = "mutated"
	e2, _ := layout.SkillRoot(layout.Codex, layout.ModeCodexExec)
	if e2.Env["CODEX_HOME"] != "boot" {
		t.Fatal("Env map is shared with the table")
	}
}

func TestSkillRoot(t *testing.T) {
	cases := []struct {
		p    layout.Provider
		m    layout.Mode
		root layout.Root
		rel  string
		flag string
	}{
		{layout.Claude, layout.ModeClaudePrint, layout.RootBoot, ".claude/skills", ""},
		{layout.Claude, layout.ModeClaudePTY, layout.RootBoot, ".claude/skills", ""},
		{layout.Claude, layout.ModeClaudeBare, layout.RootBoot, ".claude/skills", "--add-dir"},
		{layout.Codex, layout.ModeCodexExec, layout.RootBoot, "skills", ""},
		{layout.Codex, layout.ModeCodexAppServer, layout.RootBoot, "skills", ""},
		{layout.OpenCode, layout.ModeOpenCodeRun, layout.RootBoot, "skills", ""},
		{layout.OpenCode, layout.ModeOpenCodeServeHTTP, layout.RootBoot, "skills", ""},
	}
	for _, c := range cases {
		e, ok := layout.SkillRoot(c.p, c.m)
		if !ok || e.Root != c.root || e.Rel != c.rel || e.Flag != c.flag || e.Form != layout.FormDir {
			t.Errorf("SkillRoot(%s,%s) = %+v, %v", c.p, c.m, e, ok)
		}
	}
	if _, ok := layout.SkillRoot("nope", ""); ok {
		t.Error("SkillRoot of an unknown provider must report false")
	}
}

func TestForIncludesEveryModeRows(t *testing.T) {
	var sawBare, sawAny bool
	for _, e := range layout.For(layout.Claude, layout.ModeClaudePrint) {
		if e.Mode == layout.ModeClaudeBare {
			sawBare = true
		}
		if e.Mode == "" {
			sawAny = true
		}
	}
	if sawBare || !sawAny {
		t.Errorf("For(claude, print): bare rows leaked=%v, every-mode rows present=%v", sawBare, sawAny)
	}
}
