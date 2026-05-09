package provider

import (
	"context"
	"regexp"
	"testing"
	"time"
)

// TestEventReactionPipelineBasic tests the basic functionality of the event reaction pipeline.
func TestEventReactionPipelineBasic(t *testing.T) {
	// Create a mock provider
	mockProvider := &mockStreamingProvider{
		capabilities: ProviderCapabilities{
			SupportsStreamJSON: true,
			MaxTokens:          1000,
		},
	}

	// Create pipeline with default config
	config := DefaultEventReactionConfig()
	pipeline := NewEventReactionPipeline(mockProvider, config)

	// Test capabilities passthrough
	caps := pipeline.Capabilities()
	if !caps.SupportsStreamJSON {
		t.Error("Pipeline should pass through provider capabilities")
	}

	// Test streaming
	ctx := context.Background()
	messages := []ChatMessage{{Role: "user", Content: "Hello"}}

	stream, err := pipeline.StreamChat(ctx, ChatRequest{SystemPrompt: "system", Messages: messages, Model: "test-model"})
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	// Read from stream
	events := make([]StreamEvent, 0)
	for event := range stream {
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Error("Expected at least one event from stream")
	}
}

// TestEventReactionPipelineNonStreaming tests graceful fallback for non-streaming providers.
func TestEventReactionPipelineNonStreaming(t *testing.T) {
	// Create a mock non-streaming provider
	mockProvider := &mockNonStreamingProvider{
		capabilities: ProviderCapabilities{
			SupportsStreamJSON: false,
			MaxTokens:          1000,
		},
		response: "Test response",
	}

	// Create pipeline
	config := DefaultEventReactionConfig()
	pipeline := NewEventReactionPipeline(mockProvider, config)

	// Test streaming with fallback
	ctx := context.Background()
	messages := []ChatMessage{{Role: "user", Content: "Hello"}}

	stream, err := pipeline.StreamChat(ctx, ChatRequest{SystemPrompt: "system", Messages: messages, Model: "test-model"})
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	// Read from stream
	events := make([]StreamEvent, 0)
	for event := range stream {
		events = append(events, event)
	}

	// Should get delta + done events (no usage — providers no longer surface
	// per-call Usage on non-streaming completions in this lib).
	if len(events) != 2 {
		t.Errorf("Expected 2 events (delta + done), got %d", len(events))
	}

	if events[0].Type != "delta" || events[0].Content != "Test response" {
		t.Error("First event should be delta with test response")
	}

	if events[1].Type != "done" {
		t.Error("Second event should be done")
	}
}

// TestScopeGuardViolations tests the scope guard component.
func TestScopeGuardViolations(t *testing.T) {
	// Test with restrictive patterns
	patterns := []string{"/allowed/*", "safe_*"}
	guard := NewScopeGuard(patterns, "log")

	// Test allowed file access
	allowedEvent := StreamEvent{
		Type: EventToolUse,
		ToolUse: &ToolUseBlock{
			Name:  "read_file",
			Input: map[string]any{"file_path": "/allowed/test.txt"},
		},
	}
	violation := guard.CheckEvent(allowedEvent)
	if violation != nil {
		t.Error("Should not detect violation for allowed file path")
	}

	// Test disallowed file access
	disallowedEvent := StreamEvent{
		Type: EventToolUse,
		ToolUse: &ToolUseBlock{
			Name:  "read_file",
			Input: map[string]any{"file_path": "/etc/passwd"},
		},
	}
	violation = guard.CheckEvent(disallowedEvent)
	if violation == nil {
		t.Error("Should detect violation for disallowed file path")
	}

	// Test dangerous tool usage
	dangerousEvent := StreamEvent{
		Type: EventToolUse,
		ToolUse: &ToolUseBlock{
			Name:  "rm_command",
			Input: map[string]any{"args": "-rf /"},
		},
	}
	violation = guard.CheckEvent(dangerousEvent)
	if violation == nil {
		t.Error("Should detect violation for dangerous tool usage")
	}

	// Test wildcard allowance
	wildcardGuard := NewScopeGuard([]string{"*"}, "log")
	violation = wildcardGuard.CheckEvent(dangerousEvent)
	if violation != nil {
		t.Error("Wildcard pattern should allow everything")
	}
}

// TestProgressTrackerLoops tests the progress tracker component.
func TestProgressTrackerLoops(t *testing.T) {
	tracker := NewProgressTracker(3, time.Minute, "log")

	// Test content loop detection
	contentEvent := StreamEvent{
		Type:    "delta",
		Content: "Repeated content",
	}

	// Send the same content multiple times
	for i := 0; i < 5; i++ {
		loop := tracker.CheckEvent(contentEvent)
		if i < 3 && loop != nil {
			t.Errorf("Should not detect loop on iteration %d", i+1)
		}
		if i >= 3 && loop == nil {
			t.Errorf("Should detect loop on iteration %d", i+1)
		}
		if loop != nil && loop.Type != "content_loop" {
			t.Error("Should detect content_loop type")
		}
	}

	// Test tool loop detection
	tracker2 := NewProgressTracker(2, time.Minute, "log")
	toolEvent := StreamEvent{
		Type: EventToolUse,
		ToolUse: &ToolUseBlock{
			Name:  "test_tool",
			Input: map[string]any{"param": "value"},
		},
	}

	for i := 0; i < 4; i++ {
		loop := tracker2.CheckEvent(toolEvent)
		if i < 2 && loop != nil {
			t.Errorf("Should not detect tool loop on iteration %d", i+1)
		}
		if i >= 2 && loop == nil {
			t.Errorf("Should detect tool loop on iteration %d", i+1)
		}
	}
}

// TestCostMonitorBudgets tests the cost monitor component.
func TestCostMonitorBudgets(t *testing.T) {
	// Test token budget
	monitor := NewCostMonitor(100, 1.0, "log")

	// Test within budget
	usageEvent := StreamEvent{
		Type: EventUsage,
		Usage: &Usage{
			InputTokens:  30,
			OutputTokens: 20,
		},
	}
	violation := monitor.CheckEvent(usageEvent)
	if violation != nil {
		t.Error("Should not violate budget within limits")
	}

	// Test exceeding token budget
	largeUsageEvent := StreamEvent{
		Type: EventUsage,
		Usage: &Usage{
			InputTokens:  60,
			OutputTokens: 50,
		},
	}
	violation = monitor.CheckEvent(largeUsageEvent)
	if violation == nil {
		t.Error("Should detect token budget violation")
	}
	if violation != nil && violation.Type != "token_budget" {
		t.Error("Should detect token_budget violation type")
	}

	// Test cost monitoring
	monitor2 := NewCostMonitor(10000, 0.01, "log") // Very low cost budget
	highCostEvent := StreamEvent{
		Type: EventUsage,
		Usage: &Usage{
			InputTokens:  1000,
			OutputTokens: 1000,
		},
	}
	violation = monitor2.CheckEvent(highCostEvent)
	if violation == nil {
		t.Error("Should detect cost budget violation")
	}
}

// TestUsageSummary tests the usage summary functionality.
func TestUsageSummary(t *testing.T) {
	monitor := NewCostMonitor(1000, 10.0, "log")

	// Add some usage
	usageEvent := StreamEvent{
		Type: EventUsage,
		Usage: &Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}
	monitor.CheckEvent(usageEvent)

	summary := monitor.GetUsageSummary()
	if summary.TotalInputTokens != 100 {
		t.Errorf("Expected 100 input tokens, got %d", summary.TotalInputTokens)
	}
	if summary.TotalOutputTokens != 50 {
		t.Errorf("Expected 50 output tokens, got %d", summary.TotalOutputTokens)
	}
	if summary.TotalTokens != 150 {
		t.Errorf("Expected 150 total tokens, got %d", summary.TotalTokens)
	}
	if summary.TokenUtilization != 15.0 {
		t.Errorf("Expected 15%% token utilization, got %.2f%%", summary.TokenUtilization)
	}
}

// Mock providers for testing

type mockStreamingProvider struct {
	capabilities ProviderCapabilities
	events       []StreamEvent
}

func (m *mockStreamingProvider) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 10)
	go func() {
		defer close(ch)
		ch <- StreamEvent{Type: EventDelta, Content: "Hello"}
		ch <- StreamEvent{Type: EventDelta, Content: " world"}
		ch <- StreamEvent{Type: EventUsage, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}
		ch <- StreamEvent{Type: EventDone}
	}()
	return ch, nil
}

func (m *mockStreamingProvider) Complete(ctx context.Context, in ChatRequest) (string, error) {
	return "Hello world", nil
}

func (m *mockStreamingProvider) Capabilities() ProviderCapabilities {
	return m.capabilities
}

type mockNonStreamingProvider struct {
	capabilities ProviderCapabilities
	response     string
}

func (m *mockNonStreamingProvider) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	// Should not be called for non-streaming providers
	return nil, nil
}

func (m *mockNonStreamingProvider) Complete(ctx context.Context, in ChatRequest) (string, error) {
	return m.response, nil
}

func (m *mockNonStreamingProvider) Capabilities() ProviderCapabilities {
	return m.capabilities
}

// TestGlobToRegex tests the glob pattern to regex conversion.
func TestGlobToRegex(t *testing.T) {
	tests := []struct {
		glob     string
		text     string
		expected bool
	}{
		{"*", "anything", true},
		{"*.txt", "file.txt", true},
		{"*.txt", "file.doc", false},
		{"/home/*", "/home/user", true},
		{"/home/*", "/etc/passwd", false},
		{"test_?", "test_1", true},
		{"test_?", "test_ab", false},
	}

	for _, test := range tests {
		regex := globToRegex(test.glob)
		// Use proper regex matching instead of string contains
		re, err := regexp.Compile(regex)
		if err != nil {
			t.Fatalf("Invalid regex %s: %v", regex, err)
		}

		matched := re.MatchString(test.text)
		if matched != test.expected {
			t.Errorf("Pattern %s -> %s, text %s: expected %v, got %v",
				test.glob, regex, test.text, test.expected, matched)
		}
	}
}
