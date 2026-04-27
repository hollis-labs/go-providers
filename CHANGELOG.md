# Changelog

## Unreleased

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
