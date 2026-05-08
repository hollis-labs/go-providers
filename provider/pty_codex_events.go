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
// - turn.completed → events.Done
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

	case "turn.completed":
		return []events.Event{events.Done{}}, nil

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
