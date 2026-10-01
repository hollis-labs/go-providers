package layouttest_test

import (
	"fmt"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
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
	layouttest.AssertSkillPlacement(t, runtimes.Codex, perTurn, layout.RootBoot, "skills/")
	layouttest.AssertSkillPlacement(t, runtimes.Claude, bare, layout.RootBoot, "./.claude/skills")
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, runtimes.Codex, perTurn, layout.RootBoot, ".agents/skills")
	}) {
		t.Error("the pre-layout codex prefix must fail")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, runtimes.OpenCode, perTurn, layout.RootBoot, ".opencode/skills")
	}) {
		t.Error("the opencode alias must not satisfy the primary assertion")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, runtimes.Codex, perTurn, layout.RootProject, "skills")
	}) {
		t.Error("wrong root must fail")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertSkillPlacement(x, "nope", layout.Shape{}, layout.RootBoot, "skills")
	}) {
		t.Error("unknown provider must fail")
	}
}

func TestAssertProjectDirFlag(t *testing.T) {
	layouttest.AssertProjectDirFlag(t, runtimes.Claude, bare, []string{"-p", "--add-dir", "/p"})
	layouttest.AssertProjectDirFlag(t, runtimes.Codex, perTurn, []string{"exec", "--cd", "/p"})
	layouttest.AssertProjectDirFlag(t, runtimes.OpenCode, perTurn, []string{"run", "--dir=/p"})
	layouttest.AssertProjectDirFlag(t, runtimes.Codex, jsonRPC, []string{"app-server"})
	if !failed(func(x layouttest.T) {
		layouttest.AssertProjectDirFlag(x, runtimes.Codex, perTurn, []string{"exec", "--add-dir", "/p"})
	}) {
		t.Error("codex --add-dir is not the codex project flag")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertProjectDirFlag(x, runtimes.Codex, jsonRPC, []string{"app-server", "--cd", "/p"})
	}) {
		t.Error("app-server rejects --cd")
	}
}

func TestAssertEnv(t *testing.T) {
	layouttest.AssertEnv(t, runtimes.Codex, perTurn, map[string]string{"CODEX_HOME": "boot"})
	layouttest.AssertEnv(t, runtimes.OpenCode, perTurn, map[string]string{"OPENCODE_CONFIG_DIR": "boot"})
	layouttest.AssertEnv(t, runtimes.Claude, perTurn, nil)
	if !failed(func(x layouttest.T) { layouttest.AssertEnv(x, runtimes.Codex, perTurn, nil) }) {
		t.Error("missing CODEX_HOME must fail")
	}
	if !failed(func(x layouttest.T) {
		layouttest.AssertEnv(x, runtimes.Claude, perTurn, map[string]string{"HOME": "boot"})
	}) {
		t.Error("an env var the table does not require must fail")
	}
}
