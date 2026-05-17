# Changelog

## Unreleased

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
