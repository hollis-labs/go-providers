// Package layout is the single source of truth for where each agent CLI
// (Claude Code, Codex, OpenCode, Antigravity) looks for its files, skills and config, and
// which flags, environment variables and working directory locate them.
//
// The table is a Go literal (compile-checked). Every row names the launch
// root it is relative to, and carries the Step 0 probe ids
// (hack/probe-harness-layout.sh, provider/testdata/harness-discovery) whose
// measured harness behaviour justifies it. The provider package derives its
// ProviderProjection, LaunchConvention and legacy BootDirSpec of the built-in
// adapters from this table; layout/gen renders docs/LAYOUT.md and
// layout/layout.json from it for non-Go readers; layout/layouttest lets other
// modules pin their own path tables against it.
//
// The package imports only the standard library, and nothing in the
// go-providers module that it does not own: it must stay importable from
// provider without a cycle. Mode and Root are therefore plain string types
// whose values equal provider.ProviderMode and provider.RootKind; a test in
// provider guards that they cannot drift.
//
//go:generate go run ./gen
package layout
