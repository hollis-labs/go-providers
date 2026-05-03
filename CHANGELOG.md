# Changelog

## Unreleased

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
