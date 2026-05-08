package provider

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/hollis-labs/go-providers/provider/events"
)

// ParseLineEvents implements EventParser for the Claude Code CLI.
//
// Emits richer typed events than the legacy ParseLine:
// - text blocks → events.Delta
// - tool_use blocks → events.ToolUse (+ events.SubagentSpawn for "Task")
// - tool_result blocks (carried in user-role messages) → events.ToolResult
// - result events → events.Usage + events.Done (or events.Error)
// - system init → events.SessionID
//
// Lines that produce no typed events (e.g. rate_limit_event, unknown
// envelopes) return (nil, nil).
func (a *ClaudeAdapter) ParseLineEvents(line []byte) ([]events.Event, error) {
	if len(line) == 0 {
		return nil, nil
	}

	var envelope claudeEvent
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parse claude event: %w", err)
	}

	switch envelope.Type {
	case "assistant":
		return parseClaudeAssistantTyped(line)
	case "user":
		return parseClaudeUserTyped(line)
	case "result":
		return parseClaudeResultTyped(line)
	case "error":
		return parseClaudeErrorTyped(line)
	case "system":
		return parseClaudeSystemTyped(line)
	default:
		return nil, nil
	}
}

// claudeUserEvent wraps a user-role message; in claude stream-json,
// tool_result blocks are surfaced via these.
type claudeUserEvent struct {
	Type    string         `json:"type"`
	Message claudeUserMsg  `json:"message"`
}

type claudeUserMsg struct {
	Role    string                 `json:"role"`
	Content []claudeUserContentBlk `json:"content"`
}

type claudeUserContentBlk struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // string or array of content blocks
	IsError   bool   `json:"is_error,omitempty"`
}

func parseClaudeAssistantTyped(line []byte) ([]events.Event, error) {
	var ev claudeAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse assistant event: %w", err)
	}

	var out []events.Event
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				out = append(out, events.Delta{Text: block.Text, Phase: "narration"})
			}
		case "tool_use":
			input := make(map[string]any)
			if len(block.Input) > 0 {
				_ = json.Unmarshal(block.Input, &input)
			}
			out = append(out, events.ToolUse{
				ID:   block.ID,
				Name: block.Name,
				Args: input,
			})
			if block.Name == "Task" {
				out = append(out, events.SubagentSpawn{
					Tool: block.Name,
					Args: input,
				})
			}
		}
	}
	return out, nil
}

func parseClaudeUserTyped(line []byte) ([]events.Event, error) {
	var ev claudeUserEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse user event: %w", err)
	}
	var out []events.Event
	for _, block := range ev.Message.Content {
		if block.Type != "tool_result" {
			continue
		}
		preview := claudeContentPreview(block.Content)
		out = append(out, events.ToolResult{
			ID:             block.ToolUseID,
			IsError:        block.IsError,
			ContentPreview: preview,
		})
	}
	return out, nil
}

// claudeContentPreview renders a tool_result content payload to a
// short string. The wire format is either a string or an array of
// content blocks; truncate to keep the preview lightweight.
func claudeContentPreview(content any) string {
	const maxLen = 256
	switch v := content.(type) {
	case string:
		return truncate(v, maxLen)
	case []any:
		// Concatenate text from text-typed blocks.
		var s string
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "text" {
				if txt, _ := m["text"].(string); txt != "" {
					if s != "" {
						s += "\n"
					}
					s += txt
				}
			}
		}
		return truncate(s, maxLen)
	default:
		return ""
	}
}

// truncate returns s clamped to at most n bytes, with an ellipsis
// appended when truncation occurred. The cut point is walked back to
// the nearest UTF-8 rune boundary so multi-byte runes are never split,
// keeping the preview valid for log/UI surfaces. The byte budget is
// the cap, not the post-walkback length — if the n-th byte lands
// mid-rune the preview comes back slightly shorter than n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func parseClaudeResultTyped(line []byte) ([]events.Event, error) {
	var ev claudeResultEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse result event: %w", err)
	}

	if ev.IsError || ev.Subtype == "error" {
		return []events.Event{events.Error{Message: ev.Result}}, nil
	}

	var out []events.Event
	stopReason := ev.StopReason
	if stopReason == "" {
		stopReason = "end_turn"
	}
	if ev.Usage != nil {
		out = append(out, events.Usage{
			InputTokens:         ev.Usage.InputTokens,
			OutputTokens:        ev.Usage.OutputTokens,
			CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
			CacheReadTokens:     ev.Usage.CacheReadInputTokens,
			StopReason:          stopReason,
		})
	}
	out = append(out, events.Done{StopReason: stopReason})
	return out, nil
}

func parseClaudeSystemTyped(line []byte) ([]events.Event, error) {
	var ev claudeSystemEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse system event: %w", err)
	}
	if ev.Subtype == "init" && ev.SessionID != "" {
		return []events.Event{events.SessionID{ID: ev.SessionID}}, nil
	}
	return nil, nil
}

func parseClaudeErrorTyped(line []byte) ([]events.Event, error) {
	var ev claudeErrorEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil, fmt.Errorf("parse error event: %w", err)
	}
	return []events.Event{events.Error{Message: ev.Error.Message}}, nil
}
