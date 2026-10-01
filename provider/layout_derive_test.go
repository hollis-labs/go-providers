package provider

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
)

type projectingAdapter interface {
	ProjectionProvider
	BootDirSpec() BootDirSpec
}

var builtinModes = []struct {
	provider runtimes.ID
	mode     layout.Shape
	adapter  func() projectingAdapter
}{
	{runtimes.Claude, shapePerTurn, func() projectingAdapter {
		return &ClaudeAdapter{}
	}},
	{runtimes.Claude, shapeBare, func() projectingAdapter {
		return &ClaudeAdapter{Bare: true}
	}},
	{runtimes.Claude, shapePTY, func() projectingAdapter {
		return &ClaudeAdapter{PTY: true}
	}},
	{runtimes.Claude, shapeStreaming, func() projectingAdapter {
		return &ClaudeAdapter{InputMode: "stream-json"}
	}},
	{runtimes.Codex, shapePerTurn, func() projectingAdapter {
		return NewCodexAdapter()
	}},
	{runtimes.Codex, shapeJSONRPC, func() projectingAdapter {
		return &CodexAdapter{Mode: "app-server"}
	}},
	{runtimes.OpenCode, shapePerTurn, func() projectingAdapter {
		return &OpencodeAdapter{Agent: "fixture-agent"}
	}},
	{runtimes.OpenCode, shapeHTTPSSE, func() projectingAdapter {
		return &OpencodeAdapter{Mode: "serve-http", Agent: "fixture-agent"}
	}},
	{runtimes.Antigravity, shapePerTurn, func() projectingAdapter {
		return NewAntigravityAdapter()
	}},
}

// layout must not import provider, so its Root strings are guarded here. Its
// modes are the runtimes vocabulary itself; the table may use only native
// modes and the variants the built-in adapters project.
func TestLayoutVocabularyMirrorsProvider(t *testing.T) {
	roots := map[RootKind]layout.Root{
		RootBoot: layout.RootBoot, RootProject: layout.RootProject,
		RootConfig: layout.RootConfig, RootState: layout.RootState,
	}
	for rk, lr := range roots {
		if string(rk) != string(lr) {
			t.Errorf("root %q != layout %q", rk, lr)
		}
	}
	known := map[layout.Shape]bool{}
	for _, c := range builtinModes {
		known[c.mode] = true
	}
	for _, e := range layout.Table() {
		if e.Shape() != (layout.Shape{}) && !known[e.Shape()] {
			t.Errorf("table row %s/%s/%s pins a shape no built-in adapter projects", e.Provider, e.Shape(), e.Concern)
		}
		for _, r := range append([]layout.Root{e.Root, e.CWD}, envRoots(e)...) {
			if r == "" {
				continue
			}
			if _, err := rootValue(ProjectionRoots{}, RootKind(r)); err != nil {
				t.Errorf("table row %s/%s/%s uses root %q unknown to provider: %v", e.Provider, e.Shape(), e.Concern, r, err)
			}
		}
	}
}

func envRoots(e layout.Entry) []layout.Root {
	var out []layout.Root
	for _, v := range e.Env {
		out = append(out, layout.Root(v))
	}
	return out
}

// Every concern the built-in adapters read must have a row for every mode they
// run in; a gap would panic at projection time.
func TestLayoutTableCoversBuiltInAdapters(t *testing.T) {
	for _, c := range builtinModes {
		ctx := PlantContext{AgentName: "fixture-agent", MCPLoopbackURL: "http://127.0.0.1:1/mcp"}
		proj, err := c.adapter().ProviderProjection(ctx, ProjectionOptions{Skills: []SkillPackage{fixtureSkill()}})
		if err != nil {
			t.Fatalf("%s/%s: %v", c.provider, c.mode, err)
		}
		if got := (layout.Shape{Mode: proj.Mode, Variant: proj.Variant}); got != c.mode {
			t.Errorf("%s/%s: projected shape %s", c.provider, c.mode, got)
		}
		if _, ok := layout.SkillRoot(c.provider, c.mode); !ok {
			t.Errorf("%s/%s: no skill root", c.provider, c.mode)
		}
	}
}

func fixtureSkill() SkillPackage {
	return SkillPackage{Name: "fixture-skill", Files: []SkillFile{
		{RelPath: "SKILL.md", Content: []byte("---\nname: fixture-skill\ndescription: Fixture skill\n---\n\nUse the fixture.\n")},
		{RelPath: "references/info.md", Content: []byte("reference\n")},
	}}
}

// The legacy BootDirSpec of the built-in adapters and the projection must agree
// with the table on paths, environment and project-dir flag. The expectations
// are derived from layout.For directly, not through the provider helpers.
func TestBootDirSpecEqualsLayout(t *testing.T) {
	for _, c := range builtinModes {
		spec := c.adapter().BootDirSpec()
		// The legacy spec is mode-independent: claude reads print rows, opencode run rows.
		legacyMode := c.mode
		switch c.provider {
		case runtimes.Claude:
			legacyMode = shapePerTurn
		case runtimes.OpenCode:
			legacyMode = shapePerTurn
		}
		rows := layout.For(c.provider, legacyMode)

		wantFiles := map[string]bool{}
		var wantEnv []string
		wantProjectArg := ""
		for _, e := range rows {
			switch e.Concern {
			case layout.Instructions, layout.Boot, layout.MCP, layout.NativeConfig, layout.Auth, layout.Agents:
				if e.Shape() != (layout.Shape{}) && e.Shape() != legacyMode {
					continue
				}
				if e.Root == layout.RootBoot {
					wantFiles[strings.ReplaceAll(e.Rel, layout.AgentPlaceholder, "fixture-agent")] = true
				}
			case layout.ProjectDir:
				wantProjectArg = e.Flag + " {{.ProjectDir}}"
			}
			if e.Concern == layout.NativeConfig && e.Env != nil {
				for k, v := range e.Env {
					if v != "boot" {
						t.Fatalf("test only knows boot-rooted env, got %s=%s", k, v)
					}
					wantEnv = append(wantEnv, k+"={{.BootDir}}")
				}
			}
		}
		// Claude's bare-only rows repeat the same rel; the set collapses them.
		gotFiles := map[string]bool{}
		for _, f := range spec.PlantedFiles {
			gotFiles[f.RelPath] = true
		}
		for f := range wantFiles {
			if !gotFiles[f] {
				t.Errorf("%s/%s: BootDirSpec lacks layout file %q (has %v)", c.provider, c.mode, f, keys(gotFiles))
			}
		}
		for f := range gotFiles {
			if !wantFiles[f] {
				t.Errorf("%s/%s: BootDirSpec plants %q which layout does not list", c.provider, c.mode, f)
			}
		}
		sort.Strings(wantEnv)
		if strings.Join(spec.EnvAmendments, ",") != strings.Join(wantEnv, ",") {
			t.Errorf("%s/%s: env %v != layout %v", c.provider, c.mode, spec.EnvAmendments, wantEnv)
		}
		if spec.ProjectDirArg != wantProjectArg {
			t.Errorf("%s/%s: ProjectDirArg %q != layout %q", c.provider, c.mode, spec.ProjectDirArg, wantProjectArg)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ProviderCapabilityMatrix and Table() are two statements about the same
// thing; they must agree on what each mode projects.
func TestProviderCapabilityMatrixAgreesWithLayoutTable(t *testing.T) {
	concerns := map[ProviderFeature]layout.Concern{
		FeatureInstructions: layout.Instructions,
		FeatureNativeConfig: layout.NativeConfig,
		FeatureMCP:          layout.MCP,
		FeatureSkillTrees:   layout.Skills,
	}
	for _, row := range ProviderCapabilityMatrix() {
		for feature, concern := range concerns {
			_, inTable := layout.Find(row.Provider, row.Shape(), concern)
			projected := row.Features[string(feature)] == string(SupportProjected)
			if projected != inTable {
				t.Errorf("%s/%s: matrix says %s=%s, table has %s row = %v", row.Provider, row.Mode, feature, row.Features[string(feature)], concern, inTable)
			}
		}
	}
}

func TestClaudeBareSkillsAddBootDir(t *testing.T) {
	roots := ProjectionRoots{ProjectRoot: "/p/project", BootRoot: "/p/boot"}
	a := &ClaudeAdapter{Bare: true}
	without, err := a.ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	with, err := a.ProviderProjection(PlantContext{}, ProjectionOptions{Skills: []SkillPackage{fixtureSkill()}})
	if err != nil {
		t.Fatal(err)
	}
	bw, _ := without.ResolveLaunch(roots, "x")
	bs, _ := with.ResolveLaunch(roots, "x")
	if strings.Contains(strings.Join(bw.Argv, " "), "--add-dir /p/boot") {
		t.Errorf("bare without skills must not add the boot root: %v", bw.Argv)
	}
	if got := strings.Join(bs.Argv, " "); !strings.HasSuffix(got, "--add-dir /p/project --add-dir /p/boot") {
		t.Errorf("bare with skills must add the boot root after the project: %v", bs.Argv)
	}
	pr := &ClaudeAdapter{}
	p2, _ := pr.ProviderProjection(PlantContext{}, ProjectionOptions{Skills: []SkillPackage{fixtureSkill()}})
	b2, _ := p2.ResolveLaunch(roots, "x")
	if got := strings.Join(b2.Argv, " "); strings.Contains(got, "--add-dir /p/boot") || !strings.HasSuffix(got, "--add-dir /p/project") {
		t.Errorf("non-bare claude reads boot skills from cwd (no boot --add-dir) and reaches the project by --add-dir: %v", b2.Argv)
	}
}

// harnessGolden reads the newest Step 0 golden: provider -> probe id -> result.
func harnessGolden(t *testing.T) map[string]map[string]string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("testdata", "harness-discovery", "*.tsv"))
	if len(files) == 0 {
		t.Fatal("no harness-discovery golden")
	}
	sort.Strings(files)
	f, err := os.Open(files[len(files)-1])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) >= 4 && c[0] == "R" {
			if out[c[1]] == nil {
				out[c[1]] = map[string]string{}
			}
			id := c[2]
			if i := strings.Index(id, "-"); i > 0 {
				id = id[:i]
			}
			out[c[1]][id] = c[3]
		}
	}
	return out
}

func seen(result, name string) bool {
	for _, n := range strings.Split(result, ",") {
		if n == name {
			return true
		}
	}
	return false
}

// A table-derived projection must place skills at a root and prefix that the
// Step 0 golden shows the harness reads under this package's own launch
// convention. Each case names the probe whose launch equals the convention and
// the fixture skill that sits at the projected location.
func TestProjectedSkillPlacementIsReadByHarness(t *testing.T) {
	golden := harnessGolden(t)
	type check struct {
		probe   string // probe id in the golden
		visible string // fixture skill that lives where we place skills
		hidden  string // fixture at the pre-change location; must not be visible ("" = none)
	}
	cases := []struct {
		provider runtimes.ID
		mode     layout.Shape
		launch   func(b LaunchBinding, roots ProjectionRoots) bool // the probe launch matches this binding
		want     check
	}{
		{runtimes.Claude, shapePerTurn, func(b LaunchBinding, r ProjectionRoots) bool { return b.CWD == r.BootRoot }, check{"C2", "c-boot-claude-dir", ""}},
		{runtimes.Codex, shapePerTurn, func(b LaunchBinding, r ProjectionRoots) bool {
			return b.CWD == r.BootRoot && hasArgPair(b.Argv, "--cd", r.ProjectRoot) && envIs(b, "CODEX_HOME", r.BootRoot)
		}, check{"X3", "x-boot-root-dir", "x-boot-agents-dir"}},
		{runtimes.Codex, shapeJSONRPC, func(b LaunchBinding, r ProjectionRoots) bool {
			return b.CWD == r.BootRoot && envIs(b, "CODEX_HOME", r.BootRoot)
		}, check{"X2", "x-boot-root-dir", ""}},
		{runtimes.OpenCode, shapePerTurn, func(b LaunchBinding, r ProjectionRoots) bool {
			return b.CWD == r.ProjectRoot && envIs(b, "OPENCODE_CONFIG_DIR", r.BootRoot)
		}, check{"O2", "o-cfg-skills-dir", "o-cfg-dotopencode-dir"}},
		{runtimes.OpenCode, shapeHTTPSSE, func(b LaunchBinding, r ProjectionRoots) bool {
			return b.CWD == r.ProjectRoot && envIs(b, "OPENCODE_CONFIG_DIR", r.BootRoot)
		}, check{"O2", "o-cfg-skills-dir", "o-cfg-dotopencode-dir"}},
	}
	roots := ProjectionRoots{ProjectRoot: "/p/project", BootRoot: "/p/boot"}
	for _, c := range cases {
		var adapter ProjectionProvider
		for _, m := range builtinModes {
			if m.provider == c.provider && m.mode == c.mode {
				adapter = m.adapter()
				break
			}
		}
		proj, err := adapter.ProviderProjection(PlantContext{AgentName: "fixture-agent"}, ProjectionOptions{Skills: []SkillPackage{fixtureSkill()}})
		if err != nil {
			t.Fatal(err)
		}
		bind, err := proj.ResolveLaunch(roots, "x")
		if err != nil {
			t.Fatal(err)
		}
		if !c.launch(bind, roots) {
			t.Errorf("%s/%s: launch %+v is not the convention probe %s ran", c.provider, c.mode, bind, c.want.probe)
		}
		var placed []string
		for _, f := range proj.Files {
			if f.Role == "skill" {
				placed = append(placed, f.RelPath)
			}
		}
		if len(placed) == 0 || path.Base(placed[0]) != "SKILL.md" || path.Base(path.Dir(placed[0])) != "fixture-skill" {
			t.Errorf("%s/%s: skills not placed in <name>/SKILL.md form: %v", c.provider, c.mode, placed)
		}
		result := golden[string(c.provider)][c.want.probe]
		if !seen(result, c.want.visible) {
			t.Errorf("%s/%s: probe %s (%q) does not show %s: skills projected at %v would be unread", c.provider, c.mode, c.want.probe, result, c.want.visible, placed)
		}
		if c.want.hidden != "" && seen(result, c.want.hidden) {
			t.Errorf("%s/%s: probe %s shows %s: the pre-layout location is readable again, revisit the table", c.provider, c.mode, c.want.probe, c.want.hidden)
		}
	}

	// Claude bare: the harness reads only --add-dir directories (C4 none, C5
	// add-dir), so the projection must pass the boot root as --add-dir. The
	// boot-as-add-dir case is the supplementary measurement in
	// docs/HARNESS-DISCOVERY.md.
	if got := golden["claude"]["C4"]; got != "" {
		t.Errorf("C4: bare cwd skills = %q, want none; the bare --add-dir row is unnecessary", got)
	}
	if !seen(golden["claude"]["C5"], "c-proj-claude-dir") {
		t.Errorf("C5 does not show an --add-dir directory contributing .claude/skills")
	}
}

func hasArgPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

func envIs(b LaunchBinding, name, value string) bool {
	for _, d := range b.Env {
		if d.Name == name && d.Value == value {
			return true
		}
	}
	return false
}

func TestSkillPackageTreeHash(t *testing.T) {
	pkg := fixtureSkill()
	got, err := pkg.TreeHash()
	if err != nil {
		t.Fatal(err)
	}
	// go-agentdef's definition (skills.go hashTree): sorted relative paths,
	// each framed as path NUL decimal-length NUL content NUL.
	h := sha256.New()
	for _, f := range []SkillFile{pkg.Files[0], pkg.Files[1]} { // SKILL.md < references/info.md
		h.Write([]byte(f.RelPath + "\x00" + strconv.Itoa(len(f.Content)) + "\x00"))
		h.Write(f.Content)
		h.Write([]byte{0})
	}
	want := "sha256:" + hex.EncodeToString(h.Sum(nil))
	const literal = "sha256:3d19d591f0b228b601c1f597c51c41b79ec5882942c9567e94359b7ec6f77ca6"
	if got != want || got != literal {
		t.Errorf("TreeHash = %s, framing = %s, literal = %s", got, want, literal)
	}
	// Order of Files must not matter; .DS_Store must not count.
	pkg.Files = []SkillFile{pkg.Files[1], {RelPath: ".DS_Store", Content: []byte("junk")}, pkg.Files[0]}
	if again, _ := pkg.TreeHash(); again != got {
		t.Errorf("hash depends on file order or .DS_Store: %s", again)
	}
}

func TestSkillPackageHashPinIsEnforced(t *testing.T) {
	pkg := fixtureSkill()
	pkg.Hash = "sha256:3d19d591f0b228b601c1f597c51c41b79ec5882942c9567e94359b7ec6f77ca6"
	if _, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{Skills: []SkillPackage{pkg}}); err != nil {
		t.Fatalf("matching pin rejected: %v", err)
	}
	pkg.Files[1].Content = []byte("tampered\n")
	_, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{Skills: []SkillPackage{pkg}})
	if err == nil || !strings.Contains(err.Error(), "does not match pinned") {
		t.Fatalf("tampered package accepted: %v", err)
	}
}
