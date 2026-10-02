package provider

import (
	"bytes"
	"encoding/json"
	"strconv"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

// agy `--output-format stream-json` line mapping (agy 1.2.7). Each line is
// {"event": <kind>, <kind>: {...}} with a closed vocabulary:
//
//   - init → session id (init.conversation_id), once per process
//   - step_update, keyed by step_type and state (ACTIVE, DONE, ERROR):
//     agent_response carries text_delta → delta, and on DONE its usage →
//     usage for that step. A turn with tool calls has several agent_response
//     steps; usage is per step and consumers sum it. output_tokens already
//     include thinking_tokens (total = input + output), and thinking text
//     is never streamed.
//     tool ACTIVE → tool use (tool_name, tool_info.parameters); DONE/ERROR
//     → a tool result on the typed surface (tool_info.output or
//     tool_info.error.message). MCP calls are tool_name "call_mcp_tool".
//     user_input and system_message steps carry nothing a consumer needs.
//   - result → done (status SUCCESS) or error (status ERROR, result.error). The
//     done carries result.response, the turn's final message, as its text.
//     result.usage is the sum of the turn's step usage and is not emitted
//     again. result.denied_actions lists auto-denied approvals, surfaced
//     as events.PermissionDenied before done.
//
// Tool steps carry no call id, so the step index (unique within the
// conversation) stands in for one. Unknown events and lines that are not
// JSON yield nothing.

type agyLine struct {
	Event          string          `json:"event"`
	ConversationID string          `json:"conversation_id"`
	StepUpdate     *agyStepUpdate  `json:"step_update"`
	Result         *agyResult      `json:"result"`
	Init           json.RawMessage `json:"init"`
}

type agyStepUpdate struct {
	StepIndex int          `json:"step_index"`
	State     string       `json:"state"`
	StepType  string       `json:"step_type"`
	TextDelta string       `json:"text_delta"`
	ToolName  string       `json:"tool_name"`
	ToolInfo  *agyToolInfo `json:"tool_info"`
	Usage     *agyUsage    `json:"usage"`
}

type agyToolInfo struct {
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
	Output     string         `json:"output"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type agyUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
}

type agyResult struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	// Response is the turn's final message: the text of its last
	// agent_response step.
	Response      string `json:"response"`
	DeniedActions []struct {
		Action      string `json:"action"`
		DisplayName string `json:"display_name"`
	} `json:"denied_actions"`
}

func decodeAgyLine(line []byte) (agyLine, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return agyLine{}, false
	}
	var ev agyLine
	if err := json.Unmarshal(line, &ev); err != nil {
		return agyLine{}, false
	}
	return ev, true
}

func agyToolID(s *agyStepUpdate) string { return "step-" + strconv.Itoa(s.StepIndex) }

func (u *agyUsage) usage() llmtypes.Usage {
	return llmtypes.Usage{
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens,
	}
}

func agyResultError(r *agyResult) string {
	if r.Error != "" {
		return r.Error
	}
	return "antigravity error"
}

func parseAntigravityStreamLine(line []byte) []llmtypes.StreamEvent {
	ev, ok := decodeAgyLine(line)
	if !ok {
		return nil
	}
	switch ev.Event {
	case "init":
		if ev.ConversationID == "" {
			return nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventSessionID, SessionID: ev.ConversationID}}
	case "step_update":
		s := ev.StepUpdate
		if s == nil {
			return nil
		}
		switch s.StepType {
		case "agent_response":
			var out []llmtypes.StreamEvent
			if s.TextDelta != "" {
				out = append(out, llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: s.TextDelta})
			}
			if s.State == "DONE" && s.Usage != nil {
				u := s.Usage.usage()
				out = append(out, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &u})
			}
			return out
		case "tool":
			if s.State != "ACTIVE" {
				return nil
			}
			return []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{
				ID:    agyToolID(s),
				Name:  s.ToolName,
				Input: agyToolParams(s),
			}}}
		}
		return nil
	case "result":
		if ev.Result == nil {
			return nil
		}
		if ev.Result.Status == "SUCCESS" {
			return []llmtypes.StreamEvent{{Type: llmtypes.EventDone, Content: ev.Result.Response}}
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: agyResultError(ev.Result)}}
	default:
		return nil
	}
}

func agyToolParams(s *agyStepUpdate) map[string]any {
	if s.ToolInfo == nil {
		return nil
	}
	return s.ToolInfo.Parameters
}

// ParseLineEvents implements EventParser for agy. It follows the ParseLine
// mapping and adds what the legacy surface cannot carry: a tool result per
// finished tool step and a PermissionDenied per auto-denied action.
func (a *AntigravityAdapter) ParseLineEvents(line []byte) ([]events.Event, error) {
	ev, ok := decodeAgyLine(line)
	if !ok {
		return nil, nil
	}
	switch ev.Event {
	case "init":
		if ev.ConversationID == "" {
			return nil, nil
		}
		return []events.Event{events.SessionID{ID: ev.ConversationID}}, nil
	case "step_update":
		s := ev.StepUpdate
		if s == nil {
			return nil, nil
		}
		switch s.StepType {
		case "agent_response":
			var out []events.Event
			if s.TextDelta != "" {
				out = append(out, events.Delta{Text: s.TextDelta})
			}
			if s.State == "DONE" && s.Usage != nil {
				out = append(out, events.Usage{
					InputTokens:     s.Usage.InputTokens,
					OutputTokens:    s.Usage.OutputTokens,
					CacheReadTokens: s.Usage.CacheReadTokens,
				})
			}
			return out, nil
		case "tool":
			switch s.State {
			case "ACTIVE":
				return []events.Event{events.ToolUse{ID: agyToolID(s), Name: s.ToolName, Args: agyToolParams(s)}}, nil
			case "DONE", "ERROR":
				res := events.ToolResult{ID: agyToolID(s), IsError: s.State == "ERROR"}
				if s.ToolInfo != nil {
					res.ContentPreview = s.ToolInfo.Output
					if s.ToolInfo.Error != nil {
						res.ContentPreview = s.ToolInfo.Error.Message
					}
				}
				res.ContentPreview = truncate(res.ContentPreview, 256)
				return []events.Event{res}, nil
			}
		}
		return nil, nil
	case "result":
		r := ev.Result
		if r == nil {
			return nil, nil
		}
		out := make([]events.Event, 0, len(r.DeniedActions)+1)
		for _, d := range r.DeniedActions {
			out = append(out, events.PermissionDenied{Action: d.Action, DisplayName: d.DisplayName})
		}
		if r.Status == "SUCCESS" {
			return append(out, events.Done{Text: r.Response}), nil
		}
		return append(out, events.Error{Message: agyResultError(r)}), nil
	default:
		return nil, nil
	}
}
