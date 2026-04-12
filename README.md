# go-providers

`go-providers` is a Go library that provides a single `Provider` interface over a collection of LLM provider adapters (Anthropic, OpenAI, Gemini, Mistral, Azure OpenAI, OpenRouter, OpenZen, Ollama) and CLI-bridge adapters that wrap interactive coding CLIs (Claude Code, Codex, Gemini CLI, Aider, Copilot, Junie, Kiro, Qwen) via PTY or plain subprocess. It also ships cross-cutting primitives used by those adapters - a registry, retry/backoff, circuit breaker, token-rate tracker, prompt-cache hints, cost monitoring, scope guarding, progress-loop detection, and a decorator pipeline that layers those monitors on top of any underlying provider.

## Status

Beta — the core `Provider` interface is stable enough to have 26 test files exercising it and every adapter, but the surface is intentionally broad and still has a few evolving adapter details. See `AUDIT_RESULTS.md`.

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
    msgs := []provider.ChatMessage{
        {Role: "user", Content: "Say hello in one short sentence."},
    }

    stream, err := p.StreamChat(ctx, "You are concise.", msgs, "claude-sonnet-4-5")
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

Providers that also implement `Embedder` (OpenAI, Azure OpenAI, Gemini, Mistral, Ollama) can be type-asserted for embedding calls:

```go
if e, ok := p.(provider.Embedder); ok {
    res, _ := e.Embed(ctx, "hello world", "text-embedding-3-small")
    _ = res.Embedding
}
```

## API Overview

### Core interface (`provider/provider.go`)

- `Provider` — interface: `StreamChat`, `StreamChatWithTools`, `Complete`, `Capabilities`.
- `ProviderCapabilities` — struct describing streaming, tool calling, caching, embedding, image input, `MaxTokens`, `ContextWindowSize`, and embedding defaults.
- `ChatMessage`, `ContentBlock`, `ToolDefinition`, `ToolUseBlock`, `StreamEvent`, `Usage` — message and stream-event shapes.
- `WithCLISessionID` / `CLISessionIDFromContext`, `WithSandboxDir` / `SandboxDirFromContext`, `WithProcessCallback` / `ProcessCallbackFromContext`, `WithActivityCallback` / `ActivityCallbackFromContext` — context-value helpers used by the PTY and subprocess bridges.

### Registry (`provider/registry.go`)

- `Registry` — map from name to `Provider`.
- `NewRegistry`, `Register`, `Get`, `Has`, `Names`.

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
- Adapters (one file each): `ClaudeAdapter`, `CodexAdapter`, `GeminiAdapter`, `AiderAdapter`, `CopilotAdapter`, `JunieAdapter`, `KiroAdapter`, `QwenAdapter`, each with a `New…Adapter()` constructor.

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

The package is intentionally flat: one Go package (`provider`) under `libs/go-providers/provider/`, one file per adapter. The shared `Provider` interface in `provider.go` is small (four methods), and all the cross-cutting features — retries, circuit breaking, token pacing, prompt-cache hints, cost/scope/loop monitoring — are expressed either as optional interfaces the adapter opts into (`CacheableProvider`, `APIKeySetter`, `Embedder`) or as a decorator (`EventReactionPipeline`) that can wrap any `Provider` without the adapter needing to know.

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
