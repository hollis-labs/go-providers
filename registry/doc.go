// Package registry is the one list of agent CLI runtimes: a [Descriptor] per
// runtime carrying everything the libraries and apps need to know about it —
// its id and aliases, binary and how to find it, the modes it can be driven in
// and what each mode can do, the permission-posture hook, its boot-dir layout,
// and what go-providers' projection does for it ([ProjectionFacts]).
//
// The vocabulary (runtime ids, modes, capabilities) is agent-contracts-leaf's
// runtimes package (D-73); this package holds the facts. The layout is not
// copied here: [Descriptor.Layout] reads package layout's table, so there is
// one list of where each runtime reads its files.
//
// # The set is closed
//
// There is no out-of-tree registration. Every descriptor is compiled into this
// package and registered by its init through an unexported function; apps look
// runtimes up ([Lookup]) and enumerate them ([All]) but cannot add one. A new
// runtime is a new runtimes.ID in agent-contracts-leaf and a new descriptor
// here. The only other way in is [RegisterForTest], which needs a test's
// cleanup hook and removes what it added when the test ends, so shared fake
// CLIs can stand in as a runtime.
//
// Copilot and Pi are ACP-only: they have no native mode, no layout rows and no
// boot dir. A runtime with a native mode always has layout rows, and the
// reverse; registration refuses a descriptor that breaks either.
//
// # Modes and capabilities
//
// A descriptor lists the modes its runtime can be driven in ([ModeSupport]),
// each with the capabilities the code driving that mode implements; a
// capability that was not measured is not declared. [Descriptor.Supports],
// [Descriptor.Capabilities] and [Descriptor.Has] query them,
// [Descriptor.NativeModes] lists the modes that are not ACP (provider.NewAdapter
// has an adapter for each built-in one), and DefaultMode is the mode a launch
// uses when the caller names none. [Descriptor.LookPath] finds the binary:
// the EnvOverride variable, then PATH, then common install directories and
// the descriptor's LookupDirs.
//
// # Permission posture
//
// A permission posture is go-permission's Mode (D-72): default, accept-edits,
// plan or yolo. Apps pass only the Mode; each runtime's descriptor maps it
// onto that runtime's own launch flags or environment through its Posture
// hook; [Descriptor.PostureFor] is how a launch asks for it. Each mapping is
// documented on its hook in posture.go. They were measured live on 2026-10-01
// against claude 2.1.286, codex-cli 0.159.2 and opencode 1.18.33; agy 1.2.14
// was not signed in on the measuring host, so only its flag spelling was
// measured there. Copilot and Pi have no Posture hook.
//
// How each runtime behaves headless, when an action needs an approval and
// nobody is there to give it:
//
//   - Claude (-p): the action is denied and listed in the result's
//     permission_denials; the turn carries on. In default mode a write and a
//     shell command were both denied.
//   - Codex exec: the sandbox refuses it ("patch rejected: writing is blocked
//     by read-only sandbox; rejected by user approval settings"); the turn
//     carries on.
//   - Codex app-server: Codex sends the host an approval request
//     (item/fileChange/requestApproval, item/commandExecution/requestApproval)
//     and waits for the answer. agentkit's turn.CodexApprovalResponder
//     answers it from the same Mode, so the posture is applied twice, by the
//     sandbox and by the responder, consistently.
//   - OpenCode run: the request is auto-rejected ("permission requested: ...;
//     auto-rejecting") and the turn carries on. Under serve, the HTTP client
//     answers it; `opencode run --attach` auto-rejects the same way.
//   - agy: requests are auto-denied in its default request-review mode
//     (AntigravityAdapter.Permission, measured when that adapter was written).
//   - ACP: there is no launch flag to set. The agent may ask through
//     session/request_permission, and the ACP client's permission responder
//     answers it; whether the agent asks at all is the agent's choice, so the
//     posture is best effort. A Posture hook returns ErrNoPostureMapping for
//     an ACP mode.
package registry
