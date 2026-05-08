# go-providers

`go-providers` is a Go library that provides a single `Provider` interface over a collection of LLM provider adapters (Anthropic, OpenAI, Gemini, Mistral, Azure OpenAI, OpenRouter, OpenZen, Ollama) and CLI-bridge adapters that wrap interactive coding CLIs (Claude Code, Codex, Gemini CLI, Aider, Copilot, Junie, Kiro, Qwen) via PTY or plain subprocess. It also ships cross-cutting primitives used by those adapters - a registry, retry/backoff, circuit breaker, token-rate tracker, prompt-cache hints, cost monitoring, scope guarding, progress-loop detection, and a decorator pipeline that layers those monitors on top of any underlying provider.

## Status

Beta — the core `Provider` interface is stable enough to have 29 test files exercising it and every adapter, but the surface is intentionally broad and still has a few evolving adapter details. See `AUDIT_RESULTS.md`.

## Install

```bash
go get github.com/hollis-labs/go-providers
```

## Usage

Construct a provider, register it, and stream a chat response. Adapted from patterns in `provider/capabilities_test.go` and the public interface defined in `provider/provider.go`:

```go
package main

import (
    "context"
    "fmt"

    "github.com/hollis-labs/go-providers/provider"
)

func main() {
    reg := provider.NewRegistry()

    anth := provider.NewAnthropic()
    anth.SetAPIKey("sk-ant-...") // or read from ANTHROPIC_API_KEY yourself
    reg.Register("anthropic", anth)

    p, _ := reg.Get("anthropic")

    ctx := context.Background()
    req := provider.ChatRequest{
        Model:        "claude-sonnet-4-5",
        SystemPrompt: "You are concise.",
        Messages: []provider.ChatMessage{
            {Role: "user", Content: "Say hello in one short sentence."},
        },
    }

    stream, err := p.StreamChat(ctx, req)
    if err != nil {
        panic(err)
    }
    for ev := range stream {
        switch ev.Type {
        case "delta":
            fmt.Print(ev.Content)
        case "error":
            fmt.Println("error:", ev.Error)
        case "done":
            return
        }
    }
}
```

For non-streaming callers that need token accounting, type-assert to `ProviderWithUsage` and use `CompleteWithUsage`:

```go
if pwu, ok := p.(provider.ProviderWithUsage); ok {
    result, err := pwu.CompleteWithUsage(ctx, req)
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Text)
    if result.Usage != nil {
        fmt.Printf("input=%d output=%d\n", result.Usage.InputTokens, result.Usage.OutputTokens)
    }
}
```

Providers that also implement `Embedder` (OpenAI, Azure OpenAI, Gemini, Mistral, Ollama) can be type-asserted for embedding calls:

```go
if e, ok := p.(provider.Embedder); ok {
    res, _ := e.Embed(ctx, "hello world", "text-embedding-3-small")
    _ = res.Embedding
}
```

## API Overview

### Core interface (`provider/provider.go`)

- `Provider` — interface: `StreamChat`, `Complete`, `Capabilities`.
- `ProviderWithUsage` — optional extension interface: `CompleteWithUsage`.
- `ProviderCapabilities` — struct describing streaming, tool calling, caching, embedding, image input, `MaxTokens`, `ContextWindowSize`, and embedding defaults.
- `ChatMessage`, `ContentBlock`, `ToolDefinition`, `ToolUseBlock`, `StreamEvent`, `Usage`, `CompleteResult` — message, event, and non-streaming result shapes.
- `WithCLISessionID` / `CLISessionIDFromContext`, `WithSandboxDir` / `SandboxDirFromContext`, `WithProcessCallback` / `ProcessCallbackFromContext`, `WithActivityCallback` / `ActivityCallbackFromContext` — context-value helpers used by the PTY and subprocess bridges.

### Registry (`provider/registry.go`)

- `Registry` — map from name to `Provider`. Safe for concurrent use.
- `NewRegistry`, `Register`, `Unregister`, `Get`, `Has`, `Names`.

### HTTP LLM providers

- `Anthropic` / `NewAnthropic` (`anthropic.go`) — Anthropic Messages API; implements `CacheableProvider` and `APIKeySetter`; has `RetryConfig`, `CircuitBreaker`, and `TokenRateTracker` fields.
- `OpenAI` / `NewOpenAI` (`openai.go`) — OpenAI Chat Completions + embeddings; implements `Embedder`.
- `Gemini` / `NewGemini` (`gemini.go`) — Google Gemini `generateContent` + embeddings; implements `Embedder`.
- `Mistral` / `NewMistral` (`mistral.go`) — Mistral OpenAI-compatible chat + embeddings; implements `Embedder`.
- `AzureOpenAI` / `NewAzureOpenAI` (`azure_openai.go`) — Azure-hosted OpenAI (deployment + api-version); implements `Embedder`.
- `OpenRouter` / `NewOpenRouter` (`openrouter.go`) — OpenRouter OpenAI-compatible gateway.
- `OpenZen` / `NewOpenZen` (`openzen.go`) — OpenZen OpenAI-compatible gateway.
- `Ollama` / `NewOllama` (`ollama.go`) — local Ollama `/api/chat` + `/api/embed`; implements `Embedder`.
- `APIKeySetter` interface (`api_key.go`) — implemented by Anthropic, OpenAI, Gemini, Mistral, AzureOpenAI, OpenRouter, OpenZen.

### Embeddings (`provider/embedder.go`)

- `Embedder` interface — `Embed`, `EmbedBatch`, `EmbeddingDimensions`.
- `EmbeddingResult` — vector + token count.

### CLI bridges

- `CLIAdapter` interface and `CLIConfig` struct (`cli_adapter.go`) — abstraction for spawning a CLI tool.
- `PTYBridge` / `NewPTYBridge` / `NewPTYBridgeWithAdapter` (`pty.go`, non-Windows build tag) — wraps a CLI in a pseudo-terminal.
- `SubprocessBridge` / `NewSubprocessBridge` (`subprocess.go`) — wraps a CLI using plain stdin/stdout pipes (all platforms).
- Adapters (one file each): `ClaudeAdapter`, `CodexAdapter`, `GeminiAdapter`, `AiderAdapter`, `CopilotAdapter`, `JunieAdapter`, `KiroAdapter`, `OpencodeAdapter`, `QwenAdapter`, each with a `New…Adapter()` constructor.

### Per-line typed events (`provider/events/`)

In addition to the legacy `<-chan StreamEvent` returned by `Provider.StreamChat`, CLI/PTY bridges can fire a richer typed-event taxonomy when a callback is wired into the spawn context. The two surfaces are parallel: typed events do not replace `StreamEvent`; they augment it with information the legacy union struct can't carry (per-tool `ToolResult`, sub-agent spawn detection, `SubprocessStderr` lines, `Heartbeat` ticks, signed `Thinking` blocks).

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
| codex | concrete | `AGENTS.md` + `boot.md` + `.mcp.json`, cwd = bootDir, `--cd {{.ProjectDir}}` (verify per Notes) |
| opencode | concrete | `agents/<name>.md` + `agents.json` + `opencode.json` + `boot.md` + `.mcp.json`, `OPENCODE_CONFIG_DIR={{.BootDir}}`, cwd = projectDir, `--dir {{.ProjectDir}}` |
| gemini, copilot, aider, junie, kiro, qwen | stub | zero-value spec; `Notes` describes the probe needed |

`AgentsMD(AgentInfo, mcpLoopbackURL, extras...)` renders the default AGENTS.md document used by the codex spec; apps that want a custom layout can ignore it and render directly from their `PlantedFile.Render` closure.

### Prompt caching (`provider/cache.go`)

- `CacheHint`, `CacheableProvider` interface, `DefaultCacheStrategy()`.

### Reliability primitives

- `RetryConfig`, `DefaultRetryConfig`, `APIError`, `RetryableStatusCode`, `IsRetryableError`, `IsTokenRateLimit`, `ParseRetryAfter`, `StatusCallback` (`retry.go`).
- `CircuitBreaker`, `CircuitState` (`CircuitClosed`/`CircuitOpen`/`CircuitHalfOpen`), `NewCircuitBreaker`, `DefaultCooldown` (`circuit.go`).
- `TokenRateTracker`, `NewTokenRateTracker` (`ratelimit.go`) — sliding 60s input-token window.

### Monitoring + event-reaction pipeline

- `EventReactionPipeline` / `NewEventReactionPipeline` / `EventReactionConfig` / `DefaultEventReactionConfig` (`event_pipeline.go`) — decorator that wraps any `Provider` and runs each streamed event through the monitors below.
- `ScopeGuard`, `ScopeViolation`, `NewScopeGuard` (`scope_guard.go`) — glob/regex-based allow-list over file paths and tool usage.
- `ProgressTracker`, `ProgressLoop`, `NewProgressTracker` (`progress_tracker.go`) — detects repeated content, repeated tool calls with the same input, and repeated state.
- `CostMonitor`, `BudgetViolation`, `CostRate`, `UsageSummary`, `NewCostMonitor` (`cost_monitor.go`) — token and USD budget tracking.

### Model selection (`provider/model_ops.go`)

- `ModelSelector` interface, `StaticModelSelector`, `NewStaticModelSelector`, `OperationModelConfig`, operation constants `OpChat` and `OpSummarization`.

## Architecture Notes

The package is intentionally flat: one Go package (`provider`) under `provider/`, one file per adapter. The shared `Provider` interface in `provider.go` is small (three methods), and all the cross-cutting features — retries, circuit breaking, token pacing, prompt-cache hints, cost/scope/loop monitoring — are expressed either as optional interfaces the adapter opts into (`CacheableProvider`, `APIKeySetter`, `Embedder`, `ProviderWithUsage`) or as a decorator (`EventReactionPipeline`) that can wrap any `Provider` without the adapter needing to know.

CLI bridges use a two-level abstraction: a `CLIAdapter` (one per CLI tool) defines how to build arguments and parse one line of output, and a transport wrapper (`PTYBridge` for pty-based or `SubprocessBridge` for pipes) runs the child process and feeds lines through the adapter. Context-value helpers (`WithCLISessionID`, `WithSandboxDir`, `WithProcessCallback`, `WithActivityCallback`) let callers pass session-resume IDs, working directories, and process-tracking hooks through to the bridge without widening the `Provider` interface. `pty.go` has a `//go:build !windows` build tag; the subprocess bridge is the portable fallback.

The Anthropic adapter is the only provider that currently wires retry, circuit breaking, and token-rate pacing into its struct — other HTTP adapters are simpler passthroughs, and callers who want the same guarantees across all providers are expected to wrap them in `EventReactionPipeline` or to lift those primitives into their own orchestration layer.

## Dependencies

### Framework-internal

- None.

### External (direct)

- `github.com/creack/pty v1.1.24` — pseudo-terminal support for the PTY bridge.
- `go.opentelemetry.io/otel v1.43.0` and `go.opentelemetry.io/otel/trace v1.43.0` — tracing spans in the Anthropic adapter.

### External (indirect)

Standard OpenTelemetry SDK/exporter chain plus `cenkalti/backoff`, `cespare/xxhash`, `go-logr`, `google/uuid`, `grpc-gateway`, `golang.org/x/net`, `golang.org/x/sys`, `golang.org/x/text`, `google.golang.org/grpc`, `google.golang.org/protobuf`. See `go.mod` for exact versions.

## Testing

```bash
go test ./...
```

Tests are pure-Go unit tests. HTTP providers are exercised against `httptest.NewServer` fake backends (see e.g. `provider/ollama_embedding_test.go`), so no real API keys or network access are required. No testcontainers, no external services, no environment variables are needed to run the suite. The PTY/subprocess tests do not spawn real CLI binaries — they exercise the adapter arg-building and line-parsing logic directly.

## License

MIT License. See `LICENSE`.
