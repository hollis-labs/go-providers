package provider

import (
	"encoding/json"
	"fmt"
	"os"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// CodexAdapter implements CLIAdapter for the OpenAI Codex CLI.
//
// Turn boundary: emits llmtypes.EventDone on the `turn.completed` event, llmtypes.EventError on
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

func (a *CodexAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
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

type codexItemCompleted struct {
	Type string `json:"type"`
	Item struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

type codexUsage struct {
	InputTokens         int `json:"input_tokens"`
	CachedInputTokens   int `json:"cached_input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	ReasoningOutputUsed int `json:"reasoning_output_tokens"`
}

type codexTurnCompleted struct {
	Type   string      `json:"type"`
	TurnID string      `json:"turn_id"`
	Usage  *codexUsage `json:"usage,omitempty"`
}

type codexError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// parseCodexStreamLine parses a single line of Codex --json output.
func parseCodexStreamLine(line []byte) ([]llmtypes.StreamEvent, error) {
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
				return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: text}}, nil
			}
		}
		return nil, nil

	case "item.completed":
		var item codexItemCompleted
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("parse codex item.completed: %w", err)
		}
		if item.Item.Type != "agent_message" || item.Item.Text == "" {
			return nil, nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: item.Item.Text}}, nil

	case "turn.completed":
		var done codexTurnCompleted
		if err := json.Unmarshal(line, &done); err != nil {
			return nil, fmt.Errorf("parse codex turn.completed: %w", err)
		}
		out := make([]llmtypes.StreamEvent, 0, 2)
		if done.Usage != nil {
			out = append(out, llmtypes.StreamEvent{
				Type: llmtypes.EventUsage,
				Usage: &llmtypes.Usage{
					InputTokens:         done.Usage.InputTokens,
					OutputTokens:        done.Usage.OutputTokens,
					CacheReadTokens:     done.Usage.CachedInputTokens,
					CacheCreationTokens: 0,
				},
			})
		}
		out = append(out, llmtypes.StreamEvent{Type: llmtypes.EventDone})
		return out, nil

	case "turn.failed", "error":
		var errEvt codexError
		if err := json.Unmarshal(line, &errEvt); err == nil && errEvt.Message != "" {
			return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: errEvt.Message}}, nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: "codex error"}}, nil

	case "thread.started", "turn.started":
		// Informational — skip.
		return nil, nil

	default:
		return nil, nil
	}
}
