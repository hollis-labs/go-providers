package provider

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// ClaudeAdapter implements CLIAdapter for the Claude Code CLI.
//
// Turn boundary: emits llmtypes.EventDone when ParseLine sees a stream-json `result`
// event with `subtype: "success"`, and llmtypes.EventError when `is_error: true` or
// `subtype: "error"`. llmtypes.EventUsage is emitted alongside llmtypes.EventDone when token
// usage is available on the `result` event.
type ClaudeAdapter struct {
	// Binary, when set, is the executable Detect returns, used as-is: no
	// env override and no PATH search. It pins a binary per adapter so a
	// host never has to wrap the adapter for it; a wrapper hides the
	// adapter's optional interfaces (EventParser, the classifiers, ...).
	Binary string

	// ExtraArgs are a caller's extra arguments. BuildArgs places them at
	// the convention's extra slot (ArgExtra), never blindly at the end:
	// for Claude and agy that is before the variadic --add-dir <project>,
	// for codex exec and opencode run before the prompt. They are flags with
	// their values, with no "--", and the last must not be a flag that takes
	// a value (see ExtraArgsBuilder).
	ExtraArgs []string

	// SkipPermissions adds --dangerously-skip-permissions to CLI args.
	// Only set when developer_mode is enabled; never set for production.
	SkipPermissions bool

	// MCPExclusive adds --strict-mcp-config, which keeps claude to the MCP
	// servers this launch passes with --mcp-config. Measured on claude
	// 2.1.286: it leaves out the user-level top-level mcpServers of
	// ~/.claude.json and a .mcp.json in the working directory, which claude
	// otherwise loads next to the planted ones, past any MCP allow-list the
	// host applies to the planted ones. Not measured, so not claimed: claude.ai
	// account connectors, managed servers and plugin servers. Anthropic's
	// documentation (not measured here) says a deployed managed-mcp.json makes
	// claude exit at startup when the flag is passed, and that before Claude
	// Code v2.1.246 a strict session still waited on approval for project
	// servers it was not loading.
	//
	// NOTHING PLANTED MEANS NOTHING: with no --mcp-config in the argv (no
	// MCPConfigPath on the adapter path) the flag leaves claude with no MCP
	// servers and no error. A host that planted servers and needs them checks
	// that the argv carries --mcp-config. Off by default, which leaves the argv
	// as it was. Measured in every shape (registry.MCPExclusivityFlag); --bare
	// already skips the user's servers, and the flag is harmless there.
	// ProjectionOptions.MCPExclusive is the same request on the prepared path.
	MCPExclusive bool

	// PermissionMode sets `permissions.defaultMode` in the planted
	// `.claude/settings.json` (see BootDirSpec). It is a first-class knob
	// for the full Claude Code permission-mode vocabulary:
	//
	//	""                  — no override. If SkipPermissions is true the
	//	                      planted file still emits
	//	                      `bypassPermissions` for back-compat;
	//	                      otherwise no `permissions` block is planted.
	//	"default"           — standard prompt-on-each-tool behavior,
	//	                      emitted explicitly.
	//	"acceptEdits"       — auto-accept file edits, prompt for the rest.
	//	"plan"              — plan mode (read-only; no edits or commands).
	//	"bypassPermissions" — skip all permission prompts (settings-schema
	//	                      equivalent of --dangerously-skip-permissions).
	//
	// When PermissionMode is non-empty it wins over the SkipPermissions
	// back-compat default for the planted settings.json. SkipPermissions
	// independently controls the --dangerously-skip-permissions CLI flag
	// in BuildArgs; PermissionMode does not touch argv. Setting an
	// unrecognized value makes the .claude/settings.json Render fail.
	//
	// This field exists so consumers no longer post-process the planted
	// settings.json to plant `acceptEdits` / `plan` (the workaround
	// Torque and Tether carried before go-providers exposed it).
	PermissionMode string

	// AdditionalDirectories lists absolute directories the claude agent
	// may access beyond its working directory. Each entry becomes a
	// member of `permissions.additionalDirectories` in the planted
	// `.claude/settings.json`.
	//
	// Why this exists: a BootDirSpec materializes claude's cwd as a
	// throwaway per-task tempdir. claude treats that cwd as its
	// workspace and gates file access outside it; a claude agent asked
	// to write into a real project path is therefore confined to its
	// boot dir. AdditionalDirectories is claude's analogue of codex's
	// CodexAdapter.WritableRoots — it widens the accessible set to the
	// directories the task needs (the settings-file form of the
	// `--add-dir` CLI flag).
	//
	// Empty / nil → no `additionalDirectories` key is emitted and the
	// planted settings.json is byte-identical to before this field
	// existed.
	AdditionalDirectories []string

	// PTY signals the consumer runtime spawns claude as a long-lived PTY
	// (interactive TUI) rather than a per-turn subprocess. When true,
	// BuildArgs omits the print-mode flags (-p, --output-format, --verbose,
	// --system-prompt). Per-turn payloads arrive via PTY stdin (the
	// go-agent-sessions BootMode=stdin path); system prompts are routed via
	// BootPrompt rather than --system-prompt.
	//
	// When Bare is also true, Bare wins: bare mode is print-mode-focused
	// per Anthropic's docs ("recommended mode for scripted and SDK calls").
	PTY bool

	// Bare emits --bare and the explicit-injection flags listed below. In
	// bare mode the claude CLI skips auto-discovery of hooks, skills,
	// plugins, MCP servers, auto-memory, CLAUDE.md, OAuth, keychain reads,
	// and operator config: only flags passed explicitly take effect. This
	// is Anthropic's recommended mode for scripted/SDK calls and will
	// become the default for `-p` in a future claude release. Auth in bare
	// mode is strictly via ANTHROPIC_API_KEY env var or apiKeyHelper via
	// --settings (OAuth and keychain are never read).
	//
	// In bare mode the systemPrompt parameter to BuildArgs is ignored —
	// system context flows via the planted CLAUDE.md referenced through
	// AppendSystemPromptFile (see BootDirSpec / BareInjectionPaths).
	Bare bool

	// MCPConfigPath emits --mcp-config <path> when the field is
	// non-empty — in every mode (bare, PTY, streaming, print). Loading
	// the MCP config explicitly is NOT subject to the project-scoped
	// .mcp.json "Use this MCP server?" trust prompt that otherwise fires
	// in interactive (PTY) mode, so non-bare consumers set this to the
	// planted .mcp.json to give spawned agents their MCP servers without
	// an approval gate. In bare mode it is additionally the only way to
	// get MCP servers at all (bare disables .mcp.json auto-discovery).
	// Empty value emits no flag.
	MCPConfigPath string

	// AppendSystemPromptFile emits --append-system-prompt-file <path>
	// when Bare is true and the field is non-empty. Replaces the
	// auto-discovered CLAUDE.md auto-load that bare mode disables.
	// Ignored when Bare is false.
	AppendSystemPromptFile string

	// SettingsPath emits --settings <path> when Bare is true and the
	// field is non-empty. Use to flow per-task settings (apiKeyHelper,
	// approvedTools, etc.) without depending on the user's global
	// ~/.claude/settings.json. Ignored when Bare is false.
	SettingsPath string

	// ProjectDir emits --add-dir <path> when non-empty, in every mode, as
	// the projection does. Grants tool access to the project root when
	// claude runs with cwd = bootDir. A consumer that sets it must not also
	// splice BootDirSpec.ProjectDirArg into extra args.
	ProjectDir string

	// Model emits --model <name> when non-empty, in every mode. Empty keeps
	// claude's default model.
	Model string

	// SkillsDir emits --add-dir <path> when Bare is true and the field is
	// non-empty: bare claude discovers skills only under an added
	// directory, so set it to the boot dir when skills are planted there
	// (the projection adds it when ProjectionOptions.Skills is non-empty).
	// Ignored when Bare is false.
	SkillsDir string

	// InputMode selects the claude CLI's `--input-format` flag in print
	// mode. Defaults to "" (no flag emitted, claude defaults to "text").
	// Values:
	//   ""            — no --input-format flag emitted (current behavior).
	//   "stream-json" — emit --input-format stream-json. Pair with print-mode
	//                   --output-format stream-json to put claude into
	//                   "Streaming Input Mode" (Anthropic's term): a single
	//                   long-lived process reads NDJSON `{type:"user",...}`
	//                   messages from stdin, replies with stream-json over
	//                   stdout, and reuses its KV-cache across turns until
	//                   stdin EOF. The runtime that drives this loop lives
	//                   in go-agent-sessions (streamingStdio kind); this
	//                   adapter only emits the argv.
	// Ignored in Bare and PTY modes (those branches don't emit
	// --input-format). Bare/PTY callers that want streaming input should
	// use NewClaudeAdapterStreamingStdio() instead.
	InputMode string

	// ApiKeyHelperPath, when non-empty, is written into the planted
	// .claude/settings.json as `apiKeyHelper: <path>`. Bare-mode claude
	// invokes the helper per request: the helper's stdout is consumed as
	// the bearer token used for `Authorization: Bearer <token>` against
	// `https://api.anthropic.com`. Documented at
	// https://docs.claude.com/en/docs/claude-code/iam (search "apiKeyHelper").
	//
	// Why this exists: bare mode disables the CLI's
	// OAuth/keychain auto-resolution, so subscription users (no
	// ANTHROPIC_API_KEY in env, authenticated via `claude` interactive
	// login → macOS keychain) lose the auth surface bare needs. The
	// helper closes the gap: it can read the keychain (or any other
	// per-environment secret store) and emit a fresh token on demand.
	// Empirically the keychain's `claudeAiOauth.accessToken`
	// (`sk-ant-oat01-...`) authenticates against the API directly when
	// returned by an apiKeyHelper — no exchange to `sk-ant-api03-`
	// needed.
	//
	// Path requirements (per claude's docs):
	//   - Absolute path; relative paths are not honored.
	//   - Executable bit set; the file is invoked directly (no shell
	//     interpretation).
	//   - First line of stdout is consumed as the token; trailing
	//     whitespace is trimmed.
	//   - Non-zero exit aborts the request with an auth error surfaced
	//     by the CLI ("Not logged in" or similar).
	//
	// Ignored when Bare is false (the non-bare CLI runs its own auto-
	// discovery and ignores this settings.json field). Empty value
	// emits no `apiKeyHelper` field — bare mode then falls back to
	// ANTHROPIC_API_KEY only.
	ApiKeyHelperPath string
}

func NewClaudeAdapter() *ClaudeAdapter { return &ClaudeAdapter{} }

// NewClaudeAdapterDev returns a print-mode ClaudeAdapter with
// --dangerously-skip-permissions enabled, for developer-mode subprocess-per-
// turn sessions. For developer-mode long-lived PTY sessions, use
// NewClaudeAdapterDevPTY.
func NewClaudeAdapterDev() *ClaudeAdapter { return &ClaudeAdapter{SkipPermissions: true} }

// NewClaudeAdapterPTY returns a ClaudeAdapter configured for long-lived PTY
// (interactive) sessions: BuildArgs emits interactive-shape args without the
// print-mode flags. Use this when the consumer runtime spawns claude once and
// streams per-turn payloads over PTY stdin (e.g. go-agent-sessions ptyRuntime).
func NewClaudeAdapterPTY() *ClaudeAdapter { return &ClaudeAdapter{PTY: true} }

// NewClaudeAdapterDevPTY returns a PTY-mode ClaudeAdapter with
// --dangerously-skip-permissions enabled, for developer-mode long-lived
// sessions.
func NewClaudeAdapterDevPTY() *ClaudeAdapter { return &ClaudeAdapter{PTY: true, SkipPermissions: true} }

// NewClaudeAdapterBare returns a print-mode ClaudeAdapter with --bare
// enabled. Consumers populate MCPConfigPath / AppendSystemPromptFile /
// SettingsPath / ProjectDir on the returned adapter (typically via
// BareInjectionPaths against a planted BootDirSpec) before calling
// BuildArgs. Auth requires ANTHROPIC_API_KEY in env (or apiKeyHelper via
// SettingsPath).
func NewClaudeAdapterBare() *ClaudeAdapter { return &ClaudeAdapter{Bare: true} }

// NewClaudeAdapterDevBare returns a bare-mode ClaudeAdapter with
// --dangerously-skip-permissions enabled, for developer-mode scripted
// sessions.
func NewClaudeAdapterDevBare() *ClaudeAdapter {
	return &ClaudeAdapter{Bare: true, SkipPermissions: true}
}

// NewClaudeAdapterStreamingStdio returns a ClaudeAdapter configured for
// "Streaming Input Mode" (Anthropic's term in the Agent SDK docs): a
// single long-lived `claude -p` process that reads NDJSON `{type:"user",...}`
// messages from stdin, replies with stream-json on stdout, and reuses its
// KV-cache across turns until stdin EOF. The runtime that owns the stdin
// loop, attach-fanout, and session-id handling lives in
// go-agent-sessions (streamingStdio kind); this adapter only emits the
// argv shape.
//
// BuildArgs emits (with cliSessionID == ""):
//
//	-p --input-format stream-json --output-format stream-json --verbose
//
// With cliSessionID != "", --resume <id> is prepended (recovery /
// cold-start primitive).
//
// The positional prompt and --system-prompt parameter are intentionally
// dropped in this mode — per-turn payloads and system context flow over
// stdin, not argv. ApiKeyHelperPath is still settable post-construction.
func NewClaudeAdapterStreamingStdio() *ClaudeAdapter {
	return &ClaudeAdapter{InputMode: "stream-json"}
}

// NewClaudeAdapterDevStreamingStdio returns a streaming-stdio
// ClaudeAdapter with --dangerously-skip-permissions enabled, for
// developer-mode long-lived stream-json sessions.
func NewClaudeAdapterDevStreamingStdio() *ClaudeAdapter {
	return &ClaudeAdapter{InputMode: "stream-json", SkipPermissions: true}
}

func (a *ClaudeAdapter) Name() string { return "claude" }

// BuildArgs resolves Claude's launch convention (see claudeConvention) from
// the adapter's fields: the same argv a ProviderProjection resolves from the
// boot-dir layout.
func (a *ClaudeAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	return a.BuildArgsWithExtras(prompt, systemPrompt, cliSessionID, nil)
}

// BuildArgsWithExtras implements ExtraArgsBuilder.
func (a *ClaudeAdapter) BuildArgsWithExtras(prompt, systemPrompt, cliSessionID string, extras []string) []string {
	shape := claudeProjectionShape(a)
	return resolveAdapterTurn(claudeConvention(a, shape, a.fieldPaths(shape)), prompt, systemPrompt, cliSessionID, adapterExtras(a.ExtraArgs, extras))
}

func (a *ClaudeAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	return parseClaudeStreamLine(line)
}

func (a *ClaudeAdapter) Detect() (string, bool) {
	return detect(runtimes.Claude, a.Binary)
}

// IsSessionLost implements SessionLostClassifier. `claude --resume <id>`
// with an id claude no longer has writes "No conversation found with session
// ID: <id>" to stderr, a result with subtype error_during_execution, and
// exits 1, in print mode and over streaming stdio alike
// (providertest/fixtures/claude/print_resume_unknown_id and
// stream_resume_unknown_id, claude 2.1.x). The result line on stdout carries
// no reason, so stderr is where the loss shows.
func (a *ClaudeAdapter) IsSessionLost(stderrTail []byte) bool {
	return bytes.Contains(stderrTail, []byte("No conversation found with session ID"))
}

// Claude Code stream-json event types.
// See: claude -p "..." --output-format stream-json --verbose

// claudeEvent is the top-level envelope for all stream-json events.
type claudeEvent struct {
	Type    string `json:"type"`    // "system", "assistant", "result", "rate_limit_event", "error"
	Subtype string `json:"subtype"` // e.g. "init", "success"
}

// claudeAssistantEvent is an "assistant" event wrapping a message object.
type claudeAssistantEvent struct {
	Type    string             `json:"type"`
	UUID    string             `json:"uuid"`
	Message claudeAssistantMsg `json:"message"`
}

type claudeAssistantMsg struct {
	ID      string               `json:"id"`
	Role    string               `json:"role"`
	Content []claudeContentBlock `json:"content"`
	Usage   *claudeUsage         `json:"usage,omitempty"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"` // "text", "tool_use", "tool_result"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// claudeResultEvent is a "result" event emitted when the CLI run completes.
type claudeResultEvent struct {
	Type       string       `json:"type"`
	Subtype    string       `json:"subtype"` // "success" or "error"
	IsError    bool         `json:"is_error"`
	Result     string       `json:"result"`
	StopReason string       `json:"stop_reason"`
	Usage      *claudeUsage `json:"usage,omitempty"`
	SessionID  string       `json:"session_id"`
	UUID       string       `json:"uuid"`
	// TotalCostUSD and ModelUsage are running totals for the session; see
	// claude_cost.go for how they become a per-turn cost.
	TotalCostUSD *float64                    `json:"total_cost_usd,omitempty"`
	ModelUsage   map[string]claudeModelUsage `json:"modelUsage,omitempty"`
}

// claudeBlockID identifies one content block of an assistant event. Claude
// Code writes one assistant event per block, each with the block at index 0
// and the message id shared by every block of the message, so the event's
// own uuid is what tells blocks apart; the index is appended for an event
// that carries more than one. An event without a uuid falls back to the
// message id and index.
func claudeBlockID(ev claudeAssistantEvent, index int) string {
	switch {
	case ev.UUID != "" && index == 0:
		return ev.UUID
	case ev.UUID != "":
		return fmt.Sprintf("%s:%d", ev.UUID, index)
	case ev.Message.ID != "":
		return fmt.Sprintf("%s:%d", ev.Message.ID, index)
	default:
		return ""
	}
}

// claudeStopReason normalises a result's stop_reason, defaulting to
// end_turn for a successful result that names none.
func claudeStopReason(raw string) string {
	if raw == "" {
		return llmtypes.StopReasonEndTurn
	}
	return llmtypes.NormalizeStopReason(raw)
}

// claudeSystemEvent is a "system" event emitted at CLI startup.
// The "init" subtype includes the CLI session ID needed for --resume.
type claudeSystemEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
}

// claudeErrorEvent is a top-level "error" event.
type claudeErrorEvent struct {
	Type  string `json:"type"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseClaudeStreamLine parses a single line of Claude Code stream-json output
// and returns zero or more StreamEvents. Unrecognized event types are silently skipped.
func parseClaudeStreamLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	// Peek at the type field to decide which struct to unmarshal into.
	var envelope claudeEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse claude event: %w", err)
	}

	switch envelope.Type {
	case "assistant":
		return parseClaudeAssistant(line)
	case "result":
		return parseClaudeResult(line)
	case "error":
		return parseClaudeError(line)
	case "system":
		return parseClaudeSystem(line)
	case "rate_limit_event":
		// Informational — skip silently.
		return nil, nil
	default:
		// Unknown event type — skip.
		return nil, nil
	}
}

func parseClaudeAssistant(line []byte) ([]llmtypes.StreamEvent, error) {
	var ev claudeAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse assistant event: %w", err)
	}

	var events []llmtypes.StreamEvent
	for i, block := range ev.Message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				events = append(events, llmtypes.StreamEvent{
					Type:    llmtypes.EventDelta,
					Content: block.Text,
					BlockID: claudeBlockID(ev, i),
				})
			}
		case "tool_use":
			input := make(map[string]any)
			if len(block.Input) > 0 {
				_ = json.Unmarshal(block.Input, &input)
			}
			events = append(events, llmtypes.StreamEvent{
				Type: llmtypes.EventToolUse,
				ToolUse: &llmtypes.ToolUseBlock{
					ID:    block.ID,
					Name:  block.Name,
					Input: input,
				},
			})
			// tool_result blocks are internal to Claude CLI's tool loop — skip.
		}
	}

	return events, nil
}

func parseClaudeResult(line []byte) ([]llmtypes.StreamEvent, error) {
	var ev claudeResultEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse result event: %w", err)
	}

	if ev.IsError || ev.Subtype == "error" {
		return []llmtypes.StreamEvent{
			{Type: llmtypes.EventError, Error: ev.Result},
		}, nil
	}

	var events []llmtypes.StreamEvent

	// Emit usage if available.
	if ev.Usage != nil {
		events = append(events, llmtypes.StreamEvent{
			Type: llmtypes.EventUsage,
			Usage: &llmtypes.Usage{
				InputTokens:         ev.Usage.InputTokens,
				OutputTokens:        ev.Usage.OutputTokens,
				CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
				CacheReadTokens:     ev.Usage.CacheReadInputTokens,
				StopReason:          claudeStopReason(ev.StopReason),
				CostUSD:             claudeResultCost(ev),
			},
		})
	}

	events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventDone})
	return events, nil
}

func parseClaudeSystem(line []byte) ([]llmtypes.StreamEvent, error) {
	var ev claudeSystemEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse system event: %w", err)
	}
	if ev.Subtype == "init" && ev.SessionID != "" {
		return []llmtypes.StreamEvent{
			{Type: llmtypes.EventSessionID, SessionID: ev.SessionID},
		}, nil
	}
	return nil, nil
}

func parseClaudeError(line []byte) ([]llmtypes.StreamEvent, error) {
	var ev claudeErrorEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse error event: %w", err)
	}
	return []llmtypes.StreamEvent{
		{Type: llmtypes.EventError, Error: ev.Error.Message},
	}, nil
}
