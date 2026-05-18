package provider

import (
	"encoding/json"
	"fmt"
	"os"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CodexAdapter implements CLIAdapter for the OpenAI Codex CLI.
//
// Two argv shapes are supported, selected by the Mode field:
//
//   - "" (default, exec mode): emits `codex exec <prompt> --json`. One
//     subprocess per turn. Turn boundary: llmtypes.EventDone on the
//     `turn.completed` event; llmtypes.EventError on `turn.failed` or a
//     top-level `error` event. ParseLine handles the line-delimited
//     stream-json events.
//
//   - "app-server": emits `codex app-server`. One long-lived subprocess
//     speaking JSON-RPC 2.0 over stdio (default `--listen stdio://`;
//     unix-socket / websocket are documented experimental and not
//     exposed here yet). Threads live in-process until 30-min idle;
//     `thread/resume` is the cold-start primitive. ParseLine is a
//     pass-through that returns no events — JSON-RPC framing, request
//     correlation, and event mapping live in the consumer runtime
//     (go-agent-sessions jsonRpcStdio kind), not in this adapter.
type CodexAdapter struct {
	// Mode selects the argv shape. "" or "exec" → `codex exec`
	// (single-turn subprocess). "app-server" → `codex app-server`
	// (long-lived JSON-RPC daemon over stdio).
	Mode string

	// ApprovalPolicy sets `approval_policy` in the planted config.toml
	// (see BootDirSpec). It is the codex equivalent of
	// ClaudeAdapter.PermissionMode — a first-class knob for codex's
	// approval vocabulary:
	//
	//	""             — resolves to "never" (the headless-safe default).
	//	"untrusted"    — prompt before running anything not on the trusted
	//	                 list.
	//	"on-failure"   — run in the sandbox; prompt only if a command fails.
	//	"on-request"   — the model decides when to escalate for approval.
	//	"never"        — never prompt for approval.
	//
	// The default is "never" — NOT codex's own interactive default —
	// because BootDirSpec materializes a HEADLESS per-task boot dir with
	// no human at a TTY. A codex that prompts for approval under a headless
	// runtime (codex app-server emits a JSON-RPC approval request) blocks
	// forever. "never" + a writable SandboxMode is the orchestrated-run
	// equivalent of codex's `--full-auto`.
	//
	// An unrecognized value makes the config.toml Render fail.
	ApprovalPolicy string

	// SandboxMode sets `sandbox_mode` in the planted config.toml. Codex's
	// filesystem/network sandbox vocabulary:
	//
	//	""                   — resolves to "workspace-write" (default).
	//	"read-only"          — the agent may read but not write or run
	//	                       network commands.
	//	"workspace-write"    — the agent may write within its workspace
	//	                       (and /tmp); network still gated.
	//	"danger-full-access" — no sandbox.
	//
	// The default is "workspace-write": an orchestrated agent that cannot
	// write its workspace cannot do work. Pair it with ApprovalPolicy —
	// "never" approval over a "read-only" sandbox would let codex run but
	// silently fail every write.
	//
	// An unrecognized value makes the config.toml Render fail.
	SandboxMode string

	// WritableRoots lists additional absolute directories the codex
	// sandbox may write to, beyond the boot dir cwd and /tmp. Each entry
	// becomes a member of `writable_roots` in the planted config.toml's
	// `[sandbox_workspace_write]` table.
	//
	// Why this exists: under SandboxMode "workspace-write" codex's OS
	// sandbox confines writes to the workspace cwd. A BootDirSpec
	// materializes that cwd as a throwaway per-task tempdir, so a codex
	// agent asked to write into a real project path (the operator's repo,
	// an inbox dir) is silently blocked — it can only write its own boot
	// dir. WritableRoots widens the sandbox to the directories the task
	// actually needs without dropping to "danger-full-access".
	//
	// Only meaningful under SandboxMode "workspace-write" (codex ignores
	// the table under "read-only" / "danger-full-access"). Empty / nil →
	// no `[sandbox_workspace_write]` table is emitted and the planted
	// config.toml is byte-identical to before this field existed.
	WritableRoots []string
}

func NewCodexAdapter() *CodexAdapter { return &CodexAdapter{} }

// NewCodexAdapterAppServer returns a CodexAdapter configured for
// app-server mode: BuildArgs emits `["app-server"]`, ParseLine returns
// no events (the consumer runtime owns JSON-RPC framing). Use this when
// driving Codex as a long-lived headless session via the consumer's
// jsonRpcStdio runtime.
func NewCodexAdapterAppServer() *CodexAdapter { return &CodexAdapter{Mode: "app-server"} }

func (a *CodexAdapter) Name() string { return "codex" }

func (a *CodexAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	if a.Mode == "app-server" {
		// App-server mode: one long-lived process speaking JSON-RPC over
		// stdio. The positional prompt and cliSessionID are intentionally
		// ignored — `thread/start` / `thread/resume` are JSON-RPC methods
		// driven by the consumer runtime, not argv flags. Defaults to
		// --listen stdio:// (omitted; explicit-listen flags are not
		// exposed in this sprint).
		return []string{"app-server"}
	}
	// Exec mode (default): single-turn `codex exec <prompt> --json`.
	// System prompt is file-based (AGENTS.md in sandbox dir), not a flag.
	return []string{"exec", prompt, "--json"}
}

func (a *CodexAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if a.Mode == "app-server" {
		// JSON-RPC framing + event mapping live in the consumer runtime
		// (go-agent-sessions jsonRpcStdio kind). This adapter is a
		// pass-through in app-server mode; the runtime reads stdout
		// directly, frames JSON-RPC messages (Content-Length headers),
		// correlates requests to responses, and emits its own typed
		// events. Returning (nil, nil) here is deliberate — do not
		// "fix" this by adding a JSON-RPC parser; that's the wrong layer.
		return nil, nil
	}
	return parseCodexStreamLine(line)
}

func (a *CodexAdapter) Detect() (string, bool) {
	if p := os.Getenv("CODEX_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("codex")
	if err != nil {
		return "", false
	}
	return p, true
}

// Codex JSONL event types.
type codexEvent struct {
	Type string `json:"type"`
}

type codexItemMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Delta   string `json:"delta"`
}

type codexItemCompleted struct {
	Type string `json:"type"`
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

type codexUsage struct {
	InputTokens         int `json:"input_tokens"`
	CachedInputTokens   int `json:"cached_input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	ReasoningOutputUsed int `json:"reasoning_output_tokens"`
}

type codexTurnCompleted struct {
	Type   string      `json:"type"`
	TurnID string      `json:"turn_id"`
	Usage  *codexUsage `json:"usage,omitempty"`
}

type codexError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// parseCodexStreamLine parses a single line of Codex --json output.
func parseCodexStreamLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	var envelope codexEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse codex event: %w", err)
	}

	switch envelope.Type {
	case "item.message":
		var msg codexItemMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("parse codex message: %w", err)
		}
		if msg.Role == "assistant" {
			text := msg.Delta
			if text == "" {
				text = msg.Content
			}
			if text != "" {
				return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: text}}, nil
			}
		}
		return nil, nil

	case "item.completed":
		var item codexItemCompleted
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("parse codex item.completed: %w", err)
		}
		if item.Item.Type != "agent_message" || item.Item.Text == "" {
			return nil, nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: item.Item.Text}}, nil

	case "turn.completed":
		var done codexTurnCompleted
		if err := json.Unmarshal(line, &done); err != nil {
			return nil, fmt.Errorf("parse codex turn.completed: %w", err)
		}
		out := make([]llmtypes.StreamEvent, 0, 2)
		if done.Usage != nil {
			out = append(out, llmtypes.StreamEvent{
				Type: llmtypes.EventUsage,
				Usage: &llmtypes.Usage{
					InputTokens:         done.Usage.InputTokens,
					OutputTokens:        done.Usage.OutputTokens,
					CacheReadTokens:     done.Usage.CachedInputTokens,
					CacheCreationTokens: 0,
				},
			})
		}
		out = append(out, llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return out, nil

	case "turn.failed", "error":
		var errEvt codexError
		if err := json.Unmarshal(line, &errEvt); err == nil && errEvt.Message != "" {
			return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: errEvt.Message}}, nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: "codex error"}}, nil

	case "thread.started", "turn.started":
		// Informational — skip.
		return nil, nil

	default:
		return nil, nil
	}
}
