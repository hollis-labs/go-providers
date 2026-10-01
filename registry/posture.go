package registry

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	permission "github.com/hollis-labs/go-permission"
)

// ErrNoPostureMapping is returned by a Posture hook, and by
// [Descriptor.PostureFor], when a runtime has no launch mapping for a
// posture in a mode: an ACP mode, or a runtime whose descriptor has no
// Posture hook.
var ErrNoPostureMapping = errors.New("registry: no permission posture mapping")

// ErrInvalidPosture is returned for a Mode that is not one of go-permission's
// four.
var ErrInvalidPosture = errors.New("registry: invalid permission posture")

// PostureLaunch is what a runtime's launch carries to run under a posture.
type PostureLaunch struct {
	// Args are launch flags. They go at the launch convention's extra-
	// argument slot (provider.ArgExtra), which every convention places
	// before its "--" and before any variadic flag: after "--" they would be
	// prompt text (CW-20260930-0138, CW-20261001-0102).
	Args []string `json:"args,omitempty"`
	// Env are environment variables for the runtime's process, set over
	// whatever the launch already sets.
	Env map[string]string `json:"env,omitempty"`
}

// IsZero reports whether p carries nothing: the runtime runs with its own
// default posture.
func (p PostureLaunch) IsZero() bool { return len(p.Args) == 0 && len(p.Env) == 0 }

func (p PostureLaunch) clone() PostureLaunch {
	return PostureLaunch{Args: slices.Clone(p.Args), Env: maps.Clone(p.Env)}
}

// PostureFunc returns what a runtime's launch carries, in mode, to run under
// posture. posture is one of go-permission's four Modes; [Descriptor.PostureFor]
// screens out an empty or invalid one before the hook is called. A mode the
// runtime has no launch mapping for (an ACP mode) is ErrNoPostureMapping.
type PostureFunc func(posture permission.Mode, mode runtimes.Mode) (PostureLaunch, error)

// PostureFor returns what d's launch in mode carries to run under posture.
// An empty posture is the zero PostureLaunch: the runtime keeps its own
// default. A posture that is not one of go-permission's four Modes is
// ErrInvalidPosture, a mode d does not support is an error, and a runtime or
// mode with no mapping is ErrNoPostureMapping.
func (d Descriptor) PostureFor(posture permission.Mode, mode runtimes.Mode) (PostureLaunch, error) {
	if posture == "" {
		return PostureLaunch{}, nil
	}
	if !validPosture(posture) {
		return PostureLaunch{}, fmt.Errorf("%w %q (want one of: %s, %s, %s, %s)", ErrInvalidPosture, posture,
			permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeYolo)
	}
	if !d.Supports(mode) {
		return PostureLaunch{}, fmt.Errorf("registry: %s does not support mode %q", d.ID, mode)
	}
	if d.Posture == nil {
		return PostureLaunch{}, fmt.Errorf("%w for %s", ErrNoPostureMapping, d.ID)
	}
	p, err := d.Posture(posture, mode)
	if err != nil {
		return PostureLaunch{}, err
	}
	return p.clone(), nil
}

func validPosture(m permission.Mode) bool {
	switch m {
	case permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeYolo:
		return true
	}
	return false
}

func noACPMapping(id runtimes.ID, mode runtimes.Mode) error {
	return fmt.Errorf("%w for %s in %s: an ACP agent's permission requests are answered by the ACP client, best effort", ErrNoPostureMapping, id, mode)
}

// claudePosture is --permission-mode in every native mode (print, streaming,
// TUI). go-permission's yolo is Claude's bypassPermissions.
//
//	default       --permission-mode default            write and shell denied headless
//	accept-edits  --permission-mode acceptEdits        write allowed; `touch` allowed too,
//	                                                   Claude counts filesystem commands as edits
//	plan          --permission-mode plan               nothing executed; Claude writes its
//	                                                   plan under ~/.claude/plans
//	yolo          --permission-mode bypassPermissions  everything allowed
func claudePosture(posture permission.Mode, mode runtimes.Mode) (PostureLaunch, error) {
	if mode.ACP() {
		return PostureLaunch{}, noACPMapping(runtimes.Claude, mode)
	}
	value := map[permission.Mode]string{
		permission.ModeDefault:     "default",
		permission.ModeAcceptEdits: "acceptEdits",
		permission.ModePlan:        "plan",
		permission.ModeYolo:        "bypassPermissions",
	}[posture]
	return PostureLaunch{Args: []string{"--permission-mode", value}}, nil
}

// codexPosture is a pair of config overrides, the same in exec and
// app-server (app-server has no sandbox or approval flags of its own). They
// override the boot dir's planted config.toml for this launch.
//
//	              sandbox_mode        approval_policy
//	default       read-only           on-request
//	accept-edits  workspace-write     on-request
//	plan          read-only           never
//	yolo          danger-full-access  never
//
// Measured: under default, exec refused the write and app-server asked for a
// file-change approval (declined, nothing written); under accept-edits the
// write and `touch` ran inside the workspace with no approval asked; under
// plan nothing was written and nothing asked; under yolo both ran.
// app-server's approvals line up with agentkit's CodexApprovalResponder for
// the same Mode. codex-cli 0.159.2 no longer accepts approval_policy
// "untrusted", so Codex has no commands-ask, edits-allowed policy:
// accept-edits lets sandboxed commands in the workspace run.
func codexPosture(posture permission.Mode, mode runtimes.Mode) (PostureLaunch, error) {
	if mode.ACP() {
		return PostureLaunch{}, noACPMapping(runtimes.Codex, mode)
	}
	policy := map[permission.Mode][2]string{
		permission.ModeDefault:     {"read-only", "on-request"},
		permission.ModeAcceptEdits: {"workspace-write", "on-request"},
		permission.ModePlan:        {"read-only", "never"},
		permission.ModeYolo:        {"danger-full-access", "never"},
	}[posture]
	return PostureLaunch{Args: []string{
		"-c", fmt.Sprintf("sandbox_mode=%q", policy[0]),
		"-c", fmt.Sprintf("approval_policy=%q", policy[1]),
	}}, nil
}

// OpenCodePermissionEnv is the environment variable OpenCode reads a
// permission config from, as JSON, over its config files.
const OpenCodePermissionEnv = "OPENCODE_PERMISSION"

// opencodePosture is OPENCODE_PERMISSION, in run and serve alike. OpenCode
// has no posture flag except run's --auto, which leaves an explicit deny in
// place, so yolo allows each permission by name instead.
//
//	default       {"edit":"ask","bash":"ask"}     edit and shell auto-rejected under run
//	accept-edits  {"edit":"allow","bash":"ask"}   edit allowed; shell auto-rejected
//	plan          {"edit":"deny","bash":"ask"}    no edit tool; shell auto-rejected
//	                                              (OpenCode's own plan agent)
//	yolo          every permission "allow"        edit and shell allowed
//
// plan asks for bash rather than denying it, as OpenCode's own plan agent
// does: a person attached to a serve session can still approve a read-only
// command. A wildcard {"*":"allow"} is not used: a run under it hung without
// output.
func opencodePosture(posture permission.Mode, mode runtimes.Mode) (PostureLaunch, error) {
	if mode.ACP() {
		return PostureLaunch{}, noACPMapping(runtimes.OpenCode, mode)
	}
	config := map[permission.Mode]string{
		permission.ModeDefault:     `{"edit":"ask","bash":"ask"}`,
		permission.ModeAcceptEdits: `{"edit":"allow","bash":"ask"}`,
		permission.ModePlan:        `{"edit":"deny","bash":"ask"}`,
		permission.ModeYolo:        `{"edit":"allow","bash":"allow","webfetch":"allow","external_directory":"allow","doom_loop":"allow"}`,
	}[posture]
	return PostureLaunch{Env: map[string]string{OpenCodePermissionEnv: config}}, nil
}

// antigravityPosture is agy's --mode, or --dangerously-skip-permissions for
// yolo; default passes nothing, since agy's own default is its
// request-review mode, which auto-denies anything that needs approval.
//
//	default       (none)
//	accept-edits  --mode accept-edits                file edits approved, commands denied
//	plan          --mode plan
//	yolo          --dangerously-skip-permissions     everything approved
//
// agy 1.2.14 accepts --mode accept-edits and --mode plan and warns on any
// other value ("valid: accept-edits, plan"). The behaviour column is
// AntigravityAdapter.Permission's, measured when that adapter was written;
// agy was not signed in on the host this mapping was measured on.
func antigravityPosture(posture permission.Mode, _ runtimes.Mode) (PostureLaunch, error) {
	switch posture {
	case permission.ModeAcceptEdits:
		return PostureLaunch{Args: []string{"--mode", "accept-edits"}}, nil
	case permission.ModePlan:
		return PostureLaunch{Args: []string{"--mode", "plan"}}, nil
	case permission.ModeYolo:
		return PostureLaunch{Args: []string{"--dangerously-skip-permissions"}}, nil
	}
	return PostureLaunch{}, nil
}
