package provider

import (
	"os"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// OpencodeAdapter implements CLIAdapter for the opencode CLI
// (https://github.com/opencode-ai/opencode).
//
// Two argv shapes are supported, selected by the Mode field:
//
//   - "" (default, run mode): emits `opencode run --agent <Agent>
//     [--model <Model>] [--dir <Dir>] "<prompt>"`.
//
//   - "serve-http": emits `opencode serve --port 0 --hostname 127.0.0.1`.
//     One long-lived subprocess exposes an HTTP API and server-sent events.
//     Session creation, per-turn prompts, event streaming, and shutdown live
//     in the consumer runtime (go-agent-sessions ServeHTTP kind), not in this
//     adapter.
//
// Turn boundary: opencode emits plain text on stdout with no structured
// completion event. ParseLine emits llmtypes.EventDelta per non-empty line and never
// emits llmtypes.EventDone/llmtypes.EventError directly; the bridge synthesizes llmtypes.EventDone on
// clean process exit.
type OpencodeAdapter struct {
	// Mode selects the argv shape. "" or "run" → `opencode run`
	// (single-turn subprocess). "serve-http" → `opencode serve`
	// (long-lived HTTP server for go-agent-sessions ServeHTTP runtime).
	Mode string

	// Agent is the opencode agent profile name passed via --agent.
	// Required: opencode run aborts without it.
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

func (a *OpencodeAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	if a.Mode == "serve-http" {
		// Serve mode: one long-lived process exposing opencode's HTTP API.
		// The prompt, systemPrompt, and cliSessionID flow through HTTP
		// session/message endpoints owned by the runtime, not argv.
		return []string{"serve", "--port", "0", "--hostname", "127.0.0.1"}
	}
	// Keep --agent in the argv even when Agent is empty so opencode itself
	// reports the configuration error. This matches the adapter contract used
	// by the upstreaming handoff for uniform spawn/runtime error handling.
	args := []string{"run", "--agent", a.Agent}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if a.Dir != "" {
		args = append(args, "--dir", a.Dir)
	}
	args = append(args, prependOpencodeSystemPrompt(prompt, systemPrompt))
	_ = cliSessionID // opencode run has no resume/session attach flag.
	return args
}

func (a *OpencodeAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	if a.Mode == "serve-http" {
		// HTTP/SSE event mapping lives in the consumer runtime
		// (go-agent-sessions ServeHTTP kind). stdout/stderr are reserved
		// for server diagnostics and listen-URL discovery.
		return nil, nil
	}
	if len(line) == 0 {
		return nil, nil
	}
	text := string(line)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: text + "\n"}}, nil
}

func (a *OpencodeAdapter) Detect() (string, bool) {
	if p := os.Getenv("OPENCODE_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("opencode")
	if err != nil {
		return "", false
	}
	return p, true
}

func prependOpencodeSystemPrompt(prompt, systemPrompt string) string {
	if systemPrompt == "" {
		return prompt
	}
	return "System: " + systemPrompt + "\n\n" + prompt
}
