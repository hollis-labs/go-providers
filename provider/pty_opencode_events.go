package provider

import (
	"bytes"
	"encoding/json"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

// opencode `run --format json` line mapping (opencode 1.18.30):
//
//   - step_start  → session id (every line carries it; step_start opens
//     each step, so it is the one line that reports it)
//   - text        → delta. opencode writes a text part once it is complete,
//     not token by token, so one line is one whole text block.
//   - reasoning   → thinking (only emitted when run with --thinking)
//   - tool_use    → tool use, plus a tool result on the typed surface.
//     opencode writes the line once the tool has completed or failed.
//   - step_finish → usage for that step. A turn is one or more steps: each
//     step that ends in tool calls has reason "tool-calls" and the turn
//     continues; "error" ends it with the failure reported by the error
//     line; any other reason ends the turn, so that step_finish also
//     emits done. Usage is per step, not cumulative — consumers sum it.
//     step_finish also reports a dollar cost, which has no field on either
//     usage type and is left in the raw line.
//   - error       → error. opencode exits non-zero after writing it.
//
// Unknown types and lines that are not JSON (opencode prints warnings such
// as an unknown --agent to stderr, but a future version might not) yield no
// events rather than an error: one odd line must not end the turn.

const (
	opencodeReasonToolCalls = "tool-calls"
	opencodeReasonError     = "error"
)

// opencodeStepEndsTurn reports whether a step_finish closes the turn with
// success. "tool-calls" continues the turn. "error" closes it with a
// failure that the error line (or, without one, the non-zero exit the
// session layer turns into an error) already reports, so it must not also
// report done.
func opencodeStepEndsTurn(reason string) bool {
	return reason != opencodeReasonToolCalls && reason != opencodeReasonError
}

type opencodeLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionID"`
	Part      opencodePart    `json:"part"`
	Error     *opencodeErrObj `json:"error"`
}

type opencodePart struct {
	Text   string              `json:"text"`
	Tool   string              `json:"tool"`
	CallID string              `json:"callID"`
	State  opencodeToolState   `json:"state"`
	Reason string              `json:"reason"`
	Tokens *opencodeStepTokens `json:"tokens"`
}

type opencodeToolState struct {
	Status string         `json:"status"`
	Input  map[string]any `json:"input"`
	Output string         `json:"output"`
	Error  string         `json:"error"`
}

type opencodeStepTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

type opencodeErrObj struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
}

// message mirrors opencode's own stderr rendering of a session error: the
// data.message when present, else the error name.
func (e *opencodeErrObj) message() string {
	if e == nil {
		return "opencode error"
	}
	if e.Data.Message != "" {
		return e.Data.Message
	}
	if e.Name != "" {
		return e.Name
	}
	return "opencode error"
}

func decodeOpencodeLine(line []byte) (opencodeLine, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return opencodeLine{}, false
	}
	var ev opencodeLine
	if err := json.Unmarshal(line, &ev); err != nil {
		return opencodeLine{}, false
	}
	return ev, true
}

func (t *opencodeStepTokens) usage(reason string) llmtypes.Usage {
	return llmtypes.Usage{
		InputTokens:         t.Input,
		OutputTokens:        t.Output + t.Reasoning,
		CacheCreationTokens: t.Cache.Write,
		CacheReadTokens:     t.Cache.Read,
		StopReason:          reason,
	}
}

func parseOpencodeStreamLine(line []byte) []llmtypes.StreamEvent {
	ev, ok := decodeOpencodeLine(line)
	if !ok {
		return nil
	}
	switch ev.Type {
	case "step_start":
		if ev.SessionID == "" {
			return nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventSessionID, SessionID: ev.SessionID}}
	case "text":
		if ev.Part.Text == "" {
			return nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: ev.Part.Text}}
	case "reasoning":
		if ev.Part.Text == "" {
			return nil
		}
		return []llmtypes.StreamEvent{{Type: llmtypes.EventThinking, ThinkingBlock: &llmtypes.ThinkingBlock{Thinking: ev.Part.Text}}}
	case "tool_use":
		return []llmtypes.StreamEvent{{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{
			ID:    ev.Part.CallID,
			Name:  ev.Part.Tool,
			Input: ev.Part.State.Input,
		}}}
	case "step_finish":
		out := make([]llmtypes.StreamEvent, 0, 2)
		if ev.Part.Tokens != nil {
			u := ev.Part.Tokens.usage(ev.Part.Reason)
			out = append(out, llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &u})
		}
		if opencodeStepEndsTurn(ev.Part.Reason) {
			out = append(out, llmtypes.StreamEvent{Type: llmtypes.EventDone})
		}
		return out
	case "error":
		return []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: ev.Error.message()}}
	default:
		return nil
	}
}

// ParseLineEvents implements EventParser for opencode run mode. It follows
// the ParseLine mapping above and adds what the legacy surface cannot carry:
// a tool result per tool_use line, and phase-less deltas. serve-http mode
// yields nothing, as ParseLine does.
func (a *OpencodeAdapter) ParseLineEvents(line []byte) ([]events.Event, error) {
	if a.Mode == "serve-http" {
		return nil, nil
	}
	ev, ok := decodeOpencodeLine(line)
	if !ok {
		return nil, nil
	}
	switch ev.Type {
	case "step_start":
		if ev.SessionID == "" {
			return nil, nil
		}
		return []events.Event{events.SessionID{ID: ev.SessionID}}, nil
	case "text":
		if ev.Part.Text == "" {
			return nil, nil
		}
		return []events.Event{events.Delta{Text: ev.Part.Text}}, nil
	case "reasoning":
		if ev.Part.Text == "" {
			return nil, nil
		}
		return []events.Event{events.Thinking{Text: ev.Part.Text}}, nil
	case "tool_use":
		st := ev.Part.State
		preview := st.Output
		if st.Status == "error" {
			preview = st.Error
		}
		return []events.Event{
			events.ToolUse{ID: ev.Part.CallID, Name: ev.Part.Tool, Args: st.Input},
			events.ToolResult{ID: ev.Part.CallID, IsError: st.Status == "error", ContentPreview: truncate(preview, 256)},
		}, nil
	case "step_finish":
		out := make([]events.Event, 0, 2)
		if ev.Part.Tokens != nil {
			u := ev.Part.Tokens.usage(ev.Part.Reason)
			out = append(out, events.Usage{
				InputTokens:         u.InputTokens,
				OutputTokens:        u.OutputTokens,
				CacheCreationTokens: u.CacheCreationTokens,
				CacheReadTokens:     u.CacheReadTokens,
				StopReason:          u.StopReason,
			})
		}
		if opencodeStepEndsTurn(ev.Part.Reason) {
			out = append(out, events.Done{StopReason: ev.Part.Reason})
		}
		return out, nil
	case "error":
		return []events.Event{events.Error{Message: ev.Error.message()}}, nil
	default:
		return nil, nil
	}
}

// IsSessionLost implements SessionLostClassifier. `opencode run --session
// <id>` with an id opencode no longer has writes "Session not found" to
// stderr (behind ANSI styling), prints no JSON and exits 1.
func (a *OpencodeAdapter) IsSessionLost(stderrTail []byte) bool {
	return bytes.Contains(stderrTail, []byte("Session not found"))
}
