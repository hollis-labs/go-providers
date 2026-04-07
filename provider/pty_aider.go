package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AiderAdapter implements CLIAdapter for the Aider coding assistant.
// Uses `aider --message "prompt" --no-auto-commits` for single-turn execution.
// With `--message-format json`, Aider outputs structured JSON lines.
type AiderAdapter struct{}

func NewAiderAdapter() *AiderAdapter { return &AiderAdapter{} }

func (a *AiderAdapter) Name() string { return "aider" }

func (a *AiderAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	args := []string{
		"--message", prompt,
		"--no-auto-commits",
		"--no-git",
		"--yes", // auto-confirm file changes
	}
	// Aider doesn't have native resume; each invocation is single-turn.
	// System prompt can be injected via --system-prompt-file but we skip that
	// for now since CLAUDE.md-style injection is handled by the sandbox.
	return args
}

func (a *AiderAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	return parseAiderLine(line)
}

func (a *AiderAdapter) Detect() (string, bool) {
	if p := os.Getenv("AIDER_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("aider")
	if err != nil {
		return "", false
	}
	return p, true
}

// Aider output parsing.
// Aider outputs a mix of plain text and structured markers. In --message mode,
// most output is plain text with markdown formatting. We parse it line by line
// and emit text deltas.

type aiderEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

func parseAiderLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	text := string(line)
	trimmed := strings.TrimSpace(text)

	// Skip Aider's decorative/status lines.
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "Aider v") {
		// Version banner.
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "Model:") || strings.HasPrefix(trimmed, "Git repo:") {
		// Startup info lines.
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "────") || strings.HasPrefix(trimmed, "━━━") {
		// Box-drawing separators.
		return nil, nil
	}

	// Try JSON parse for structured output (future Aider versions may support this).
	if line[0] == '{' {
		var ev aiderEvent
		if err := json.Unmarshal(line, &ev); err == nil {
			switch ev.Type {
			case "message":
				if ev.Content != "" {
					return []StreamEvent{{Type: "delta", Content: ev.Content}}, nil
				}
			case "error":
				return []StreamEvent{{Type: "error", Error: ev.Error}}, nil
			case "done":
				return []StreamEvent{{Type: "done"}}, nil
			}
			return nil, nil
		}
		// Not valid JSON — fall through to plain text.
	}

	// Check for error patterns.
	if strings.HasPrefix(trimmed, "Error:") || strings.HasPrefix(trimmed, "error:") {
		return []StreamEvent{{Type: "error", Error: trimmed}}, nil
	}

	// Plain text content.
	return []StreamEvent{{Type: "delta", Content: text + "\n"}}, nil
}

// parseAiderJSON is reserved for future use when Aider adds structured JSON output.
func parseAiderJSON(line []byte) ([]StreamEvent, error) {
	var ev aiderEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse aider json: %w", err)
	}

	switch ev.Type {
	case "message":
		return []StreamEvent{{Type: "delta", Content: ev.Content}}, nil
	case "error":
		return []StreamEvent{{Type: "error", Error: ev.Error}}, nil
	case "done":
		return []StreamEvent{{Type: "done"}}, nil
	default:
		return nil, nil
	}
}
