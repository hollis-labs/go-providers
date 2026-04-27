package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// JunieAdapter implements CLIAdapter for the JetBrains Junie CLI.
// Uses `junie --output-format json "task"` for non-interactive JSON output.
//
// Turn boundary: emits EventDone on the `result` event with
// `subtype: "success"`, EventError on errors. Same shape as ClaudeAdapter.
type JunieAdapter struct{}

func NewJunieAdapter() *JunieAdapter { return &JunieAdapter{} }

func (a *JunieAdapter) Name() string { return "junie" }

func (a *JunieAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	args := []string{
		"--output-format", "json",
	}
	if cliSessionID != "" {
		args = append(args, "--session-id", cliSessionID)
	}
	if model := os.Getenv("JUNIE_MODEL"); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, prompt)
	return args
}

func (a *JunieAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	return parseJunieStreamLine(line)
}

func (a *JunieAdapter) Detect() (string, bool) {
	if p := os.Getenv("JUNIE_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("junie")
	if err != nil {
		return "", false
	}
	return p, true
}

// Junie JSON event types.

type junieEvent struct {
	Type string `json:"type"`
}

type junieMessageEvent struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Delta   string `json:"delta"`
}

type junieToolEvent struct {
	Type     string         `json:"type"`
	ToolName string         `json:"tool_name"`
	ToolID   string         `json:"tool_id"`
	Input    map[string]any `json:"input"`
}

type junieSessionEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type junieResultEvent struct {
	Type   string `json:"type"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
	Usage  *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// parseJunieStreamLine parses a single line of Junie --output-format json output.
func parseJunieStreamLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	// Non-JSON lines — plain text fallback.
	if line[0] != '{' {
		text := strings.TrimSpace(string(line))
		if text == "" {
			return nil, nil
		}
		// Skip Junie banner/decorative lines.
		if strings.Contains(text, "//////") || strings.Contains(text, "junie") {
			return nil, nil
		}
		return []StreamEvent{{Type: EventDelta, Content: string(line) + "\n"}}, nil
	}

	var envelope junieEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse junie event: %w", err)
	}

	switch envelope.Type {
	case "message", "assistant":
		var msg junieMessageEvent
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("parse junie message: %w", err)
		}
		text := msg.Delta
		if text == "" {
			text = msg.Content
		}
		if text != "" && (msg.Role == "" || msg.Role == "assistant") {
			return []StreamEvent{{Type: EventDelta, Content: text}}, nil
		}
		return nil, nil

	case "tool_use":
		var tu junieToolEvent
		if err := json.Unmarshal(line, &tu); err != nil {
			return nil, fmt.Errorf("parse junie tool_use: %w", err)
		}
		return []StreamEvent{{
			Type: EventToolUse,
			ToolUse: &ToolUseBlock{
				ID:    tu.ToolID,
				Name:  tu.ToolName,
				Input: tu.Input,
			},
		}}, nil

	case "session":
		var ev junieSessionEvent
		if err := json.Unmarshal(line, &ev); err == nil && ev.SessionID != "" {
			return []StreamEvent{{Type: EventSessionID, SessionID: ev.SessionID}}, nil
		}
		return nil, nil

	case "result", "done":
		var ev junieResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("parse junie result: %w", err)
		}
		if ev.Error != "" {
			return []StreamEvent{{Type: EventError, Error: ev.Error}}, nil
		}
		var events []StreamEvent
		if ev.Usage != nil {
			events = append(events, StreamEvent{
				Type: EventUsage,
				Usage: &Usage{
					InputTokens:  ev.Usage.InputTokens,
					OutputTokens: ev.Usage.OutputTokens,
					StopReason:   "end_turn",
				},
			})
		}
		events = append(events, StreamEvent{Type: EventDone})
		return events, nil

	case "error":
		var ev junieResultEvent
		if err := json.Unmarshal(line, &ev); err == nil && ev.Error != "" {
			return []StreamEvent{{Type: EventError, Error: ev.Error}}, nil
		}
		return []StreamEvent{{Type: EventError, Error: "junie error"}}, nil

	default:
		return nil, nil
	}
}
