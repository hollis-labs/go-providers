# providertest fixtures

What each agent CLI writes on the wire, one directory per runtime id. The
`providertest` package embeds this tree (`providertest.Fixtures`) and its
fake binary replays it: `providertest.Replay("claude/print_turn1")`.

Every fixture is either **captured** from a real CLI run and then scrubbed,
or **synthetic**, written by hand from the protocol. Synthetic fixtures are
marked as such below; only captured ones are evidence of what a CLI does.

## File formats

A fixture is named by its stem, e.g. `claude/print_turn1`.

**Per-turn** (one process per turn: `claude -p`, `codex exec`,
`opencode run`, `agy -p`):

| File | Content |
|---|---|
| `<stem>.jsonl` | stdout, verbatim apart from scrubbing |
| `<stem>.stderr` | stderr, when the CLI wrote any |
| `<stem>.exit` | exit code, when it was not 0 |

Replay writes the stdout lines, then the stderr lines, then exits with the
code.

**Duplex** (one long-lived process: claude streaming stdio, codex
app-server, ACP): `<stem>.transcript.jsonl`, one step per line, seen from
the CLI's side:

| Line | Meaning |
|---|---|
| `{"recv":{…}}` | the client wrote this frame to the CLI's stdin |
| `{"send":{…}}` | the CLI wrote this frame to stdout |
| `{"stderr":"…"}` | the CLI wrote this line to stderr |
| `{"eof":true}` | the client closed stdin |
| `{"exit":N}` | the CLI exited with code N |

Replay waits at each `recv` for a matching frame (same JSON-RPC method,
same response id, or same `type`), answers JSON-RPC requests with the
live request's id, and otherwise follows the lines in order. The step
reference is `providertest.Step`.

`captured.json` in a captured directory records the CLI version, the
capture date and the exact argv of each fixture.

## claude — captured, Claude Code 2.1.286, model haiku

Captured with `--setting-sources local --strict-mcp-config
--disable-slash-commands`, so the operator's hooks, MCP servers and skills
stay out of `system/init`.

| Fixture | What it is |
|---|---|
| `print_turn1` | `-p "say hi" --output-format stream-json --verbose`: first turn |
| `print_turn2_resume` | `--resume <turn 1 session> -p "say bye"` |
| `print_resume_unknown_id` | `--resume <unknown id>`: an `error_during_execution` result, `No conversation found with session ID` on stderr, exit 1 |
| `print_tool_use` | Bash tool allowed (`--allowedTools 'Bash(echo:*)'`): tool_use, tool_result, reply |
| `print_tool_denied` | a `touch` under the default permission mode: tool_result error, `permission_denials` in the result |
| `print_error_unknown_model` | `--model claude-nonexistent-0`: an assistant error message from model `<synthetic>`, a result with `is_error` and `terminal_reason: api_error`, a stderr line, exit 1 |
| `stream_two_turns` | `-p --input-format stream-json --output-format stream-json --verbose`: two user frames on stdin, a result for each, stdin closed, exit 0 |
| `stream_resume` | the same, with `--resume <stream session>` |
| `stream_resume_unknown_id` | streaming with an unknown `--resume` id: claude waits for the first user frame, then writes the error result and exits 1 without waiting for stdin to close |

## codex — captured, codex-cli 0.159.2, model gpt-6-luna (reasoning effort low)

| Fixture | What it is |
|---|---|
| `exec_turn1` | `exec "say hi" --json --skip-git-repo-check` |
| `exec_turn2_resume` | `exec resume <thread> "say bye" --json …` |
| `exec_resume_unknown_id` | `exec resume <unknown id>`: no stdout, `no rollout found for thread id` on stderr, exit 1 |
| `exec_tool_use` | `-s read-only`, `echo providertest`: `command_execution` item started/completed |
| `exec_error_unknown_model` | `-m gpt-nonexistent-0`: `error` and `turn.failed` events, exit 1 |
| `app_server_turn` | `app-server`: initialize, initialized, thread/start, two turn/start turns to `turn/completed`, stdin closed |
| `app_server_resume` | initialize, thread/resume of that thread, one turn |
| `app_server_resume_unknown_id` | thread/resume of an unknown id: JSON-RPC error -32600 `no rollout found`; the server stays up until stdin closes |
| `app_server_tool_approval` | thread/start with `approvalPolicy: untrusted`, sandbox read-only: an `item/commandExecution/requestApproval` server request answered `{"decision":"accept"}`, then the command runs |

`exec` reads stdin when it is not a terminal, so the captures carry
`Reading additional input from stdin...` on stderr, as real runs under a
bridge do.

## opencode — captured, opencode 1.18.30

Verbatim `opencode run --format json` stdout, local paths rewritten to
`/work/fixture`.

| Fixture | What it is |
|---|---|
| `run_turn1` | first turn, one step, text reply |
| `run_turn2_resume` | `--session <turn 1 id>`, which recalled turn 1's content |
| `run_tool_use` | one turn of three steps: glob, read, reply |
| `run_error_unknown_model` | opencode **1.18.33**, `-m anthropic/claude-nonexistent-0`. A single `error` line, `UnknownError` "Unexpected server error. Check server logs for details." with `data.ref` and no model name, then exit 1. No stderr. |

## antigravity — captured, agy 1.2.7

Verbatim `agy -p=<prompt> --output-format stream-json` stdout, paths and
names scrubbed.

| Fixture | What it is |
|---|---|
| `print_turn1` | first turn, text reply |
| `print_turn2_resume` | `--conversation <turn 1 id>`, recalled turn 1 |
| `print_tool_run` | run_command under `--dangerously-skip-permissions` |
| `print_tool_denied` | the same command under request-review: auto-denied |
| `print_mcp_tool` | a workspace-plugin MCP call (`call_mcp_tool`) |
| `print_resume_unknown_id` | `--conversation` with an unknown id: a new conversation, a stderr warning, exit 0 |

## copilot — SYNTHETIC

`copilot --acp` over stdio (newline-delimited JSON-RPC 2.0, ACP protocol
version 1). Copilot CLI is not installed on the capture machine, so these
transcripts are written by hand from the ACP spec and from the behaviour
go-agent-wrapper's `adapters/copilotacp` package documents as verified
live against Copilot CLI 1.0.12: initialize → session/new → session/prompt
with `session/update` notifications, then the prompt response carrying
`stopReason`; `session/request_permission` with allow-once / allow-always /
reject-once options for a shell command. Field values (ids, titles,
capability flags) are illustrative. Replace them with captures when a
copilot binary is available.

| Fixture | What it is |
|---|---|
| `acp_turn` | initialize, session/new, one prompt: thought chunk, two message chunks, `end_turn` |
| `acp_resume` | session/load of that session: history replayed as updates, then one prompt |
| `acp_resume_unknown_id` | session/load of an unknown id: JSON-RPC error -32002 |
| `acp_permission` | a prompt whose `pwd` tool call asks `session/request_permission`; the client selects `allow_once` |

## pi — SYNTHETIC

The `pi-acp` bridge (`npx -y pi-acp`) over stdio. Pi is not installed on the
capture machine; these transcripts follow the behaviour go-agent-wrapper's
`adapters/piacp` package documents as verified live against pi-acp 0.0.33 +
pi 0.84.2: `session_info_update` heartbeats and `available_commands_update`,
`agent_message_chunk` only (no thought stream), `session/load` returning no
`sessionId` and replaying the prior turn first, bash tool calls with
`_meta.terminal_*` output, and no permission requests. Field values are
illustrative.

| Fixture | What it is |
|---|---|
| `acp_turn` | initialize, session/new, one prompt |
| `acp_resume` | session/load replay, then one prompt |
| `acp_resume_unknown_id` | session/load of an unknown id: JSON-RPC error -32002 |
| `acp_tool_use` | a bash tool call with incremental terminal output and exit code |

## Scrubbing

These files ship in a public module. Captures are scrubbed before they are
committed:

- session, thread, conversation, item and installation UUIDs become
  `00000000-0000-4000-8000-0000000000NN`, mapped consistently within one
  runtime's capture so a resume fixture names the id its first turn
  reported; `…0000000000ff` is the deliberately unknown resume id
- API ids (`msg_…`, `toolu_…`, `call_…`, `req_…`) become `<prefix>_fixtureNNNN`
- the capture directory becomes `/work/project`, the home directory
  `/home/user`, the host name `fixture-host`, the messaging socket
  `/tmp/providertest.sock`
- thinking signatures and encrypted reasoning become `REDACTED`; rate-limit
  utilization and credit balance become 0; emails become `user@example.com`
- prompts are trivial ("say hi", "say bye", `echo providertest`)

Before committing new or re-captured fixtures, run from the repository
root and check that every UUID it prints is a placeholder:

```bash
grep -rniE "$(whoami)|@|sk-|Bearer|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}" providertest/fixtures/*/ \
  | grep -oiE "[^ \"]*@[^ \"]*|sk-[^ \"]{0,20}|Bearer[^\"]{0,20}|$(whoami)|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}" \
  | grep -viE '^00000000-0000-4000-8000-' | sort | uniq -c
```

The only expected hits are claude's built-in plugin names
(`cc-plugin-…@builtin`).

## Re-capturing

`hack/capturefixtures/main.go` re-records the claude and codex fixtures
with real, cheap model calls and applies the scrubbing above:

```bash
go run hack/capturefixtures/main.go -runtimes claude,codex
```

Review the diff and run the grep before committing. A changed fixture is a
changed wire format: the adapters' tests that replay it say whether the
parsers still agree.
