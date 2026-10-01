// Package layout is the single source of truth for where each agent CLI
// (Claude Code, Codex, OpenCode, Antigravity) looks for its files, skills and config, and
// which flags, environment variables and working directory locate them. Copilot
// and Pi have no rows: they are launched only over ACP and have no boot dir.
//
// The table is a Go literal (compile-checked). Every row names the launch
// root it is relative to, and carries either the Step 0 probe ids
// (hack/probe-harness-layout.sh, provider/testdata/harness-discovery) whose
// measured harness behavior justifies it or an Unprobed reason (Antigravity's
// rows were verified live, outside the probe). The provider package derives
// its ProviderProjection, LaunchConvention (and so every adapter's BuildArgs
// flags) and legacy BootDirSpec of the built-in adapters from this table,
// including each MCP or config file's FileMode; layout/gen renders docs/LAYOUT.md and
// layout/layout.json from it for non-Go readers; layout/layouttest lets other
// modules pin their own path tables against it.
//
// Rows are keyed by runtime id and launch shape in the agent-contracts-leaf
// runtimes vocabulary: a [Shape] is a transport mode plus an optional
// runtime-specific [Variant] (Claude's bare). The registry package derives each
// runtime descriptor's layout from this table, so there is one list.
//
// The package imports only the standard library and agent-contracts-leaf, and
// nothing in the go-providers module that it does not own: it must stay
// importable from provider and registry without a cycle. Root is therefore a
// plain string type whose values equal provider.RootKind; a test in provider
// guards that they cannot drift.
//
//go:generate go run ./gen
package layout
