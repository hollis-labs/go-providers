package provider

import (
	"encoding/json"
	"fmt"
	"os"
)

// QwenAdapter implements CLIAdapter for the Qwen Code CLI.
// Uses `qwen -o stream-json "prompt"` for streaming JSON output.
// Qwen supports the same stream-json format as Claude Code.
//
// Turn boundary: emits EventDone on the `result` event with
// `subtype: "success"`, EventError on `is_error: true` or `subtype: "error"`.
// Same turn-boundary shape as ClaudeAdapter.
type QwenAdapter struct{}

func NewQwenAdapter() *QwenAdapter { return &QwenAdapter{} }

func (a *QwenAdapter) Name() string { return "qwen" }

func (a *QwenAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	args := []string{
		"-o", "stream-json",
	}
	if cliSessionID != "" {
		args = append(args, "--continue")
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	if model := os.Getenv("QWEN_MODEL"); model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, prompt)
	return args
}

func (a *QwenAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	return parseQwenStreamLine(line)
}

func (a *QwenAdapter) Detect() (string, bool) {
	if p := os.Getenv("QWEN_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("qwen")
	if err != nil {
		return "", false
	}
	return p, true
}

// Qwen stream-json event types.
// Qwen Code uses a stream-json format similar to Claude Code.

type qwenEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

type qwenAssistantEvent struct {
	Type    string         `json:"type"`
	Message qwenAssistMsg  `json:"message"`
}

type qwenAssistMsg struct {
	Role    string             `json:"role"`
	Content []qwenContentBlock `json:"content"`
}

type qwenContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type qwenResultEvent struct {
	Type       string     `json:"type"`
	Subtype    string     `json:"subtype"`
	IsError    bool       `json:"is_error"`
	Result     string     `json:"result"`
	StopReason string     `json:"stop_reason"`
	Usage      *qwenUsage `json:"usage,omitempty"`
}

type qwenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type qwenSystemEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
}

type qwenErrorEvent struct {
	Type  string `json:"type"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseQwenStreamLine parses a single line of Qwen -o stream-json output.
// The format is similar to Claude Code's stream-json format.
func parseQwenStreamLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	var envelope qwenEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse qwen event: %w", err)
	}

	switch envelope.Type {
	case "assistant":
		return parseQwenAssistant(line)
	case "result":
		return parseQwenResult(line)
	case "error":
		return parseQwenError(line)
	case "system":
		return parseQwenSystem(line)
	default:
		return nil, nil
	}
}

func parseQwenAssistant(line []byte) ([]StreamEvent, error) {
	var ev qwenAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse qwen assistant: %w", err)
	}

	var events []StreamEvent
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				events = append(events, StreamEvent{
					Type:    "delta",
					Content: block.Text,
				})
			}
		case "tool_use":
			input := make(map[string]any)
			if len(block.Input) > 0 {
				_ = json.Unmarshal(block.Input, &input)
			}
			events = append(events, StreamEvent{
				Type: EventToolUse,
				ToolUse: &ToolUseBlock{
					ID:    block.ID,
					Name:  block.Name,
					Input: input,
				},
			})
		}
	}

	return events, nil
}

func parseQwenResult(line []byte) ([]StreamEvent, error) {
	var ev qwenResultEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse qwen result: %w", err)
	}

	if ev.IsError || ev.Subtype == "error" {
		return []StreamEvent{
			{Type: EventError, Error: ev.Result},
		}, nil
	}

	var events []StreamEvent
	if ev.Usage != nil {
		stopReason := ev.StopReason
		if stopReason == "" {
			stopReason = "end_turn"
		}
		events = append(events, StreamEvent{
			Type: EventUsage,
			Usage: &Usage{
				InputTokens:  ev.Usage.InputTokens,
				OutputTokens: ev.Usage.OutputTokens,
				StopReason:   stopReason,
			},
		})
	}
	events = append(events, StreamEvent{Type: EventDone})
	return events, nil
}

func parseQwenSystem(line []byte) ([]StreamEvent, error) {
	var ev qwenSystemEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse qwen system: %w", err)
	}
	if ev.Subtype == "init" && ev.SessionID != "" {
		return []StreamEvent{
			{Type: EventSessionID, SessionID: ev.SessionID},
		}, nil
	}
	return nil, nil
}

func parseQwenError(line []byte) ([]StreamEvent, error) {
	var ev qwenErrorEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse qwen error: %w", err)
	}
	return []StreamEvent{
		{Type: EventError, Error: ev.Error.Message},
	}, nil
}
