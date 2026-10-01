package registry_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	permission "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/go-providers/registry"
)

// The mappings measured live on 2026-10-01 (see the package doc). A change
// here is a change in what each runtime is allowed to do.
func TestPostureFor(t *testing.T) {
	codex := func(sandbox, approval string) registry.PostureLaunch {
		return registry.PostureLaunch{Args: []string{"-c", `sandbox_mode="` + sandbox + `"`, "-c", `approval_policy="` + approval + `"`}}
	}
	opencode := func(config string) registry.PostureLaunch {
		return registry.PostureLaunch{Env: map[string]string{"OPENCODE_PERMISSION": config}}
	}
	args := func(a ...string) registry.PostureLaunch { return registry.PostureLaunch{Args: a} }
	for _, c := range []struct {
		id      runtimes.ID
		modes   []runtimes.Mode
		posture permission.Mode
		want    registry.PostureLaunch
	}{
		{runtimes.Claude, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeStreamingStdio, runtimes.ModePTY}, permission.ModeDefault, args("--permission-mode", "default")},
		{runtimes.Claude, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeStreamingStdio, runtimes.ModePTY}, permission.ModeAcceptEdits, args("--permission-mode", "acceptEdits")},
		{runtimes.Claude, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeStreamingStdio, runtimes.ModePTY}, permission.ModePlan, args("--permission-mode", "plan")},
		{runtimes.Claude, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeStreamingStdio, runtimes.ModePTY}, permission.ModeYolo, args("--permission-mode", "bypassPermissions")},

		{runtimes.Codex, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeJSONRPCStdio}, permission.ModeDefault, codex("read-only", "on-request")},
		{runtimes.Codex, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeJSONRPCStdio}, permission.ModeAcceptEdits, codex("workspace-write", "on-request")},
		{runtimes.Codex, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeJSONRPCStdio}, permission.ModePlan, codex("read-only", "never")},
		{runtimes.Codex, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeJSONRPCStdio}, permission.ModeYolo, codex("danger-full-access", "never")},

		{runtimes.OpenCode, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeHTTPSSE}, permission.ModeDefault, opencode(`{"edit":"ask","bash":"ask"}`)},
		{runtimes.OpenCode, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeHTTPSSE}, permission.ModeAcceptEdits, opencode(`{"edit":"allow","bash":"ask"}`)},
		{runtimes.OpenCode, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeHTTPSSE}, permission.ModePlan, opencode(`{"edit":"deny","bash":"ask"}`)},
		{runtimes.OpenCode, []runtimes.Mode{runtimes.ModeSubprocessPerTurn, runtimes.ModeHTTPSSE}, permission.ModeYolo, opencode(`{"edit":"allow","bash":"allow","webfetch":"allow","external_directory":"allow","doom_loop":"allow"}`)},

		{runtimes.Antigravity, []runtimes.Mode{runtimes.ModeSubprocessPerTurn}, permission.ModeDefault, registry.PostureLaunch{}},
		{runtimes.Antigravity, []runtimes.Mode{runtimes.ModeSubprocessPerTurn}, permission.ModeAcceptEdits, args("--mode", "accept-edits")},
		{runtimes.Antigravity, []runtimes.Mode{runtimes.ModeSubprocessPerTurn}, permission.ModePlan, args("--mode", "plan")},
		{runtimes.Antigravity, []runtimes.Mode{runtimes.ModeSubprocessPerTurn}, permission.ModeYolo, args("--dangerously-skip-permissions")},
	} {
		d := mustLookup(t, string(c.id))
		for _, mode := range c.modes {
			got, err := d.PostureFor(c.posture, mode)
			if err != nil {
				t.Errorf("%s/%s %s: %v", c.id, mode, c.posture, err)
				continue
			}
			if !slices.Equal(got.Args, c.want.Args) || !maps.Equal(got.Env, c.want.Env) {
				t.Errorf("%s/%s %s = %+v, want %+v", c.id, mode, c.posture, got, c.want)
			}
		}
	}
}

func TestPostureFor_EmptyIsTheRuntimeDefault(t *testing.T) {
	for _, d := range registry.All() {
		for _, m := range d.Modes {
			if got, err := d.PostureFor("", m.Mode); err != nil || !got.IsZero() {
				t.Errorf("%s/%s: PostureFor(\"\") = %+v, %v; want nothing", d.ID, m.Mode, got, err)
			}
		}
	}
}

func TestPostureFor_Refusals(t *testing.T) {
	claude := mustLookup(t, "claude")
	for _, bad := range []permission.Mode{"bypass", "acceptEdits", "bypassPermissions", "read-only", "ask"} {
		if _, err := claude.PostureFor(bad, runtimes.ModeStreamingStdio); !errors.Is(err, registry.ErrInvalidPosture) {
			t.Errorf("PostureFor(%q) = %v, want ErrInvalidPosture", bad, err)
		}
	}
	// ACP has no launch flag for a posture; its permission requests are the
	// ACP client's to answer.
	for _, id := range []string{"claude", "codex", "opencode", "copilot", "pi"} {
		d := mustLookup(t, id)
		if _, err := d.PostureFor(permission.ModePlan, runtimes.ModeACPStdio); !errors.Is(err, registry.ErrNoPostureMapping) {
			t.Errorf("%s over ACP: %v, want ErrNoPostureMapping", id, err)
		}
	}
	if _, err := mustLookup(t, "agy").PostureFor(permission.ModePlan, runtimes.ModeJSONRPCStdio); err == nil {
		t.Error("a mode agy does not support was accepted")
	}
}

func TestPostureFor_ReturnsACopy(t *testing.T) {
	d := mustLookup(t, "opencode")
	p, _ := d.PostureFor(permission.ModePlan, runtimes.ModeSubprocessPerTurn)
	p.Env["OPENCODE_PERMISSION"] = "mutated"
	again, _ := d.PostureFor(permission.ModePlan, runtimes.ModeSubprocessPerTurn)
	if again.Env["OPENCODE_PERMISSION"] == "mutated" {
		t.Error("PostureFor returned shared state")
	}
}
