# Changelog

## v0.1.0

- Added `Registry.Unregister(name) bool` to support plugin hot-unload.
- `Registry` is now safe for concurrent use (internal `sync.RWMutex`).

## Unreleased

- Added optional `ProviderWithUsage.CompleteWithUsage(ctx, req) (CompleteResult, error)` and `CompleteResult` so non-streaming completions can return token usage while preserving the existing `Provider` interface and `Complete()` call sites.
- Updated Anthropic, OpenAI, Azure OpenAI, Gemini, Mistral, OpenRouter, OpenZen, Ollama, subprocess, PTY, and event-pipeline adapters to preserve non-streaming usage metadata.
- Standardized the package documentation, added a local Anthropic tracing helper, removed the out-of-tree `replace` directive, added an MIT `LICENSE`, and added runnable examples.
