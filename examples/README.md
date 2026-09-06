# go-providers examples

Three runnable examples that exercise the `BootDirSpec` plant-and-spawn
pattern against the supported CLI adapters:

| Example | Adapter | Transport | What it shows |
|---|---|---|---|
| [`claude_bare/`](./claude_bare) | `ClaudeAdapter` (`Bare: true`) | `SubprocessBridge` | Bare-mode Claude — explicit context injection via `--mcp-config`, `--append-system-prompt-file`, `--settings`, and `--add-dir`, plus the planted `.claude/settings.json` `apiKeyHelper` field for OAuth-via-keychain auth. |
| [`codex_bootdir/`](./codex_bootdir) | `CodexAdapter` | direct `os/exec` using `ProviderProjection.ResolveLaunch` | Codex via `codex exec --json`, with pure projection for `AGENTS.md` + `boot.md` + `config.toml`, caller-approved `auth.json` preparation via `-auth-json`, and project access via `--cd`. |
| [`opencode_bootdir/`](./opencode_bootdir) | `OpencodeAdapter` | `SubprocessBridge` | opencode with `agents/<name>.md` + `agents.json` + `opencode.json` planted into a config dir, `OPENCODE_CONFIG_DIR` set, and project access via `--dir`. |

## Common shape

Every example follows the same five steps:

1. Construct the adapter (`provider.NewClaudeAdapterBare()`, etc).
2. Materialize provider files into a fresh per-task tempdir (the *boot dir*).
   Claude/opencode demonstrate the legacy `BootDirSpec` path; Codex
   demonstrates pure `ProviderProjection`.
3. Prepare runtime effects only when the caller approves them. The Codex
   example writes `auth.json` only when `-auth-json /path/to/auth.json` is
   provided, through `PrepareRuntime` and a caller-owned resolver.
4. Wire launch bindings from the provider metadata
   (`adapter.BareInjectionPaths(...)` for claude, `OPENCODE_CONFIG_DIR`
   for opencode, `ProviderProjection.ResolveLaunch` for codex).
5. Detect the CLI binary, spawn it, and print deltas to stdout.

The boot dir is removed on exit; the project dir is whatever you point
the example at via `-project` (defaults to `.`).

## Running

Each example is a separate `main` package. Build and run with:

```bash
go run ./examples/claude_bare      -project /path/to/project
go run ./examples/codex_bootdir    -project /path/to/project -auth-json /approved/codex/auth.json
go run ./examples/opencode_bootdir -project /path/to/project
```

The examples are **dry-run-friendly**: if the CLI binary isn't on PATH
(or the override env var isn't set), the example prints the boot-dir
layout and the spawn args it *would* have used and exits 0. This lets
you sanity-check the wiring without installing the underlying CLI.

To force a real spawn:

| Example | Required |
|---|---|
| `claude_bare` | `claude` on PATH (or `CLAUDE_CLI_PATH` set), and either `ANTHROPIC_API_KEY` in env **or** an `apiKeyHelper` script (see flag `-apikey-helper`). |
| `codex_bootdir` | `codex` on PATH (or `CODEX_CLI_PATH` set), and a caller-approved auth source passed with `-auth-json` when a real spawn requires credentials. |
| `opencode_bootdir` | `opencode` on PATH (or `OPENCODE_CLI_PATH` set), and provider auth configured for the chosen agent profile. |

## Auth notes

- **Claude bare mode**: bare strictly requires `ANTHROPIC_API_KEY` in
  env *or* an `apiKeyHelper` configured via the planted
  `.claude/settings.json`. Bare disables the CLI's OAuth /
  keychain auto-resolution. The `claude_bare` example demonstrates the
  helper path — point `-apikey-helper` at any executable that prints a
  bearer token on its first line of stdout.
- **Codex**: projection never reads ambient `CODEX_HOME` or `HOME`. The
  example uses `PrepareRuntime` only when `-auth-json` is passed, and the
  prepared file is mode `0600` with a cleanup handle.
- **opencode**: the adapter does not manage provider auth. Configure the
  underlying CLI as you would for an interactive session, or add an explicit
  preparation step in the caller once that provider effect is supported.
