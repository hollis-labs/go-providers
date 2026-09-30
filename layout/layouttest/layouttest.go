package layouttest

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/hollis-labs/go-providers/layout"
)

// T is the part of *testing.T (and *testing.B) the assertions use.
type T interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertSkillPlacement fails unless the table places skills for (p, m) under
// root at rel (a directory holding <name>/SKILL.md). rel is compared after
// path.Clean, so "skills/" and "./skills" equal "skills". Only the primary
// skills row counts; an alias row (for example OpenCode's .opencode/skills,
// valid only when cwd is the boot root) does not satisfy the assertion.
func AssertSkillPlacement(t T, p layout.Provider, m layout.Mode, root layout.Root, rel string) {
	t.Helper()
	e, ok := layout.SkillRoot(p, m)
	if !ok {
		t.Errorf("layout: no skills row for %s/%s", p, m)
		return
	}
	if e.Root != root || e.Rel != path.Clean(rel) {
		t.Errorf("layout: skills for %s/%s belong under %s at %q, got %s at %q", p, m, e.Root, e.Rel, root, path.Clean(rel))
	}
}

// AssertProjectDirFlag fails unless argv carries the flag the table names for
// granting (p, m) access to the project directory, or, when the table has no
// such flag for the mode, unless argv carries none of the provider's
// project-dir flags.
func AssertProjectDirFlag(t T, p layout.Provider, m layout.Mode, argv []string) {
	t.Helper()
	has := func(flag string) bool {
		for _, a := range argv {
			if a == flag || strings.HasPrefix(a, flag+"=") {
				return true
			}
		}
		return false
	}
	if e, ok := layout.Find(p, m, layout.ProjectDir); ok {
		if !has(e.Flag) {
			t.Errorf("layout: argv for %s/%s lacks %s (project-dir flag): %q", p, m, e.Flag, argv)
		}
		return
	}
	for _, e := range layout.Table() {
		if e.Provider == p && e.Concern == layout.ProjectDir && has(e.Flag) {
			t.Errorf("layout: %s/%s has no project-dir flag but argv passes %s: %q", p, m, e.Flag, argv)
		}
	}
}

// AssertEnv fails unless env equals the environment the table requires for
// (p, m). env maps a variable name to the Root name its value must be (for
// example {"CODEX_HOME": "boot"}); pass nil for a provider that needs none.
func AssertEnv(t T, p layout.Provider, m layout.Mode, env map[string]string) {
	t.Helper()
	want := map[string]string{}
	for _, e := range layout.For(p, m) {
		for k, v := range e.Env {
			want[k] = v
		}
	}
	if render(want) != render(env) {
		t.Errorf("layout: env for %s/%s is %s, got %s", p, m, render(want), render(env))
	}
}

func render(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=<%s>", k, env[k])
	}
	return "{" + strings.Join(parts, " ") + "}"
}
