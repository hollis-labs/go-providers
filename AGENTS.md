# go-providers

The facts about six agent CLI runtimes, and the adapters that drive four of
them. `registry` describes claude, codex, opencode, antigravity (`agy`),
copilot and pi. Claude Code, Codex, OpenCode and Antigravity have native
`CLIAdapter`s here, run through PTY or plain subprocess behind one `Provider`
interface. Copilot and Pi are ACP-only: the registry describes them, and
go-agent-wrapper's ACP clients drive them. Around the adapters sit the
cross-cutting primitives: boot-dir specs and pure projections, per-turn argv,
cost monitoring, scope guarding, progress-loop detection, typed per-line events
and a decorator pipeline. It is CLI/PTY-only — it does not own LLM contracts,
rate budgets, or any direct HTTP chat or embedding path.

## Start Here

- `registry/` is the one list of runtimes: a `Descriptor` per runtime (binary,
  env override, modes with per-mode capabilities, default mode, posture hook,
  projection facts) over the agent-contracts-leaf `runtimes` vocabulary. Its
  layout is read from `layout/`, never copied. Start here to learn what a
  runtime is and what it can do.
- `provider/new_adapter.go` is the one table of native adapters:
  `NewAdapter(runtime, mode)` returns the adapter in that mode's shape, and
  `ErrNoAdapter` for an ACP mode.
- `provider/argv.go` is the one owner of each runtime's argv: a convention
  builder per runtime, which `ProviderProjection` resolves against launch roots
  and each adapter's `BuildArgs` resolves from its own fields, both through
  `LaunchConvention.ResolveTurn`.
- `registry.MCPExclusivity` and `ClaudeAdapter.MCPExclusive` keep a launch to
  the MCP servers it plants. The registry declares it per mode only where
  `hack/probe-mcp-exclusive.sh` measured it, and
  `provider/testdata/mcp-exclusive` holds the recorded results.
- `README.md` states the CLI/PTY-only scope and the minimum viable call shape.
- The `Provider` interface is go-llm-contracts'; the bridges implement it.
  `provider/provider.go` holds the context-value helpers the bridges read;
  `provider/registry.go` is a name-keyed registry of `Provider` instances
  (not the runtime registry).
- `provider/session_lost.go` and `provider/turn_interrupt.go` declare the
  optional adapter interfaces a session layer uses: session-lost, resume-id
  and auth-failure classifiers, `Preflighter`, and the stdin and JSON-RPC
  turn interrupters.
- `provider/mcp_servers.go` validates `PlantContext.MCPServers` and maps each
  runtime with a boot dir to the MCP config file and entry form it renders
  them into.
- `provider/bootdir.go` plus `bootdir_claude.go`, `bootdir_codex.go`,
  `bootdir_opencode.go` and `bootdir_antigravity.go` own the per-provider
  boot-dir specs.
- `provider/pty.go` and `provider/subprocess.go` are the two transports.
- `provider/scope_guard.go`, `provider/cost_monitor.go` and
  `provider/progress_tracker.go` are the decorator monitors.
- `provider/projection.go` and `provider/preparation.go` produce the pure
  values `agentkit` converts into materialization requests.
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
go run ./layout/gen -check   # docs/LAYOUT.md and layout/layout.json are generated
golangci-lint run --new-from-rev=ab81540281903332964eb2eb8a20bbfeeaebc512 ./...   # v2.11.4
go test -race -count=1 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

`.github/workflows/check.yml` runs these on every pull request and push to
main. `.golangci.yml` is the portfolio go-baseline config. The lint is
ratcheted at ab81540, so only lines changed since then are linted. The
findings that predate it are a backlog, not a gate.

Smoke tests that spawn a real CLI are env-gated (`CLAUDE_PTY_SMOKE`,
`CLAUDE_BARE_SMOKE`) and skip by default.

## Boundaries

The shared model types live in `go-llm-types` and the provider contracts and
rate-budget primitives in `go-llm-contracts`. Reintroducing an HTTP chat or
embedding adapter here reverses a deliberate split — this library bridges CLIs.

An MCP-exclusivity claim is a security statement, so it needs evidence: a
mode gets a `registry.MCPExclusivity` value only when the probe's golden shows
the user's server absent with it
(`TestMCPExclusivityClaimsAreMeasured`), and the claim must match the code
(`TestMCPExclusivityMatchesTheAdapters`). The flag is spelled in
`claudeConvention` only, with a literal argv test per shape
(`TestClaudeMCPExclusiveArgv`). Off, the argv is unchanged.

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

A runtime's argv is authored once, in `provider/argv.go`. Do not add a flag to
an adapter's `BuildArgs` or to a projection separately:
`TestBuildArgsMatchesProjectionResolveTurn` fails when the adapter path and the
prepared path disagree for any runtime, mode or turn, and
`TestNoPositionalAfterAddDir` guards the variadic `--add-dir`. Turn text is
untrusted: the prompt is the last argument, after `--` (agy: inline `-p=`),
with extra and posture flags before it (`TestUntrustedPromptIsNeverAFlag`,
`TestResolveTurnRefusesArgumentsAfterThePrompt`,
`TestPostureFlagsPrecedeThePrompt`).

Every file that can carry an MCP server's env is 0600, in the boot-dir spec
and the projection alike (`TestMCPBearingFilesAreOwnerOnly`).

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
interface must be declared (`TestDeclaredCapabilitiesMatchAdapters`).
`NewAdapter` covers exactly the registry's native modes
(`TestNewAdapterCoversTheRegistry`). Modes are agent-contracts-leaf
`runtimes.Mode` values and layout rows are keyed by `layout.Shape`; there
are no composite mode names (`claude-print` and the like) and no aliases
for them.

`projection.go` and `preparation.go` return pure values. Keeping them free of
materialization means `agentkit` owns writing to disk and this library stays
testable without a filesystem.
