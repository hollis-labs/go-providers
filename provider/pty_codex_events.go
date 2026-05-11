package provider

import (
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/go-providers/provider/events"
)

// ParseLineEvents implements EventParser for the OpenAI Codex CLI.
//
// Codex's --json output is line-oriented; per-line types observed:
// - item.message (assistant role, delta or content) → events.Delta
// - item.completed (agent_message text) → events.Delta(final)
// - turn.completed (with optional usage) → events.Usage + events.Done
// - turn.failed / error → events.Error
// - thread.started / turn.started → informational, no event
func (a *CodexAdapter) ParseLineEvents(line []byte) ([]events.Event, error) {
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
		if msg.Role != "assistant" {
			return nil, nil
		}
		text := msg.Delta
		phase := "narration"
		if text == "" {
			text = msg.Content
			phase = "final"
		}
		if text == "" {
			return nil, nil
		}
		return []events.Event{events.Delta{Text: text, Phase: phase}}, nil

	case "item.completed":
		var item codexItemCompleted
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("parse codex item.completed: %w", err)
		}
		if item.Item.Type != "agent_message" || item.Item.Text == "" {
			return nil, nil
		}
		return []events.Event{events.Delta{Text: item.Item.Text, Phase: "final"}}, nil

	case "turn.completed":
		var done codexTurnCompleted
		if err := json.Unmarshal(line, &done); err != nil {
			return nil, fmt.Errorf("parse codex turn.completed: %w", err)
		}
		out := make([]events.Event, 0, 2)
		if done.Usage != nil {
			out = append(out, events.Usage{
				InputTokens:         done.Usage.InputTokens,
				OutputTokens:        done.Usage.OutputTokens,
				CacheCreationTokens: 0,
				CacheReadTokens:     done.Usage.CachedInputTokens,
			})
		}
		out = append(out, events.Done{})
		return out, nil

	case "turn.failed", "error":
		var errEvt codexError
		msg := "codex error"
		if err := json.Unmarshal(line, &errEvt); err == nil && errEvt.Message != "" {
			msg = errEvt.Message
		}
		return []events.Event{events.Error{Message: msg}}, nil

	default:
		return nil, nil
	}
}
