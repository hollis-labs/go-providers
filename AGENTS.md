# go-providers

One `Provider` interface over CLI-bridge adapters (Claude Code, Codex, Gemini
CLI, Aider, Copilot, Junie, Kiro, Opencode, Qwen) driven through PTY or plain
subprocess, plus the cross-cutting adapter primitives: registry, boot-dir
specs, cost monitoring, scope guarding, progress-loop detection, typed per-line
events and a decorator pipeline. Since v0.11.0 it is CLI/PTY-only — it does not
own LLM contracts, rate budgets, or any direct HTTP chat or embedding path.

## Start Here

- `README.md` states the CLI/PTY-only scope and the minimum viable call shape.
- `provider/provider.go` declares the interface; `provider/registry.go` owns
  adapter lookup.
- `provider/bootdir.go` plus `bootdir_claude.go`, `bootdir_codex.go` and
  `bootdir_opencode.go` own the per-provider boot-dir specs.
- `provider/pty.go` and `provider/subprocess.go` are the two transports.
- `provider/scope_guard.go`, `provider/cost_monitor.go` and
  `provider/progress_tracker.go` are the decorator monitors.
- `provider/projection.go` and `provider/preparation.go` produce the pure
  values `agentkit` converts into materialization requests.
- `provider/events/` and `provider/event_pipeline.go` own typed per-line events.
- `examples/claude_bare`, `examples/codex_bootdir`, `examples/opencode_bootdir`
  are runnable.

## Commands

```bash
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

Smoke tests that spawn a real CLI are env-gated (`CLAUDE_PTY_SMOKE`,
`CLAUDE_BARE_SMOKE`) and skip by default. There is no CI workflow in this repo.

## Boundaries

Since v0.11.0 the shared model types live in `go-llm-types` and the provider
contracts and rate-budget primitives in `go-llm-contracts`. Reintroducing an
HTTP chat or embedding adapter here reverses a deliberate split — this library
bridges CLIs.

Boot-dir specs write real files into a real directory for a real CLI, so the
"no side effect" cases are load-bearing: an empty boot dir must produce no
`settings.json` write, and trust seeding happens only under the explicit legacy
opt-in (`TestClaudeBootDirSpec_SettingsJSON_NoSideEffectWhenBootDirEmpty`,
`TestClaudeBootDirSpec_SettingsJSON_NoSideEffectWithoutLegacyOptIn`,
`TestClaudeBootDirSpec_SettingsJSON_SeedsTrustWithLegacyOptIn`). Pre-accepting
trust on a user's behalf without that opt-in is the failure these guard.

Every adapter must implement the boot-dir provider surface —
`TestBootDirProvider_AssertedOnAllAdapters` fails when a new adapter is added
without it, which is the point.

Codex argv differs by mode on purpose: exec mode carries the project-dir
argument and app-server mode must not
(`TestCodexAdapter_ExecMode_BootDirSpec_HasProjectDirArg`,
`TestCodexAdapter_AppServer_BootDirSpec_NoProjectDirArg`).

`projection.go` and `preparation.go` return pure values. Keeping them free of
materialization means `agentkit` owns writing to disk and this library stays
testable without a filesystem.
