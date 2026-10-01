package provider

import (
	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// OpencodeAdapter implements CLIAdapter for the opencode CLI
// (https://github.com/opencode-ai/opencode).
//
// Two argv shapes are supported, selected by the Mode field:
//
//   - "" (default, run mode): emits `opencode run --format json --agent
//     <Agent> [--model <Model>] [--dir <Dir>] [--session <id>] [extra] --
//     <prompt>` (see opencodeConvention).
//
//   - "serve-http": emits `opencode serve --port 0 --hostname 127.0.0.1`.
//     One long-lived subprocess exposes an HTTP API and server-sent events.
//     Session creation, per-turn prompts, event streaming, and shutdown live
//     in the consumer runtime (go-agent-sessions ServeHTTP kind), not in this
//     adapter.
//
// Run mode drives `opencode run --format json`, which writes one JSON
// object per line: step_start, text, tool_use, reasoning (only with
// --thinking), step_finish and error. Every line carries the top-level
// sessionID, and `--session <id>` resumes that conversation on a later
// turn (verified against opencode 1.18.30). ParseLine maps the stream to
// typed llmtypes events; see pty_opencode_events.go for the mapping and
// the turn boundary.
//
// For "serve-http" mode, turn completion is signaled a different way
// entirely — see NewOpencodeAdapterServeHTTP's doc comment and the consumer
// runtime's own session.idle/session.error SSE handling; this ParseLine
// method returns no events at all in that mode (see below).
type OpencodeAdapter struct {
	// Binary, when set, is the executable Detect returns, used as-is: no
	// env override and no PATH search. It pins a binary per adapter so a
	// host never has to wrap the adapter for it; a wrapper hides the
	// adapter's optional interfaces (EventParser, the classifiers, ...).
	Binary string

	// ExtraArgs are a caller's extra arguments. BuildArgs places them at
	// the convention's extra slot (ArgExtra), never blindly at the end:
	// for Claude and agy that is before the variadic --add-dir <project>,
	// for codex exec and opencode run before the prompt.
	ExtraArgs []string

	// Mode selects the argv shape. "" or "run" → `opencode run`
	// (single-turn subprocess). "serve-http" → `opencode serve`
	// (long-lived HTTP server for go-agent-sessions ServeHTTP runtime).
	Mode string

	// Agent is the opencode agent profile name passed via --agent. An
	// empty or unknown name makes opencode 1.18.30 fall back to its
	// default agent (unknown names with a warning on stderr).
	Agent string

	// Model optionally overrides the agent's default model via --model.
	Model string

	// Dir optionally sets the agent's working directory via --dir.
	Dir string
}

func NewOpencodeAdapter() *OpencodeAdapter { return &OpencodeAdapter{} }

// NewOpencodeAdapterServeHTTP returns an OpencodeAdapter configured for
// serve-http mode: BuildArgs emits `["serve", "--port", "0", "--hostname",
// "127.0.0.1"]`, ParseLine returns no events, and the consumer runtime owns
// HTTP session creation, SSE event mapping, and shutdown.
func NewOpencodeAdapterServeHTTP() *OpencodeAdapter {
	return &OpencodeAdapter{Mode: "serve-http"}
}

func (a *OpencodeAdapter) Name() string { return "opencode" }

// BuildArgs resolves OpenCode's launch convention (see opencodeConvention)
// from the adapter's fields.
func (a *OpencodeAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	return a.BuildArgsWithExtras(prompt, systemPrompt, cliSessionID, nil)
}

// BuildArgsWithExtras implements ExtraArgsBuilder.
func (a *OpencodeAdapter) BuildArgsWithExtras(prompt, systemPrompt, cliSessionID string, extras []string) []string {
	shape := opencodeShape(a)
	p := pathArgs{projectDirs: fieldProjectDirs(runtimes.OpenCode, shape, a.Dir)}
	return resolveAdapterTurn(opencodeConvention(a, shape, a.Agent, p), prompt, systemPrompt, cliSessionID, adapterExtras(a.ExtraArgs, extras))
}

func (a *OpencodeAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if a.Mode == "serve-http" {
		// HTTP/SSE event mapping lives in the consumer runtime
		// (go-agent-sessions ServeHTTP kind). stdout/stderr are reserved
		// for server diagnostics and listen-URL discovery.
		return nil, nil
	}
	return parseOpencodeStreamLine(line), nil
}

func (a *OpencodeAdapter) Detect() (string, bool) {
	return detect(runtimes.OpenCode, a.Binary)
}
