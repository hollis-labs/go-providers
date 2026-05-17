# go-providers

`go-providers` is a Go library that provides a single `Provider` interface over a collection of CLI-bridge adapters (Claude Code, Codex, Gemini CLI, Aider, Copilot, Junie, Kiro, Opencode, Qwen) wrapped via PTY or plain subprocess. It also ships cross-cutting primitives for the adapter layer — a registry, cost monitoring, scope guarding, progress-loop detection, per-line typed events, boot-dir spec metadata, and a decorator pipeline that layers monitors on top of any underlying provider.

As of **v0.11.0** this library is **CLI/PTY-only** and no longer owns the shared LLM contracts or rate-budget primitives. Direct HTTP chat and embedding adapters were removed in v0.10.0; the shared transport-agnostic model types now live in `github.com/hollis-labs/go-llm-types`, and the shared provider contracts/rate-budget primitives live in `github.com/hollis-labs/go-llm-contracts`.

## Status

Beta — the core `Provider` interface is stable. Public API is intentionally narrow now that the lib is CLI/PTY-only; smoke tests for the claude PTY/bare paths exist behind env-gated `*_SMOKE` tests.

## Install

```bash
go get github.com/hollis-labs/go-providers
```

## Usage

The minimum viable shape is: pick an adapter, materialize its
`BootDirSpec` into a per-task tempdir, wire the adapter's flag fields
from the planted layout, and call `StreamChat` through a transport
bridge.

```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    llmtypes "github.com/hollis-labs/go-llm-types"
    "github.com/hollis-labs/go-providers/provider"
)

func main() {
    // 1. Construct the adapter (bare-mode Claude in this example).
    adapter := provider.NewClaudeAdapterBare()

    // 2. Materialize the BootDirSpec into a fresh tempdir.
    bootDir, _ := os.MkdirTemp("", "boot-claude-*")
    defer os.RemoveAll(bootDir)

    projectDir, _ := filepath.Abs(".")

    spec := adapter.BootDirSpec()
    pctx := provider.PlantContext{
        SystemPrompt: "You are a terse assistant.",
        ProjectDir:   projectDir,
        BootDir:      bootDir,
    }
    for _, pf := range spec.PlantedFiles {
        content, _ := pf.Render(pctx)
        dst := filepath.Join(bootDir, pf.RelPath)
        os.MkdirAll(filepath.Dir(dst), 0o755)
        os.WriteFile(dst, []byte(content), 0o644)
    }

    // 3. Wire bare-mode flag values from the planted layout.
    inj := adapter.BareInjectionPaths(bootDir, projectDir)
    adapter.MCPConfigPath = inj.MCPConfigPath
    adapter.AppendSystemPromptFile = inj.AppendSystemPromptFile
    adapter.SettingsPath = inj.SettingsPath
    adapter.ProjectDir = inj.ProjectDir

    // 4. Detect the binary and stream a chat turn.
    cliPath, _ := adapter.Detect()
    bridge := provider.NewSubprocessBridge(adapter, cliPath)

    stream, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{
        Model: "claude-sonnet-4-5",
        Messages: []llmtypes.ChatMessage{
            {Role: "user", Content: "Say hello in one short sentence."},
        },
    })
    if err != nil {
        panic(err)
    }
    for ev := range stream {
        switch ev.Type {
        case llmtypes.EventDelta:
            fmt.Print(ev.Content)
        case llmtypes.EventError:
            fmt.Println("error:", ev.Error)
        case llmtypes.EventDone:
            return
        }
    }
}
```

Three runnable end-to-end programs (claude bare, codex, opencode) live
under `examples/`; see `examples/README.md` for the differences and
auth-setup notes.

### Bare-mode auth

Bare-mode Claude requires either `ANTHROPIC_API_KEY` in env *or* an
`apiKeyHelper` executable referenced from the planted
`.claude/settings.json`. Set `adapter.ApiKeyHelperPath = "/path/to/bin"`
before iterating `BootDirSpec().PlantedFiles`; the helper is invoked
per request and its first line of stdout is consumed as the bearer
token. Use this for OAuth-via-keychain auth or any other
per-environment secret store.

### Permission mode

`ClaudeAdapter.PermissionMode` sets `permissions.defaultMode` in the
planted `.claude/settings.json` — the full Claude Code vocabulary:
`default`, `acceptEdits`, `plan`, `bypassPermissions`. Set it before
iterating `BootDirSpec().PlantedFiles`:

```go
adapter := provider.NewClaudeAdapter()
adapter.PermissionMode = "acceptEdits"
```

When `PermissionMode` is empty the back-compat behavior holds:
`SkipPermissions == true` still plants `bypassPermissions`, and
otherwise no `permissions` block is planted. A non-empty
`PermissionMode` wins over that default; an unrecognized value fails
the planted-file render.

## API Overview

### Core interface (`provider/provider.go`)

- `Provider` — interface: `StreamChat`, `Complete`, `Capabilities`.
- `ProviderCapabilities` — struct describing streaming, tool calling, caching, image input, `MaxTokens`, `ContextWindowSize`.
- `ChatMessage`, `ContentBlock`, `ToolDefinition`, `ToolUseBlock`, `StreamEvent`, `Usage`, `CompleteResult`, `ThinkingBlock` — message, event, and result shapes.
- `WithCLISessionID` / `CLISessionIDFromContext`, `WithSandboxDir` / `SandboxDirFromContext`, `WithProcessCallback` / `ProcessCallbackFromContext`, `WithActivityCallback` / `ActivityCallbackFromContext`, `WithWaitDelay` / `WaitDelayFromContext` — context-value helpers used by the PTY and subprocess bridges.

### Registry (`provider/registry.go`)

- `Registry` — map from name to `Provider`. Safe for concurrent use.
- `NewRegistry`, `Register`, `Unregister`, `Get`, `Has`, `Names`.

### CLI bridges and adapters

- `CLIAdapter` interface and `CLIConfig` struct (`cli_adapter.go`) — abstraction for spawning a CLI tool.
- `PTYBridge` / `NewPTYBridge` / `NewPTYBridgeWithAdapter` (`pty.go`, non-Windows build tag) — wraps a CLI in a pseudo-terminal.
- `SubprocessBridge` / `NewSubprocessBridge` (`subprocess.go`) — wraps a CLI using plain stdin/stdout pipes (all platforms).
- Adapters (one file each): `ClaudeAdapter`, `CodexAdapter`, `GeminiAdapter`, `AiderAdapter`, `CopilotAdapter`, `JunieAdapter`, `KiroAdapter`, `OpencodeAdapter`, `QwenAdapter`. Each ships `New…Adapter()` plus PTY/Dev/Bare variants where applicable. `ClaudeAdapter` additionally ships `NewClaudeAdapterStreamingStdio()` / `NewClaudeAdapterDevStreamingStdio()` for vendor-documented long-lived NDJSON-over-stdin sessions; `CodexAdapter` ships `NewCodexAdapterAppServer()` for long-lived JSON-RPC-over-stdio sessions. See [Long-lived headless modes](#long-lived-headless-modes).

### Per-line typed events (`provider/events/`)

In addition to the legacy `<-chan StreamEvent` returned by `Provider.StreamChat`, CLI/PTY bridges can fire a richer typed-event taxonomy when a callback is wired into the spawn context. The two surfaces are parallel: typed events do not replace `StreamEvent`; they augment it with information the legacy union struct can't carry (per-tool `ToolResult`, sub-agent spawn detection, `SubprocessStderr` lines, `Heartbeat` ticks, signed `Thinking` blocks, `Usage` blocks).

```go
import "github.com/hollis-labs/go-providers/provider/events"

ctx := provider.WithEvents(ctx, func(ev events.Event) {
    switch e := ev.(type) {
    case events.Delta:
        // streaming text fragment; e.Phase is "narration", "final", or "thinking"
    case events.ToolUse:
        // e.Name + e.Args (or sha256-digested keys when WithToolArgFingerprint is on)
    case events.ToolResult:
        // result of a previous ToolUse with matching e.ID
    case events.SubagentSpawn:
        // claude's "Task" tool emits this in addition to ToolUse
    case events.SubprocessStderr:
        // subprocess transport only — PTYs merge stderr into stdout
    case events.Heartbeat:
        // synthesized when no other event has fired for the configured interval
    case events.Usage:
        // token accounting carried per-turn for adapters that report it (claude)
    case events.Thinking:
        // signed thinking block (claude interleaved thinking)
    case events.Done:
        // turn-terminal success
    case events.Error:
        // turn-terminal failure with e.Err (Go error) and/or e.Message
    }
})
stream, _ := bridge.StreamChat(ctx, req) // legacy channel still works
for ev := range stream { /* ... */ }
```

`WithToolArgFingerprint(ctx, true)` swaps `events.ToolUse.Args` values for `sha256:<hex>` digests of their JSON-marshalled form (keys preserved). Use this when logs may cross trust boundaries; default is off.

`WithHeartbeatInterval(ctx, d)` adjusts the heartbeat cadence (`DefaultHeartbeatInterval` is 5s; non-positive disables).

Adapters can implement the optional `EventParser` interface (`ParseLineEvents(line []byte) ([]events.Event, error)`) to produce typed events natively from the wire format. `ClaudeAdapter` and `CodexAdapter` do; the claude path additionally surfaces user-role `tool_result` blocks and `Task` sub-agent spawns. Adapters without `EventParser` get a best-effort `StreamEvent` → typed translation.

### Boot dir specs (`BootDirProvider` / `BootDirSpec`)

Each CLI adapter can expose its per-task tempdir layout convention as read-only metadata. Apps loop over the spec instead of writing per-provider switch statements.

```go
if bp, ok := adapter.(provider.BootDirProvider); ok {
    spec := bp.BootDirSpec()
    if spec.Notes != "" {
        // stub spec — verify or fall back to bespoke planting
    }
    pctx := provider.PlantContext{
        SystemPrompt:   "...",
        BootContent:    "Read @./instructions.md and start.",
        AgentName:      "orchestrator",
        MCPLoopbackURL: "http://localhost:9999/mcp",
        ProjectDir:     "/work/project",
    }
    for _, pf := range spec.PlantedFiles {
        content, err := pf.Render(pctx)
        // apps materialize: filepath.Join(bootDir, pf.RelPath), content
    }
    // apps substitute {{.BootDir}} / {{.ProjectDir}} in spec.EnvAmendments + spec.ProjectDirArg
    cwd := spec.SpawnWorkdir(bootDir, projectDir) // honors CwdPreference
}
```

| Adapter | Status | Layout |
|---|---|---|
| claude | concrete | `CLAUDE.md` + `boot.md` + `.claude/settings.json` + `.mcp.json`, cwd = bootDir, `--add-dir {{.ProjectDir}}` |
| codex | concrete | `AGENTS.md` + `boot.md` + `config.toml` + `auth.json` + `.mcp.json`, cwd = bootDir, `--cd {{.ProjectDir}}` (exec mode) |
| opencode | concrete | `agents/<name>.md` + `agents.json` + `opencode.json` + `boot.md` + `.mcp.json`, `OPENCODE_CONFIG_DIR={{.BootDir}}`, cwd = projectDir, `--dir {{.ProjectDir}}` |
| gemini, copilot, aider, junie, kiro, qwen | stub | zero-value spec; `Notes` describes the probe needed |

`AgentsMD(AgentInfo, mcpLoopbackURL, extras...)` renders the default AGENTS.md document used by the codex spec; apps that want a custom layout can ignore it and render directly from their `PlantedFile.Render` closure.

The codex `config.toml` always carries an `approval_policy` / `sandbox_mode` header, controlled by `CodexAdapter.ApprovalPolicy` / `CodexAdapter.SandboxMode` (the codex analogue of `ClaudeAdapter.PermissionMode`). The defaults are `never` / `workspace-write` — NOT codex's interactive defaults — because a `BootDirSpec` is a headless boot with no TTY: a codex that prompts for approval under a headless runtime (codex `app-server` emits a JSON-RPC approval request) blocks forever.

codex reads its MCP servers from `config.toml [mcp_servers.*]` — it has no `.mcp.json` sidecar — so every MCP server it sees has to be co-rendered into that one file. Beyond the per-task loopback (`PlantContext.MCPLoopbackURL`) and the mux aggregator (`PlantContext.Mux*`), a consumer adds its own servers via `PlantContext.MCPServers` (`[]MCPServerSpec` — name + an HTTP-URL or stdio command). The codex `config.toml` renderer emits a `[mcp_servers.<name>]` block for each, so `config.toml` stays single-owner and no consumer post-processes the planted file. The names `loopback` and `mux` are reserved.

### Long-lived headless modes

Two adapter shapes target vendor-documented long-lived headless lifecycles. Both emit argv only; the runtime that owns the I/O loop, session-id handling, and attach fan-out lives upstream in `go-agent-sessions`.

**Claude — `streamingStdio`** (Anthropic calls it "Streaming Input Mode"):

```go
adapter := provider.NewClaudeAdapterStreamingStdio()
adapter.BuildArgs("", "", "")
// → ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"]
```

One long-lived `claude -p` process reads NDJSON `{type:"user",...}` messages from stdin, replies with stream-json on stdout, and reuses its KV-cache across turns until stdin EOF. Per-turn payloads + system context flow over stdin, not argv — so the positional prompt and `--system-prompt` parameters are intentionally dropped from `BuildArgs` in this mode. Use `--resume <id>` (passed as `cliSessionID`) for cold-start / crash recovery; it is not the long-lived primary.

**Codex — `app-server`**:

```go
adapter := provider.NewCodexAdapterAppServer()
adapter.BuildArgs("", "", "")
// → ["app-server"]
```

One long-lived `codex app-server` process speaking JSON-RPC 2.0 over stdio (default `--listen stdio://`). Same engine that backs OpenAI's official VS Code extension. Threads live in memory until 30-min idle; `thread/start` and `thread/resume` are JSON-RPC methods, not CLI flags — so all per-turn params are intentionally dropped from `BuildArgs`. `ParseLine` returns no events in this mode; the consumer runtime owns JSON-RPC framing, request/response correlation, and event mapping.

The underlying `InputMode` field (`ClaudeAdapter`) and `Mode` field (`CodexAdapter`) are public for callers that want to compose these flags onto custom adapter configurations. Default zero values preserve the existing print-mode / exec-mode behavior byte-for-byte.

### Reliability primitives

- `CircuitBreaker`, `CircuitState` (`CircuitClosed`/`CircuitOpen`/`CircuitHalfOpen`), `NewCircuitBreaker`, `DefaultCooldown` (`circuit.go`).
- `PacingWait`, `ErrRequestExceedsRateBudget` (`ratelimit.go`) — generic time-based pacing with periodic status callbacks.

### Monitoring + event-reaction pipeline

- `EventReactionPipeline` / `NewEventReactionPipeline` / `EventReactionConfig` / `DefaultEventReactionConfig` (`event_pipeline.go`) — decorator that wraps any `Provider` and runs each streamed event through the monitors below.
- `ScopeGuard`, `ScopeViolation`, `NewScopeGuard` (`scope_guard.go`) — glob/regex-based allow-list over file paths and tool usage.
- `ProgressTracker`, `ProgressLoop`, `NewProgressTracker` (`progress_tracker.go`) — detects repeated content, repeated tool calls with the same input, and repeated state.
- `CostMonitor`, `BudgetViolation`, `CostRate`, `UsageSummary`, `NewCostMonitor` (`cost_monitor.go`) — token and USD budget tracking.

### Model selection (`provider/model_ops.go`)

- `ModelSelector` interface, `StaticModelSelector`, `NewStaticModelSelector`, `OperationModelConfig`, operation constants `OpChat` and `OpSummarization`.

## Architecture Notes

The package is intentionally flat: one Go package (`provider`) under `provider/`, one file per adapter. The shared `Provider` interface in `provider.go` is small (three methods). Cross-cutting features — circuit breaking, rate pacing, cost/scope/loop monitoring — are expressed either as adapter-implemented behavior or as a decorator (`EventReactionPipeline`) that can wrap any `Provider` without the adapter needing to know.

CLI bridges use a two-level abstraction: a `CLIAdapter` (one per CLI tool) defines how to build arguments and parse one line of output, and a transport wrapper (`PTYBridge` for pty-based or `SubprocessBridge` for pipes) runs the child process and feeds lines through the adapter. Context-value helpers (`WithCLISessionID`, `WithSandboxDir`, `WithProcessCallback`, `WithActivityCallback`, `WithWaitDelay`) let callers pass session-resume IDs, working directories, and process-tracking hooks through to the bridge without widening the `Provider` interface. `pty.go` has a `//go:build !windows` build tag; the subprocess bridge is the portable fallback.

## Dependencies

### Framework-internal

- None.

### External (direct)

- `github.com/creack/pty v1.1.24` — pseudo-terminal support for the PTY bridge.

### External (indirect)

- None.

## Testing

```bash
go test ./...
```

Tests are pure-Go unit tests. PTY/subprocess tests do not spawn real CLI binaries by default — they exercise the adapter arg-building and line-parsing logic directly. Real-spawn smoke tests (`TestClaudeAdapter_BareSpawn_Smoke`, `TestClaudeAdapter_BareSpawn_PopulatedMCP_Smoke`, `TestClaudeAdapter_PTYSmoke`, `TestClaudeAdapter_BootDirSmoke`) are env-gated (`CLAUDE_BARE_SMOKE=1`, `CLAUDE_PTY_SMOKE=1`, etc.); skipped when the relevant CLI binary or auth env var is absent.

## License

MIT License. See `LICENSE`.
