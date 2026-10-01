// Package provider drives agent CLIs (Claude Code, Codex, OpenCode and
// Antigravity) through one [CLIAdapter] per runtime, run by a PTY or plain
// subprocess bridge that implements go-llm-contracts' Provider interface.
// [NewAdapter] returns the adapter for a native runtime and mode of package
// registry; an ACP mode has none ([ErrNoAdapter]).
//
// Each runtime's argv is built once, in argv.go, and both an adapter's
// BuildArgs and a [ProviderProjection] resolve it through
// [LaunchConvention.ResolveTurn]. Around the adapters sit boot-dir specs
// ([BootDirSpec]), pure projections with explicit runtime preparation
// ([PrepareRuntime]), typed per-line events (package events, [WithEvents]),
// cost, scope and progress-loop monitors with an [EventReactionPipeline] that
// wraps any Provider, and a name-keyed [Registry] of Provider values (not the
// runtime registry).
//
// The package is CLI/PTY-only: it has no HTTP chat or embedding adapter.
// The shared model types live in go-llm-types, and the Provider interface
// and rate-budget primitives in go-llm-contracts.
package provider
