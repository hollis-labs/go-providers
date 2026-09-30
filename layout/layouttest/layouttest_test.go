package layouttest_test

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/go-providers/layout"
	"github.com/hollis-labs/go-providers/layout/layouttest"
)

type recorder struct{ failures []string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func failed(f func(t layouttest.T)) bool {
	r := &recorder{}
	f(r)
	return len(r.failures) > 0
}

func TestAssertSkillPlacement(t *testing.T) {
	layouttest.AssertSkillPlacement(t, layout.Codex, layout.ModeCodexExec, layout.RootBoot, "skills/")
	layouttest.AssertSkillPlacement(t, layout.Claude, layout.ModeClaudeBare, layout.RootBoot, "./.claude/skills")
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, layout.Codex, layout.ModeCodexExec, layout.RootBoot, ".agents/skills")
	}) {
		t.Error("the pre-layout codex prefix must fail")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, layout.OpenCode, layout.ModeOpenCodeRun, layout.RootBoot, ".opencode/skills")
	}) {
		t.Error("the opencode alias must not satisfy the primary assertion")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, layout.Codex, layout.ModeCodexExec, layout.RootProject, "skills")
	}) {
		t.Error("wrong root must fail")
	}
	if !failed(func(x layouttest.T) { layouttest.AssertSkillPlacement(x, "nope", "", layout.RootBoot, "skills") }) {
		t.Error("unknown provider must fail")
	}
}

func TestAssertProjectDirFlag(t *testing.T) {
	layouttest.AssertProjectDirFlag(t, layout.Claude, layout.ModeClaudeBare, []string{"-p", "--add-dir", "/p"})
	layouttest.AssertProjectDirFlag(t, layout.Codex, layout.ModeCodexExec, []string{"exec", "--cd", "/p"})
	layouttest.AssertProjectDirFlag(t, layout.OpenCode, layout.ModeOpenCodeRun, []string{"run", "--dir=/p"})
	layouttest.AssertProjectDirFlag(t, layout.Codex, layout.ModeCodexAppServer, []string{"app-server"})
	if !failed(func(x layouttest.T) {
		layouttest.AssertProjectDirFlag(x, layout.Codex, layout.ModeCodexExec, []string{"exec", "--add-dir", "/p"})
	}) {
		t.Error("codex --add-dir is not the codex project flag")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertProjectDirFlag(x, layout.Codex, layout.ModeCodexAppServer, []string{"app-server", "--cd", "/p"})
	}) {
		t.Error("app-server rejects --cd")
	}
}

func TestAssertEnv(t *testing.T) {
	layouttest.AssertEnv(t, layout.Codex, layout.ModeCodexExec, map[string]string{"CODEX_HOME": "boot"})
	layouttest.AssertEnv(t, layout.OpenCode, layout.ModeOpenCodeRun, map[string]string{"OPENCODE_CONFIG_DIR": "boot"})
	layouttest.AssertEnv(t, layout.Claude, layout.ModeClaudePrint, nil)
	if !failed(func(x layouttest.T) { layouttest.AssertEnv(x, layout.Codex, layout.ModeCodexExec, nil) }) {
		t.Error("missing CODEX_HOME must fail")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertEnv(x, layout.Claude, layout.ModeClaudePrint, map[string]string{"HOME": "boot"})
	}) {
		t.Error("an env var the table does not require must fail")
	}
}
