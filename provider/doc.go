// Package provider provides CLI bridge adapters for interactive coding CLIs
// (Claude Code, Codex, Gemini CLI, Aider, Copilot, Junie, Kiro, Opencode, Qwen),
// wrapped via PTY or plain subprocess, plus orchestration primitives — a
// registry, circuit breaker, rate-limit pacing, cost monitoring, scope
// guarding, progress-loop detection, per-line typed events, boot-dir spec
// metadata, and an event-reaction pipeline that layers monitors on top of
// any underlying provider.
//
// As of v0.10.0 the package is CLI/PTY-only — direct HTTP chat and embedding
// adapters were removed. See CHANGELOG.md for the migration note.
package provider
