# Harness discovery (Step 0)

Which skill and config forms do Claude Code, Codex and OpenCode actually read
under the launch conventions go-providers uses? This is measured, not
inferred: `hack/probe-harness-layout.sh` builds fixtures in a scratch
directory, runs each harness under `env -i` with a fixture `HOME`, and records
what the harness reports. It makes no model call and no network use. The raw
output is committed as the golden
`provider/testdata/harness-discovery/2026-09-29-claude-2.1.285-codex-0.154.0-opencode-1.18.30.tsv`.
Every row of `layout.Table()` cites probe ids from that golden (`Entry.Probe`)
or says why it has none (`Entry.Unprobed`), and a test fails when a cited id
is missing. Antigravity is not in the probe: its rows are `Unprobed`, verified
live against agy 1.2.7.

| | |
|---|---|
| Date | 2026-09-29 |
| Claude Code | 2.1.285 |
| Codex | codex-cli 0.154.0 |
| OpenCode | 1.18.30 |
| Re-run | `bash hack/probe-harness-layout.sh "$(mktemp -d)/p"`; or `go test -tags harnessprobe ./layout -run TestHarnessProbe` |

Observation channels: Claude = first stream-json line (`system/init`:
`skills`, `permissionMode`, `mcp_servers`), with the API base pointed at a
closed port; Codex = the skills block of `codex debug prompt-input`; OpenCode =
`opencode debug skill` and `opencode debug config`. Fixture skill names encode
where they live (`<h>-<root>-<location>-<form>`).

## Results

`root skills/` means `skills/<n>/SKILL.md` directly under the named root.

| id | launch under test | skills the harness saw | finding |
|---|---|---|---|
| C1 | claude, cwd=project | home `.claude/skills`, project `.claude/skills` | directory form under `<cwd>/.claude/skills` is read; flat `.md`, `.agents/skills`, root `skills/`, `.opencode/skills` are not |
| C2 | claude, cwd=boot (go-providers convention) | boot `.claude/skills`, home `.claude/skills` | boot-root `.claude/skills/<n>/SKILL.md` is read |
| C3 | claude, cwd=boot, `--add-dir project` | boot, home, project `.claude/skills` | an `--add-dir` directory contributes its `.claude/skills` |
| C4 | claude `--bare`, cwd=boot | none | bare skips cwd and user skills |
| C5 | claude `--bare --add-dir project` | project `.claude/skills` only | in bare mode only `--add-dir` directories contribute skills |
| CFG1 | claude cwd with `.claude/settings.json` and `.mcp.json` | mode=plan, mcp=probe-mcp | both are read from cwd in `-p` mode |
| CFG2 | claude `--settings file` | mode=acceptEdits | honoured |
| CFG3 | claude `--add-dir` only | mode=default, no mcp | `--add-dir` loads neither settings nor `.mcp.json` |
| CFG4 | claude `--mcp-config file` | mcp=probe-mcp | honoured |
| X1 | codex, cwd=project, default CODEX_HOME | home `.agents`, home `.codex`, project `.agents`, project `.codex` | project root `skills/`, flat, `.claude/skills`, `.opencode/skills` are not read |
| X2 | codex, cwd=boot, CODEX_HOME=boot (go-providers convention) | boot `.agents`, boot root `skills/`, home `.agents` | `$CODEX_HOME/skills` and cwd `.agents/skills` are read; singular `skill/` and flat are not |
| X3 | X2 plus `-C project` (go-providers `exec --cd`) | boot root `skills/`, home `.agents`, project `.agents`, project `.codex` | boot `.agents/skills` disappears; `$CODEX_HOME/skills` survives |
| X4 | codex, cwd=project, CODEX_HOME=boot | same as X3 | the cwd does not matter; CODEX_HOME does |
| CFG1 (codex) | cwd=project, HOME fixture, default CODEX_HOME | TOKEN-AGENTS-PROJ, TOKEN-HOME-DOTCODEX | `$CODEX_HOME/config.toml` (default `~/.codex`) and cwd `AGENTS.md` are read; project `.codex/config.toml` is not applied |
| CFG2 (codex) | cwd=boot, CODEX_HOME=boot | AGENTS-BOOT, CODEX-HOME-BOOT | `$CODEX_HOME/config.toml` and `AGENTS.md` are read |
| CFG3 (codex) | cwd=project, CODEX_HOME=boot | AGENTS-BOOT, AGENTS-PROJ, CODEX-HOME-BOOT | `AGENTS.md` is read from both cwd and `$CODEX_HOME` |
| CFG4 (codex) | HOME=boot2 with `.codex/config.toml` | WRAPPER-HOME-REDIRECT | a `.codex/config.toml` is read only when HOME itself is redirected |
| O1 | opencode, cwd=project | home `.agents`, `.claude`, `.config/opencode/skills`; project `.agents`, `.claude`, `.opencode` | root `skills/`, flat `.md` and `.codex/skills` are not read |
| O2 | O1 plus `OPENCODE_CONFIG_DIR=cfg` (go-providers convention) | adds `cfg/skills` and `cfg/skill` | **`cfg/.opencode/skills` is NOT read** |
| O3 | opencode, cwd=boot | boot `.agents`, `.claude`, `.opencode` (+home) | boot root `skills/` is not read without the config-dir env |
| O4 | O1 plus `OPENCODE_DISABLE_EXTERNAL_SKILLS=1` | home `.config/opencode/skills`, project `.opencode` only | an ambient value of this variable hides every `.claude` and `.agents` result, hence `env -i` |
| CFG1 (opencode) | project `opencode.json`, `.opencode/opencode.json`, home config | all three merged | |
| CFG2 (opencode) | plus `OPENCODE_CONFIG_DIR/opencode.json` | merged as well | |

Flat `<name>.md` skills were read by none of the three harnesses in any
location. The layout table therefore emits only the directory form.

## MCP exclusivity (`hack/probe-mcp-exclusive.sh`)

A separate script and golden
(`provider/testdata/mcp-exclusive/2026-10-01-claude-2.1.286-codex-0.159.3-opencode-1.18.33.tsv`),
so the layout golden above is not re-baselined. The question: which MCP servers
does each CLI load next to the ones a launch plants? A scratch `HOME` holds a
user-level server (`user-probe`); the planted set is passed the way the launch
conventions pass it (`planted-probe`); every probe runs under `env -i`. No
model call: Claude's API base is a closed port and its `system/init` lists the
MCP servers; Codex lists its config and, for `exec`, runs against a closed port
with a dummy key; OpenCode prints its resolved config. Stdio marker servers
record that they were spawned, so a result is also a spawn, not only a listing.

| Runtime | Probe | Launch | Loaded |
|---|---|---|---|
| Claude | MCP1 / MCP2 | per-turn, `--mcp-config`, without / with `--strict-mcp-config` | user + planted / planted |
| Claude | MCP3 | `--strict-mcp-config`, nothing passed | none (a cwd `.mcp.json` is ignored) |
| Claude | MCP5 / MCP6 | `--bare`, without / with the flag | planted / planted |
| Claude | MCP7 / MCP8 | streaming-stdio, without / with the flag | user + planted / planted |
| Claude | MCP9 / MCP10 | PTY, without / with the flag (spawned servers) | user + planted / planted |
| Codex | MCP1 / MCP2 | `codex mcp list`, `CODEX_HOME` unset / the boot dir | user / planted |
| Codex | MCP3 / MCP4 | the same with a project `.codex/config.toml` | planted / user (the project's is not applied) |
| Codex | MCP5 / MCP6 | `codex exec` (spawned servers), `CODEX_HOME` unset / the boot dir | user / planted |
| OpenCode | MCP1 | `OPENCODE_CONFIG_DIR` = the boot dir | planted + project + user |
| OpenCode | MCP2 / MCP3 / MCP4 | `XDG_CONFIG_HOME` empty / `OPENCODE_DISABLE_PROJECT_CONFIG=1` / both | planted + project / planted + user / planted |

Claude's PTY starts MCP servers only after its first-run dialogs, so MCP9 and
MCP10 pre-approve onboarding, the dummy API key and the project's MCP servers
in the scratch config and drive the TUI through a pty (the script needs
`python3` for those two). Claude `--bare` never loads the user's server, with
or without the flag. Codex's app-server reads the same `config.toml` as exec; a
by-hand run of `thread/start` spawned only the planted server, but it is not
in the script. OpenCode's config view was also confirmed by spawning, with
`opencode run`, which uses OpenCode's built-in free model: that is why the
script does not.

OpenCode has no MCP-only switch: the two isolations that remove the user's and
the project's servers remove all of their other config as well, and
`XDG_CONFIG_HOME` is read by other tools too. Not measured: Antigravity (it
will not start without a login), Claude's account connectors (a login), and
managed or plugin servers.

## Supplementary measurement (not in the golden)

The probe has no row for claude `--bare` with the boot directory itself as an
`--add-dir`, which is what the table's bare skills row needs. Measured
separately on the same versions (fixture: `<boot>/.claude/skills/c-boot-claude-dir/SKILL.md`,
cwd=boot, `env -i`, `CLAUDE_CONFIG_DIR` fixture, dummy API key, closed API port):
`claude --bare -p x ...` reports no skill, `claude --bare --add-dir <boot> -p x ...`
reports `c-boot-claude-dir`, and adding a second `--add-dir <project>` does not
change that. The row cites C4 and C5 in the golden and this measurement.

## Not measured

Codex project trust for `.codex/config.toml`; Claude interactive mode; OpenCode
`skills.paths` and `skills.urls`; Codex `--add-dir` (the agent-launcher /
Cairn `project_dir_arg`); `--dir` for OpenCode; model-visible effect of
`CLAUDE.md`, `AGENTS.md`, `agents/<n>.md`. Rows depending on these carry
`Entry.Unprobed`, never a probe id.

Stage B (`codex exec --json "list skills"`, one model call) was not run. The
Codex skill root is not ambiguous: X2, X3 and X4 agree that
`$CODEX_HOME/skills` is scanned with and without `-C`, and `debug prompt-input`
builds its skill list through the same loader the exec path uses. Stage B would
only confirm that the exec path does not filter that root out; if the lead wants
that confirmation, the command is
`CODEX_HOME=$BOOT codex exec --skip-git-repo-check -C "$PROJ" --json "list skill names starting with x-, nothing else"`
and the expected result is the X3 set.

## What it means per consumer

| Finding | go-providers | agentkit | go-agent-wrapper | Nanite | Cairn | Tether |
|---|---|---|---|---|---|---|
| flat `.md` skills are never read (all) | never emits flat | two flat mappings plant skills nobody reads | n/a | dir form, correct | dir form, correct | flat claude compile plants unread skills |
| Claude reads `<cwd>/.claude/skills/<n>/SKILL.md` (C2) | unchanged | wrong form today | n/a | correct | correct | wrong form today |
| Claude `--bare` needs `--add-dir <boot>` for boot skills (C4, C5) | bare convention adds it when skills are projected | n/a | n/a | n/a | n/a | n/a |
| Codex reads `$CODEX_HOME/skills`, not `.agents/skills`, once `--cd` is passed (X2, X3) | skills move to `skills/` | `skills/<id>.md` flat is unread | n/a | "no native mechanism" is wrong | `.agents/skills` is lost under `--cd` | AGENTS.md sections are not skills; clobbers AGENTS.md |
| OpenCode reads `$OPENCODE_CONFIG_DIR/skills`, not `.opencode/skills` (O2, O3) | skills move to `skills/` | `.opencode/skills/<id>.md` flat and misplaced | n/a | dual planting: the `.opencode/skills` copy is dead when cwd is the project | no opencode layout | n/a |
| Codex `.codex/config.toml` needs HOME redirected (CFG4) | uses `CODEX_HOME` + root `config.toml` | n/a | `.codex/config.toml` is right only if HOME is redirected | uses `CODEX_HOME` | uses `CODEX_HOME` | n/a |
