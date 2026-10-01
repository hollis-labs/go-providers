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
