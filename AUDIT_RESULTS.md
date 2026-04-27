# Audit — go-providers

**Audited:** 2026-04-09
**Auditor:** general-purpose subagent (BOOT_STANDARDIZATION audit)
**Path:** libs/go-providers
**Kind:** lib

## Summary

`go-providers` is a functional, well-tested library (26 `*_test.go` files covering every adapter, the circuit breaker, the rate tracker, retries, the cache strategy, and the event-reaction pipeline) that provides a unified `Provider` interface across 8 HTTP LLM providers, 8 CLI-bridge adapters, and a decorator-style monitoring pipeline. It now has a package-level `doc.go`, an MIT `LICENSE`, runnable examples, and a local Anthropic tracing helper so the module no longer depends on an out-of-tree `replace` target. The public API is broad (~60 exported types and constructors in a single flat package) but cohesive, and there are no state/session files to exclude.

## Checklist

| # | Check | Status | Notes |
|---|---|---|---|
| 1 | `go.mod` present | pass | `github.com/hollis-labs/go-providers`, `go 1.26.1`. The out-of-tree `replace` was removed. |
| 2 | `README.md` present (before this audit) | pass | Present before apply; kept and updated to reflect the resolved dependency and license state. |
| 3 | `LICENSE` present | pass | MIT `LICENSE` added in the library root. |
| 4 | `doc.go` with `// Package X ...` godoc comment | pass | `provider/doc.go` now carries the package overview. |
| 5 | Module path matches intended repo layout | pass | `github.com/hollis-labs/go-providers` is retained as the standalone repo module path per D2; no external replace remains. |
| 6 | README has standard sections (title, desc, install, usage, API, examples) | pass | README covers title, description, Status, Install, Usage, API Overview, Architecture Notes, Dependencies, Testing, License. |
| 7 | Tests exist (`*_test.go`) | pass | 26 test files in `provider/`. HTTP adapters use `httptest.NewServer`; no network or API keys required. Coverage percentage not computed in this read-only audit. |
| 8 | Examples (`example_test.go` or `examples/`) | pass | `provider/example_test.go` adds runnable examples for the registry and Anthropic streaming path. |
| 9 | State/session files NOT misclassified as library docs | pass | No `.agentrc/`, `BOOT.md`, `CLAUDE.md`, `bootstrap.md`, `boot-prompt.md`, or `boot/` directory present in this library. |
| 10 | Public API sanity: errors typed/sentinel, context.Context first arg | pass | `APIError` is a typed error with `Error()` + `RetryAfter`; `BudgetViolation`, `ScopeViolation`, `ProgressLoop` all implement `Error()`. All `Provider` methods (`StreamChat`, `StreamChatWithTools`, `Complete`), `Embedder` methods, and bridge stream calls take `context.Context` as the first parameter. |
| 11 | `CHANGELOG.md` present (nice to have) | pass | `CHANGELOG.md` added with an `Unreleased` entry. |
| 12 | No circular/suspicious deps on other framework libs | pass | No framework-internal dependencies remain; Anthropic tracing now uses a local helper package under this module. |

## Findings — Required Fixes

1. **What:** `go.mod` used `replace github.com/hollis-labs/otel => ../../fragments-engine/libs/otel`, which resolved to a sibling repository (`fragments-engine`), not to anything inside this framework.
   **Why:** External consumers of `github.com/hollis-labs/go-providers` could not resolve the replace target, so `go get` of this module would fail outside a developer checkout that also had `fragments-engine` cloned in a specific relative location.
   **Suggested fix:** Resolved by replacing the external helper with a local `internal/otel` wrapper and removing the `replace`/external requirement from `go.mod`.

2. **What:** No `LICENSE` file at the library root.
   **Why:** Without a license, downstream users have no legal basis to use, copy, or redistribute the code, which blocks any notion of "release-ready."
   **Suggested fix:** Resolved by adding an MIT `LICENSE` file and updating the README license section.

3. **What:** No `doc.go` with a `// Package provider ...` godoc comment.
   **Why:** `pkg.go.dev` and `go doc github.com/hollis-labs/go-providers/provider` would render an empty package overview. Readers discovering this library via godoc would have zero context.
   **Suggested fix:** Resolved by adding `provider/doc.go` with a package overview.

4. **What:** No `README.md` existed before this audit (the new one was written by this session). Also no `CHANGELOG.md`.
   **Why:** A library with this many exported types and adapters cannot be consumed without documentation describing what each adapter does and how they compose.
   **Suggested fix:** Resolved by keeping and updating the README plus adding a minimal `CHANGELOG.md` with an `Unreleased` section.

5. **What:** No `example_test.go` and no `examples/` directory.
   **Why:** The Usage block in the README had to be hand-composed from `provider.go` and `capabilities_test.go`. There was no compiler-verified end-to-end example, so the snippet could silently drift from the real API.
   **Suggested fix:** Resolved by adding `provider/example_test.go` with `ExampleRegistry` and `ExampleAnthropic_StreamChat`.

## Findings — Nice-to-Have

1. **What:** Exported fields on `Anthropic` (`Retry`, `OnStatus`, `CircuitBreaker`, `OnCircuitOpen`, `RateTracker`) give callers direct access to internal state.
   **Why:** This is convenient but couples downstream users to the struct layout. Other HTTP adapters don't expose any of this.
   **Suggested fix:** Consider functional options (e.g. `NewAnthropic(WithRetryConfig(...), WithCircuitBreaker(...))`) once the API stabilizes, and mirror that shape on the other HTTP adapters so retry/circuit-breaker/rate-tracking are available uniformly.

2. **What:** The `CostMonitor.estimateCost` comment says "we'd need provider context for accurate rates" and currently hard-codes `"anthropic"` rates for every stream, regardless of which adapter emitted the event.
   **Why:** Budget enforcement is incorrect for non-Anthropic providers.
   **Suggested fix:** Pass the provider name (or a `CostRate`) into `CheckEvent` / `CostMonitor`, or tag `StreamEvent` with the originating adapter.

3. **What:** `globToRegex` in `scope_guard.go` hand-rolls a glob→regex conversion instead of using `filepath.Match` or a library.
   **Why:** It is easy for these hand-rolled conversions to have edge-case bugs (character classes, nested wildcards).
   **Suggested fix:** Audit the conversion for correctness or replace it with `path.Match` / a vetted glob library.

4. **What:** `ProgressTracker.hashInput` sorts `parts` with a hand-rolled O(n²) bubble sort.
   **Why:** Works, but `sort.Strings` is one line and idiomatic.
   **Suggested fix:** Replace with `sort.Strings(parts)`.

5. **What:** `event_pipeline.go` imports `"log"` directly and calls `log.Printf` for violation reporting.
   **Why:** A library should not write to the standard logger on its own — it hides events from structured logging setups and from OpenTelemetry spans.
   **Suggested fix:** Take an `io.Writer`, a `*slog.Logger`, or emit `StreamEvent{Type: "warn"}` instead.

6. **What:** `EventReactionConfig.DefaultEventReactionConfig` sets `TokenBudget: 100000` and `CostBudgetUSD: 10.0` as "moderate" defaults.
   **Why:** These are load-bearing magic numbers buried in the package default. A library shouldn't silently cap user budgets.
   **Suggested fix:** Default to `0` (meaning "unlimited / disabled") and require callers to opt in.

7. **What:** The Anthropic adapter is the only one that wires `RetryConfig`, `CircuitBreaker`, and `TokenRateTracker` into its struct; other HTTP adapters do not.
   **Why:** Inconsistent reliability guarantees across providers that claim to satisfy the same interface.
   **Suggested fix:** Either lift the retry/circuit/rate machinery into a shared base struct or document in the README that only Anthropic has built-in retry/circuit-breaker (the new README notes this, but ideally the code would be uniform).

## Prior Documentation

- **`README.md`** existed before this apply session and was updated in place. No `README.original.md` rename was needed.
- **`provider/doc.go`** was added during this apply session to provide the package overview.
- **`LICENSE`** was added during this apply session as an MIT license.
- **`CHANGELOG.md`** was added during this apply session with an `Unreleased` entry.
- **`provider/example_test.go`** was added during this apply session to provide runnable examples.
- **No `docs/` subdirectory.**
- **No state/session files.** There is no `.agentrc/`, no `BOOT.md`, no `CLAUDE.md`, no `bootstrap.md`, no `boot-prompt.md`, and no `boot/` directory in this library's tree.
- The only pre-existing files at the library root were `go.mod`, `go.sum`, and the `provider/` source directory (plus `.git/`).

## Public API Snapshot

Grouped by file, exported types and top-level functions only. All in package `provider` at `libs/go-providers/provider/`.

### `provider.go`

- types: `ProviderCapabilities`, `ToolDefinition`, `ToolUseBlock`, `ContentBlock`, `StreamEvent`, `Usage`, `ChatMessage`, `Provider` (interface), `ProcessCallback`, `ActivityCallback`
- funcs: `WithCLISessionID`, `CLISessionIDFromContext`, `WithSandboxDir`, `SandboxDirFromContext`, `WithProcessCallback`, `ProcessCallbackFromContext`, `WithActivityCallback`, `ActivityCallbackFromContext`

### `registry.go`

- types: `Registry`
- funcs: `NewRegistry`, `(*Registry).Register`, `(*Registry).Get`, `(*Registry).Has`, `(*Registry).Names`

### `embedder.go`

- types: `EmbeddingResult`, `Embedder` (interface)

### `api_key.go`

- types: `APIKeySetter` (interface)
- methods: `SetAPIKey` on `*Anthropic`, `*OpenAI`, `*Gemini`, `*Mistral`, `*AzureOpenAI`, `*OpenRouter`, `*OpenZen`

### `cache.go`

- types: `CacheHint`, `CacheableProvider` (interface)
- funcs: `DefaultCacheStrategy`

### `retry.go`

- constants: `MaxRetryAfter`
- types: `RetryConfig`, `APIError`, `StatusCallback`
- funcs: `DefaultRetryConfig`, `RetryableStatusCode`, `IsRetryableError`, `ParseRetryAfter`, `(RetryConfig).BackoffDelay`, `IsTokenRateLimit`

### `circuit.go`

- constants: `CircuitClosed`, `CircuitOpen`, `CircuitHalfOpen`, `DefaultCooldown`
- types: `CircuitState`, `CircuitBreaker`
- funcs: `NewCircuitBreaker`, `(*CircuitBreaker).RecordFailure`, `(*CircuitBreaker).RecordSuccess`, `(*CircuitBreaker).Reset`, `(*CircuitBreaker).IsOpen`, `(*CircuitBreaker).State`

### `ratelimit.go`

- types: `TokenRateTracker`
- funcs: `NewTokenRateTracker`, `(*TokenRateTracker).Record`, `(*TokenRateTracker).Available`, `(*TokenRateTracker).WaitTime`, `(*TokenRateTracker).UpdateLimit`, `(*TokenRateTracker).Remaining`

### `cli_adapter.go`

- types: `CLIAdapter` (interface), `CLIConfig`

### `cli_detect.go`

- (no exported symbols; helper `lookPathExpanded` is unexported)

### `anthropic.go`

- types: `Anthropic`
- funcs: `NewAnthropic`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`, `SetCacheHints`

### `openai.go`

- types: `OpenAI`
- funcs: `NewOpenAI`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`, `Embed`, `EmbedBatch`, `EmbeddingDimensions`

### `gemini.go`

- types: `Gemini`
- funcs: `NewGemini`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`, `Embed`, `EmbedBatch`, `EmbeddingDimensions`

### `mistral.go`

- types: `Mistral`
- funcs: `NewMistral`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`, `Embed`, `EmbedBatch`, `EmbeddingDimensions`

### `azure_openai.go`

- types: `AzureOpenAI`
- funcs: `NewAzureOpenAI`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`, `Embed`, `EmbedBatch`, `EmbeddingDimensions`

### `openrouter.go`

- types: `OpenRouter`
- funcs: `NewOpenRouter`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`

### `openzen.go`

- types: `OpenZen`
- funcs: `NewOpenZen`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`

### `ollama.go`

- types: `Ollama`
- funcs: `NewOllama`, plus `StreamChat`, `StreamChatWithTools`, `Complete`, `CompleteWithUsage`, `Capabilities`, `Embed`, `EmbedBatch`, `EmbeddingDimensions`

### `pty.go` (build tag `!windows`)

- types: `PTYBridge`
- funcs: `NewPTYBridge`, `NewPTYBridgeWithAdapter`, plus `Provider` interface methods

### `subprocess.go`

- types: `SubprocessBridge`
- funcs: `NewSubprocessBridge`, plus `Provider` interface methods

### `pty_claude.go`, `pty_codex.go`, `pty_gemini.go`, `pty_aider.go`, `pty_copilot.go`, `pty_junie.go`, `pty_kiro.go`, `pty_qwen.go`

- types: `ClaudeAdapter`, `CodexAdapter`, `GeminiAdapter`, `AiderAdapter`, `CopilotAdapter`, `JunieAdapter`, `KiroAdapter`, `QwenAdapter`
- funcs: `NewClaudeAdapter`, `NewCodexAdapter`, `NewGeminiAdapter`, `NewAiderAdapter`, `NewCopilotAdapter`, `NewJunieAdapter`, `NewKiroAdapter`, `NewQwenAdapter`, each implementing `CLIAdapter`

### `cost_monitor.go`

- types: `BudgetViolation`, `CostMonitor`, `CostRate`, `UsageSummary`
- funcs: `NewCostMonitor`, plus `CheckEvent`, `GetUsageSummary`, `Reset`, `SetCostRate`

### `scope_guard.go`

- types: `ScopeViolation`, `ScopeGuard`
- funcs: `NewScopeGuard`, plus `CheckEvent`

### `progress_tracker.go`

- types: `ProgressLoop`, `ProgressTracker`
- funcs: `NewProgressTracker`, plus `CheckEvent`

### `event_pipeline.go`

- types: `EventReactionConfig`, `EventReactionPipeline`
- funcs: `DefaultEventReactionConfig`, `NewEventReactionPipeline`, plus `Provider` interface methods

### `model_ops.go`

- constants: `OpChat`, `OpSummarization`
- types: `OperationModelConfig`, `ModelSelector` (interface), `StaticModelSelector`
- funcs: `NewStaticModelSelector`, `(*StaticModelSelector).SetOperation`, `(*StaticModelSelector).ModelForOperation`

## Open Questions

1. None from the standardization pass. The remaining product questions are outside the scope of this apply session.
