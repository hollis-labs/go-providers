package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// KiroAdapter implements CLIAdapter for the Kiro CLI (kiro-cli).
// Uses `kiro-cli chat --no-interactive --trust-all-tools "prompt"` for
// non-interactive execution. Output is plain text by default; JSON events
// are parsed when present.
type KiroAdapter struct{}

func NewKiroAdapter() *KiroAdapter { return &KiroAdapter{} }

func (a *KiroAdapter) Name() string { return "kiro" }

func (a *KiroAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	args := []string{
		"chat",
		"--no-interactive",
		"--trust-all-tools",
	}
	if cliSessionID != "" {
		args = append(args, "--resume")
	}
	if model := os.Getenv("KIRO_MODEL"); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)
	return args
}

func (a *KiroAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	return parseKiroStreamLine(line)
}

func (a *KiroAdapter) Detect() (string, bool) {
	if p := os.Getenv("KIRO_CLI_PATH"); p != "" {
		return p, true
	}
	// Primary binary name.
	if p, err := lookPathExpanded("kiro-cli"); err == nil {
		return p, true
	}
	// Fallback: some installs may use "kiro".
	if p, err := lookPathExpanded("kiro"); err == nil {
		return p, true
	}
	return "", false
}

// Kiro JSON event types.

type kiroEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// parseKiroStreamLine parses a single line of Kiro CLI output.
func parseKiroStreamLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	// Try JSON parse.
	if line[0] == '{' {
		var ev kiroEvent
		if err := json.Unmarshal(line, &ev); err == nil {
			switch ev.Type {
			case "message", "assistant", "delta":
				if ev.Content != "" {
					return []StreamEvent{{Type: "delta", Content: ev.Content}}, nil
				}
			case "error":
				return []StreamEvent{{Type: "error", Error: ev.Error}}, nil
			case "done", "result":
				return []StreamEvent{{Type: "done"}}, nil
			}
			return nil, nil
		}
		// Not valid JSON — fall through to plain text.
	}

	text := string(line)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}

	// Skip Kiro's decorative box-drawing and banner lines.
	if strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "╰") ||
		strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "❯") {
		return nil, nil
	}

	// Check for error patterns.
	if strings.HasPrefix(trimmed, "Error:") || strings.HasPrefix(trimmed, "error:") {
		return []StreamEvent{{Type: "error", Error: trimmed}}, nil
	}

	return []StreamEvent{{Type: "delta", Content: text + "\n"}}, nil
}

// parseKiroJSON parses a structured JSON event from Kiro.
func parseKiroJSON(line []byte) ([]StreamEvent, error) {
	var ev kiroEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse kiro json: %w", err)
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
