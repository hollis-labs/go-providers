package provider

import "testing"

func TestAiderAdapter_Name(t *testing.T) {
	a := NewAiderAdapter()
	if a.Name() != "aider" {
		t.Errorf("expected aider, got %s", a.Name())
	}
}

func TestAiderAdapter_BuildArgs(t *testing.T) {
	a := NewAiderAdapter()
	args := a.BuildArgs("fix the bug", "", "")
	expected := []string{"--message", "fix the bug", "--no-auto-commits", "--no-git", "--yes"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, exp := range expected {
		if args[i] != exp {
			t.Errorf("arg[%d]: expected %q, got %q", i, exp, args[i])
		}
	}
}

func TestAiderParser(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType EventType
		wantN    int
	}{
		{"empty", "", "", 0},
		{"version banner", "Aider v0.50.0", "", 0},
		{"model info", "Model: gpt-4o", "", 0},
		{"separator", "────────────────", "", 0},
		{"content", "Here is the fix:", "delta", 1},
		{"error line", "Error: file not found", "error", 1},
		{"json message", `{"type":"message","content":"hello"}`, "delta", 1},
		{"json error", `{"type":"error","error":"bad input"}`, "error", 1},
		{"json done", `{"type":"done"}`, "done", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := parseAiderLine([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(events) != tt.wantN {
				t.Fatalf("expected %d events, got %d: %v", tt.wantN, len(events), events)
			}
			if tt.wantN > 0 && events[0].Type != tt.wantType {
				t.Errorf("expected type %s, got %s", tt.wantType, events[0].Type)
			}
		})
	}
}
