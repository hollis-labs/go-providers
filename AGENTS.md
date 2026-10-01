# go-providers

One `Provider` interface over CLI-bridge adapters (Claude Code, Codex, Gemini
CLI, Aider, Copilot, Junie, Kiro, Opencode, Qwen) driven through PTY or plain
subprocess, plus the cross-cutting adapter primitives: registry, boot-dir
specs, cost monitoring, scope guarding, progress-loop detection, typed per-line
events and a decorator pipeline. It is CLI/PTY-only — it does not own LLM
contracts, rate budgets, or any direct HTTP chat or embedding path.

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
- `registry/` is the one list of runtimes: a `Descriptor` per runtime (binary,
  env override, modes with per-mode capabilities, default mode, posture hook)
  over the agent-contracts-leaf `runtimes` vocabulary. Its layout is read from
  `layout/`, never copied.
- `layout/` is the one table of where each agent CLI reads files, skills and config
  (`docs/LAYOUT.md`, `docs/HARNESS-DISCOVERY.md`), keyed by runtime id and
  `layout.Shape` (a `runtimes.Mode` plus an optional variant such as Claude's
  `bare`); the adapters derive from it.
- `provider/events/` and `provider/event_pipeline.go` own typed per-line events.
- `examples/claude_bare`, `examples/codex_bootdir`, `examples/opencode_bootdir`
  are runnable.
- `providertest/` is the fake CLI other repos test against;
  `providertest/fixtures/` holds the captured wire output it replays.

## Commands

```bash
gofmt -l .
go vet ./...
go test -race -count=1 ./...
go run ./layout/gen -check   # docs/LAYOUT.md and layout/layout.json are generated
```

Smoke tests that spawn a real CLI are env-gated (`CLAUDE_PTY_SMOKE`,
`CLAUDE_BARE_SMOKE`) and skip by default. There is no CI workflow in this repo.

## Boundaries

The shared model types live in `go-llm-types` and the provider contracts and
rate-budget primitives in `go-llm-contracts`. Reintroducing an HTTP chat or
embedding adapter here reverses a deliberate split — this library bridges CLIs.

Boot-dir specs write real files into a real directory for a real CLI, so the
"no side effect" cases are load-bearing: an empty boot dir must produce no
`settings.json` write, and trust seeding happens only under the explicit legacy
opt-in (`TestClaudeBootDirSpec_SettingsJSON_NoSideEffectWhenBootDirEmpty`,
`TestClaudeBootDirSpec_SettingsJSON_NoSideEffectWithoutLegacyOptIn`,
`TestClaudeBootDirSpec_SettingsJSON_SeedsTrustWithLegacyOptIn`). Pre-accepting
trust on a user's behalf without that opt-in is the failure these guard.

`providertest` must not import `provider`: the `provider` package's own tests
import it, so that dependency would be an import cycle. It reads runtimes from
`registry`. Fixtures ship in a public module: re-capture through
`hack/capturefixtures` and run the scrub grep in
`providertest/fixtures/README.md` before committing one.

Every adapter must implement the boot-dir provider surface —
`TestBootDirProvider_AssertedOnAllAdapters` fails when a new adapter is added
without it, which is the point.

Codex argv differs by mode on purpose: exec mode carries the project-dir
argument and app-server mode must not
(`TestCodexAdapter_ExecMode_BootDirSpec_HasProjectDirArg`,
`TestCodexAdapter_AppServer_BootDirSpec_NoProjectDirArg`).

The registry is a closed set compiled into `registry/descriptors.go`; there is
no out-of-tree registration, and `RegisterForTest` is the only other way in.
A runtime has layout rows exactly when it has a native mode — Copilot and Pi
are ACP-only — and `register` panics at init otherwise
(`TestValidateBuiltinRules`). A capability a descriptor declares for a native
mode must be backed by the adapter's optional interface, and an implemented
interface must be declared (`TestDeclaredCapabilitiesMatchAdapters`). The
runtime vocabulary has no aliases for the old composite modes (`claude-print`
and the rest) or for `layout.Mode`: there is no `layout.Mode`.

`projection.go` and `preparation.go` return pure values. Keeping them free of
materialization means `agentkit` owns writing to disk and this library stays
testable without a filesystem.
