# Changelog

## v0.41.0 — 2026-10-01

Codex exec resumes a thread (CW-20261001-0109).

### Added

- **`CodexAdapter` exec mode resumes.** A turn given a thread id (the
  `cliSessionID` to `BuildArgs`, or `TurnInput.ResumeID` on the prepared
  path) runs
  `exec [-c model="m"] [extra] --json --skip-git-repo-check [--cd project] resume <id> -- <prompt>`.
  It is one resume slot in the existing convention, not a separate
  convention. Every exec option stays in front of the `resume`
  subcommand, where codex applies it to the resumed turn.
- **`thread.started`** now reports its thread id as the session id:
  `llmtypes.EventSessionID` from `ParseLine`, `events.SessionID` from
  `ParseLineEvents`. That is the id a later turn resumes.
- **`CodexAdapter.IsSessionLost`** matches `no rollout found for thread id`,
  which `exec … resume <unknown id>` writes to stderr before exiting 1
  with no stdout.
- **Registry:** codex subprocess-per-turn declares `resume` and
  `session-lost-classifier`. App-server still declares no classifier: its
  loss is a JSON-RPC error that agentkit classifies.

### Measured

Live on codex-cli 0.159.2. The process cwd was the boot dir in every run,
as in a real launch.

- Turn 1 with `--cd <project>`: the shell ran in the project.
- `exec resume <id>` with no `--cd`: the same thread id, but the shell ran
  in the boot dir. A resumed thread takes the process cwd, not the cwd it
  started in. Dropping `--cd` on resume turns would have moved the agent's
  work out of the project.
- `exec --cd <project> resume <id>`, and the full form above: the same
  thread id, and the shell ran in the project.
- A `-c sandbox_mode="workspace-write"` override before `resume` let a
  `touch` succeed that the default read-only sandbox refused. So the
  registry posture's `-c` overrides reach the resumed turn.
- An unknown id in the same form, with the prompt
  `--dangerously-bypass-approvals-and-sandbox` after `--`: exit 1, the
  stderr above. The prompt was read as text.

### Changed

- **Fixtures:** `codex/exec_turn2_resume` and `codex/exec_resume_unknown_id`
  were re-recorded in the adapter's argv form. Before, they used
  `exec resume <id> <prompt> --json …`.
- **Consumers that pass a session id to codex exec now resume.** Before,
  exec ignored it. They also now receive a session id from codex exec turns.
- **Caveat:** `codex exec resume --help` lists no `--cd`, `-s` or
  `--add-dir` after the subcommand. `--cd` and `-s` before it were
  accepted, and that is where `ExtraArgs` go.

## v0.40.1 — 2026-10-01

`CodexAdapter` refuses approval policy "untrusted" (CW-20261001-0127).

### Fixed

- **`CodexAdapter.ApprovalPolicy` "untrusted"** now fails the config.toml
  render, with an error that says codex-cli 0.159.2 no longer supports it
  and names "on-request" with SandboxMode "read-only" as the closest
  replacement. Before, it planted a config that codex then refused to load.
  Measured on codex-cli 0.159.2 against an empty CODEX_HOME:
  - "untrusted" in config.toml, or as `-c approval_policy="untrusted"`,
    fails with "failed to load configuration".
  - "on-failure", "on-request" and "never" load.

  The vocabulary error no longer lists "untrusted". The app-server's
  per-thread `thread/start` `approvalPolicy` is a separate surface: it
  still accepted "untrusted" on 0.159.2 (fixture
  `codex/app_server_tool_approval`), and this change does not touch it.

## v0.40.0 — 2026-10-01

Turn interrupts for Codex app-server and OpenCode serve (CW-20261001-0160).

### Added

- **`provider.RPCTurnInterrupter`**, an optional CLIAdapter extension for a
  JSON-RPC runtime that can interrupt the turn in flight and keep its
  process. `TurnNotification(method, params)` follows turns through the
  runtime's notifications, and `InterruptCall(handle)` is the request that
  interrupts one. `CodexAdapter` implements it: `turn/started` and
  `turn/completed` carry `{threadId, turn: {id}}`, and the interrupt is
  `turn/interrupt {threadId, turnId}`. Measured on codex-cli 0.159.2:
  - Codex answers `{}` and completes the turn with `status: "interrupted"`.
  - The thread and the process stay up, and the next `turn/start` runs
    normally.
  - With no turn running, the interrupt is a JSON-RPC error: -32600 "no
    active turn to interrupt".
- **Live fixtures:**
  - `codex/app_server_interrupt`.
  - `opencode/serve_abort`, in a new `.http.jsonl` format: requests,
    responses and server-sent events in arrival order. OpenCode's
    `POST /session/{id}/abort` answers `true`. The turn ends with
    `session.error` `MessageAbortedError` and `session.idle`; the aborted
    tool's cleanup then sends a second `session.idle`. The next prompt runs on
    the same session.
- **`hack/capturefixtures`:**
  - It records `opencode serve` (`-runtimes opencode`, `serve_abort` only).
    The run fixtures stay hand-captured.
  - The codex recipes honour `-only`.
  - `-only` starts a manifest when the runtime has none.
  - The scrubber also replaces paths reported without their leading slash
    (opencode reports the project that way), and replaces OpenCode's `ses_`,
    `prt_` and `evt_` ids with `…_fixtureNNNN` placeholders. This is the
    policy v0.39.1's run fixtures follow.

## v0.39.3 — 2026-10-01

### Fixed

- **`PTYBridge.CompleteWithUsage` names the adapter that failed**
  (CW-20260930-0048).
  - A CLI error was always reported as `claude cli error: …`, a leftover
    from when the PTY bridge was Claude-only. `NewPTYBridgeWithAdapter`
    takes any `CLIAdapter`, so a codex or opencode failure was mislabelled.
  - It now reads `<adapter name> cli error: …`: `codex cli error: …`, and
    unchanged for Claude.
  - `TestPTYBridge_CompleteErrorNamesTheAdapter` replays real codex and
    claude error captures through the PTY bridge.

## v0.39.2 — 2026-10-01

### Fixed

- **providertest: a call file appears only once its start record is in it**
  (CW-20261001-0119).
  - The fake created `calls/NNNNNN.jsonl` with `O_CREATE|O_EXCL` and wrote
    the start record (Args, Dir, Env, PID) afterwards. A `Fake.Calls()` that
    ran in between read a call with no Args, so a consumer test that
    asserted argv right after a launch could fail at random.
  - The start record now goes into a temp file (`.call-*.tmp`, which
    `Calls()` ignores), which is then hard-linked to the call name.
    `link(2)` fails if the name exists, so uniqueness is kept as `O_EXCL`
    kept it. Later records append to the same file.
- **Tested:** `TestCallsNeverSeeAStartlessCall` runs 200 concurrent launches
  while 8 goroutines call `Calls()`. On main it saw torn calls in 3 of 10
  runs; with the fix, 0 in 10 runs under `-race`.

## v0.39.1 — 2026-10-01

### Fixed

- **OpenCode errors say what failed** (CW-20261001-0122).
  - The `opencode run` error line used to surface only `data.message`.
    OpenCode 1.18.33 reports a model it cannot resolve as `UnknownError`
    "Unexpected server error. Check server logs for details.", so a bad model
    id looked like a server fault.
  - The surfaced error, on both `StreamEvent.Error` and the typed
    `events.Error.Message`, now keeps `data.message` and appends the error's
    name, the model when the error carries `data.providerID`/`modelID`
    (`ProviderModelNotFoundError`), and `data.ref`, the reference OpenCode
    files the failure under. For example:
    `Unexpected server error. Check server logs for details. (UnknownError, ref err_7707db6c)`.
  - A message equal to the name, and an error with nothing in it, read as
    before.
  - **Behaviour change:** a consumer matching the exact old message text
    sees the appended detail.
- **New fixture:** `providertest/fixtures/opencode/run_error_unknown_model`,
  a capture from opencode 1.18.33.

## v0.39.0 — 2026-10-01

A cooperative turn interrupt for Claude's streaming stdin (CW-20261001-0103).

### Added

- **`provider.TurnInterrupter`**, an optional CLIAdapter extension for a CLI
  whose long-lived stdin protocol can end the turn in flight and keep the
  process. `InterruptRequest(id)` is the stdin frame to write;
  `InterruptResponse(line)` recognises the answer (`ErrInterruptRefused`
  wraps a refusal). `ClaudeAdapter` implements it with stream-json's
  `{"type":"control_request","request_id":…,"request":{"subtype":"interrupt"}}`.
  Measured on claude 2.1.286:
  - Claude answers with a `control_response` (`subtype: success`) whether or
    not a turn is in flight. With none, nothing else follows.
  - A tool that was running is rejected: "[Request interrupted by user for
    tool use]". The turn ends with an `error_during_execution` result whose
    `terminal_reason` is `aborted_tools`, or `aborted_streaming` when the
    model was generating.
  - The process stays up, and the next user frame runs a normal turn.
- **The `claude/stream_interrupt` fixture**: a live capture of exactly that
  sequence.
- **providertest replays Claude's control protocol.** A `control_response`
  answers the live `control_request`'s `request_id`, as a JSON-RPC response
  answers the live id.
- **`hack/capturefixtures -only <stems>`** re-records named fixtures and
  merges them into `captured.json`. It refuses when the CLI version differs
  from the manifest's.

## v0.38.0 — 2026-10-01

### Added

- **`ClaudeAdapter` implements `SessionLostClassifier`** (CW-20261001-0047).
  - `claude --resume <id>` with an id claude no longer has writes "No
    conversation found with session ID: <id>" to stderr and exits 1. Its
    result line carries no reason.
  - `IsSessionLost` recognizes that line, so a session layer can map an
    unknown resume id to `ErrProviderSessionLost`.
  - Claude's streaming-stdio and subprocess-per-turn modes now declare
    `session-lost-classifier`. Both are measured: the replay tests drive
    `claude/print_resume_unknown_id` and `claude/stream_resume_unknown_id`
    through providertest. PTY mode is not measured and does not declare it.

### Decided

- **Codex exec stays single-turn, with no session-lost classifier.**
  - `CodexAdapter` builds no `exec resume` argv, so a classifier for an id
    it never passes would be a claim nothing exercises, and
    `TestDeclaredCapabilitiesMatchAdapters` would require declaring it.
  - `codex exec resume` (0.159.2) accepts neither `--cd` nor `-s`, both of
    which exec turns may carry, so resumable exec would need its own
    convention.
  - Resumable Codex is app-server (D-74), where agentkit classifies a lost
    thread from the JSON-RPC error.
  - The unknown-id capture stays in
    `providertest/fixtures/codex/exec_resume_unknown_id`.

## v0.37.0 — 2026-10-01

Permission posture mapped per runtime (CW-20260930-0138, D-72).

### Added

- **`registry.Descriptor.PostureFor(posture, mode)`** maps go-permission's
  `Mode` (`default`, `accept-edits`, `plan`, `yolo`) onto each runtime's own
  launch flags or environment, returned as a `registry.PostureLaunch{Args,
  Env}`. Apps pass only the Mode. Args belong at the launch convention's
  extra-argument slot, which is before `--` in every convention; a test holds
  each posture's flags there.

  | posture | claude | codex | opencode | agy |
  |---|---|---|---|---|
  | default | `--permission-mode default` | `-c sandbox_mode="read-only" -c approval_policy="on-request"` | `OPENCODE_PERMISSION={"edit":"ask","bash":"ask"}` | none |
  | accept-edits | `--permission-mode acceptEdits` | `workspace-write`, `on-request` | `{"edit":"allow","bash":"ask"}` | `--mode accept-edits` |
  | plan | `--permission-mode plan` | `read-only`, `never` | `{"edit":"deny","bash":"ask"}` | `--mode plan` |
  | yolo | `--permission-mode bypassPermissions` | `danger-full-access`, `never` | every permission `allow` | `--dangerously-skip-permissions` |

  Measured live with cheap runs on claude 2.1.286, codex-cli 0.159.2 (exec
  and app-server) and opencode 1.18.33 (run and serve). agy 1.2.14 was not
  signed in on the measuring host, so only its flag spelling was measured
  there. The registry package doc records what each runtime did headless.
- `registry.ErrNoPostureMapping`: ACP modes, and Copilot and Pi (ACP-only),
  have no launch mapping. An ACP agent's permission requests are answered by
  the ACP client, best effort. `registry.ErrInvalidPosture` for a value that
  is not one of the four Modes.
- Requires go-permission v0.1.0 (its only dependency is yaml.v3).

### Changed

- **`registry.PostureFunc` takes a `permission.Mode` and returns a
  `PostureLaunch`** (was a provisional `string` posture and `[]string`).
  Claude, Codex, OpenCode and Antigravity now have a Posture hook; Copilot and
  Pi do not.

### Notes

- codex-cli 0.159.2 rejects `approval_policy = "untrusted"` ("no longer
  supported"), so Codex's accept-edits lets sandboxed commands in the
  workspace run; there is no longer an edits-only policy to map it to. The
  adapter-level `CodexAdapter.ApprovalPolicy` still accepts "untrusted".
- The adapter posture fields (`ClaudeAdapter.PermissionMode` /
  `SkipPermissions`, `CodexAdapter.ApprovalPolicy` / `SandboxMode`,
  `AntigravityAdapter.Permission`) are unchanged. Where both are set, the
  posture flag wins: claude's `--permission-mode acceptEdits` over a planted
  `permissions.defaultMode: default` was measured, and codex's `-c` overrides
  config.toml by its own definition. Set one, not both.

## v0.36.0 — 2026-10-01

MCP servers reach every runtime's boot dir (CW-20260930-0136, W4b and item 3).

### Changed

- **`PlantContext.MCPServers` is planted for every runtime with a boot-dir MCP
  config**, stdio and http alike. Only codex read it before; claude and
  opencode apps had to render MCP config themselves. Each server is written in
  the CLI's own form into the file its layout row names:

  | runtime | file | stdio | http |
  |---|---|---|---|
  | claude | `.mcp.json` | `{type: stdio, command, args, env}` | `{type: http, url}` |
  | codex | `config.toml` `[mcp_servers.<name>]` | `command`, `args`, `[.env]` | `url` |
  | opencode | `opencode.json` `mcp` | `{type: local, command: [cmd, args...], environment}` | `{type: remote, url, enabled}` |
  | antigravity | `.agents/plugins/tether/mcp_config.json` | `{command, args, env}` | `{serverUrl}` |

  The `.mcp.json` mirrors codex and opencode plant for operators carry the
  same servers in claude's form. Copilot and Pi take MCP servers over ACP, not
  from a boot dir.
- One validation for every renderer (`validateMCPServers`): a bad, reserved
  (`loopback`, `mux`) or duplicate name, or anything other than exactly one
  transport, fails the Render and the `ProviderProjection`, where claude,
  opencode and antigravity used to ignore the field.
- With no `MCPServers`, every planted config is byte-identical to before.

### Security

- **Every file that can carry MCP server env is planted owner-only (0600).**
  OpenCode's `opencode.json` now carries MCP servers and their `environment`,
  but its layout row had no `FileMode`, so it was written with the default
  mode. The row is now 0600. Claude's `.mcp.json` was 0600 in the projection
  but had no mode on the `BootDirSpec` path; it now takes the layout's mode
  there too. `TestMCPBearingFilesAreOwnerOnly` renders with a secret in a
  server's env and requires every planted and projected file containing it
  to be 0600, so a future row cannot regress this. `MCPServerSpec` has no
  headers, so no HTTP auth header is written anywhere.

## v0.35.0 — 2026-10-01

Event vocabulary (CW-20260930-0137; closes the provider half of CW-20260930-0228
§1 and §2 and CW-20260930-0222 L1). Additive fields; one behaviour change in
the stop-reason values.

### Added

- **Block boundaries.** `llmtypes.StreamEvent.BlockID` and the typed
  `events.Delta.BlockID` / `events.Thinking.BlockID` are set wherever the CLI
  says which block a fragment belongs to, so consumers separate consecutive
  blocks without per-provider guessing:
  - Claude: the assistant event's `uuid`. Claude writes one event per block,
    every block at content index 0 under a shared message id, so the uuid is
    what tells them apart; the message id and index stand in without one.
  - codex exec: `item.id`.
  - opencode: the text or reasoning part's `id`.
- **Phase on the legacy surface.** codex exec deltas carry `Phase` (`final`
  for `item.completed`, `narration` for streamed `item.message` deltas), as
  the typed surface already did. opencode reasoning carries `thinking`.
- **Cost.** `Usage.CostUSD` and the new typed `events.Usage.CostUSD` are
  populated as per-event deltas:
  - opencode: each `step_finish`'s own `cost` (per step, so the steps sum to
    the turn).
  - Claude: the per-turn difference of `total_cost_usd`, which is a running
    total for the CLI session, across turns and across `--resume`.
    Baselines live in a process-wide ledger keyed by session id (bounded to
    1024 sessions). A resumed session this process never saw reports zero
    for its first turn rather than charging its history to it.
- `events.AuthFailed{Message}`: the typed event the session layer emits when
  an `AuthFailureClassifier` recognises a sign-in failure.

### Changed

- **Stop reasons are normalised** with `llmtypes.NormalizeStopReason`:
  - opencode `step_finish` reasons: `tool-calls` → `tool_use`, `stop` →
    `end_turn`, `length` → `max_tokens`.
  - codex exec now reports `end_turn` for a completed turn; it reported none.
  - Claude's values were already canonical and are normalised the same way.
  - A consumer that compared opencode's raw `stop` / `tool-calls` must
    compare the normalised values instead.
- Requires `go-llm-types` v0.5.1 (was v0.1.0); v0.5.1 spells the thinking phase `thought`.
## v0.34.1 — 2026-10-01

### Security

- **Untrusted turn text could be parsed as a CLI flag (CW-20261001-0069).**
  - **Bug.** The per-turn conventions passed the turn's prompt as a bare
    positional (`claude -p <prompt> …`, `codex exec <prompt> …`,
    `opencode run … <prompt>`). Turn text is untrusted: another agent's
    message, a wake, a steering notice. So a turn starting with `-` was
    parsed as an option, and a turn equal to a real flag was honoured. For
    example, `--dangerously-bypass-approvals-and-sandbox` (codex),
    `--dangerously-skip-permissions` (claude) or `--auto` (opencode): a
    sandbox/permission escape.
  - **Fix.** `ArgPrompt` now resolves to an end-of-options `--` followed by
    the prompt, and every convention puts it last. `ResolveTurn` refuses a
    convention with anything after the prompt.
  - **Argv changes** (for both `BuildArgs` and the projection's
    `ResolveTurn`):

    | Runtime | New argv |
    |---|---|
    | Claude print and bare | `-p --output-format stream-json --verbose … [--add-dir …] [--dangerously-skip-permissions] -- <prompt>` |
    | codex exec | `exec … --json --skip-git-repo-check [--cd project] -- <prompt>` |
    | opencode run | `run … [extra] -- <prompt>` |

    Claude's system prompt is passed as `--system-prompt=<text>`, so a value
    starting with `-` stays its value.
  - **Unaffected:** agy (inline `-p=<prompt>`), and Claude streaming/PTY,
    codex app-server and opencode serve, whose turns go over stdin or their
    protocol (pinned by tests).
  - **Verified live** on claude 2.1.286, codex-cli 0.159.2 and opencode
    1.18.33. Each accepts `--`, answers, and receives
    `--dangerously-skip-permissions`, `--dangerously-bypass-approvals-and-sandbox`,
    `--model` and `--auto` as prompt text. Claude stayed in `default`
    permission mode and codex stayed read-only.

## v0.34.0 — 2026-10-01

### Added

- `Binary` and `ExtraArgs` on `ClaudeAdapter`, `CodexAdapter`,
  `OpencodeAdapter` and `AntigravityAdapter` (CW-20260930-0134, the
  capability-forwarding half of CW-20260930-0137). `Detect` returns a set
  `Binary` as-is, before the env override and PATH. `BuildArgs` places
  `ExtraArgs` at the convention's extra slot:
  - before the variadic `--add-dir` (Claude, agy);
  - before `--json` (codex exec);
  - before the trailing message (opencode run);
  - last for codex app-server and opencode serve.

  Hosts set these fields instead of wrapping the adapter, which hid its
  optional interfaces (`EventParser`, the classifiers, `Preflighter`,
  `SessionResumeVerifier`, `BootDirProvider`).
- `provider.NewAdapter(id, mode)` and `ErrNoAdapter`: the one constructor
  table for native runtimes. It returns a fresh adapter in the shape the mode
  needs, for example Claude's streaming adapter for `streaming-stdio`, which
  agentkit's `DefaultResolver` got wrong by always building print mode.
  agentkit's `DefaultResolver` and go-agent-wrapper's `launch` factories build
  from it instead of keeping their own per-runtime switches. ACP modes are
  `ErrNoAdapter`. `TestNewAdapterCoversTheRegistry` holds the table to the
  registry's native modes.

### Changed

- Codex's app-server mode (`jsonrpc-stdio`) declares `resume`
  (CW-20261001-0052). `thread/resume` was measured live, in the codex-cli
  0.159.2 capture `providertest/fixtures/codex/app_server_resume`, and
  agentkit's `turn.CodexAppServerSession` implements it (agentkit v0.11.0).
  It still declares no `session-lost-classifier`: agentkit's `turn` package
  classifies a lost thread from the JSON-RPC error, not `CodexAdapter`.

## v0.33.0 — 2026-10-01

One argv owner (CW-20260930-0135, go-providers half). Each runtime's argv is
built in one place, `provider/argv.go`; a `ProviderProjection` resolves it
against launch roots and each adapter's `BuildArgs` resolves the same
convention from the adapter's fields, so the adapter path and the prepared path
produce the same argv for the same launch, turn by turn.

### Added

- `LaunchConvention.ResolveTurn(roots, TurnInput, extra)` and
  `ProviderProjection.ResolveTurn`: resolve any turn, not only the first.
  `TurnInput` carries the prompt, system prompt and resume id; extra arguments
  go at the convention's `ArgExtra` slot. `ResolveLaunch(roots, prompt)` is
  now the first-turn case of it.
- Argument kinds `ArgResume` (resume flag and id, omitted on a new session),
  `ArgPromptInline` (agy's `-p=<prompt>`), `ArgSystemPrompt` and `ArgExtra`,
  and `ArgTemplate.WithSystem` / `FirstTurnOnly`. The projected conventions now
  carry resume (`--resume`, `--session`, `--conversation`), system-prompt and
  extra slots; a first turn without them resolves to the same argv as before
  (`TestLayoutRegression_NonSkillProjectionUnchanged`'s resolved lines are
  unchanged).
- `ClaudeAdapter.Model` (`--model`), `ClaudeAdapter.SkillsDir` (bare
  `--add-dir` for planted skills), `CodexAdapter.Model` (`-c model="…"`, the
  form app-server accepts) and `CodexAdapter.ProjectDir` (exec `--cd`).
- `registry.Descriptor.Projection` (`ProjectionFacts`: tested version, feature
  support, per-mode notes), with `registry.Feature`, `registry.Support` and
  `registry.Features()`. `ProviderCapabilityMatrix` reads it; the separate
  per-runtime table in `provider` is gone.

### Changed

- `LaunchConvention.Executable` comes from the registry descriptor's `Binary`
  instead of a literal per runtime (same values).
- `ClaudeAdapter.BuildArgs`: `ProjectDir` now emits `--add-dir` in every mode,
  as the projection does (it was bare-only; agentkit sets it only for bare, and
  non-bare launches still get `--add-dir` from `ProjectDirArg`). In print mode
  `--system-prompt` now comes before `--add-dir` and
  `--dangerously-skip-permissions` instead of after them.
- The projected agy convention matches `BuildArgs`: the prompt is inline
  (`-p=<prompt>`, was `-p <prompt>`) and it carries the permission, model,
  effort and agent flags. The projected opencode prompt carries the system
  prompt as `BuildArgs` does.

### Removed

- **Breaking:** `provider.ProviderFeature`, `provider.CapabilitySupport` and
  their constants (`provider.FeatureInstructions` … `provider.FeatureTrust`,
  `provider.SupportProjected`/`SupportExplicit`/`SupportUnsupported`). The
  vocabulary now lives with the per-runtime facts in package `registry`: use
  `registry.Feature`, `registry.Support` and their constants (same names and
  string values). `ProjectionOptions.RequiredFeatures` is
  `[]registry.Feature` and `ProjectionDiagnostic.Feature` a `registry.Feature`.

## v0.32.0 — 2026-10-01

### Added

- `providertest`: a fake agent CLI for tests. `providertest.New(t, "claude",
  providertest.Replay("claude/print_turn1"))` returns a binary named after the
  runtime that replays captured wire output: per-turn stdout/stderr/exit, and
  duplex transcripts (claude streaming stdio, codex app-server JSON-RPC, ACP)
  that wait for each client frame and answer JSON-RPC requests under the live
  request's id. Each invocation is recorded (argv, cwd, env, stdin, signals,
  exit code). The fake is the test binary itself, re-entered through a symlink
  and found by argv[0], so it needs no setup beyond the import, works when the
  caller replaces the child environment, avoids ETXTBSY and runs under
  `go test -race` with no real CLI installed. The fake takes its binary name
  and CLI-path variable from the `registry` descriptor; a test stands in a
  fake runtime with `registry.RegisterForTest`.
- Fixtures under `providertest/fixtures`, embedded as `providertest.Fixtures`:
  new captures from Claude Code 2.1.286 (print turn, resume, unknown resume
  id, tool use, tool denied, unknown model; streaming stdio two turns, resume,
  unknown resume id) and codex-cli 0.159.2 (`exec` turn, resume, unknown
  thread, tool use, unknown model; `app-server` turns, resume, unknown thread,
  command approval), scrubbed for publication; synthetic ACP transcripts for
  copilot and pi, marked as such in `fixtures/README.md`.
  `hack/capturefixtures` re-records the claude and codex captures.

### Fixed

- `SubprocessBridge` with `WithEvents` could drop stderr lines from a CLI that
  writes its error and exits at once (claude on an unknown `--resume` id): it
  called `cmd.Wait`, which closes the stderr pipe, before its stderr reader had
  finished. It now drains stderr first, bounded by the wait delay.
- `SubprocessBridge` could deliver a `SubprocessStderr` typed event after the
  turn's terminal `Done`/`Error` when the adapter parsed the terminal from
  stdout, since stderr is read on its own goroutine. Terminal typed events are
  now held until stderr is drained, as the typed-events contract requires.

### Changed

- The opencode and antigravity captures moved from `provider/testdata` to
  `providertest/fixtures`. go-providers' own fake-CLI tests now use
  `providertest` instead of shell scripts, and new replay tests drive each
  adapter's captures through the subprocess bridge.

## v0.31.0 — 2026-10-01

### Added

- Package `registry`: the runtime descriptor registry (CW-20260930-0132), the
  one list of agent CLI runtimes that libraries and apps read in place of
  their own provider lists. A `Descriptor` per runtime — Claude, Codex,
  OpenCode, Copilot, Pi, Antigravity — carries its id and lookup aliases
  (`claude-code`, `agy`, ...), binary, env override (`CLAUDE_CLI_PATH`, ...)
  and extra lookup dirs, its modes with the capabilities each declares, its
  default mode (Codex: `jsonrpc-stdio`, per D-74), a posture hook (nil until
  CW-20260930-0138), and its layout, read from the `layout` table. `Lookup`
  takes an id or alias; `All` enumerates. The set is closed: there is no
  out-of-tree registration, and `RegisterForTest` exists only for test fakes.
  Copilot and Pi are ACP-only, with no layout rows.
- `layout.Shape` and `layout.Variant` (`VariantBare`), and `layout.Runtimes`.
- `ProviderEffectKind.Class` (`EffectClassCredential`, `EffectClassHostConfig`)
  and `ProviderEffectKind.Secret`, so hosts classify and redact projection
  effects from go-providers rather than keeping a per-kind table. An unknown
  kind is a secret credential.

### Changed

- New module dependency: `github.com/hollis-labs/agent-contracts-leaf` v0.3.0
  (standard library only), for the `runtimes` vocabulary.
- **Breaking:** the runtime vocabulary is agent-contracts-leaf `runtimes`
  (v0.3.0, D-73), with no aliases for the old spellings (D-22):
  - `layout.Entry.Provider` is a `runtimes.ID`; `layout.Entry.Mode` is a
    `runtimes.Mode`, and a new `Variant` field carries Claude's `bare`.
  - `layout.For`, `Find` and `SkillRoot` take `(runtimes.ID, layout.Shape)`,
    and the most specific applicable row wins. `layout.Providers` is now
    `layout.Runtimes`, in canonical order.
  - `layouttest` assertions take `(runtimes.ID, layout.Shape)`.
  - `ProviderProjection`, `LaunchConvention`, `ProviderCapabilityRow` and
    `CredentialRequest` carry `Provider runtimes.ID`, `Mode runtimes.Mode` and
    `Variant layout.Variant`. The mode values change: `claude-print`,
    `codex-exec`, `opencode-run` and `antigravity-print` are all
    `subprocess-per-turn`; `claude-bare` is `subprocess-per-turn` with variant
    `bare`; `claude-pty` is `pty`; `claude-streaming-stdio` is
    `streaming-stdio`; `codex-app-server` is `jsonrpc-stdio`;
    `opencode-serve-http` is `http-sse`.
  - `ProviderCapabilityMatrix` derives its rows from the registry (one per
    native mode, plus Claude's bare variant), so an ACP-only runtime has none.
    The row order follows each descriptor's modes: Claude is now
    streaming-stdio, subprocess-per-turn, subprocess-per-turn+bare, pty
    (was print, bare, pty, streaming), and Codex jsonrpc-stdio then
    subprocess-per-turn (was exec, app-server). Look rows up by runtime and
    shape, not by index.
  - Known downstream break: Torque's
    `internal/runtime/agent/codex_auth.go:47` uses `provider.ProviderCodex`;
    it becomes `runtimes.Codex` at Torque's next bump. agentkit's
    `agentlaunch/provider_projection_bridge.go` (CW-20260930-0133) is the
    other consumer of the removed names.
- The adapters' `Detect` resolves through the registry descriptor. The
  `~/.opencode/bin` fallback now applies to OpenCode only, still searched
  before `/usr/local/bin` as it was.
- Claude's projected launch convention passes the layout's project-dir
  argument (`--add-dir <project>`) in every mode, not only under bare, so the
  projection's argv is complete. agentkit no longer has to append it.

### Removed

- **Breaking:** `layout.Mode` and its constants, `layout.Provider` and its
  constants, `provider.ProviderMode` and its constants, and
  `provider.ProviderID` and its constants. Use `runtimes.ID`, `runtimes.Mode`
  and `layout.Shape`.
- `layout.Entry.Aliases`. Its only use was the old `serve-http`/`http-sse`
  spelling of OpenCode's HTTP mode, which is now the mode itself.

## v0.30.0 — 2026-09-30

### Changed

- Docs: the README no longer lists Gemini, Aider, Copilot, Junie, Kiro or Qwen
  adapters (removed in v0.12.0) and now names the four that ship — Claude Code,
  Codex, OpenCode and Antigravity. `docs/CONSUMERS.md` records that agentkit's
  legacy skill paths are fixed (opencode in agentkit v0.7.0, claude in v0.9.0).

### Fixed

- `AntigravityAdapter.IsNotAuthenticated` now matches agy's real login-failure
  output: "Authentication required. Please visit the URL" and
  "not authenticated: no stored credentials found" (plus the existing "Waiting
  for authentication" and "authentication failed or timed out"). It does not
  match "trying silent auth", which agy logs on every healthy run.

### Removed

- **Breaking:** `AntigravityAdapter.Preflight` and the `CredentialsPath` field.
  agy authenticates from the macOS Keychain, not from
  `~/.gemini/oauth_creds.json` (a file that belongs to the retired Gemini
  CLI), so the stat was wrong both ways: it passed whenever gemini-cli was
  logged in and refused healthy agy launches once that file was gone. The
  Keychain service name is not known statically and a probe cannot be shown to
  avoid a Keychain prompt, so the adapter no longer implements `Preflighter`;
  a login failure is reported after the fact through `IsNotAuthenticated` and
  `ErrProviderNotAuthenticated`. Callers that set `CredentialsPath` must drop it.

## v0.29.0 — 2026-09-30

### Added

- `AntigravityAdapter` for the Antigravity CLI (`agy`, verified against 1.2.7):
  one subprocess per turn, `agy --output-format stream-json [--conversation
  <id>] -p=<prompt>`, with `--model`, `--effort`, `--agent`, `--add-dir` and a
  permission posture (`bypass` → `--dangerously-skip-permissions`,
  `accept-edits`/`plan` → `--mode`). `ParseLine` maps init → session id,
  text_delta → delta, each agent step's usage → usage, tool steps → tool use,
  result → done/error; `ParseLineEvents` adds tool results and
  `events.PermissionDenied` for auto-denied approvals. Fixtures in
  `provider/testdata/antigravity`.
- Antigravity layout rows, capability-matrix row, `ProviderProjection` and
  `BootDirSpec`: workspace-only projection into `<boot>/.agents`
  (`plugins/tether/{plugin.json,mcp_config.json}` for MCP, `skills/`) plus
  `AGENTS.md`, cwd = boot, project via `--add-dir`. agy's global
  `~/.gemini/config` is shared with the desktop app and never written.
- Optional adapter extensions and sentinels: `SessionResumeVerifier` (a resume
  that reports a different id lost the requested session; agy replaces an
  unknown conversation silently instead of failing), `Preflighter`,
  `AuthFailureClassifier` and `ErrProviderNotAuthenticated`. Typed events
  `events.SessionLost` and `events.PermissionDenied` (both non-terminal).

## v0.28.0 — 2026-09-30

### Added

- `SessionLostClassifier` (optional `CLIAdapter` extension) and
  `ErrProviderSessionLost`, for CLIs that report an unknown resume id only on
  stderr. `OpencodeAdapter` implements it: `opencode run --session <id>` with
  an id opencode no longer has prints `Session not found`, no JSON, and exits 1.
- `OpencodeAdapter.ParseLineEvents` (`EventParser`), adding a `ToolResult` per
  tool call and `Done.StopReason`.

### Changed — BREAKING (output shapes; no exported Go identifier removed)

- OpenCode run mode is structured. `BuildArgs` emits `run --format json` and
  `--session <id>` when resuming (opencode 1.18.30 resumes the conversation;
  the old "no resume flag" note was wrong). `ParseLine` maps the JSON stream to
  typed events instead of one plain-text delta per line: `step_start` → session
  id, `text` → delta (one whole text block per line), `reasoning` → thinking,
  `tool_use` → tool use, `step_finish` → usage for that step, plus done when its
  reason is not `tool-calls`, `error` → error. Usage is per step: a turn with
  tool calls reports several and consumers sum them. Non-JSON and unknown lines
  yield nothing, and a step that ends in `error` reports usage but not done
  (the error line is the turn's terminal event). **Consumers that treated
  opencode output as plain text now receive only the reply text in deltas,
  and a done event per turn.** A consumer's session layer must not treat
  usage as terminal (agentkit does not from the release that pairs with
  this one); with an older agentkit a mid-turn crash after a step's usage
  goes unreported.
- OpenCode boot dir: `agents/<name>.md` now carries frontmatter
  (`description`, `mode: primary`) and is the whole agent definition.
  `agents.json` is no longer planted or projected (opencode does not read it),
  and `opencode.json` no longer defines the agent (its `{file:}` prompt would
  now include the frontmatter). The projection's run argv gains
  `--format json`. `layout` drops the OpenCode `agents` row, so
  `layout.json` and `docs/LAYOUT.md` lose it; the `layout.Agents` concern
  constant stays. Anything that expected `agents.json` in an OpenCode boot
  dir or projection, or the `opencode.json` agent entry, must stop.

## v0.27.0 — 2026-09-29

### Added

- Package `layout` (stdlib only, imports nothing): one compile-checked table of
  where Claude Code, Codex and OpenCode read files, skills and config, relative
  to which root, with the flag, environment variables and working directory that
  locate them. `Table`, `For`, `Find`, `SkillRoot`, `Providers`. Every row cites
  Step 0 probe ids (`Entry.Probe`) or says why it has none (`Entry.Unprobed`).
- `layout/gen` (`go generate ./...`; `go run ./layout/gen -check` fails when
  stale): renders `docs/LAYOUT.md` and `layout/layout.json` for non-Go readers.
- `layout/layouttest`: `AssertSkillPlacement`, `AssertProjectDirFlag`,
  `AssertEnv`, for other modules to pin their own tables in their own tests.
- Step 0 harness discovery: `hack/probe-harness-layout.sh`, the raw golden
  `provider/testdata/harness-discovery/2026-09-29-claude-2.1.285-codex-0.154.0-opencode-1.18.30.tsv`,
  `docs/HARNESS-DISCOVERY.md`, and an opt-in `go test -tags harnessprobe ./layout`
  that re-runs the probe against the installed harnesses and diffs with the
  newest golden.
- `docs/CONSUMERS.md`: the nine path tables and five skill authors, with the
  `layouttest` call that would pin each.
- `SkillPackage.Hash` (optional pin) and `SkillPackage.TreeHash()`:
  `sha256:<hex>` over the sorted whole skill tree, each file framed as
  path NUL length NUL content NUL, `.DS_Store` skipped. This is go-agentdef's
  definition (`skills.go hashTree`), so a hash from either module verifies in
  the other. Projection fails when `Hash` is set and the files do not match.

### Changed

Behaviour changes to projected paths, flags and environment. Without
`ProjectionOptions.Skills`, the projected files, `LaunchConvention`, resolved
binding and legacy `BootDirSpec` of the three built-in adapters are byte-identical
to v0.26.0 (pinned by `TestLayoutRegression_NonSkillProjectionUnchanged`, recorded
before the change). Only skill placement and one argv addition changed:

| Adapter / mode | Path, flag or env | Was | Now | Probe |
|---|---|---|---|---|
| Codex exec, app-server | skill tree prefix (under the boot root = `CODEX_HOME`) | `.agents/skills/<name>/` | `skills/<name>/` | X2, X3, X4 |
| OpenCode run, serve-http | skill tree prefix (under the boot root = `OPENCODE_CONFIG_DIR`) | `.opencode/skills/<name>/` | `skills/<name>/` | O2, O3 |
| Claude bare, when skills are projected | argv | no boot-root flag | `--add-dir <boot>` appended after the project `--add-dir` | C4, C5 (+ supplementary measurement in docs/HARNESS-DISCOVERY.md) |
| Claude print, pty, streaming-stdio | skill tree prefix | `.claude/skills/<name>/` | unchanged | C1, C2 |

Everything else changed here is provenance, not value:

- `ProviderProjection`, `LaunchConvention` and the built-in adapters' `BootDirSpec`
  read paths, flags, env and cwd from `layout`. The `BootDirSpec` type is kept.
  Legacy `PlantedFile.Mode` values are unchanged (the claude legacy `.mcp.json`
  keeps mode 0; the projection's is 0600, as before).
- `ProviderCapabilityMatrix` `TestedVersion`: claude 2.1.263 to 2.1.285, codex
  0.153.4 to 0.154.0, opencode 1.15.6 to 1.18.30 (the versions the probe measured).
- go directive is `go 1.26.6` (landed as 8481a89, before this work).

Skills are emitted only in the directory form `<name>/SKILL.md`; none of the
three harnesses reads flat `<name>.md` (probes C1, X1, O1). The table records
Form "dir" only.

### Known limitations

- Not measured (rows say so via `Unprobed`): `CLAUDE.md` / `AGENTS.md` /
  `agents/<name>.md` model-visible effect, OpenCode `--dir`, Codex project trust,
  Claude interactive mode, OpenCode `skills.paths`, Codex `--add-dir`. Stage B
  (one model call) was not run; the Codex skill root is unambiguous from X2-X4.
- The provider's PTY/subprocess argv builders (`BuildArgs`, `BareInjectionPaths`)
  are unchanged and still author argv by hand.

### What consumers can delete after adopting `layout` (not done here)

- agentkit: both flat `skillRelPath` copies (`agentlaunch/providerplant/plant.go:386`,
  `agentlaunch/materialize.go:407`); accept a `provider.SkillPackage` in
  `NativeFile{Kind: skill}`; `appendMissingProjectArg` (`providerplant/plant.go:178`)
  once the convention carries the project flag.
- go-agent-wrapper: `plant.providerSettingsPath` / `hookPath` (`plant/plant.go:203-227`),
  or route them through `layout`.
- Nanite: the dual `skills/` plus `.opencode/skills/` planting and the "codex has no
  native skill mechanism" branch in `skill_plant.go:325-362` (becomes a thin overlay).
- Tether: bump from go-providers v0.25.0; replace `internal/skills` compile
  (`skills.go:164-249`) with `provider.SkillPackage`; codex skills stop being
  `AGENTS.md` sections, which also removes the latent `AGENTS.md` clobber.
- Cairn and agent-launcher: read `layout/layout.json` for skill destinations and
  the project-dir flag (codex `--cd` versus `--add-dir`).
- Adoption changes what Torque and Tether launches deliver: flat skills, which no
  harness ever read, become directory skills, which all three do.

## v0.26.0 — 2026-09-06

### Added

- Pure provider projections for Claude, Codex and OpenCode: files and trees,
  executable modes, provider configuration and exact launch bindings without
  credential reads, workspace trust changes or process launch.
- Explicit runtime-preparation requests and results for caller-approved
  credential and trust effects, with capability diagnostics and validation.

### Changed

- Existing boot-directory adapters delegate to the shared projection and
  preparation contracts while retaining their compatibility entry points.

## v0.25.0 — 2026-09-02

### Added

- `ClaudeAdapter.SettingsDocument() (map[string]any, error)` — the
  planted `.claude/settings.json` (`apiKeyHelper`,
  `permissions.defaultMode`, `permissions.additionalDirectories`) as a
  document to merge into rather than bytes to parse. Rendering the file
  through the `BootDirSpec` was already supported and yields the same
  content — the trust seed in that closure is gated on
  `ctx.BootDir`, per `PlantedFile.Render`'s contract, so a zero
  `PlantContext` renders the settings and touches nothing. What the
  accessor changes is the shape of the answer and how it is reached:
  the document instead of encoded JSON, a name instead of a positional
  index into `PlantedFiles`, and no gate for the caller to honor, since
  it takes no `PlantContext` and so cannot seed anything whatever it is
  passed. Encoding it as `json.MarshalIndent(doc, "", "  ")` plus a
  trailing newline reproduces the planted file byte for byte. The map
  and the `additionalDirectories` slice within it are built fresh per
  call, so neither merging into the document nor writing through that
  slice reaches back into the adapter. No arguments: every input is
  already a field on the adapter.
- `CodexAdapter.ConfigDocument(PlantContext) (string, error)` — the
  planted `config.toml` (the `approval_policy` / `sandbox_mode` header,
  the `[sandbox_workspace_write]` `writable_roots` table, and every
  `[mcp_servers.*]` block) under a name rather than a positional index,
  callable without assembling a plant. It takes a `PlantContext`
  because that is already the per-plant input set for this file.
  Unlike the claude settings it returns text, not a document: codex's
  config has no in-memory intermediate anywhere in this package, it is
  built as TOML directly. A consumer adding MCP servers therefore
  supplies `PlantContext.MCPServers` — `config.toml` is single-owner by
  design and that field's name/duplicate validation is what keeps two
  `[mcp_servers.<name>]` tables, which codex rejects, out of the file.
  Appending to the returned string stays valid TOML but nothing
  validates what is appended.

### Changed

- Both `PlantedFile.Render` closures now delegate to the new accessors,
  so each planted file has exactly one implementation and the accessor
  cannot drift from what gets written. Internally `claudeSettingsStub`
  is split into `claudeSettingsDocument` (builds the map) and
  `marshalClaudeSettings` (decides the on-disk encoding). The claude
  trust seed stays in the Render closure, where the plant is.
- Additive only: no exported signature or behavior changed, the planted
  `.claude/settings.json` and `config.toml` are byte-identical, and the
  Render error messages are unchanged (the accessors return the
  underlying validation errors unwrapped so the closures keep their own
  prefixes).

### Docs

- `OpencodeAdapter`'s doc comment (`provider/pty_opencode.go`) no longer
  claims "the bridge synthesizes llmtypes.EventDone on clean process exit" —
  no such synthesis exists anywhere in this package. The real synthesis (as
  of `agentkit` v0.5.0) lives one layer up, in the *consuming*
  `agentkit/agentsessions` subprocess-per-turn adapter runtime, not here.
  Doc-only change; `ParseLine`'s actual behavior (EventDelta-only, never a
  terminal event, for the default run mode) is unchanged. No version bump —
  nothing behavioral changed in this package.

## v0.24.0 — 2026-08-21

### Fixed

- `CodexAdapter.BuildArgs`'s exec-mode argv now includes
  `--skip-git-repo-check`. Every real `codex exec` invocation from a
  BootDirSpec-planted boot dir (always a throwaway, non-git tempdir) was
  failing 100% of the time — the real `codex` CLI's own trust gate refuses
  to run non-interactively outside a git repo / trusted directory
  ("Not inside a trusted directory and --skip-git-repo-check was not
  specified"), confirmed against a real `codex-cli 0.147.0` binary. The
  flag only widens which directories codex is willing to start in; it does
  not touch the sandbox (`approval_policy` / `sandbox_mode` in the planted
  `config.toml` remain the mechanism gating what codex may do once
  running), so it's safe to pass unconditionally. `app-server` mode's
  argv and JSON-RPC `thread/start` protocol have no equivalent flag/gate —
  confirmed unaffected by direct JSON-RPC round-trip against the real
  binary in a non-git tempdir — and is unchanged.

## v0.22.0 — 2026-05-18

### Added

- `CodexAdapter.WritableRoots` (`[]string`) — additional absolute
  directories the codex sandbox may write to beyond the boot dir cwd.
  Non-empty values render a `[sandbox_workspace_write]` table with a
  `writable_roots` array in the planted `config.toml`. Under
  `SandboxMode "workspace-write"` codex confines writes to the workspace
  cwd; when that cwd is a throwaway boot dir, an agent cannot write a
  real project path. `WritableRoots` widens the sandbox without dropping
  to `danger-full-access`.
- `ClaudeAdapter.AdditionalDirectories` (`[]string`) — directories the
  claude agent may access beyond its cwd workspace. Non-empty values
  render `permissions.additionalDirectories` in the planted
  `.claude/settings.json` (the settings-file form of `--add-dir`). The
  claude analogue of `CodexAdapter.WritableRoots`.

### Changed

- The codex `config.toml` renderer emits a `[sandbox_workspace_write]`
  table (after the `approval_policy` / `sandbox_mode` header, before any
  `[mcp_servers.*]` table) when `CodexAdapter.WritableRoots` is non-empty.
- The claude `.claude/settings.json` renderer emits the `permissions`
  object when EITHER `PermissionMode` or `AdditionalDirectories` is set
  (previously only `PermissionMode`).
- Back-compat: with both new fields empty, the planted `config.toml` and
  `.claude/settings.json` are byte-identical to v0.21.0.

## v0.21.0 — 2026-05-17

### Added

- `PlantContext.MCPServers` (`[]MCPServerSpec`) — a slot for arbitrary
  additional MCP servers beyond the per-task loopback (`MCPLoopbackURL`) and
  the mux aggregator (`Mux*`). Each `MCPServerSpec` is a name plus one
  transport: `HTTPURL` (streamable-HTTP) or `Command` (+ `Args` / `Env`,
  stdio).

### Changed

- The codex `config.toml` renderer now emits a `[mcp_servers.<name>]` block
  for each `PlantContext.MCPServers` entry, after the loopback / mux blocks.
  codex has **no `.mcp.json` sidecar** — every MCP server it sees must be
  co-rendered into the single `config.toml` — so a consumer's own server
  (e.g. Nanite's `nanite mcp`) previously could not be added without
  post-processing the planted file. `MCPServers` keeps `config.toml`
  single-owner. The names `loopback` and `mux` are reserved; an invalid
  spec (empty/reserved/non-`[A-Za-z0-9_-]` name, duplicate name, or not
  exactly one transport) fails the `config.toml` `Render`.
- Back-compat: with `MCPServers` empty the planted `config.toml` is
  byte-identical to v0.20.0. claude `.mcp.json` and opencode `opencode.json`
  are unaffected — those providers keep a dedicated MCP-config file a
  consumer can extend directly.

## v0.20.0 — 2026-05-17

### Added

- `CodexAdapter.ApprovalPolicy` and `CodexAdapter.SandboxMode` — first-class
  fields for codex's `approval_policy` / `sandbox_mode` config vocabulary, the
  codex analogue of `ClaudeAdapter.PermissionMode`. They thread into the
  planted `config.toml`. An unrecognized value fails the `config.toml`
  `Render`.

### Changed

- The codex `BootDirSpec` `config.toml` now **always** emits an
  `approval_policy` / `sandbox_mode` header (previously it emitted only
  `[mcp_servers.*]` blocks, and nothing at all when there were no MCP
  servers). The defaults are `never` / `workspace-write` — deliberately NOT
  codex's interactive defaults.

### Fixed

- **Headless-codex approval deadlock.** A `BootDirSpec` materializes a
  headless per-task boot with no human at a TTY. With no `approval_policy`
  planted, codex fell back to its interactive default and prompted for
  approval before running any tool — and under the `app-server` runtime that
  prompt is a JSON-RPC approval request no one answers, so the run hung
  forever. Planting `approval_policy = "never"` (with a writable
  `sandbox_mode`) makes a headless codex non-interactive by default — the
  orchestrated-run equivalent of `--full-auto`. Pairs with the
  `go-agent-sessions` v0.9.5 fix that stops the runtime from dropping any
  server-initiated request that does slip through.

## v0.19.0 — 2026-05-17

### Added

- `ClaudeAdapter.PermissionMode` — a first-class field for the full
  Claude Code permission-mode vocabulary (`default`, `acceptEdits`,
  `plan`, `bypassPermissions`). It threads into the planted
  `.claude/settings.json` as `permissions.defaultMode`. Previously the
  stub could only emit `bypassPermissions` (derived from
  `SkipPermissions`), so consumers that needed `acceptEdits` or `plan`
  post-processed the planted file after planting. Setting an
  unrecognized value fails the `.claude/settings.json` `Render`.

### Changed

- The planted `.claude/settings.json` `permissions.defaultMode` value
  is now resolved by `resolveClaudeDefaultMode`: a non-empty
  `PermissionMode` wins; otherwise `SkipPermissions == true` still
  yields `bypassPermissions` (back-compat); otherwise no `permissions`
  block is planted.

### Compatibility

- Minor release. No signature changes on exported types — `PermissionMode`
  is a new field with a zero value (`""`) that preserves the prior
  behavior exactly. The unexported `claudeSettingsStub` signature
  changed (`bypassPermissions bool` → `defaultMode string`). Regression-
  guarded by `TestClaudeSettingsStub_PermissionMode` and the existing
  `TestClaudeSettingsStub_BypassPermissions`.

### Downstream cleanup

- Torque's `internal/runtime/agent/permission_mode.go`
  `applyPermissionMode` post-processing of the planted settings.json
  becomes removable once it adopts `ClaudeAdapter.PermissionMode`
  (CW-20260517-0038). Tether carries an equivalent workaround.

## v0.18.0 — 2026-05-16

### Fixed

- `claudeSettingsStub` (the planted `.claude/settings.json`) no longer
  emits the `approvedTools` and `mcpServers` keys. Current Claude Code
  ignores both — they are not part of the settings schema — so writing
  them pre-approved nothing. Spawned agents whose only permission
  signal was this stub fell back to `default` permission mode and
  blocked on prompts. The stub now emits the current schema.

### Added

- `ClaudeAdapter.SkipPermissions` now also threads into the planted
  `.claude/settings.json` as `permissions.defaultMode:
  "bypassPermissions"` — the settings-schema equivalent of the
  `--dangerously-skip-permissions` CLI flag. The planted file now
  backstops the flag for any consumer that reaches settings.json.
- `ClaudeAdapter.MCPConfigPath` is honored in **all** modes (PTY,
  streaming-stdio, print) — previously `--mcp-config` was emitted only
  in bare mode. Loading the MCP config explicitly is not subject to
  the project-scoped `.mcp.json` "Use this MCP server?" trust prompt
  that otherwise fires in interactive (PTY) mode, so non-bare
  consumers can set this to the planted `.mcp.json` to give spawned
  agents their MCP servers without an approval gate.

### Compatibility

- Minor release. No signature changes on exported types. The unexported
  `claudeSettingsStub` gained a `bypassPermissions bool` parameter.
  Behavior change: the planted `.claude/settings.json` shape changed
  (deprecated keys dropped; `permissions` block added when
  `SkipPermissions`). `MCPConfigPath` set on a non-bare adapter now
  emits a flag where it previously did not — a no-op for the default
  empty value. Regression-guarded by `TestClaudeSettingsStub_BypassPermissions`
  and `TestClaudeBuildArgs_MCPConfig_NonBare`.

## v0.17.1 — 2026-05-12

### Fixed

- `CodexAdapter.BootDirSpec` no longer emits `ProjectDirArg = "--cd
  {{.ProjectDir}}"` in `app-server` mode. `codex app-server` rejects
  `--cd` (codex 0.130.0 exits 2 with
  `error: unexpected argument '--cd' found`), so the long-lived
  JSON-RPC daemon could not be spawned via `AutoPlantBootDir` from
  go-agent-sessions v0.9.x. App-server now returns
  `ProjectDirArg = ""`; project access is granted via JSON-RPC
  `thread/start` parameters at the consumer runtime layer. Exec mode
  is unchanged. Reproduced from the agent-mux v005-07 lib-tier
  adoption smoke (`agentsessions: jsonrpc-stdio waiter abnormal …
  err="exit status 2"`).

### Compatibility

- Patch release; no signature or behavior changes for exec mode.
  Regression-guarded by the new
  `TestCodexAdapter_ExecMode_BootDirSpec_HasProjectDirArg` and
  `TestCodexAdapter_AppServer_BootDirSpec_NoProjectDirArg` tests.

## v0.17.0 — 2026-05-11

### Added

- `ClaudeAdapter.InputMode` field (string). Defaults to `""` (no
  `--input-format` flag emitted; current behavior preserved). Setting
  `InputMode = "stream-json"` routes `BuildArgs` through a dedicated
  branch that emits
  `-p --input-format stream-json --output-format stream-json --verbose`
  and intentionally drops the positional prompt and `--system-prompt`
  parameter — per-turn payloads and system context flow over NDJSON
  stdin in Anthropic's "Streaming Input Mode" (one long-lived
  `claude -p` process, KV-cache reused across turns until stdin EOF).
  The runtime that owns the stdin loop, attach fan-out, and session-id
  handling lives in `go-agent-sessions` (`streamingStdio` kind); this
  lib only emits the argv shape.
- `NewClaudeAdapterStreamingStdio()` and
  `NewClaudeAdapterDevStreamingStdio()` constructors mirror the existing
  PTY/Bare style.
- `CodexAdapter.Mode` field (string). Default `""` (or `"exec"`)
  preserves the existing `codex exec <prompt> --json` single-turn
  subprocess shape. `Mode = "app-server"` switches `BuildArgs` to emit
  `["app-server"]` and makes `ParseLine` a pass-through that returns no
  events: in app-server mode codex speaks JSON-RPC 2.0 over stdio
  (default `--listen stdio://`); the consumer runtime
  (`go-agent-sessions` `jsonRpcStdio` kind) owns framing, request /
  response correlation, and event mapping.
- `NewCodexAdapterAppServer()` constructor.

### Changed

- Deleted the stale `pty_codex.go` comment that claimed `"Resume is
  interactive-only in Codex, so we always use single-turn exec."` —
  confirmed false against codex 0.130.0; superseded by the new
  app-server lane.

### Compatibility

- Source- and behavior-compatible with v0.16.2. Default zero values for
  `InputMode` and `Mode` reproduce the v0.16.2 argv byte-for-byte,
  pinned by the existing `TestClaudeBuildArgs_NonBare_ByteForByteIdentical`
  and `TestCodexAdapter_BuildArgs` tests plus new
  `_InputModeAbsentByDefault` and `_ExecMode_ParseLineStillWorks` guards.
- Positional composite literals to `ClaudeAdapter` / `CodexAdapter`
  continue to compile because the new fields are appended after existing
  fields and default to zero values; keyed literals remain preferred
  per the v0.9.0 caveat.
- No new module dependencies, no API surface removals, no signature
  changes to existing constructors.

### Rationale

Five prior portfolio sessions tried to make Mux / Nanite / Clockwork run
long-lived headless Claude/Codex sessions, all bouncing between
PTY-driving-the-TUI (unproven, fights the tool) and `--resume <id>`
subprocess-per-turn chaining (re-injects context every turn). Empirical
investigation on 2026-05-11 confirmed both vendors ship documented
long-lived headless modes that nobody had wired:

- Claude: `claude -p --input-format stream-json --output-format
  stream-json --verbose` — one process, NDJSON over stdin/stdout,
  KV-cache reused. Anthropic's term: "Streaming Input Mode (Default &
  Recommended)" in the Agent SDK docs.
- Codex: `codex app-server` — same engine that backs the official VS
  Code extension. JSON-RPC 2.0 over stdio. Threads in memory until
  30-min idle.

This release closes the go-providers argv half of the gap. The runtime
half ships in `go-agent-sessions` v0.8 (parallel sprint). Full rationale
+ empirical evidence:
`agent-workspaces/knowledge/portfolio/cli-agent-long-lived-modes.md`.

> **CHANGELOG drift note.** Versions v0.15.0 and v0.16.0–v0.16.2 were
> tagged + pushed without CHANGELOG entries; see `git log
> v0.14.0..v0.16.2` for the commit trail (bootdir-related fixes for
> codex and opencode MCP planting). Captured as a follow-up to write
> retroactive entries; not addressed in this release to keep scope
> tight.

## v0.14.0 — 2026-05-10

### Added

- `examples/` directory with three runnable programs that exercise the
  `BootDirSpec` plant-and-spawn pattern end-to-end:
  - `examples/claude_bare/` — bare-mode Claude Code with explicit
    context injection (`--mcp-config`, `--append-system-prompt-file`,
    `--settings`, `--add-dir`) and the `apiKeyHelper` auth path.
  - `examples/codex_bootdir/` — Codex via `codex exec --json` with
    `AGENTS.md` planted and project access via `--cd`.
  - `examples/opencode_bootdir/` — opencode with the agent profile +
    config files planted and `OPENCODE_CONFIG_DIR` set.
  Each example is dry-run-friendly: when the underlying CLI binary is
  not detectable, the program prints the boot-dir layout and would-be
  spawn args and exits cleanly so the wiring can be inspected without
  installing the CLI.

### Changed

- README hardened for public release: tightened the bare-mode
  walkthrough, added a pointer to `examples/`, and removed
  internal-workflow language.
- Inline code comments and CHANGELOG entries no longer reference
  internal sprint/ticket identifiers; the technical "why" content is
  preserved.

### Fixed

- CHANGELOG: removed an unresolved merge-conflict region around the
  v0.9.x entries (a duplicated v0.9.2 block that pre-dated the v0.13.0
  rebase). v0.9.2 was never tagged; its content rolled forward into
  v0.13.0.

### Repository hygiene

- Added a top-level `.gitignore` covering Go build artifacts, editor
  scratch files, and OS metadata files.

### Compatibility

- Source- and behavior-compatible with v0.13.0. No new module
  dependencies, no API surface changes. The new `examples/` subtree
  uses only the existing public API plus stdlib.

## v0.13.0 — 2026-05-09

- Added `ApiKeyHelperPath` field to `ClaudeAdapter`. When set on a bare-mode adapter, the planted `.claude/settings.json` includes `"apiKeyHelper": "<path>"`; bare-mode claude invokes the helper per request and consumes its first line of stdout as the bearer token used for `Authorization: Bearer <token>` against `https://api.anthropic.com`. This closes an auth gap in bare mode: bare disables the CLI's OAuth/keychain auto-resolution, so subscription users (no `ANTHROPIC_API_KEY` in env, authenticated via `claude` interactive login → macOS keychain) lose the auth surface bare needs. The helper closes that gap by reading the keychain (or any other per-environment secret store) and emitting a fresh token on demand.
- Empirically verified (probed against claude 2.1.137): the keychain's `claudeAiOauth.accessToken` (`sk-ant-oat01-…` format) authenticates against the API directly when returned by an `apiKeyHelper` — no exchange to a long-lived API key needed. `apiKeySource: "apiKeyHelper"` is confirmed in claude's stream-json `system/init` event; the request returns `result/success`. `security find-generic-password -s "Claude Code-credentials" -a "$USER" -w` reads the keychain entry from a launchd-spawned daemon-spawned subprocess without firing a Touch-ID prompt or GUI dialog.
- `claudeSettingsStub` signature changed from `claudeSettingsStub() string` to `claudeSettingsStub(apiKeyHelperPath string) string`. The function is unexported, so this is a private-API change with no external impact. The `BootDirSpec().PlantedFiles[2].Render` closure now passes `a.ApiKeyHelperPath` through (closure captures the receiver). Empty `ApiKeyHelperPath` (default zero value) emits no `apiKeyHelper` field — backward-compatible with the v0.9.0/v0.9.1 stub.
- Tests: three new unit tests in `provider/bootdir_test.go` — `TestClaudeBootDirSpec_ApiKeyHelper_Absent` pins that the field is absent in settings.json when `ApiKeyHelperPath` is empty (default), `_Set` pins that a non-empty path threads into the JSON-encoded settings, and `_BareAdapterRespects` pins that both `NewClaudeAdapterBare()` and `NewClaudeAdapterDevBare()` honor the field. `go test -race -count=1 ./...` clean on darwin. `go vet ./...` clean.

### Compatibility

- Behavior-additive. Existing callers that don't set `ApiKeyHelperPath` get byte-for-byte identical settings.json (the `apiKeyHelper` key is omitted, so the stub remains `{"mcpServers":{},"approvedTools":[]}`). The `ClaudeAdapter` struct gains one additive field; positional composite literals continue to compile because the new field is appended after the existing six and defaults to its zero value, but keyed literals are preferred per the v0.9.0 caveat.
- Non-bare callers can set `ApiKeyHelperPath` if they want — the field threads into `.claude/settings.json` regardless of `Bare`. The non-bare CLI honors the same `apiKeyHelper` settings.json field per its docs, so this is a uniform improvement.
- `claudeSettingsStub`'s signature change is internal-only (unexported); no external consumers affected.

### Consumer integration sketch

A minimal helper executable looks like: read `$ANTHROPIC_API_KEY` first; on empty, run `security find-generic-password -s "Claude Code-credentials" -a "$USER" -w`, parse the JSON, write `claudeAiOauth.accessToken` to stdout. Plant the helper next to your dispatcher binary and set `adapter.ApiKeyHelperPath` alongside `adapter.MCPConfigPath` etc. Subscription users then dispatch without needing `ANTHROPIC_API_KEY` in the dispatcher env; API-key users keep the env-first fast path.

## v0.12.0 — 2026-05-09

### BREAKING — Removed (transitional aliases dropped per Path B)

- Type aliases in `provider/provider.go` for migrated types:
  `ProviderCapabilities`, `ToolDefinition`, `ToolUseBlock`, `ContentBlock`,
  `EventType`, `ThinkingBlock`, `StreamEvent`, `Usage`, `CompleteResult`,
  `ChatMessage`, `SlotBlock`, `ChatRequest`, `Provider`.
- Constant aliases for `EventDelta`, `EventToolUse`, `EventUsage`,
  `EventError`, `EventDone`, `EventSessionID`, `EventThinking`.
- The `IsTurnComplete` re-export shim function (canonical implementation
  lives in `go-llm-types`).

### BREAKING — Removed (unused PTY adapters)

- `pty_aider.go`, `pty_copilot.go`, `pty_gemini.go`, `pty_junie.go`,
  `pty_kiro.go`, `pty_qwen.go` and their `_test.go` siblings.
- Their constructors (`provider.NewAiderAdapter`, `NewCopilotAdapter`,
  `NewGeminiAdapter`, `NewJunieAdapter`, `NewKiroAdapter`,
  `NewQwenAdapter`) and adapter types.
- The companion `bootdir_stubs.go` (TBD `BootDirSpec` methods on the
  deleted adapter types).

### Why

The aliases were introduced in v0.11.0 to ease the consumer-side
migration to `go-llm-contracts` + `go-llm-types`. The clean-break path
was chosen here: known consumers migrated their imports directly to the
canonical homes in lockstep, so the transitional aliases are no longer
needed.

The six unused PTY adapters were preserved through earlier releases
because downstream apps still consumed them. Those apps have since
migrated to the three production adapters (claude / codex / opencode);
the unused adapters are dropped cleanly.

### Migration

Consumers must import:
- Data types (`ChatRequest`, `StreamEvent`, `ChatMessage`, `ContentBlock`,
  `ToolDefinition`, `ToolUseBlock`, `ThinkingBlock`, `SlotBlock`,
  `CompleteResult`, `Usage`, `EventType`, `ProviderCapabilities`) and
  event constants (`EventDelta`, `EventToolUse`, `EventUsage`,
  `EventError`, `EventDone`, `EventSessionID`, `EventThinking`) from
  `github.com/hollis-labs/go-llm-types`.
- The `Provider` interface (and `IsTurnComplete` predicate) from
  `github.com/hollis-labs/go-llm-contracts` (and `go-llm-types`
  respectively).
- The `Embedder` interface from
  `github.com/hollis-labs/go-embed-contracts`.

go-providers retains a tight CLI/PTY/subprocess surface — `Registry`,
`CLIAdapter`, claude/codex/opencode adapter constructors
(`NewClaudeAdapter`, `NewCodexAdapter`, `NewOpencodeAdapter`), context
helpers (`WithCLISessionID`, `WithSandboxDir`, `WithProcessCallback`,
`WithActivityCallback`, `WithWaitDelay`), `EventsCallback`, `AgentInfo`,
`AgentsMD`, `CostMonitor`, `ProgressTracker`, `ScopeGuard`,
`EventReactionPipeline`, and the `BootDirSpec` plumbing for the three
production adapters.

Companion modules: go-llm-contracts, go-llm-types, go-embed-contracts

## v0.11.0

### Breaking — relocated rate-budget primitives; shared model types extracted

- Removed `TokenRateTracker`, `CircuitBreaker`, `ErrRequestExceedsRateBudget`,
  `PacingWait`, `CircuitState`, and `DefaultCooldown` from this module.
  Their new home is `github.com/hollis-labs/go-llm-contracts` (`v0.1.0+`).
- Extracted the transport-agnostic request/response/tool/event data model to
  `github.com/hollis-labs/go-llm-types`.
- `provider.Provider` now aliases the canonical interface in
  `github.com/hollis-labs/go-llm-contracts`, while request/stream carrier types
  such as `provider.ChatRequest`, `provider.StreamEvent`, and `provider.Usage`
  alias the corresponding `go-llm-types` definitions.

### Why

With HTTP-backed adapters already removed in v0.10.0, the surviving
rate-budget primitives were no longer part of the PTY/CLI implementation
surface. Moving them to `go-llm-contracts` keeps this module focused on
adapter implementations while giving SDK/HTTP wrappers a stable shared home.

## v0.10.0

### Breaking — HTTP providers removed; lib is now CLI/PTY/subprocess-only

- Deleted all 8 HTTP-bound chat adapter implementations and their tests: `Anthropic`, `OpenAI`, `Gemini`, `Mistral`, `Ollama`, `OpenRouter`, `OpenZen`, `AzureOpenAI` (constructors `NewAnthropic`, `NewOpenAI`, `NewGemini`, `NewMistral`, `NewOllama`, `NewOpenRouter`, `NewOpenZen`, `NewAzureOpenAI` are gone, along with their structs, methods, and embedder implementations).
- Deleted `Embedder` interface (no implementations remain) and all `*_embedding_test.go` files.
- Deleted HTTP-only support code: `api_key.go` (the `APIKeySetter` interface + per-receiver `SetAPIKey` methods), `retry.go` (HTTP-status-code retry/backoff used by HTTP providers), `cache.go` (`CacheHint` / `CacheableProvider` / `DefaultCacheStrategy` — Anthropic-style prompt-caching strategy).
- Trimmed `provider.Provider` extension interfaces and Anthropic-specific context helpers: `ProviderWithUsage`, `RateLimited`, `Cacheable`, `ReasoningConfig`, `WithReasoningConfig`, `ReasoningConfigFromContext`. `EventReactionPipeline.CompleteWithUsage` removed; the non-streaming fallback now uses `Provider.Complete` and emits `delta` + `done` (no synthesized `usage` event).
- Kept (despite HTTP-adjacent origin): `Usage` struct + cache-token fields, `EventUsage`, `EventThinking`, `ThinkingBlock` — PTY adapters (`pty_claude`, `pty_gemini`, `pty_junie`, `pty_qwen`) emit these. Also kept: `circuit.go`, `ratelimit.go`, `cost_monitor.go`, `progress_tracker.go`, `scope_guard.go`, `model_ops.go`, `agents_md.go` — generic primitives over `StreamEvent`.

### Why

First-party agent flows have moved to CLI/PTY adapters
(claude / codex / opencode / gemini-cli / qwen / junie / kiro / copilot /
aider). Maintaining HTTP chat adapters duplicates work the CLIs do
(auth, retry, model selection, prompt caching) and accretes maintenance
debt with no remaining first-party consumer.

### Consumer breakage (informational)

- The heaviest internal consumer migrated its HTTP-provider registry,
  embedder-selection plumbing, and the few `*provider.Anthropic`
  type-assertion sites in lockstep with this release; the dual-path
  HTTP fallback path is retired.
- All other internal consumers were CLI/PTY-only and pick this release
  up unchanged.

### Tests

- `go vet ./...` clean.
- `go test -race -count=1 ./...` clean (33.5s on darwin).
- `GOOS=linux go vet ./... && GOOS=linux go build ./...` clean.
- Deleted: `capabilities_test.go` (HTTP-provider-only capability assertions). Trimmed: `event_pipeline_test.go` (`TestEventReactionPipelineNonStreaming` now expects 2 events instead of 3; `TestEventReactionPipelineCompleteWithUsageFallback` deleted; `mockStreamingProvider`/`mockNonStreamingProvider` lost their `CompleteWithUsage` methods; `stubNoUsageProvider` deleted). Trimmed: `example_test.go` (`ExampleAnthropic_StreamChat` deleted along with `exampleRewriteTransport` and the `net/http`/`net/http/httptest`/`net/url`/`strings` imports; `ExampleRegistry` retained). Surviving smoke tests (`bare_claude_smoke_test.go`, `bootdir_claude_smoke_test.go`, `pty_claude_smoke_test.go`) gated on env vars and exercise the surviving CLI/PTY surface.

### Migration

Replace HTTP provider construction with the corresponding CLI/PTY adapter: e.g. `provider.NewAnthropic()` → `provider.NewClaudeAdapter()` or `provider.NewClaudeAdapterBare()` (depending on whether you want non-bare auto-discovery or bare-mode strict validation); `provider.NewGemini()` → `provider.NewGeminiAdapter()` (PTY); `provider.NewOpenAI()` → no direct successor in this lib (OpenAI doesn't ship a first-party CLI agent). Embedding consumers must migrate to a different lib — go-providers v0.10.0 no longer ships embedding adapters.

## v0.9.1

- `renderMCPJSON(loopbackURL)` now emits `{"type": "http", "url": "..."}` for the loopback entry instead of `{"url": "..."}`. The bare-mode CLI's `--mcp-config <path>` triggers strict schema validation that requires an explicit transport discriminator on every server entry; without `type`, the validator defaults to the stdio shape and rejects with `Invalid MCP server config for "loopback": command: expected string, received undefined`. Empirical probe against claude 2.1.137 (recorded in `agent-workspaces/execution/go-providers/2026-05-09-bare-mode-mcp-shape/probe-results.md`): of six candidate shapes (`{url}`, `{transport: http, url}`, `{type: http, url}`, `{http: {url}}`, `{type: sse, url}`, `{type: streamable-http, url}`), only the three with a top-level `type:` field pass bare-mode validation. `type: "http"` is also accepted by non-bare auto-discovery (probed against the same binary), so option (b) from the ticket — single shape for all callers — works without branching.
- Surfaced empirically: child sessions spawned with the v0.9.0 bare adapter against a populated MCP loopback exited 1 within a second with the validator error above as their only stderr. The v0.9.0 empty-MCP probe verified the empty-servers shape (`{"mcpServers":{}}`) passed bare validation but didn't probe the populated-loopback shape; this fix closes that gap.
- Why `type: "http"` over `sse` / `streamable-http`: the loopback is a streamable-HTTP MCP server, not an SSE stream — `sse` would mislabel it. Both `claude --help` and `claude mcp add --transport http <name> <url>` use `http` as the canonical transport keyword, so the planted file mirrors what claude itself writes when a user runs `mcp add`. `streamable-http` matches MCP-spec terminology but isn't the user-surface keyword.
- All three adapters that share `renderMCPJSON` (claude / codex / opencode) inherit the fix transparently. Codex/opencode don't use bare mode today; the change is neutral for their auto-discovery flow (verified empirically) and forward-compatible if either adapter migrates to a strict-validation flag in the future.
- Tests: `TestRenderMCPJSON_PopulatedShape` pins the exact emitted bytes for a non-empty URL; `TestRenderMCPJSON_Empty` mirrors `TestClaudeBootDirSpec_EmptyMCP` at the function level. `TestClaudeBootDirSpec` extended to assert `"type": "http"` is present in the populated `.mcp.json`. New gated real-spawn smoke `TestClaudeAdapter_BareSpawn_PopulatedMCP_Smoke` (`CLAUDE_BARE_SMOKE=1`) plants the full BootDirSpec layout with a populated loopback URL pointing at an unreachable port, spawns `claude --bare --mcp-config <path>` with the bare-mode arg shape via `BareInjectionPaths`, and asserts: exit 0 inside 30s, no `Invalid MCP configuration` / `Invalid MCP server config` / `command: expected string, received undefined` sentinels in stderr, stream-json output present, response contains `TEST_OK_BARE`. Claude eagerly probes MCP servers at session init but treats connect failure as non-fatal (`mcp_servers:[{name:"loopback",status:"failed"}]` in the init event) and proceeds to respond, so an unreachable port doesn't gate exit 0. `go test -race -count=1 ./...` clean on darwin (33.6s). `GOOS=linux go build/vet ./...` clean. Existing bare-mode unit tests and `TestClaudeAdapter_BareSpawn_Smoke` (empty MCP) pass unchanged.

### Compatibility

- Behavior-additive for all callers. Empty-loopback shape (`{"mcpServers":{}}`) is unchanged. Populated-loopback shape gains a `"type": "http"` field. Non-bare auto-discovery accepts both old and new shapes; bare mode rejects the old shape and accepts the new — net win.
- Consumers that asserted on the exact populated-loopback bytes need to update. The only such assertion in this repo (`TestClaudeBootDirSpec` substring check on the loopback URL) was already loose; it has been tightened to also assert the new `type` field. Codex and opencode adapters share `renderMCPJSON` and inherit the new shape transparently; their own tests assert by substring on the URL only.

### Consumer pickup

- Bump go-providers to `v0.9.1` and re-apply any bare-mode wiring that
  was reverted while the v0.9.0 populated-loopback bug was outstanding
  (swap to `NewClaudeAdapterDevBare()` and populate the four bare
  fields from `BareInjectionPaths(bootDir, projectDir)`).

## v0.9.0

- Added `--bare` mode support to `ClaudeAdapter`. New `Bare bool` field plus four explicit-injection path fields (`MCPConfigPath`, `AppendSystemPromptFile`, `SettingsPath`, `ProjectDir`) and constructors `NewClaudeAdapterBare()` / `NewClaudeAdapterDevBare()`. When `Bare=true`, `BuildArgs` emits `--bare` plus `--mcp-config`, `--append-system-prompt-file`, `--settings`, `--add-dir` for each non-empty path field, on top of the existing print-mode shape (`-p`, `--output-format stream-json`, `--verbose`). Per Anthropic's claude 2.1.133+ docs, bare mode is "the recommended mode for scripted and SDK calls, and will become the default for `-p` in a future release"; it skips auto-discovery of hooks, skills, plugins, MCP servers, auto-memory, CLAUDE.md, OAuth, keychain reads, and operator config — only flags passed explicitly take effect. Adopting it now positions consumers correctly for the future-default shift and eliminates an entire class of operator-config bleed-through (`remoteControlAtStartup`, workspace-trust dialog, etc.) for scripted spawns.
- Added `ClaudeBareInjection` struct + `(*ClaudeAdapter).BareInjectionPaths(bootDir, projectDir)` helper that derives the four flag values from the planted-file layout in `BootDirSpec`. Consumer flow: `inj := adapter.BareInjectionPaths(bootDir, projectDir)`, copy the four fields onto the adapter, then `BuildArgs(prompt, "", sessionID)`. Empty `bootDir` or `projectDir` produce empty corresponding fields (no flag emitted — bare mode then has zero of that context category, which is the documented behavior).
- `BuildArgs` branch precedence is now `Bare` > `PTY` > default print-mode. If both `Bare=true` and `PTY=true` are set, bare wins because bare mode is print-mode-focused per the docs. The `systemPrompt` parameter to `BuildArgs` is ignored in bare mode — system context flows via the planted CLAUDE.md referenced through `AppendSystemPromptFile`. Stream-json subprocess-per-turn semantics (`--resume <id>` chaining included) work identically in bare mode; `--resume` is positioned first as in non-bare print mode.
- Surfaced empirically (post-v0.8.2): `remoteControlAtStartup: true` in `~/.claude.json` repeatedly forced programmatic spawns into remote-control mode (local stdin inert). Bare mode obsoletes that issue and the broader operator-config bleed surface for bare consumers in one shot. Workspace-trust dialog seeding from v0.8.2 stays — non-bare callers still need it; bare bypasses the dialog (no projects-map read).
- Auth requirement: bare mode strictly requires `ANTHROPIC_API_KEY` env var or `apiKeyHelper` via `--settings` (OAuth and keychain are never read). Documented in field comments + the bare smoke test gate. Callers that already provide `ANTHROPIC_API_KEY` in env need no auth changes.
- Tests: 12 new bare-mode unit tests in `provider/pty_claude_test.go` (`TestClaudeBuildArgs_Bare_*` covering no-paths, each individual flag, all-paths in stable order, skip-permissions, resume positioning, bare>PTY precedence, ignored systemPrompt parameter, plus `TestClaudeBareInjectionPaths` covering populated/empty bootDir+projectDir combinations). New `TestClaudeBuildArgs_NonBare_ByteForByteIdentical` sentinel pins five non-bare arg shapes (Dev print, with/without system prompt, with resume, PTY empty, DevPTY+resume) to guard against accidental leakage of bare additions into non-bare branches. Constructor-defaults test extended for the two new bare constructors. Pre-existing PTY-mode + print-mode tests pass byte-for-byte unchanged. New gated real-spawn smoke in `provider/bare_claude_smoke_test.go` (`TestClaudeAdapter_BareSpawn_Smoke`, gated on `CLAUDE_BARE_SMOKE=1`) plants a minimal CLAUDE.md + valid `.mcp.json` into a tempdir, spawns `claude --bare` with the full bare arg shape via `BareInjectionPaths`, and asserts exit 0 inside 30s, stream-json `system` event present, response contains `TEST_OK_BARE`, no `Quicksafetycheck`/`trust this folder`/`Quick safety check` trust-dialog markers, and no `remoteControl`/`remote-control` operator-config markers. Skips when the claude binary is absent or `ANTHROPIC_API_KEY` is unset (bare requires env-var auth). Existing `CLAUDE_PTY_SMOKE=1` regression continues to pass.
- `renderMCPJSON("")` now emits `{"mcpServers":{}}` instead of bare `{}`. Bare-mode `--mcp-config` references `.mcp.json` directly and triggers strict schema validation that requires `mcpServers` to be a record (probed empirically against claude 2.1.136: bare `{}` fails with `mcpServers: Invalid input: expected record, received undefined`). Auto-discovery (the non-bare path) accepts both shapes, so this change is harmless for existing callers and prevents a footgun for bare consumers planting via `BootDirSpec`. The pinned `TestClaudeBootDirSpec_EmptyMCP` test is updated to match.
- `go test -race -count=1 ./...` clean on darwin (33.7s). `GOOS=linux go build/vet ./...` clean. Both `CLAUDE_PTY_SMOKE=1` and the unit-level bare suite verified locally. The gated `CLAUDE_BARE_SMOKE=1` real-spawn test was hand-validated against `claude --bare` (claude 2.1.136) with a planted bootdir; the spawn produced stream-json output and the only blocker was authentication (no env-var key), which matches the documented bare-mode auth contract.

### Compatibility

- Behavior-additive. Existing callers of `NewClaudeAdapter()` / `NewClaudeAdapterDev()` / `NewClaudeAdapterPTY()` / `NewClaudeAdapterDevPTY()` produce byte-for-byte identical args (sentinel test pins this). The `CLIAdapter.BuildArgs` interface signature is unchanged.
- The `ClaudeAdapter` struct gains five additive fields (`Bare`, `MCPConfigPath`, `AppendSystemPromptFile`, `SettingsPath`, `ProjectDir`). All zero-value to off; non-bare callers that don't populate them see no behavior change.
- Source-compatibility caveat for unkeyed composite literals: any downstream code that constructs `ClaudeAdapter` positionally (e.g. `ClaudeAdapter{true, false}`) continues to compile because the new fields are appended after the existing two and default to their zero values, but consumers should prefer keyed literals (`ClaudeAdapter{SkipPermissions: true}`) or the constructor functions to remain robust against future field additions. The constructors and keyed-literal call sites in this repo are unaffected.
- `BootDirSpec()` is unchanged (same planted files, same `CwdPreference: CwdBootDir`, same trust-dialog seeding via `.claude/settings.json` `Render` closure). The new affordance is a method on `*ClaudeAdapter`, not a spec mutation.
- `renderMCPJSON("")` content change: `{}` → `{"mcpServers":{}}`. The schema is strictly more correct and accepted by both auto-discovery and `--mcp-config` paths. Callers asserting on the empty-loopback content directly need to update; the only such assertion in this repo (`TestClaudeBootDirSpec_EmptyMCP`) is updated. Codex and opencode adapters share `renderMCPJSON` and inherit the same fix transparently.

### Why v0.9.0 (not v0.8.3)

Bare mode is a meaningful capability addition: new constructors, new `BuildArgs` branch, new helper, new field surface. v0.8.x has been incremental fixes (PTY arg shape in v0.8.1, trust dialog in v0.8.2). Clean minor-version bump.

### Consumer pickup

- Bump go-providers to `v0.9.0`, swap the adapter constructor to `NewClaudeAdapterDevBare()` for scripted/subprocess-per-turn paths, and after planting via `BootDirSpec` populate the four bare fields:
  ```go
  inj := claude.BareInjectionPaths(bootDir, projectDir)
  claude.MCPConfigPath          = inj.MCPConfigPath
  claude.AppendSystemPromptFile = inj.AppendSystemPromptFile
  claude.SettingsPath           = inj.SettingsPath
  claude.ProjectDir             = inj.ProjectDir
  ```
  PTY-spawned long-lived sessions stay on `NewClaudeAdapterDevPTY()` — bare mode is print-mode-focused.
- Other PTY adapters (codex / opencode / gemini / copilot / aider / junie / kiro / qwen): each has its own scripted-call shape; downstream callers can file per-adapter follow-ups as needed.

## v0.8.2

- `BootDirSpec` for the claude adapter now pre-accepts the workspace trust dialog for the per-task bootdir. The `.claude/settings.json` planted-file `Render` closure side-effects on `~/.claude.json`'s `projects` map when `PlantContext.BootDir` is set, writing `projects[<realpath(bootDir)>] = {hasTrustDialogAccepted: true, hasCompletedProjectOnboarding: true}` via an atomic temp-file rename. Side effect is gated on a non-empty `BootDir` so existing callers that invoke `Render` for content-only purposes (unit tests, dry runs) don't pollute global state.
- Added `BootDir` field to `provider.PlantContext`, mirroring the existing `ProjectDir` field. Apps populate it from their bootdir factory; adapter `Render` closures that need to seed external state keyed on the bootdir read from this field. Documented invariant on `PlantedFile.Render`: closures MAY perform environment setup gated on `ctx.BootDir != ""`, otherwise stay pure.
- Surfaced empirically (post-v0.8.1): the long-lived PTY claude no longer dies on arg validation, but stalls indefinitely on the first-run `Quick safety check: Is this a project you created or one you trust?` dialog when the per-task tempdir is a fresh path. Per `claude --help`, the dialog auto-skips only in non-interactive mode (`-p` / piped stdout). PTY = TTY = dialog fires. `--dangerously-skip-permissions` covers per-tool permission checks, not this gate.
- Probe results: per-cwd `.claude/settings.json` does NOT honor any trust field (probed: `hasTrustDialogAccepted`, `trustDialogAccepted`, `trusted`, `workspaceTrust` — all leave the dialog firing). Trust state is canonical at `~/.claude.json` → `projects[<realpath(cwd)>].hasTrustDialogAccepted`. Path keying must use `filepath.EvalSymlinks` because claude resolves cwd via realpath on macOS (`/var/folders/…` → `/private/var/folders/…`); seeding with the unresolved path leaves the dialog firing. Binary string evidence: `checkHasTrustDialogAccepted` / `hasTrustDialogAccepted` / `resetTrustDialogAcceptedCache` symbols in claude 2.1.133+.
- Tests: seven unit tests (`TestSeedClaudeWorkspaceTrust_NewConfig`, `TestSeedClaudeWorkspaceTrust_PreservesExistingKeys`, `TestSeedClaudeWorkspaceTrust_PreservesExistingProjectKeys`, `TestSeedClaudeWorkspaceTrust_NonObjectProjectsErrors`, `TestSeedClaudeWorkspaceTrust_NonObjectEntryErrors`, `TestSeedClaudeWorkspaceTrust_MalformedConfigErrors`, `TestSeedClaudeWorkspaceTrust_RejectsEmpty`) cover create-from-scratch, key preservation across both top-level keys and nested `projects[...]` entries, refusal-on-shape-mismatch (errors when `projects` or `projects[<resolved>]` is present but not a JSON object — preserves existing data rather than silently overwriting), malformed-config refusal (does not overwrite the user's config), and empty-input rejection. Two `BootDirSpec` settings.json render tests pin the gating contract: `Render` is a no-op on `~/.claude.json` when `BootDir == ""`, and seeds the projects entry when `BootDir != ""`. A real-spawn integration smoke (`TestClaudeBootDirSpec_TrustPreAccept_Smoke`, `provider/bootdir_claude_smoke_test.go`) gated on `CLAUDE_PTY_SMOKE=1` plants the spec into a fresh tempdir, spawns claude in PTY mode, and asserts the dialog sentinels (`Quicksafetycheck`, `Isthisaprojectyoucreated`, `trustthisfolder`, `Yes,Itrustthisfolder`) do not appear in PTY output within a 4s window. Best-effort cleanup removes the `projects[bootDir]` entry on test exit so contributors don't accumulate stale entries. The render-closure tests use a `setHomeForTest` helper that sets both `HOME` (unix) and `USERPROFILE` (windows) so `os.UserHomeDir()` redirection works cross-platform. `go test -race -count=1 ./...` clean on darwin; `GOOS=linux go build/vet ./...` and `GOOS=windows go build/vet ./...` clean.
- Subprocess-per-turn (print mode) callers: byte-for-byte unchanged. The `BuildArgs` shape is identical to v0.8.1, the `--print` invocation auto-skips the trust dialog per `claude --help`, and `Render` closures only seed when `BootDir` is set — print-mode callers that never populate `BootDir` continue to be pure. Existing tests pass unchanged.

### Compatibility

- Additive: `PlantContext` gains a `BootDir` field. Existing callers constructing `PlantContext{...}` without it get the zero value, which gates the seeding side effect off. The smoke test for the v0.8.1 fix (`TestClaudeAdapter_PTYSpawn_Smoke`) continues to pass — `BuildArgs` arg shape is unchanged.
- The `BootDirProvider` interface signature is unchanged. Adapters that don't implement environment seeding (codex, opencode, stubs) are unaffected.

### Side-effect surface (option (b) per ticket)

- Trust seeding writes to `~/.claude.json` from the lib. Surface narrowed to a single key under `projects[<realpath(bootDir)>]`; other top-level keys (`oauthAccount`, `anonymousId`, etc.) and other projects entries are preserved verbatim. Test coverage pins both invariants. The helper also refuses to overwrite when `projects` or the per-path entry is present but not a JSON object — guards against future claude versions that change the shape, and against user-edited configs.
- Atomic write: temp-file-then-rename in the same directory as `~/.claude.json` so partial writes can't corrupt the file. Full payload is verified with an explicit byte-count check (`n != len(out)`) before rename so future swaps of the temp-file backend can't quietly drop bytes via short writes. Read-modify-rename is not lock-aware against concurrent claude writes; in the rare case where another claude process writes between our read and our rename, the bootdir's trust marker could be clobbered and the dialog would fire on the next spawn. Concurrency hardening (file lock on a sidecar) is filed as a follow-up; the surface is intentionally narrow today.
- Cleanup: the lib does not remove the `projects[bootDir]` entry. Boot dirs are tempdirs that consumers remove at session teardown; the stale projects entry references a non-existent path and is harmless. If accumulation becomes an issue, consumers can sweep entries whose path matches the consumer's per-task tempdir prefix on startup.

### Consumer pickup

- The trust seeding fires only when `PlantContext.BootDir` is populated. Consumers that build `provider.PlantContext{...}` without setting `BootDir` need to add `BootDir: bootDir` to the literal so the seed runs — this is a one-line change. The `PlantedFiles` iteration loop picks up file-content changes automatically; the seeding side effect depends on `PlantContext.BootDir` being populated.
- Other PTY adapters (codex / opencode / gemini / copilot / aider / junie / kiro / qwen): each has its own first-run UX (trust dialog, license acceptance, telemetry opt-in) and would need its own per-adapter handling.

## v0.8.1

- Added PTY-mode awareness to `ClaudeAdapter`. New `PTY bool` field plus `NewClaudeAdapterPTY()` and `NewClaudeAdapterDevPTY()` constructors. When `PTY=true`, `BuildArgs` emits interactive-shape args: it omits `-p`, `--print`, `--output-format`, `--verbose`, and `--system-prompt`, and ignores both the `prompt` and `systemPrompt` parameters. Optional `--resume <id>` is included when `cliSessionID != ""`, and `--dangerously-skip-permissions` is included when `SkipPermissions` is set. Subprocess-per-turn callers (`NewClaudeAdapter()` / `NewClaudeAdapterDev()`) see byte-for-byte unchanged behavior.
- Surfaced empirically: a downstream PTY-session caller invoked `BuildArgs("", systemPrompt, sessionIDPreset)` for the initial PTY spawn, so the previous always-print-mode args caused claude to exit immediately with `Error: Input must be provided either through stdin or as a prompt argument when using --print`. Per-turn payloads in PTY mode arrive via PTY stdin, and system prompts route via the boot-prompt mechanism rather than `--system-prompt`.
- Tests: PTY-mode arg-shape unit tests cover empty, skip-permissions, resume, resume+skip-permissions, and prompt/systemPrompt-ignored cases, plus a negative check that none of `-p` / `--print` / `--system-prompt` / `--output-format` / `--verbose` appear in PTY-mode argv. Pre-existing print-mode tests pass unchanged. A real-spawn smoke test (`TestClaudeAdapter_PTYSpawn_Smoke`, `provider/pty_claude_smoke_test.go`) is gated on `CLAUDE_PTY_SMOKE=1` and asserts the process survives 1s without dying on arg validation; it skips when the `claude` binary is not on PATH so CI doesn't auto-run it. `go test -race -count=1 ./...` clean on darwin; `GOOS=linux go build/vet ./...` clean.

### Compatibility

- Additive only. Existing callers of `NewClaudeAdapter()` / `NewClaudeAdapterDev()` are unaffected — they remain print-mode and produce identical args.
- The `CLIAdapter.BuildArgs` interface signature is unchanged.

### Consumer follow-ups (not landing here)

- Downstream adapter factories should branch on PTY capability to call the new `*PTY` constructors when a long-lived PTY session is wanted, and stay on the print-mode constructors otherwise.
- PTY adapters for `codex` / `opencode` / `gemini` / `copilot` / `aider` / `junie` / `kiro` / `qwen` each have their own interactive-mode question; per-adapter PTY support is not in scope here.

## v0.8.0

- Added per-line typed event taxonomy at `provider/events/` (`events.Event` interface with concrete types `Delta`, `ToolUse`, `ToolResult`, `Thinking`, `Usage`, `Done`, `Error`, `SessionID`, `SubagentSpawn`, `SubprocessStderr`, `Heartbeat`). Apps wire a callback into the spawn context via `WithEvents(ctx, cb)`; the PTYBridge / SubprocessBridge fires typed events alongside the existing `StreamEvent` channel returned by `Provider.StreamChat`. The legacy channel is unchanged; the typed surface is purely additive.
- Adapters can opt into native typed parsing via the new `EventParser` optional interface (`ParseLineEvents(line []byte) ([]events.Event, error)`). `ClaudeAdapter` and `CodexAdapter` implement it; the claude path additionally captures user-role `tool_result` blocks as `events.ToolResult` (previously dropped at the legacy `StreamEvent` layer) and emits `events.SubagentSpawn` for the `Task` tool. Adapters that don't implement `EventParser` fall back to a best-effort `StreamEvent` → typed translation via `translateStreamEvents`.
- Added `WithToolArgFingerprint(ctx, true)` opt-in privacy mode. When set, typed `events.ToolUse.Args` (and `events.SubagentSpawn.Args`) values are replaced with `sha256:<hex>` digests of their JSON-marshalled form; argument keys are preserved and `Fingerprint=true` is set on the event. Default off — full args are emitted, matching v0.7.0 behavior. Use this when logs may cross trust boundaries.
- Added `events.SubprocessStderr` for subprocess-transport stderr capture. SubprocessBridge wires `cmd.StderrPipe()` only when `WithEvents` is set; without a callback, stderr stays at its default destination (Go's exec default of `/dev/null`). PTYBridge does not emit `SubprocessStderr` because PTYs merge stderr into the tty stream at the kernel level.
- Added `events.Heartbeat` synthesized by the bridge on a configurable interval when no other typed event has fired in that window. Default interval is `DefaultHeartbeatInterval` (5s); apps can adjust via `WithHeartbeatInterval(ctx, d)` (`d <= 0` disables). Useful for "agent is alive but idle" UX indicators.
- Added `BootDirSpec()` per adapter via the new optional `BootDirProvider` interface. Each adapter exposes its per-task tempdir layout convention as read-only metadata: `PlantedFiles` (relative paths + render closures), `EnvAmendments` (with `{{.BootDir}}` / `{{.ProjectDir}}` placeholders for app-side substitution), `CwdPreference` (boot dir vs. project dir), `ProjectDirArg` (e.g. `--add-dir {{.ProjectDir}}`). Concrete specs landed for claude (`CLAUDE.md` + `boot.md` + `.claude/settings.json` + `.mcp.json`, cwd = bootDir, `--add-dir`), codex (`AGENTS.md` + `boot.md` + `.mcp.json`, cwd = bootDir, `--cd` — verify against installed codex), opencode (`agents/<name>.md` + `agents.json` + `opencode.json` + `boot.md` + `.mcp.json`, `OPENCODE_CONFIG_DIR={{.BootDir}}`, cwd = projectDir, `--dir`). Stub specs (zero-value + non-empty `Notes`) for gemini, copilot, aider, junie, kiro, qwen — the convention probe is filed as a follow-up.
- Added `AgentsMD(agent AgentInfo, mcpLoopbackURL, extras...)` shared helper that renders an `AGENTS.md` document with frontmatter (name/role/description), an H1 title, the system prompt body, and an optional "## MCP" section. Used by the codex `BootDirSpec.AGENTS.md` `PlantedFile.Render` closure by default; apps that want a custom layout can ignore it and render their own content.
- Preserved the no-silent-drop guard at `pty.go` / `subprocess.go` (`"CLI bridge cannot forward tool calls"` when only tool_use blocks arrive without text deltas) and mirrored it to the typed-events callback so consumers wired into `WithEvents` see the same sentinel as `events.Error`.
- Tests: per-adapter `ParseLineEvents` fixtures, every `BootDirSpec` planted-file render, end-to-end PTY spawn of a fake claude shell script with `WithEvents` callback assertion, fingerprint-mode SHA-256 digest verification, backward-compat snapshot showing v0.7.0 semantics preserved when `WithEvents` is not set. `go test -race -count=1 ./...` clean on darwin; `GOOS=linux go build/vet ./...` clean.

### Compatibility

- The four capabilities are entirely additive. Callers that don't import `provider/events` and don't call `WithEvents` / `WithToolArgFingerprint` / `BootDirSpec` see no behavior change vs. v0.7.0. Existing `Provider`, `ProviderWithUsage`, `CLIAdapter`, and `EventReactionPipeline` consumers are unaffected.
- The new typed-event taxonomy lives at `provider/events/` to avoid colliding with the existing `EventType` string constants (`EventDelta`, `EventToolUse`, …) in `provider/provider.go`. Both surfaces remain valid; consumers picking up typed events import the sub-package.
- `BootDirProvider` is a runtime type-assertion — apps must check `if bp, ok := adapter.(BootDirProvider); ok` and inspect `Notes` before iterating `PlantedFiles` for stub-spec adapters.

### Out of scope (filed as portfolio follow-ups)

- Probing `BootDirSpec` for gemini / copilot / aider / junie / kiro / qwen against installed CLI versions.
- Verifying the codex `--cd` flag and MCP config convention against the installed codex revision (Notes flag this).
- Verifying opencode's MCP config convention.
- Adding `events.Thinking` emission from the claude PTY adapter — the CLI's stream-json doesn't currently surface thinking blocks at the assistant level; the existing `StreamEvent.EventThinking` is from the Anthropic HTTP adapter. When the claude CLI exposes them, ParseLineEvents will fold them in.
- Updating consumer apps' `go.mod` to v0.8.0 — separate per-app work.

## v0.7.0

- Added `Cacheable` optional interface (`EstimateCacheablePrefix(ctx, req) int`) for pre-flight cacheable-prefix observability. Implemented by `*Anthropic` via a shared `buildRequestBody` helper that marshals the same payload the wire request would carry, divided by 4 for the token approximation. Callers type-assert; providers without prompt caching do not implement it.
- Added `RateLimited` optional interface (`RateLimitTPM() int`) for input-tokens-per-minute observability. Implemented by `*Anthropic`. Returns `0` when no calibration has happened yet — the contract requires implementations to treat the seeded default as "unknown" rather than surfacing it as an observed value.
- Flipped `ToolDefinition.Strict` default. Previously `nil` meant strict-on by default on the Anthropic adapter; now `nil` is non-strict and callers must explicitly set `Strict` to a pointer to `true` to opt in. Rationale: strict was being applied blanket-fashion across all tools, conflating input-shape validation (where strict adds value) with high-blast-radius permission concerns (which belong at project/session/agent-profile scope).
- Anthropic non-streaming `Complete`/`CompleteWithUsage` now honor `ChatRequest.MaxTokens`. Historical hardcoded cap was 128 tokens, which silently truncated longer completions; the default now mirrors streaming at 16384 when callers leave `MaxTokens` unset.
- Anthropic rate-budget pre-flight is now cache-aware: estimates subtract the cacheable-prefix bytes so requests with healthy cache hits don't trip `ErrRequestExceedsRateBudget` unnecessarily. Default rate-tracker seed raised from 30,000 to 50,000 TPM. The seed is overridable via the `ANTHROPIC_RATE_LIMIT_TPM` env var for callers on higher tiers.
- Tightened the cache-marker heuristic to match `"cache_control":{` (key + colon + opening brace) rather than the bare `"cache_control"` token. Prevents false positives from user content or tool-schema strings that contain the literal substring `cache_control` and would otherwise produce a spurious cacheable-prefix offset.
- Added structured `slog` logging on rate-limit calibration. Logs only on first calibration or real tier transitions; same-value re-calibrations are silent so the signal stays meaningful.
- Added regression tests: cache-marker false-positive guard, `RateLimitTPM` returns 0 pre-calibration and the header value post-calibration, `CompleteWithUsage` default `max_tokens=16384` and caller-supplied `MaxTokens` forwarding.
- Doc updates: `ChatRequest.MaxTokens` flagged as Anthropic-only today (OpenAI/Azure/Gemini/Mistral/Ollama/OpenRouter/OpenZen/PTY adapters silently ignore it pending future passthrough work); `ToolDefinition.Strict` documents the breaking-default change with rationale; `RateLimited.RateLimitTPM` doc tightened to require pre-calibration `0`.

### Compatibility

- `ToolDefinition.Strict` default change is the only behavior-breaking item. Callers that relied on `nil`-as-strict-on must set `Strict: &true` per tool where Anthropic's server-side schema enforcement is wanted.
- `Cacheable` and `RateLimited` are additive optional interfaces. Existing `Provider` callers are unaffected; new callers type-assert when they want the observability hooks.
- `RateLimitTPM` returning `0` pre-calibration is a new contract — telemetry callers should treat `0` as "unknown" and skip emitting the limit field rather than reporting a guess.

## v0.5.1

- Added Anthropic interleaved-thinking-2025-05-14 support: new `ReasoningConfig` (with `Enabled`, `BudgetTokens`, `BetasHeader`) plumbed via `WithReasoningConfig` / `ReasoningConfigFromContext`. The Anthropic adapter sends the `interleaved-thinking-2025-05-14` beta header and `thinking_config` request parameter as a pair, gated on `BudgetTokens > 0` AND a supported model.
- Added `EventThinking` stream event and `ThinkingBlock` payload (`Thinking` text + cryptographic `Signature`). The Anthropic adapter parses `thinking_delta` + `signature_delta` SSE blocks and emits a complete `EventThinking` on `content_block_stop`. Signatures must round-trip verbatim on subsequent turns; assistant-message marshaling preserves them via the new `Signature` field on `ContentBlock`.
- Added `modelSupportsInterleavedThinking` feature detection. Accepts the canonical `claude-{opus|sonnet|haiku}-4[-<minor>]-<YYYYMMDD>` shape and requires the trailing date ≥ `20250514` (`minInterleavedThinkingModelDate`). Structured matching avoids `strings.Contains` false positives like `claude-opus-40-*`.
- Fixed `marshalMessagesWithCacheCount` cache_control handling for messages ending with a thinking block (e.g. `[text, thinking]`). Previously, cache_control was skipped entirely when the last block was thinking; now `lastNonThinkingIdx` is computed and cache_control attaches to the last non-thinking block.
- Tightened the interleaved-thinking gate: `Enabled=true` with `BudgetTokens=0` no longer sends the beta header (which would have been a silent no-op without `thinking_config`). Extracted as `shouldEnableInterleavedThinking` helper. `ReasoningConfig.BudgetTokens` doc updated to require `> 0` for reasoning to actually be requested.
- Added regression tests: model-detection boundaries (false-prefix, pre-min-date, year-mismatch, non-numeric-minor, trailing-garbage); gate combinations (full enable, BudgetTokens=0, Enabled=false, missing/wrong header, unsupported model); cache_control with `[text, thinking]`, `[thinking, text]`, all-thinking, and cached-multi-block shapes; SSE thinking-delta accumulation + disabled-path no-op; thinking-block round-trip serialization.

### Compatibility

- `ContentBlock` gains a `Signature` field (only set on `type="thinking"` blocks). Existing callers of `ContentBlock` that don't construct thinking blocks are unaffected.
- `StreamEvent` gains a `ThinkingBlock` field; existing event consumers that switch on `Type` can ignore `EventThinking` until they're ready to consume thinking deltas.

## v0.5.0

- Introduced distinct named type `EventType` with canonical constants (`EventDelta`, `EventToolUse`, `EventUsage`, `EventError`, `EventDone`, `EventSessionID`); `StreamEvent.Type` is now compiler-enforced and consumers should use the named constants instead of string literals.
- Added free helper `IsTurnComplete(ev StreamEvent) bool` for terminal-event detection — universal across all CLI adapters; no `CLIAdapter` interface change.
- Both `PTYBridge` and `SubprocessBridge` now guarantee exactly one terminal event before channel close (adapter passthrough → ctx-cancel error → non-zero-exit error → synthetic `EventDone` on clean exit). The no-silent-drop guard's `EventError` now **replaces** the adapter's `EventDone` rather than preceding it, restoring the "either is terminal" contract.
- Added grace-period termination via `cmd.Cancel = SIGTERM` + `cmd.WaitDelay` in both spawn paths, replacing the manual goroutine race in `PTYBridge.killProcess` (removed) and the bare `cmd.Process.Kill()` in `SubprocessBridge`. Configurable via new `WithWaitDelay(ctx, d)` / `WaitDelayFromContext(ctx)` context helpers; `DefaultWaitDelay = 5 * time.Second`.
- Added per-adapter docstrings documenting turn-boundary semantics across Claude / Qwen / Gemini / Junie / Codex / Aider / Kiro / Copilot.
- Fixed `Gemini.readSSE` emitting `EventDone` then potentially `EventError`; now emits exactly one terminal event ordered correctly.
- Added regression tests: per-adapter golden-line `IsTurnComplete` fixtures (with explicit Copilot EOF outlier subtest), `EventType` constant-pinning, SIGTERM-then-SIGKILL ordering with explicit lower-bound + upper-bound timing assertions, synthetic-`EventDone`-on-clean-exit.

### Compatibility

- `StreamEvent.Type` changes from `string` to a distinct named `EventType`. Existing untyped string-literal comparisons (e.g. `ev.Type == "delta"`) still compile because Go's untyped-constant assignability rules permit it, but new code should use the named constants for compiler-enforced safety.
- The bridge terminal-event guarantee is a new contract: consumers can rely on the channel-final event being turn-terminal (`IsTurnComplete(ev) == true`). Callers that previously inspected exit codes separately to detect turn boundaries can simplify.
- Removed `PTYBridge.killProcess` (was internal). The grace-period behavior it implemented is now stdlib-driven via `cmd.Cancel` + `cmd.WaitDelay`.

## v0.4.0

- Added optional `ProviderWithUsage.CompleteWithUsage(ctx, req) (CompleteResult, error)` and `CompleteResult` so non-streaming completions can return token usage while preserving the existing `Provider` interface and `Complete()` call sites.
- Updated Anthropic, OpenAI, Azure OpenAI, Gemini, Mistral, OpenRouter, OpenZen, Ollama, subprocess, PTY, and event-pipeline adapters to preserve non-streaming usage metadata.
- Standardized the package documentation, added a local Anthropic tracing helper, removed the out-of-tree `replace` directive, added an MIT `LICENSE`, and added runnable examples.

## v0.1.0

- Added `Registry.Unregister(name) bool` to support plugin hot-unload.
- `Registry` is now safe for concurrent use (internal `sync.RWMutex`).
