# Changelog

## Unreleased

## v0.8.2

- `BootDirSpec` for the claude adapter now pre-accepts the workspace trust dialog for the per-task bootdir. The `.claude/settings.json` planted-file `Render` closure side-effects on `~/.claude.json`'s `projects` map when `PlantContext.BootDir` is set, writing `projects[<realpath(bootDir)>] = {hasTrustDialogAccepted: true, hasCompletedProjectOnboarding: true}` via an atomic temp-file rename. Side effect is gated on a non-empty `BootDir` so existing callers that invoke `Render` for content-only purposes (unit tests, dry runs) don't pollute global state.
- Added `BootDir` field to `provider.PlantContext`, mirroring the existing `ProjectDir` field. Apps populate it from their bootdir factory; adapter `Render` closures that need to seed external state keyed on the bootdir read from this field. Documented invariant on `PlantedFile.Render`: closures MAY perform environment setup gated on `ctx.BootDir != ""`, otherwise stay pure.
- Surfaced by clockwork-manifold S2.5 plan-execute smoke retry on 2026-05-08 (post-v0.8.1, session `SES-01KR4KG32X6EC2GC3KMF2M644F`): the long-lived PTY claude no longer dies on arg validation, but stalls indefinitely on the first-run `Quick safety check: Is this a project you created or one you trust?` dialog because each clockwork session uses a fresh `clockwork-boot-claude-...` tempdir as cwd. Per `claude --help`, the dialog auto-skips only in non-interactive mode (`-p` / piped stdout). PTY = TTY = dialog fires. `--dangerously-skip-permissions` covers per-tool permission checks, not this gate.
- Probe results (recorded in `agent-workspaces/execution/go-providers/2026-05-08-claude-pty-trust/implementer-report.md`): per-cwd `.claude/settings.json` does NOT honor any trust field (probed: `hasTrustDialogAccepted`, `trustDialogAccepted`, `trusted`, `workspaceTrust` — all leave the dialog firing). Trust state is canonical at `~/.claude.json` → `projects[<realpath(cwd)>].hasTrustDialogAccepted`. Path keying must use `filepath.EvalSymlinks` because claude resolves cwd via realpath on macOS (`/var/folders/…` → `/private/var/folders/…`); seeding with the unresolved path leaves the dialog firing. Binary string evidence: `checkHasTrustDialogAccepted` / `hasTrustDialogAccepted` / `resetTrustDialogAcceptedCache` symbols in claude 2.1.133+.
- Tests: five unit tests (`TestSeedClaudeWorkspaceTrust_NewConfig`, `TestSeedClaudeWorkspaceTrust_PreservesExistingKeys`, `TestSeedClaudeWorkspaceTrust_PreservesExistingProjectKeys`, `TestSeedClaudeWorkspaceTrust_MalformedConfigErrors`, `TestSeedClaudeWorkspaceTrust_RejectsEmpty`) cover create-from-scratch, key preservation across both top-level keys and nested `projects[...]` entries, malformed-config refusal (does not overwrite the user's config), and empty-input rejection. Two `BootDirSpec` settings.json render tests pin the gating contract: `Render` is a no-op on `~/.claude.json` when `BootDir == ""`, and seeds the projects entry when `BootDir != ""`. A real-spawn integration smoke (`TestClaudeBootDirSpec_TrustPreAccept_Smoke`, `provider/bootdir_claude_smoke_test.go`) gated on `CLAUDE_PTY_SMOKE=1` plants the spec into a fresh tempdir, spawns claude in PTY mode, and asserts the dialog sentinels (`Quicksafetycheck`, `Isthisaprojectyoucreated`, `trustthisfolder`, `Yes,Itrustthisfolder`) do not appear in PTY output within a 4s window. Best-effort cleanup removes the `projects[bootDir]` entry on test exit so contributors don't accumulate stale entries. `go test -race -count=1 ./...` clean on darwin; `GOOS=linux go build/vet ./...` clean.
- Subprocess-per-turn (print mode) callers: byte-for-byte unchanged. The `BuildArgs` shape is identical to v0.8.1, the `--print` invocation auto-skips the trust dialog per `claude --help`, and `Render` closures only seed when `BootDir` is set — print-mode callers that never populate `BootDir` continue to be pure. Existing tests pass unchanged.

### Compatibility

- Additive: `PlantContext` gains a `BootDir` field. Existing callers constructing `PlantContext{...}` without it get the zero value, which gates the seeding side effect off. The smoke test for the v0.8.1 fix (`TestClaudeAdapter_PTYSpawn_Smoke`) continues to pass — `BuildArgs` arg shape is unchanged.
- The `BootDirProvider` interface signature is unchanged. Adapters that don't implement environment seeding (codex, opencode, stubs) are unaffected.

### Side-effect surface (option (b) per ticket)

- Trust seeding writes to `~/.claude.json` from the lib. Surface narrowed to a single key under `projects[<realpath(bootDir)>]`; other top-level keys (`oauthAccount`, `anonymousId`, etc.) and other projects entries are preserved verbatim. Test coverage pins both invariants.
- Atomic write: temp-file-then-rename in the same directory as `~/.claude.json` so partial writes can't corrupt the file. Read-modify-rename is not lock-aware against concurrent claude writes; in the rare case where another claude process writes between our read and our rename, the bootdir's trust marker could be clobbered and the dialog would fire on the next spawn. Concurrency hardening (file lock on a sidecar) is filed as a follow-up; the surface is intentionally narrow today.
- Cleanup: the lib does not remove the `projects[bootDir]` entry. Bootdirs are tempdirs that consumers remove at session teardown; the stale projects entry references a non-existent path and is harmless. If accumulation becomes an issue, consumers can sweep entries whose path matches the `clockwork-boot-claude-` prefix on startup.

### Consumer pickup

- `clockwork-manifold` (CW-20260508-0007 follow-up): `internal/runtime/agent/bootdir.go` builds `provider.PlantContext{...}` without setting `BootDir`. Add `BootDir: bootDir` to the `plantCtx` literal so the trust seed fires. This is a one-line change. Discovered during this ticket — the boot prompt's note that "consumers pick up the new spec automatically" via `PlantedFiles` iteration is true for the file-content half of the change but not for the seeding side effect, which depends on `PlantContext.BootDir` being populated.
- Other PTY adapters (codex / opencode / gemini / copilot / aider / junie / kiro / qwen): each has its own first-run UX (trust dialog, license acceptance, telemetry opt-in). Filed as separate per-adapter follow-ups.

## v0.8.1

- Added PTY-mode awareness to `ClaudeAdapter`. New `PTY bool` field plus `NewClaudeAdapterPTY()` and `NewClaudeAdapterDevPTY()` constructors. When `PTY=true`, `BuildArgs` emits interactive-shape args: it omits `-p`, `--print`, `--output-format`, `--verbose`, and `--system-prompt`, and ignores both the `prompt` and `systemPrompt` parameters. Optional `--resume <id>` is included when `cliSessionID != ""`, and `--dangerously-skip-permissions` is included when `SkipPermissions` is set. Subprocess-per-turn callers (`NewClaudeAdapter()` / `NewClaudeAdapterDev()`) see byte-for-byte unchanged behavior.
- Surfaced by clockwork-manifold S2.5 plan-execute smoke retry on 2026-05-08: `go-agent-sessions/agentsessions/pty_session.go` calls `BuildArgs("", systemPrompt, sessionIDPreset)` for the initial PTY spawn, so the previous always-print-mode args caused claude to exit immediately with `Error: Input must be provided either through stdin or as a prompt argument when using --print`. Per-turn payloads in PTY mode arrive via PTY stdin (the lib's `BootMode=stdin` mechanism), and system prompts route via `BootPrompt` rather than `--system-prompt`.
- Tests: PTY-mode arg-shape unit tests cover empty, skip-permissions, resume, resume+skip-permissions, and prompt/systemPrompt-ignored cases, plus a negative check that none of `-p` / `--print` / `--system-prompt` / `--output-format` / `--verbose` appear in PTY-mode argv. Pre-existing print-mode tests pass unchanged. A real-spawn smoke test (`TestClaudeAdapter_PTYSpawn_Smoke`, `provider/pty_claude_smoke_test.go`) is gated on `CLAUDE_PTY_SMOKE=1` and asserts the process survives 1s without dying on arg validation; it skips when the `claude` binary is not on PATH so CI doesn't auto-run it. `go test -race -count=1 ./...` clean on darwin; `GOOS=linux go build/vet ./...` clean.

### Compatibility

- Additive only. Existing callers of `NewClaudeAdapter()` / `NewClaudeAdapterDev()` are unaffected — they remain print-mode and produce identical args.
- The `CLIAdapter.BuildArgs` interface signature is unchanged.

### Consumer follow-ups (not landing here)

- `clockwork-manifold` (CW-20260508-0005): branch the adapter factory in `internal/runtime/agent/factory.go` on PTY caps to call the new `*PTY` constructors; revert the `pty: false` workaround on the 5 claude profiles in `profiles.yaml`.
- `agent-mux`: future migration from internal `claudecode` runtime to this adapter (tracked separately).
- PTY adapters for `codex` / `opencode` / `gemini` / `copilot` / `aider` / `junie` / `kiro` / `qwen`: each has its own interactive-mode question; not in scope here.

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
- Updating consumer apps' `go.mod` to v0.8.0 — separate per-app work in nanite, agent-mux, clockwork-manifold.

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
