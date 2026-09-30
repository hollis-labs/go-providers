package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateLayoutGolden = flag.Bool("update-layout-golden", false, "rewrite provider/testdata/layout-regression/projection.golden")

// regressionCases are the built-in adapters and modes whose non-skill
// projection, launch convention and legacy BootDirSpec must not change when
// they are re-derived from package layout. The golden was recorded from the
// pre-layout code before provider was edited.
func regressionCases() []struct {
	name string
	a    interface {
		ProjectionProvider
		BootDirSpec() BootDirSpec
	}
} {
	return []struct {
		name string
		a    interface {
			ProjectionProvider
			BootDirSpec() BootDirSpec
		}
	}{
		{"claude-print", &ClaudeAdapter{}},
		{"claude-print-skip-permissions", &ClaudeAdapter{SkipPermissions: true}},
		{"claude-bare", &ClaudeAdapter{Bare: true}},
		{"claude-pty", &ClaudeAdapter{PTY: true}},
		{"claude-streaming-stdio", &ClaudeAdapter{InputMode: "stream-json"}},
		{"codex-exec", NewCodexAdapter()},
		{"codex-app-server", &CodexAdapter{Mode: "app-server"}},
		{"opencode-run", &OpencodeAdapter{Agent: "fixture-agent", Model: "fixture/model"}},
		{"opencode-run-default-agent", &OpencodeAdapter{}},
		{"opencode-serve-http", &OpencodeAdapter{Mode: "serve-http", Agent: "fixture-agent"}},
	}
}

func renderLayoutRegression(t *testing.T) string {
	t.Helper()
	ctx := PlantContext{
		SystemPrompt:   "Follow fixture instructions.",
		BootContent:    "Read @./boot.md and continue.",
		AgentName:      "fixture-agent",
		MCPLoopbackURL: "http://127.0.0.1:60000/mcp",
	}
	roots := ProjectionRoots{ProjectRoot: "/p/project", BootRoot: "/p/boot", StateRoot: "/p/state"}
	var b strings.Builder
	for _, c := range regressionCases() {
		fmt.Fprintf(&b, "== %s\n", c.name)
		proj, err := c.a.ProviderProjection(ctx, ProjectionOptions{Version: "v"})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		fmt.Fprintf(&b, "provider=%s mode=%s\n", proj.Provider, proj.Mode)
		for _, f := range proj.Files {
			sum := sha256.Sum256(f.Content)
			fmt.Fprintf(&b, "file %s mode=%04o role=%s sha=%s\n", f.RelPath, f.Mode, f.Role, hex.EncodeToString(sum[:6]))
		}
		launch, _ := json.Marshal(proj.Launch)
		fmt.Fprintf(&b, "launch %s\n", launch)
		effects, _ := json.Marshal(proj.Effects)
		fmt.Fprintf(&b, "effects %s\n", effects)
		bind, err := proj.ResolveLaunch(roots, "PROMPT")
		if err != nil {
			t.Fatalf("%s: resolve: %v", c.name, err)
		}
		bj, _ := json.Marshal(bind)
		fmt.Fprintf(&b, "binding %s\n", bj)

		spec := c.a.BootDirSpec()
		for _, f := range spec.PlantedFiles {
			fmt.Fprintf(&b, "legacy-file %s mode=%04o\n", f.RelPath, f.Mode)
		}
		fmt.Fprintf(&b, "legacy-env %q cwd=%d project-arg=%q\n", spec.EnvAmendments, spec.CwdPreference, spec.ProjectDirArg)
	}
	return b.String()
}

// TestLayoutRegression_NonSkillProjectionUnchanged pins the non-skill file set,
// launch convention, resolved binding and legacy BootDirSpec of the three
// built-in adapters against the golden recorded before they were derived from
// package layout.
func TestLayoutRegression_NonSkillProjectionUnchanged(t *testing.T) {
	got := renderLayoutRegression(t)
	path := filepath.Join("testdata", "layout-regression", "projection.golden")
	if *updateLayoutGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("projection drifted from the pre-layout golden; diff with:\n  go test ./provider -run TestLayoutRegression -update-layout-golden && git diff %s", path)
		gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
			if gotLines[i] != wantLines[i] {
				t.Fatalf("first difference at line %d:\n got: %s\nwant: %s", i+1, gotLines[i], wantLines[i])
			}
		}
	}
}
