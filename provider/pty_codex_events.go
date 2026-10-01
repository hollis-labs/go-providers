package provider

import (
	"encoding/json"
	"fmt"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

// ParseLineEvents implements EventParser for the OpenAI Codex CLI.
//
// Codex's --json output is line-oriented; per-line types observed:
// - item.message (assistant role, delta or content) → events.Delta
// - item.completed (agent_message text) → events.Delta(final)
// - turn.completed (with optional usage) → events.Usage + events.Done
// - turn.failed / error → events.Error
// - thread.started → events.SessionID (the thread a later turn resumes)
// - turn.started → informational, no event
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
		return []events.Event{events.Delta{Text: item.Item.Text, Phase: "final", BlockID: item.Item.ID}}, nil

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
				StopReason:          llmtypes.StopReasonEndTurn,
			})
		}
		out = append(out, events.Done{StopReason: llmtypes.StopReasonEndTurn})
		return out, nil

	case "turn.failed", "error":
		var errEvt codexError
		msg := "codex error"
		if err := json.Unmarshal(line, &errEvt); err == nil && errEvt.Message != "" {
			msg = errEvt.Message
		}
		return []events.Event{events.Error{Message: msg}}, nil

	case "thread.started":
		var started codexThreadStarted
		if err := json.Unmarshal(line, &started); err != nil {
			return nil, fmt.Errorf("parse codex thread.started: %w", err)
		}
		if started.ThreadID == "" {
			return nil, nil
		}
		return []events.Event{events.SessionID{ID: started.ThreadID}}, nil

	default:
		return nil, nil
	}
}
