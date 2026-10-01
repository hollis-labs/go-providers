package provider

import (
	"encoding/json"
	"errors"
	"fmt"
)

// TurnInterrupter is an optional CLIAdapter extension for a CLI whose
// long-lived stdin protocol can end the turn in flight and keep the process:
// the session writes InterruptRequest on stdin and recognises the answer
// with InterruptResponse. The interrupted turn then ends the CLI's usual way
// (for Claude, an error result) and the next turn runs on the same process.
//
// Only a session that keeps one process across turns and writes frames to
// its stdin (Claude's streaming-stdio) can use it.
type TurnInterrupter interface {
	// InterruptRequest returns the stdin frame, without a newline, that asks
	// the CLI to interrupt its current turn, tagged with id.
	InterruptRequest(id string) []byte
	// InterruptResponse reports whether line answers an interrupt request:
	// ok is false for any other line. id is the request's id; err is the
	// CLI's refusal, or nil when it accepted.
	InterruptResponse(line []byte) (id string, ok bool, err error)
}

// ErrInterruptRefused wraps a CLI's refusal of an interrupt request.
var ErrInterruptRefused = errors.New("provider: interrupt refused")

// InterruptRequest is Claude's stream-json control request to interrupt the
// current turn. Claude answers it with a control_response whether or not a
// turn is in flight; with one, the turn ends with an error_during_execution
// result whose terminal_reason is aborted_tools (a tool was running) or
// aborted_streaming (the model was generating), and the process stays up.
// Measured on claude 2.1.286 (providertest fixture
// claude/stream_interrupt.transcript.jsonl).
func (a *ClaudeAdapter) InterruptRequest(id string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": id,
		"request":    map[string]string{"subtype": "interrupt"},
	})
	return b
}

// InterruptResponse recognises Claude's control_response to a control
// request: {"type":"control_response","response":{"subtype":"success" or
// "error","request_id":...,"error":...}}.
func (a *ClaudeAdapter) InterruptResponse(line []byte) (string, bool, error) {
	var frame struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &frame) != nil || frame.Type != "control_response" || frame.Response.RequestID == "" {
		return "", false, nil
	}
	if frame.Response.Subtype != "success" {
		msg := frame.Response.Error
		if msg == "" {
			msg = frame.Response.Subtype
		}
		return frame.Response.RequestID, true, fmt.Errorf("%w: %s", ErrInterruptRefused, msg)
	}
	return frame.Response.RequestID, true, nil
}
