# Changelog

## v0.1.0

- Added `Registry.Unregister(name) bool` to support plugin hot-unload.
- `Registry` is now safe for concurrent use (internal `sync.RWMutex`).

## Unreleased

- Standardized the package documentation, added a local Anthropic tracing helper, removed the out-of-tree `replace` directive, added an MIT `LICENSE`, and added runnable examples.
