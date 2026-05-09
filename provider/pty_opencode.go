package provider

import (
	"os"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// OpencodeAdapter implements CLIAdapter for the opencode CLI
// (https://github.com/opencode-ai/opencode).
//
// Command shape: opencode run --agent <Agent> [--model <Model>] [--dir <Dir>] "<prompt>"
//
// Turn boundary: opencode emits plain text on stdout with no structured
// completion event. ParseLine emits llmtypes.EventDelta per non-empty line and never
// emits llmtypes.EventDone/llmtypes.EventError directly; the bridge synthesizes llmtypes.EventDone on
// clean process exit.
type OpencodeAdapter struct {
	// Agent is the opencode agent profile name passed via --agent.
	// Required: opencode run aborts without it.
	Agent string

	// Model optionally overrides the agent's default model via --model.
	Model string

	// Dir optionally sets the agent's working directory via --dir.
	Dir string
}

func NewOpencodeAdapter() *OpencodeAdapter { return &OpencodeAdapter{} }

func (a *OpencodeAdapter) Name() string { return "opencode" }

func (a *OpencodeAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
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
