package provider

import (
	"encoding/json"
	"fmt"
	"os"
)

// CodexAdapter implements CLIAdapter for the OpenAI Codex CLI.
//
// Turn boundary: emits EventDone on the `turn.completed` event, EventError on
// `turn.failed` or top-level `error` events. Codex always runs single-turn via
// `codex exec`, so each invocation produces exactly one turn boundary.
type CodexAdapter struct{}

func NewCodexAdapter() *CodexAdapter { return &CodexAdapter{} }

func (a *CodexAdapter) Name() string { return "codex" }

func (a *CodexAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	// Codex uses: codex exec "prompt" --json
	// System prompt is file-based (AGENTS.md in sandbox dir), not a flag.
	// Resume is interactive-only in Codex, so we always use single-turn exec.
	return []string{"exec", prompt, "--json"}
}

func (a *CodexAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
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

type codexTurnCompleted struct {
	Type   string `json:"type"`
	TurnID string `json:"turn_id"`
}

type codexError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// parseCodexStreamLine parses a single line of Codex --json output.
func parseCodexStreamLine(line []byte) ([]StreamEvent, error) {
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
				return []StreamEvent{{Type: EventDelta, Content: text}}, nil
			}
		}
		return nil, nil

	case "turn.completed":
		return []StreamEvent{{Type: EventDone}}, nil

	case "turn.failed", "error":
		var errEvt codexError
		if err := json.Unmarshal(line, &errEvt); err == nil && errEvt.Message != "" {
			return []StreamEvent{{Type: EventError, Error: errEvt.Message}}, nil
		}
		return []StreamEvent{{Type: EventError, Error: "codex error"}}, nil

	case "thread.started", "turn.started":
		// Informational — skip.
		return nil, nil

	default:
		return nil, nil
	}
}
