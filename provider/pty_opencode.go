package provider

import (
	"os"
	"strings"
)

// OpencodeAdapter implements CLIAdapter for the opencode CLI
// (https://github.com/opencode-ai/opencode).
//
// Command shape: opencode run --agent <Agent> [--model <Model>] [--dir <Dir>] "<prompt>"
//
// Turn boundary: opencode emits plain text on stdout with no structured
// completion event. ParseLine emits EventDelta per non-empty line and never
// emits EventDone/EventError directly; the bridge synthesizes EventDone on
// clean process exit (same pattern as AiderAdapter).
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

func (a *OpencodeAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}
	text := string(line)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return []StreamEvent{{Type: EventDelta, Content: text}}, nil
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
