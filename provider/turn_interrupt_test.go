package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/providertest"
)

var _ TurnInterrupter = (*ClaudeAdapter)(nil)

func TestClaudeInterruptFrames(t *testing.T) {
	a := NewClaudeAdapterStreamingStdio()
	var req map[string]any
	if err := json.Unmarshal(a.InterruptRequest("req-1"), &req); err != nil {
		t.Fatal(err)
	}
	if req["type"] != "control_request" || req["request_id"] != "req-1" || req["request"].(map[string]any)["subtype"] != "interrupt" {
		t.Errorf("request = %v", req)
	}

	// The captured answer, from claude 2.1.286.
	var ack []byte
	for _, line := range providertest.FixtureLines(t, "claude/stream_interrupt.transcript.jsonl") {
		var step struct {
			Send json.RawMessage `json:"send"`
		}
		if json.Unmarshal(line, &step) == nil && strings.Contains(string(step.Send), `"control_response"`) {
			ack = step.Send
		}
	}
	if id, ok, err := a.InterruptResponse(ack); !ok || err != nil || id != "req_interrupt_1" {
		t.Errorf("InterruptResponse(captured) = %q, %v, %v", id, ok, err)
	}
	refused := []byte(`{"type":"control_response","response":{"subtype":"error","request_id":"r2","error":"no turn"}}`)
	if id, ok, err := a.InterruptResponse(refused); !ok || id != "r2" || !errors.Is(err, ErrInterruptRefused) || !strings.Contains(err.Error(), "no turn") {
		t.Errorf("InterruptResponse(refused) = %q, %v, %v", id, ok, err)
	}
	for _, other := range []string{`{"type":"result","subtype":"success"}`, `not json`, `{"type":"control_response","response":{}}`} {
		if _, ok, _ := a.InterruptResponse([]byte(other)); ok {
			t.Errorf("InterruptResponse(%s) recognised a non-answer", other)
		}
	}
}

var _ RPCTurnInterrupter = (*CodexAdapter)(nil)

// The interrupt the capture sent is the one InterruptCall builds from the
// turn/started notification before it.
func TestCodexRPCTurnInterrupt(t *testing.T) {
	a := NewCodexAdapterAppServer()
	var handle json.RawMessage
	var sent, ended map[string]any
	for _, line := range providertest.FixtureLines(t, "codex/app_server_interrupt.transcript.jsonl") {
		var step struct {
			Send, Recv json.RawMessage
		}
		if json.Unmarshal(line, &step) != nil {
			continue
		}
		var f struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(append(step.Send, step.Recv...), &f)
		switch {
		case step.Recv != nil && f.Method == "turn/interrupt" && sent == nil:
			_ = json.Unmarshal(f.Params, &sent)
		case step.Send != nil && handle == nil:
			if h, started, _ := a.TurnNotification(f.Method, f.Params); started {
				handle = h
			}
		case step.Send != nil && ended == nil:
			if h, _, done := a.TurnNotification(f.Method, f.Params); done {
				_ = json.Unmarshal(h, &ended)
			}
		}
	}
	if handle == nil || sent == nil {
		t.Fatalf("fixture lacks a turn/started or the turn/interrupt: %s, %v", handle, sent)
	}
	method, params := a.InterruptCall(handle)
	var built map[string]any
	_ = json.Unmarshal(params, &built)
	if method != "turn/interrupt" || built["threadId"] != sent["threadId"] || built["turnId"] != sent["turnId"] {
		t.Errorf("InterruptCall = %s %v, the capture sent %v", method, built, sent)
	}
	if ended["turnId"] != built["turnId"] {
		t.Errorf("the turn/completed after the interrupt names %v, want the interrupted turn", ended)
	}
	if _, s, e := a.TurnNotification("item/started", json.RawMessage(`{"threadId":"t","turn":{"id":"x"}}`)); s || e {
		t.Error("an item notification read as a turn boundary")
	}
}
