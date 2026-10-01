# go-providers

`go-providers` is a Go library that drives agent CLIs (Claude Code, Codex, OpenCode, Antigravity) through CLI-bridge adapters wrapped via PTY or plain subprocess, each bridge implementing go-llm-contracts' `Provider` interface. It also ships the runtime registry and cross-cutting primitives for the adapter layer — cost monitoring, scope guarding, progress-loop detection, per-line typed events, boot-dir spec metadata, and a decorator pipeline that layers monitors on top of any underlying provider.

This library is **CLI/PTY-only**: it has no direct HTTP chat or embedding adapter and does not own the shared LLM contracts or rate-budget primitives. The shared transport-agnostic model types live in `github.com/hollis-labs/go-llm-types`, and the shared provider contracts (the `Provider` interface) and rate-budget primitives in `github.com/hollis-labs/go-llm-contracts`.

## Status

Beta — the core `Provider` interface is stable. Public API is intentionally narrow now that the lib is CLI/PTY-only; smoke tests for the claude PTY/bare paths exist behind env-gated `*_SMOKE` tests.

## Install

```bash
go get github.com/hollis-labs/go-providers
```

## Usage

The minimum viable shape is: pick an adapter, materialize its
`BootDirSpec` into a per-task tempdir, wire the adapter's flag fields
from the planted layout, and call `StreamChat` through a transport
bridge.

```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    llmtypes "github.com/hollis-labs/go-llm-types"
    "github.com/hollis-labs/go-providers/provider"
)

func main() {
    // 1. Construct the adapter (bare-mode Claude in this example).
    adapter := provider.NewClaudeAdapterBare()

    // 2. Materialize the BootDirSpec into a fresh tempdir.
    bootDir, _ := os.MkdirTemp("", "boot-claude-*")
    defer os.RemoveAll(bootDir)

    projectDir, _ := filepath.Abs(".")

    spec := adapter.BootDirSpec()
    pctx := provider.PlantContext{
        SystemPrompt: "You are a terse assistant.",
        ProjectDir:   projectDir,
        BootDir:      bootDir,
    }
    for _, pf := range spec.PlantedFiles {
        content, _ := pf.Render(pctx)
        dst := filepath.Join(bootDir, pf.RelPath)
        os.MkdirAll(filepath.Dir(dst), 0o755)
        mode := pf.Mode
        if mode == 0 {
            mode = 0o644
        }
        os.WriteFile(dst, []byte(content), mode)
    }

    // 3. Wire bare-mode flag values from the planted layout.
    inj := adapter.BareInjectionPaths(bootDir, projectDir)
    adapter.MCPConfigPath = inj.MCPConfigPath
    adapter.AppendSystemPromptFile = inj.AppendSystemPromptFile
    adapter.SettingsPath = inj.SettingsPath
    adapter.ProjectDir = inj.ProjectDir

    // 4. Detect the binary and stream a chat turn.
    cliPath, _ := adapter.Detect()
    bridge := provider.NewSubprocessBridge(adapter, cliPath)

    stream, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{
        Model: "claude-sonnet-4-5",
        Messages: []llmtypes.ChatMessage{
            {Role: "user", Content: "Say hello in one short sentence."},
        },
    })
    if err != nil {
        panic(err)
    }
    for ev := range stream {
        switch ev.Type {
        case llmtypes.EventDelta:
            fmt.Print(ev.Content)
        case llmtypes.EventError:
            fmt.Println("error:", ev.Error)
        case llmtypes.EventDone:
            return
        }
    }
}
```

Three runnable end-to-end programs (claude bare, codex, opencode) live
under `examples/`; see `examples/README.md` for the differences and
auth-setup notes.

### Bare-mode auth

Bare-mode Claude requires either `ANTHROPIC_API_KEY` in env *or* an
`apiKeyHelper` executable referenced from the planted
`.claude/settings.json`. Set `adapter.ApiKeyHelperPath = "/path/to/bin"`
before iterating `BootDirSpec().PlantedFiles`; the helper is invoked
per request and its first line of stdout is consumed as the bearer
token. Use this for OAuth-via-keychain auth or any other
per-environment secret store.

### Permission mode

`ClaudeAdapter.PermissionMode` sets `permissions.defaultMode` in the
planted `.claude/settings.json` — the full Claude Code vocabulary:
`default`, `acceptEdits`, `plan`, `bypassPermissions`. Set it before
iterating `BootDirSpec().PlantedFiles`:

```go
adapter := provider.NewClaudeAdapter()
adapter.PermissionMode = "acceptEdits"
```

When `PermissionMode` is empty the back-compat behavior holds:
`SkipPermissions == true` still plants `bypassPermissions`, and
otherwise no `permissions` block is planted. A non-empty
`PermissionMode` wins over that default; an unrecognized value fails
the planted-file render.

## API Overview

### Core interface and context helpers

- `Provider` is go-llm-contracts' interface (`StreamChat`, `Complete`, `Capabilities`); the bridges and `EventReactionPipeline` implement it. Request, message, event and result shapes (`ChatRequest`, `ChatMessage`, `StreamEvent`, `Usage`, `CompleteResult`, `ProviderCapabilities`, …) are go-llm-types'. Neither is redeclared here, and circuit breaking and rate pacing (`CircuitBreaker`, `PacingWait`) are go-llm-contracts'.
- `WithCLISessionID` / `CLISessionIDFromContext`, `WithSandboxDir` / `SandboxDirFromContext`, `WithProcessCallback` / `ProcessCallbackFromContext`, `WithActivityCallback` / `ActivityCallbackFromContext`, `WithWaitDelay` / `WaitDelayFromContext` (`provider/provider.go`) — context-value helpers used by the PTY and subprocess bridges.

### Runtime descriptors (`registry/`)

The one list of agent CLI runtimes (Claude Code, Codex, OpenCode, Copilot CLI, Pi, Antigravity). Ids, modes and capability names come from [agent-contracts-leaf `runtimes`](https://github.com/hollis-labs/agent-contracts-leaf); the facts live here.

- `Descriptor` — `ID`, `Aliases`, `Binary`, `EnvOverride`, `LookupDirs`, `Modes` (each a `ModeSupport{Mode, Capabilities}`: capabilities are declared per mode), `DefaultMode`, `Projection` (`*ProjectionFacts`: tested CLI version, per-`Feature` support and per-mode notes; set exactly when the runtime has a layout), and a `Posture` hook. Methods: `Supports`, `Capabilities`, `Has`, `NativeModes`, `Layout` (the runtime's rows of the `layout` table, read rather than copied), `HasLayout`, `LookPath`, `PostureFor`.
- `Lookup(idOrAlias)` and `All()`. The set is closed and compiled in; there is no out-of-tree registration. `RegisterForTest` adds a fake for one test and removes it at cleanup.
- Copilot and Pi are ACP-only: no native mode, no layout rows, no boot dir.
- Permission posture is [go-permission](https://github.com/hollis-labs/go-permission)'s `Mode` (`default`, `accept-edits`, `plan`, `yolo`; D-72). `Descriptor.PostureFor(mode, runtimeMode)` returns a `PostureLaunch{Args, Env}`: flags for the launch convention's extra-argument slot (always before `--`) and environment variables. Apps pass only the Mode.

  | posture | claude | codex (exec and app-server) | opencode (`OPENCODE_PERMISSION`) | agy |
  |---|---|---|---|---|
  | default | `--permission-mode default` | `-c sandbox_mode="read-only" -c approval_policy="on-request"` | `{"edit":"ask","bash":"ask"}` | none (its request-review default) |
  | accept-edits | `--permission-mode acceptEdits` | `workspace-write`, `on-request` | `{"edit":"allow","bash":"ask"}` | `--mode accept-edits` |
  | plan | `--permission-mode plan` | `read-only`, `never` | `{"edit":"deny","bash":"ask"}` | `--mode plan` |
  | yolo | `--permission-mode bypassPermissions` | `danger-full-access`, `never` | every permission `allow` | `--dangerously-skip-permissions` |

  Headless, an action that needs approval is denied (claude `-p`, codex exec, opencode run, agy) or, on codex app-server, sent to the host as an approval request for agentkit's `CodexApprovalResponder` to answer from the same Mode. ACP modes have no launch mapping (`ErrNoPostureMapping`): an ACP agent's permission requests are the ACP client's to answer, best effort. An empty posture is the zero `PostureLaunch` (the runtime keeps its own default); anything other than the four Modes is `ErrInvalidPosture`. The registry package doc has the measurements.

### Provider registry (`provider/registry.go`)

- `Registry` — map from name to `Provider` instances (not the runtime registry). Safe for concurrent use.
- `NewRegistry`, `Register`, `Unregister`, `Get`, `Has`, `Names`.

### CLI bridges and adapters

- `CLIAdapter` interface and `CLIConfig` struct (`cli_adapter.go`) — abstraction for spawning a CLI tool.
- `PTYBridge` / `NewPTYBridge` / `NewPTYBridgeWithAdapter` (`pty.go`, non-Windows build tag) — wraps a CLI in a pseudo-terminal.
- `SubprocessBridge` / `NewSubprocessBridge` (`subprocess.go`) — wraps a CLI using plain stdin/stdout pipes (all platforms).
- Adapters (one file each): `ClaudeAdapter`, `CodexAdapter`, `OpencodeAdapter`, `AntigravityAdapter`. Each ships `New…Adapter()` plus PTY/Dev/Bare variants where applicable. `ClaudeAdapter` additionally ships `NewClaudeAdapterStreamingStdio()` / `NewClaudeAdapterDevStreamingStdio()` for vendor-documented long-lived NDJSON-over-stdin sessions; `CodexAdapter` ships `NewCodexAdapterAppServer()` for long-lived JSON-RPC-over-stdio sessions; `OpencodeAdapter` ships `NewOpencodeAdapterServeHTTP()` for long-lived HTTP/SSE sessions. See [Long-lived headless modes](#long-lived-headless-modes).
- `NewAdapter(id, mode)` (`new_adapter.go`) — the one table of native adapters: a fresh adapter in that registry mode's shape, or `ErrNoAdapter` for an ACP mode, an unknown runtime or a mode the runtime lacks. Hosts then set the adapter's fields (`Binary` pins the executable `Detect` returns; `ExtraArgs` go at the convention's extra slot) rather than wrapping it, which would hide its optional interfaces.
- Optional adapter interfaces: `EventParser` (all four), `BootDirProvider` and `ProjectionProvider` (all four), `ExtraArgsBuilder` (all four: a caller's extra arguments at the convention's extra slot, as the prepared path places them; extras are flags with their values, with no `--`, and the last must not be a flag that takes a value), `SessionLostClassifier` (`IsSessionLost` on a stderr tail: claude, codex exec, opencode, agy), `SessionResumeVerifier` (agy, whose unknown conversation id silently starts a new one), `AuthFailureClassifier` (agy), `Preflighter` (no built-in adapter has one), `TurnInterrupter` (claude streaming stdio: a `control_request` interrupt) and `RPCTurnInterrupter` (codex app-server: `turn/interrupt`). Session layers wrap `ErrProviderSessionLost`, `ErrProviderNotAuthenticated` and `ErrInterruptRefused`.

### Per-line typed events (`provider/events/`)

In addition to the `<-chan llmtypes.StreamEvent` a bridge's `StreamChat` returns, CLI/PTY bridges can fire a richer typed-event taxonomy when a callback is wired into the spawn context. The two surfaces are parallel: typed events do not replace `StreamEvent`; they augment it with information the legacy union struct can't carry (per-tool `ToolResult`, sub-agent spawn detection, `SubprocessStderr` lines, `Heartbeat` ticks, `PermissionDenied` refusals).

```go
import "github.com/hollis-labs/go-providers/provider/events"

ctx := provider.WithEvents(ctx, func(ev events.Event) {
    switch e := ev.(type) {
    case events.Delta:
        // streaming text fragment; e.Phase is "narration", "final" or "thought";
        // e.BlockID is the same for every fragment of one content block
    case events.ToolUse:
        // e.Name + e.Args (or sha256-digested keys when WithToolArgFingerprint is on)
    case events.ToolResult:
        // result of a previous ToolUse with matching e.ID
    case events.SubagentSpawn:
        // claude's "Task" tool emits this in addition to ToolUse
    case events.SubprocessStderr:
        // subprocess transport only — PTYs merge stderr into stdout
    case events.Heartbeat:
        // synthesized when no other event has fired for the configured interval
    case events.Usage:
        // token accounting (all four adapters); e.StopReason is normalized with
        // llmtypes.NormalizeStopReason; e.CostUSD is a per-event delta to sum
        // (claude, opencode), zero when no cost was reported
    case events.Thinking:
        // completed thinking block (opencode reasoning parts), with BlockID
    case events.SessionID:
        // the CLI's session id (all four adapters); informational
    case events.PermissionDenied:
        // a tool action refused because headless mode cannot ask (agy); non-terminal
    case events.SessionLost, events.AuthFailed:
        // emitted by the session layer that owns the stored id and runs the
        // classifiers, not by the adapters
    case events.Done:
        // turn-terminal success
    case events.Error:
        // turn-terminal failure with e.Err (Go error) and/or e.Message
    }
})
stream, _ := bridge.StreamChat(ctx, req) // legacy channel still works
for ev := range stream { /* ... */ }
```

`WithToolArgFingerprint(ctx, true)` swaps `events.ToolUse.Args` values for `sha256:<hex>` digests of their JSON-marshalled form (keys preserved). Use this when logs may cross trust boundaries; default is off.

`WithHeartbeatInterval(ctx, d)` adjusts the heartbeat cadence (`DefaultHeartbeatInterval` is 5s; non-positive disables).

Adapters can implement the optional `EventParser` interface (`ParseLineEvents(line []byte) ([]events.Event, error)`) to produce typed events natively from the wire format. All four built-in adapters do; the claude path additionally surfaces user-role `tool_result` blocks and `Task` sub-agent spawns. Adapters without `EventParser` get a best-effort `StreamEvent` → typed translation.

### Boot dir specs (`BootDirProvider` / `BootDirSpec`)

Each CLI adapter can expose its per-task tempdir layout convention as read-only metadata. Apps loop over the spec instead of writing per-provider switch statements.

```go
if bp, ok := adapter.(provider.BootDirProvider); ok {
    spec := bp.BootDirSpec()
    if spec.Notes != "" {
        // stub spec — verify or fall back to bespoke planting
    }
    pctx := provider.PlantContext{
        SystemPrompt:   "...",
        BootContent:    "Read @./instructions.md and start.",
        AgentName:      "orchestrator",
        MCPLoopbackURL: "http://localhost:9999/mcp",
        ProjectDir:     "/work/project",
    }
    for _, pf := range spec.PlantedFiles {
        content, err := pf.Render(pctx)
        // apps materialize: filepath.Join(bootDir, pf.RelPath), content
        // honor pf.Mode, using 0644 only when pf.Mode is zero
    }
    // apps substitute {{.BootDir}} / {{.ProjectDir}} in spec.EnvAmendments + spec.ProjectDirArg
    cwd := spec.SpawnWorkdir(bootDir, projectDir) // honors CwdPreference
}
```

| Adapter | Status | Layout |
|---|---|---|
| claude | concrete | `CLAUDE.md` + `boot.md` + `.claude/settings.json` + `.mcp.json`, cwd = bootDir, `--add-dir {{.ProjectDir}}` |
| codex | concrete | `AGENTS.md` + `boot.md` + `config.toml` + `auth.json` + `.mcp.json`, cwd = bootDir, `--cd {{.ProjectDir}}` (exec mode) |
| opencode | concrete | `agents/<name>.md` (markdown agent with frontmatter) + `opencode.json` + `boot.md` + `.mcp.json`, `OPENCODE_CONFIG_DIR={{.BootDir}}`, cwd = projectDir, `--dir {{.ProjectDir}}` |
| antigravity | concrete | `AGENTS.md` + `boot.md` + `.agents/plugins/tether/{plugin.json,mcp_config.json}` + `.agents/skills/<name>/SKILL.md` (workspace root; agy has no config-dir variable), cwd = bootDir, `--add-dir {{.ProjectDir}}` |

`AgentsMD(AgentInfo, mcpLoopbackURL, extras...)` renders the default AGENTS.md document used by the codex and antigravity specs; apps that want a custom layout can ignore it and render directly from their `PlantedFile.Render` closure.

Files that carry an MCP endpoint or a credential (claude `.mcp.json`, codex `config.toml`, `auth.json` and `.mcp.json`, opencode `opencode.json` and `.mcp.json`, agy `mcp_config.json`) declare `PlantedFile.Mode` 0600, taken from the layout row's `FileMode` where the table records one; honor it.

### Pure provider projections

`ProviderProjection` is the provider-owned handoff for shared materialization. It returns relative files, file modes, byte content, structured argv/env conventions, cwd/config-root selection, and explicit runtime-effect descriptors without importing `agentkit`, reading credentials, writing a real home directory, trusting a workspace, or starting a provider process.

```go
ctx := provider.PlantContext{
    SystemPrompt:   "You are the task agent.",
    BootContent:    "Read @./boot.md and start.",
    AgentName:      "worker",
    MCPLoopbackURL: "http://127.0.0.1:60000/mcp",
}
proj, err := provider.NewCodexAdapter().ProviderProjection(ctx, provider.ProjectionOptions{
    Skills: []provider.SkillPackage{{
        Name: "repo-test",
        Files: []provider.SkillFile{
            {RelPath: "SKILL.md", Content: []byte("---\nname: repo-test\ndescription: Run repo tests\n---\n")},
            {RelPath: "scripts/run.sh", Content: []byte("#!/bin/sh\ngo test ./...\n"), Mode: 0o755},
        },
    }},
    RequiredFeatures: []registry.Feature{
        registry.FeatureInstructions,
        registry.FeatureNativeConfig,
        registry.FeatureMCP,
        registry.FeatureSkillTrees,
    },
})
if err != nil {
    // Unsupported required capabilities return an UnsupportedFeatureError
    // with diagnostics instead of silently dropping caller intent.
}
binding, err := proj.ResolveLaunch(provider.ProjectionRoots{
    BootRoot:    "/tmp/boot root",
    ProjectRoot: "/tmp/project root",
}, "implement the task")
```

`ResolveLaunch` resolves a first turn. `ResolveTurn` resolves any turn: the prompt, the system prompt where the runtime takes one, the session to resume, and caller extra arguments, which go where the runtime's convention puts them (never after Claude's variadic `--add-dir`). Turn text is untrusted, so claude `-p`, codex exec and opencode run take the prompt last, after `--`, where a turn starting with `-` is text, not a flag; agy takes it inline as `-p=<prompt>`. Extra arguments and posture flags always go before `--`:

```go
binding, err = proj.ResolveTurn(roots, provider.TurnInput{
    Prompt:   "continue",
    ResumeID: sessionID, // --resume / --session / --conversation, per runtime
}, []string{"--model", "sonnet"})
```

Each runtime's argv is built in one place (`provider/argv.go`). An adapter's `BuildArgs` resolves the same convention from its own fields (`MCPConfigPath`, `ProjectDir`, …), so the adapter path and the prepared path produce the same argv for the same launch.

`ProviderCapabilityMatrix()` returns the capability matrix: one row per native mode (and launch variant, such as Claude's `bare`) of each runtime with a layout, built from the registry's `ProjectionFacts`:

| Provider / mode | Fixture version | Projected here | Explicit later |
|---|---:|---|---|
| Claude bare/print/PTY/streaming | 2.1.285 | `CLAUDE.md`, `.claude/settings.json`, `.mcp.json`, `.claude/skills/<name>/...` (bare adds `--add-dir <boot>` when skills are projected), structured argv roots | credential helper execution, workspace trust, hooks, commands, subagents |
| Codex exec/app-server | 0.154.0 | `AGENTS.md`, `config.toml`, `.mcp.json` mirror, `skills/<name>/...` (under `CODEX_HOME`), structured `CODEX_HOME`/argv roots | `auth.json` credential materialization, hooks, custom subagents |
| OpenCode run/serve-http | 1.18.30 | `agents/<name>.md`, `opencode.json`, `.mcp.json` mirror, `skills/<name>/...` (under `OPENCODE_CONFIG_DIR`), structured `OPENCODE_CONFIG_DIR`/argv roots | provider auth, commands, subagents |
| Antigravity print (`agy`) | 1.2.7 | `AGENTS.md`, `.agents/plugins/tether/` (plugin MCP), `.agents/skills/<name>/...`, `--add-dir` project | agy login (Keychain; never projected), hooks, commands, subagents |

The Claude, Codex and OpenCode versions above are the ones `hack/probe-harness-layout.sh` measured on 2026-09-29 (see [docs/HARNESS-DISCOVERY.md](docs/HARNESS-DISCOVERY.md)); agy 1.2.7 was verified live, outside the probe, as its layout rows say. Every path, flag and environment variable in these projections comes from one table, package `layout` ([docs/LAYOUT.md](docs/LAYOUT.md), [layout/layout.json](layout/layout.json) for non-Go readers). Skills are always emitted in the directory form `<name>/SKILL.md`; no harness reads flat `<name>.md`. `SkillPackage.Hash` optionally pins a package's content (`sha256:<hex>`, the same tree hash go-agentdef uses; compute it with `SkillPackage.TreeHash`). Other modules can pin their own path tables with `layout/layouttest` ([docs/CONSUMERS.md](docs/CONSUMERS.md)).

`ProviderProjection` keeps `ProjectRoot`, `BootRoot`, `ConfigRoot`, `StateRoot`, `ScratchRoot`, and process cwd distinct. `LaunchConvention.Argv` is a list of typed arguments, and `EnvDelta` carries set/prepend/append/unset plus precedence, so paths with spaces or non-ASCII characters are never split through a shell string.

### Explicit runtime preparation

Pure projection only declares required runtime effects. It never reads `HOME`, copies `CODEX_HOME/auth.json`, seeds Claude trust, or starts a provider. Callers decide which effects are required immediately before launch and pass the policy and secret resolver that make those effects legal.

```go
proj, _ := provider.NewCodexAdapter().ProviderProjection(provider.PlantContext{
    AgentName: "worker",
}, provider.ProjectionOptions{})

result, err := provider.PrepareRuntime(ctx, provider.RuntimePreparationRequest{
    Projection: proj,
    Roots: provider.ProjectionRoots{
        BootRoot:    bootDir,
        ProjectRoot: projectDir,
    },
    Policy: provider.PreparationPolicy{
        AllowCredentials: true,
        AllowCleanup:    true,
    },
    CredentialResolver: provider.CredentialResolverFunc(func(ctx context.Context, req provider.CredentialRequest) (provider.Credential, error) {
        // Caller-controlled source: a vault, OS keychain, test fixture, or
        // approved file path. go-providers never falls back to ambient auth.
        return provider.Credential{Bytes: fakeOrResolvedAuthJSON, Mode: 0o600}, nil
    }),
    RequiredEffects: []provider.ProviderEffectKind{provider.EffectCodexAuthJSON},
})
if err != nil {
    // Required but unauthorized or unavailable effects fail here, before spawn.
}
defer result.Cleanup(ctx)
```

Claude workspace trust uses the same preparation surface with `EffectClaudeWorkspaceTrust`, `PreparationPolicy.AllowHostMutation`, a caller-supplied synthetic or real `HomeDir`, and `Roots.BootRoot`. The cleanup handle removes only the session-owned trust entry; unrelated Claude config and project keys are preserved. Secret-bearing preparation results expose only `"<redacted>"` metadata and restrictive file modes.

`BootDirSpec` remains available for older apps. Its render functions are pure by default; setting `PlantContext.LegacyAllowHostEffects` opts into the pre-M07 compatibility behavior where Claude may seed `~/.claude.json` and Codex may read ambient auth during render. New callers should prefer `ProviderProjection` plus `PrepareRuntime`.

The codex `config.toml` always carries an `approval_policy` / `sandbox_mode` header, controlled by `CodexAdapter.ApprovalPolicy` / `CodexAdapter.SandboxMode` (the codex analogue of `ClaudeAdapter.PermissionMode`). The defaults are `never` / `workspace-write` — NOT codex's interactive defaults — because a `BootDirSpec` is a headless boot with no TTY: a codex that prompts for approval under a headless runtime (codex `app-server` emits a JSON-RPC approval request) blocks forever. An unrecognized value fails the render, and so does `untrusted`, which codex-cli 0.159.2 refuses to load.

Beyond the per-task loopback (`PlantContext.MCPLoopbackURL`) and the mux aggregator (`PlantContext.Mux*`), a consumer adds its own MCP servers via `PlantContext.MCPServers` (`[]MCPServerSpec` — name + an HTTP-URL or stdio command). Every runtime with a boot dir renders them, in its CLI's own form, into the file its layout names (`provider/mcp_servers.go`): claude `.mcp.json`, codex `config.toml [mcp_servers.<name>]` (codex reads no `.mcp.json`), opencode `opencode.json` `"mcp"`, agy `.agents/plugins/tether/mcp_config.json`, plus the `.mcp.json` mirrors codex and opencode plant for operators. So each config file stays single-owner and no consumer post-processes it. The names `loopback` and `mux` are reserved; an invalid, reserved or duplicate name, or a spec without exactly one transport, fails the render and the projection.

### Keeping a launch to its own MCP servers

A CLI normally loads the user's own MCP servers next to the ones a launch plants, so an allow-list a host applies to the planted servers does not cover them. `registry.Descriptor.MCPExclusivity(mode)` says, per native mode, whether and how a launch can be made exclusive:

| Runtime / mode | Value | What makes it exclusive |
|---|---|---|
| Claude, per-turn, streaming-stdio and PTY | `flag` | Set `ClaudeAdapter.MCPExclusive`. The convention adds `--strict-mcp-config` right after `--mcp-config`. With nothing planted, Claude loads no MCP server. `--bare` already skips the user's servers, and the flag is harmless there. |
| Codex, exec and app-server | `layout` | Nothing to pass. The planted layout sets `CODEX_HOME` to the boot dir, and Codex reads MCP servers only from there. It holds only for a launch that sets it. |
| OpenCode | none | `OPENCODE_CONFIG_DIR` is merged with the user's and the project's config. Only whole-config isolation (`XDG_CONFIG_HOME` at an empty directory, `OPENCODE_DISABLE_PROJECT_CONFIG=1`) removed them, and that drops every other setting too, so none is offered. |
| Antigravity | none | Not measured: `agy` will not start without a login. |

Copilot and Pi are ACP-only and have no native mode. A host asks for it with `ProjectionOptions.MCPExclusive` on the prepared path, or `ClaudeAdapter.MCPExclusive` on the adapter path. Off by default, and then the argv is unchanged. A mode with no value cannot be made exclusive: `ProviderProjection` fails with `ErrMCPExclusiveUnsupported` rather than launch it non-exclusive, and a host on the adapter path should refuse it itself. A host that got its adapter from elsewhere and cannot trust it to honor the option runs `provider.CheckMCPExclusive` on the projection it got back: it judges the projection, not the request. The values are declared only where `hack/probe-mcp-exclusive.sh` measured them (a scratch `HOME` under `env -i`, no model call; claude 2.1.286, codex-cli 0.159.3, opencode 1.18.33), and `provider/testdata/mcp-exclusive` records the results ([docs/HARNESS-DISCOVERY.md](docs/HARNESS-DISCOVERY.md)). Not covered: Claude's account connectors, which need a login, and managed or plugin servers. Exclusivity decides which servers load; which of their tools may run is still the host's allow-list.

### Long-lived headless modes

Three adapter shapes target vendor-documented long-lived headless lifecycles. They emit argv only; the runtime that owns the I/O loop, session-id handling, and attach fan-out lives upstream in `go-agent-sessions`.

**Claude — `streamingStdio`** (Anthropic calls it "Streaming Input Mode"):

```go
adapter := provider.NewClaudeAdapterStreamingStdio()
adapter.BuildArgs("", "", "")
// → ["-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"]
```

One long-lived `claude -p` process reads NDJSON `{type:"user",...}` messages from stdin, replies with stream-json on stdout, and reuses its KV-cache across turns until stdin EOF. Per-turn payloads + system context flow over stdin, not argv — so the positional prompt and `--system-prompt` parameters are intentionally dropped from `BuildArgs` in this mode. Use `--resume <id>` (passed as `cliSessionID`) for cold-start / crash recovery; it is not the long-lived primary.

**Codex — `app-server`**:

```go
adapter := provider.NewCodexAdapterAppServer()
adapter.BuildArgs("", "", "")
// → ["app-server"]
```

One long-lived `codex app-server` process speaking JSON-RPC 2.0 over stdio (default `--listen stdio://`). Same engine that backs OpenAI's official VS Code extension. Threads live in memory until 30-min idle; `thread/start` and `thread/resume` are JSON-RPC methods, not CLI flags — so all per-turn params are intentionally dropped from `BuildArgs`. `ParseLine` returns no events in this mode; the consumer runtime owns JSON-RPC framing, request/response correlation, and event mapping.

**Opencode — `serve-http`**:

```go
adapter := provider.NewOpencodeAdapterServeHTTP()
adapter.BuildArgs("", "", "")
// → ["serve", "--port", "0", "--hostname", "127.0.0.1"]
```

One long-lived `opencode serve` process exposes opencode's HTTP API and server-sent event stream. Session creation, message POSTs, event mapping, health checks, and shutdown are runtime concerns owned by `go-agent-sessions` (`ServeHTTP` kind), so the prompt, system prompt, and session id parameters are intentionally dropped from `BuildArgs`.

The underlying `InputMode` field (`ClaudeAdapter`) and `Mode` fields (`CodexAdapter`, `OpencodeAdapter`) are public for callers that want to compose these flags onto custom adapter configurations. Default zero values preserve the existing print-mode / exec-mode / run-mode behavior byte-for-byte.

### Monitoring + event-reaction pipeline

- `EventReactionPipeline` / `NewEventReactionPipeline` / `EventReactionConfig` / `DefaultEventReactionConfig` (`event_pipeline.go`) — decorator that wraps any `Provider` and runs each streamed event through the monitors below.
- `ScopeGuard`, `ScopeViolation`, `NewScopeGuard` (`scope_guard.go`) — glob/regex-based allow-list over file paths and tool usage.
- `ProgressTracker`, `ProgressLoop`, `NewProgressTracker` (`progress_tracker.go`) — detects repeated content, repeated tool calls with the same input, and repeated state.
- `CostMonitor`, `BudgetViolation`, `CostRate`, `UsageSummary`, `NewCostMonitor` (`cost_monitor.go`) — token and USD budget tracking.

### Model selection (`provider/model_ops.go`)

- `ModelSelector` interface, `StaticModelSelector`, `NewStaticModelSelector`, `OperationModelConfig`, operation constants `OpChat` and `OpSummarization`.

## Architecture Notes

The `provider` package is intentionally flat: one file per adapter. `layout` (with `layout/gen` and `layout/layouttest`) is a separate package holding the table those adapters derive their paths from, keyed by runtime id and launch shape (a `runtimes.Mode` plus an optional variant such as Claude's `bare`); `layout` imports only the standard library and agent-contracts-leaf. `registry` imports `layout`, and `provider` imports both. The shared `Provider` interface (go-llm-contracts) is small (three methods). Cost, scope and loop monitoring are a decorator (`EventReactionPipeline`) that can wrap any `Provider` without the adapter needing to know.

CLI bridges use a two-level abstraction: a `CLIAdapter` (one per CLI tool) defines how to build arguments and parse one line of output, and a transport wrapper (`PTYBridge` for pty-based or `SubprocessBridge` for pipes) runs the child process and feeds lines through the adapter. Context-value helpers (`WithCLISessionID`, `WithSandboxDir`, `WithProcessCallback`, `WithActivityCallback`, `WithWaitDelay`) let callers pass session-resume IDs, working directories, and process-tracking hooks through to the bridge without widening the `Provider` interface. `pty.go` has a `//go:build !windows` build tag; the subprocess bridge is the portable fallback.

## Dependencies

### Framework-internal

- `github.com/hollis-labs/agent-contracts-leaf` — the `runtimes` vocabulary (runtime ids, modes, capabilities).
- `github.com/hollis-labs/go-llm-contracts` — the `Provider` interface.
- `github.com/hollis-labs/go-llm-types` — request, message, event and usage types.
- `github.com/hollis-labs/go-permission` — the permission posture `Mode`.

### External (direct)

- `github.com/creack/pty v1.1.24` — pseudo-terminal support for the PTY bridge.

### External (indirect)

- `gopkg.in/yaml.v3`. Versions are pinned in `go.mod`.

## Compatibility

- Go 1.26.6 or newer (`go` directive).
- The tested harness versions are listed under "Pure provider projections". Harness behaviour moves between releases; `go test -tags harnessprobe ./layout` re-checks it against the installed binaries.

## Out of scope

- Materializing boot dirs: projections are pure values; `agentkit` owns writing them. The bridges spawn a CLI but plant nothing.
- Credentials, workspace trust and provider auth (explicit `PrepareRuntime` effects only).
- Direct HTTP chat or embedding adapters, shared LLM contracts and rate budgets (`go-llm-types`, `go-llm-contracts`).
- The runtime vocabulary itself (ids, modes, capability names: agent-contracts-leaf `runtimes`), and any provider-neutral launch type or planter.
- Cross-repo drift checks: `layout/layouttest` provides assertions; adopting them is each consumer's decision.

## Testing

```bash
go test ./...
```

Tests are pure-Go unit tests. PTY/subprocess tests do not spawn real CLI binaries by default: they run a fake CLI from `providertest` that replays captured output. Real-spawn smoke tests (`TestClaudeAdapter_BareSpawn_Smoke`, `TestClaudeAdapter_BareSpawn_PopulatedMCP_Smoke`, `TestClaudeAdapter_PTYSpawn_Smoke`, `TestClaudeBootDirSpec_TrustPreAccept_Smoke`) are env-gated (`CLAUDE_BARE_SMOKE=1`, `CLAUDE_PTY_SMOKE=1`); skipped when the relevant CLI binary or auth env var is absent.

### Fake CLIs for consumers (`providertest`)

`providertest` gives any test a fake agent CLI that replays what the real one writes, so libraries and apps test against one shared, captured wire format instead of hand-written scripts:

```go
import "github.com/hollis-labs/go-providers/providertest"

fake := providertest.New(t, "claude",
    providertest.Replay("claude/print_turn1"),
    providertest.Replay("claude/print_turn2_resume").When("--resume"),
)
bridge := provider.NewSubprocessBridge(provider.NewClaudeAdapter(), fake.Path)
// … run two turns …
id, _ := fake.Call(1).ArgAfter("--resume")
```

`Replay` covers turns, resume, unknown resume ids, tool use, interrupts (claude, codex) and errors for claude, codex, opencode and antigravity (captured) and copilot and pi (synthetic ACP); `Script` and `Lines` build a run by hand. `Fake.Install` (or `Fake.Env`, for code that takes an explicit environment) points the runtime's CLI-path variable and `PATH` at the fake. `providertest/fixtures/README.md` lists every fixture and how it was captured and scrubbed.

## License

MIT License. See `LICENSE`.
