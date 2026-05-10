package provider

import (
	"encoding/json"
	"fmt"
	"os"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// ClaudeAdapter implements CLIAdapter for the Claude Code CLI.
//
// Turn boundary: emits llmtypes.EventDone when ParseLine sees a stream-json `result`
// event with `subtype: "success"`, and llmtypes.EventError when `is_error: true` or
// `subtype: "error"`. llmtypes.EventUsage is emitted alongside llmtypes.EventDone when token
// usage is available on the `result` event.
type ClaudeAdapter struct {
	// SkipPermissions adds --dangerously-skip-permissions to CLI args.
	// Only set when developer_mode is enabled; never set for production.
	SkipPermissions bool

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

	// MCPConfigPath emits --mcp-config <path> when Bare is true and the
	// field is non-empty. Empty value emits no flag — bare mode then has
	// zero MCP servers, which is the documented behavior. Ignored when
	// Bare is false.
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

	// ProjectDir emits --add-dir <path> when Bare is true and the field
	// is non-empty. Grants tool access to the project root when claude
	// runs with cwd = bootDir. Ignored when Bare is false. (Non-bare
	// consumers continue to add --add-dir from BootDirSpec.ProjectDirArg.)
	ProjectDir string

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

func (a *ClaudeAdapter) Name() string { return "claude" }

func (a *ClaudeAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	if a.Bare {
		// Bare mode: print-mode shape (-p / --output-format / --verbose)
		// plus --bare and the explicit-injection flags. The systemPrompt
		// parameter is ignored — system context flows via the planted
		// CLAUDE.md referenced through AppendSystemPromptFile.
		args := []string{
			"-p", prompt,
			"--output-format", "stream-json",
			"--verbose",
			"--bare",
		}
		if a.MCPConfigPath != "" {
			args = append(args, "--mcp-config", a.MCPConfigPath)
		}
		if a.AppendSystemPromptFile != "" {
			args = append(args, "--append-system-prompt-file", a.AppendSystemPromptFile)
		}
		if a.SettingsPath != "" {
			args = append(args, "--settings", a.SettingsPath)
		}
		if a.ProjectDir != "" {
			args = append(args, "--add-dir", a.ProjectDir)
		}
		if a.SkipPermissions {
			args = append(args, "--dangerously-skip-permissions")
		}
		if cliSessionID != "" {
			args = append([]string{"--resume", cliSessionID}, args...)
		}
		return args
	}

	if a.PTY {
		// Interactive / long-lived spawn. The claude TUI does not accept
		// `-p` / `--print`; passing them with an empty prompt makes the
		// process exit immediately on arg validation. The prompt and
		// systemPrompt parameters are intentionally ignored: per-turn
		// payloads arrive via PTY stdin, and system prompts are routed
		// via BootPrompt at the lib layer rather than `--system-prompt`.
		var args []string
		if cliSessionID != "" {
			args = append(args, "--resume", cliSessionID)
		}
		if a.SkipPermissions {
			args = append(args, "--dangerously-skip-permissions")
		}
		return args
	}

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
	}
	if a.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	if cliSessionID != "" {
		args = append([]string{"--resume", cliSessionID}, args...)
	} else if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	return args
}

func (a *ClaudeAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	return parseClaudeStreamLine(line)
}

func (a *ClaudeAdapter) Detect() (string, bool) {
	if p := os.Getenv("CLAUDE_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("claude")
	if err != nil {
		return "", false
	}
	return p, true
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
	Message claudeAssistantMsg `json:"message"`
}

type claudeAssistantMsg struct {
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
	// ModelUsage contains per-model breakdowns; we extract aggregate usage instead.
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
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				events = append(events, llmtypes.StreamEvent{
					Type:    "delta",
					Content: block.Text,
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
		stopReason := ev.StopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}
		events = append(events, llmtypes.StreamEvent{
			Type: llmtypes.EventUsage,
			Usage: &llmtypes.Usage{
				InputTokens:         ev.Usage.InputTokens,
				OutputTokens:        ev.Usage.OutputTokens,
				CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
				CacheReadTokens:     ev.Usage.CacheReadInputTokens,
				StopReason:          stopReason,
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
