// Package provider provides CLI bridge adapters for interactive coding CLIs
// (Claude Code, Codex, Opencode), wrapped via PTY or plain subprocess, plus
// orchestration primitives — a registry, cost monitoring, scope guarding,
// progress-loop detection, per-line typed events, boot-dir spec metadata,
// and an event-reaction pipeline that layers monitors on top of any
// underlying provider.
//
// As of v0.10.0 the package is CLI/PTY-only — direct HTTP chat and embedding
// adapters were removed. As of v0.12.0 the unused PTY adapters
// (aider/copilot/gemini/junie/kiro/qwen) and the transitional type aliases
// for migrated llmtypes/llmcontracts symbols were removed. See CHANGELOG.md
// for the migration note.
package provider
