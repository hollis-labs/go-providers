package provider

import (
	"encoding/json"
	"fmt"
	"os"
)

// GeminiAdapter implements CLIAdapter for the Google Gemini CLI.
//
// Turn boundary: emits EventDone on the `result` event with
// `subtype: "success"`, EventError on errors. Same shape as ClaudeAdapter.
type GeminiAdapter struct{}

func NewGeminiAdapter() *GeminiAdapter { return &GeminiAdapter{} }

func (a *GeminiAdapter) Name() string { return "gemini" }

func (a *GeminiAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	// Gemini uses: gemini -p "prompt" --output-format stream-json
	// System prompt is file-based (GEMINI_SYSTEM_MD env var), not a flag.
	// Resume: --resume <id>
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
	}
	if cliSessionID != "" {
		args = append([]string{"--resume", cliSessionID}, args...)
	}
	return args
}

func (a *GeminiAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	return parseGeminiStreamLine(line)
}

func (a *GeminiAdapter) Detect() (string, bool) {
	if p := os.Getenv("GEMINI_CLI_PATH"); p != "" {
		return p, true
	}
	p, err := lookPathExpanded("gemini")
	if err != nil {
		return "", false
	}
	return p, true
}

// Gemini JSONL event types.
type geminiEvent struct {
	Type string `json:"type"`
}

type geminiMessageEvent struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Delta   string `json:"delta"`
}

type geminiInitEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type geminiResultEvent struct {
	Type     string `json:"type"`
	Response string `json:"response"`
	Stats    *struct {
		TokensInput  int `json:"tokens_input"`
		TokensOutput int `json:"tokens_output"`
	} `json:"stats,omitempty"`
	Error string `json:"error,omitempty"`
}

type geminiToolUseEvent struct {
	Type     string         `json:"type"`
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
}

// parseGeminiStreamLine parses a single line of Gemini --output-format stream-json output.
func parseGeminiStreamLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	var envelope geminiEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse gemini event: %w", err)
	}

	switch envelope.Type {
	case "init":
		var ev geminiInitEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("parse gemini init: %w", err)
		}
		if ev.SessionID != "" {
			return []StreamEvent{{Type: EventSessionID, SessionID: ev.SessionID}}, nil
		}
		return nil, nil

	case "message":
		var msg geminiMessageEvent
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("parse gemini message: %w", err)
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

	case "tool_use":
		var tu geminiToolUseEvent
		if err := json.Unmarshal(line, &tu); err != nil {
			return nil, fmt.Errorf("parse gemini tool_use: %w", err)
		}
		return []StreamEvent{{
			Type: EventToolUse,
			ToolUse: &ToolUseBlock{
				Name:  tu.ToolName,
				Input: tu.Args,
			},
		}}, nil

	case "result":
		var ev geminiResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("parse gemini result: %w", err)
		}
		if ev.Error != "" {
			return []StreamEvent{{Type: EventError, Error: ev.Error}}, nil
		}
		var events []StreamEvent
		if ev.Stats != nil {
			events = append(events, StreamEvent{
				Type: EventUsage,
				Usage: &Usage{
					InputTokens:  ev.Stats.TokensInput,
					OutputTokens: ev.Stats.TokensOutput,
					StopReason:   "end_turn",
				},
			})
		}
		events = append(events, StreamEvent{Type: EventDone})
		return events, nil

	default:
		return nil, nil
	}
}
