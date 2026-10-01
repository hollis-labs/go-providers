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

// RPCTurnInterrupter is an optional CLIAdapter extension for a JSON-RPC
// runtime whose protocol can interrupt the turn in flight and keep its
// process: the session follows turns through TurnNotification and interrupts
// the open one with the request InterruptCall returns. The turn then ends the
// runtime's usual way and the next turn runs on the same process.
type RPCTurnInterrupter interface {
	// TurnNotification reports what a server notification says about a
	// turn: its handle and whether it started or ended. Any other
	// notification returns neither. A handle is opaque to the caller; it
	// identifies one turn and is passed back to InterruptCall.
	TurnNotification(method string, params json.RawMessage) (handle json.RawMessage, started, ended bool)
	// InterruptCall returns the request that interrupts the turn handle
	// names.
	InterruptCall(handle json.RawMessage) (method string, params json.RawMessage)
}

// TurnNotification follows Codex app-server turns: turn/started opens one
// and turn/completed ends it, each carrying {threadId, turn: {id}}. The
// handle is {"threadId", "turnId"}.
func (a *CodexAdapter) TurnNotification(method string, params json.RawMessage) (json.RawMessage, bool, bool) {
	started := method == "turn/started"
	if !started && method != "turn/completed" {
		return nil, false, false
	}
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &p) != nil || p.ThreadID == "" || p.Turn.ID == "" {
		return nil, false, false
	}
	handle, _ := json.Marshal(map[string]string{"threadId": p.ThreadID, "turnId": p.Turn.ID})
	return handle, started, !started
}

// InterruptCall is Codex app-server's turn/interrupt for the turn. Codex
// answers {} and completes the turn with status "interrupted"; the thread
// and process stay up and the next turn/start runs normally. With no turn
// running it answers a JSON-RPC error ("no active turn to interrupt").
// Measured on codex-cli 0.159.2 (providertest fixture
// codex/app_server_interrupt.transcript.jsonl).
func (a *CodexAdapter) InterruptCall(handle json.RawMessage) (string, json.RawMessage) {
	return "turn/interrupt", handle
}
