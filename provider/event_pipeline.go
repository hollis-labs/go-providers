package provider

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// EventReactionConfig configures the behavior of the event reaction pipeline.
type EventReactionConfig struct {
	// ScopeGuard configuration
	EnableScopeGuard   bool
	AllowedScopes      []string // List of allowed file/directory patterns
	ScopeViolationMode string   // "log" or "kill"

	// ProgressTracker configuration
	EnableProgressTracker bool
	MaxLoopIterations     int           // Maximum iterations before considering it a loop
	LoopDetectionWindow   time.Duration // Time window for detecting loops
	LoopDetectionMode     string        // "log" or "kill"

	// CostMonitor configuration
	EnableCostMonitor  bool
	TokenBudget        int     // Maximum tokens allowed
	CostBudgetUSD      float64 // Maximum cost in USD
	BudgetExceededMode string  // "log" or "kill"
}

// DefaultEventReactionConfig returns a default configuration with moderate settings.
func DefaultEventReactionConfig() EventReactionConfig {
	return EventReactionConfig{
		EnableScopeGuard:      true,
		AllowedScopes:         []string{"*"}, // Allow all by default
		ScopeViolationMode:    "log",
		EnableProgressTracker: true,
		MaxLoopIterations:     1000,
		LoopDetectionWindow:   time.Minute * 5,
		LoopDetectionMode:     "log",
		EnableCostMonitor:     true,
		TokenBudget:           100000, // 100k token budget
		CostBudgetUSD:         10.0,   // $10 budget
		BudgetExceededMode:    "log",
	}
}

// EventReactionPipeline wraps a Provider and adds monitoring capabilities for stream events.
// It implements the decorator pattern, transparently adding monitoring to any Provider.
type EventReactionPipeline struct {
	provider Provider
	config   EventReactionConfig

	// Monitoring components
	scopeGuard      *ScopeGuard
	progressTracker *ProgressTracker
	costMonitor     *CostMonitor

	mu     sync.RWMutex
	active bool
}

// NewEventReactionPipeline creates a new event reaction pipeline wrapping the given provider.
func NewEventReactionPipeline(provider Provider, config EventReactionConfig) *EventReactionPipeline {
	pipeline := &EventReactionPipeline{
		provider: provider,
		config:   config,
		active:   true,
	}

	// Initialize monitoring components based on configuration
	if config.EnableScopeGuard {
		pipeline.scopeGuard = NewScopeGuard(config.AllowedScopes, config.ScopeViolationMode)
	}

	if config.EnableProgressTracker {
		pipeline.progressTracker = NewProgressTracker(
			config.MaxLoopIterations,
			config.LoopDetectionWindow,
			config.LoopDetectionMode,
		)
	}

	if config.EnableCostMonitor {
		pipeline.costMonitor = NewCostMonitor(
			config.TokenBudget,
			config.CostBudgetUSD,
			config.BudgetExceededMode,
		)
	}

	return pipeline
}

// StreamChat implements Provider.StreamChat with event monitoring.
func (p *EventReactionPipeline) StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (<-chan StreamEvent, error) {
	// Check if provider supports streaming
	if !p.provider.Capabilities().SupportsStreamJSON {
		// Graceful fallback for non-streaming providers
		return p.fallbackToNonStreaming(ctx, systemPrompt, messages, model, nil)
	}

	// Get the original stream
	originalStream, err := p.provider.StreamChat(ctx, systemPrompt, messages, model)
	if err != nil {
		return nil, err
	}

	// Create monitored stream
	return p.monitorStream(ctx, originalStream), nil
}

// StreamChatWithTools implements Provider.StreamChatWithTools with event monitoring.
func (p *EventReactionPipeline) StreamChatWithTools(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	// Check if provider supports streaming
	if !p.provider.Capabilities().SupportsStreamJSON {
		// Graceful fallback for non-streaming providers
		return p.fallbackToNonStreaming(ctx, systemPrompt, messages, model, tools)
	}

	// Get the original stream
	originalStream, err := p.provider.StreamChatWithTools(ctx, systemPrompt, messages, model, tools)
	if err != nil {
		return nil, err
	}

	// Create monitored stream
	return p.monitorStream(ctx, originalStream), nil
}

// Complete implements Provider.Complete (no streaming monitoring needed).
func (p *EventReactionPipeline) Complete(ctx context.Context, systemPrompt string, messages []ChatMessage, model string) (string, error) {
	return p.provider.Complete(ctx, systemPrompt, messages, model)
}

// Capabilities implements Provider.Capabilities.
func (p *EventReactionPipeline) Capabilities() ProviderCapabilities {
	return p.provider.Capabilities()
}

// monitorStream creates a new channel that monitors events from the original stream.
func (p *EventReactionPipeline) monitorStream(ctx context.Context, originalStream <-chan StreamEvent) <-chan StreamEvent {
	monitoredStream := make(chan StreamEvent, 64)

	go func() {
		defer close(monitoredStream)

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-originalStream:
				if !ok {
					return
				}

				// Process event through monitoring pipeline
				if p.shouldTerminate(ctx, event) {
					// Send error event and terminate
					monitoredStream <- StreamEvent{
						Type:  "error",
						Error: "Event reaction pipeline terminated due to policy violation",
					}
					return
				}

				// Forward the event if not terminated
				monitoredStream <- event
			}
		}
	}()

	return monitoredStream
}

// shouldTerminate checks all monitoring components to see if processing should be terminated.
func (p *EventReactionPipeline) shouldTerminate(ctx context.Context, event StreamEvent) bool {
	p.mu.RLock()
	active := p.active
	p.mu.RUnlock()

	if !active {
		return true
	}

	// Check scope guard
	if p.scopeGuard != nil {
		if violation := p.scopeGuard.CheckEvent(event); violation != nil {
			log.Printf("Scope violation detected: %v", violation)
			if p.config.ScopeViolationMode == "kill" {
				p.terminate("scope violation")
				return true
			}
		}
	}

	// Check progress tracker
	if p.progressTracker != nil {
		if loop := p.progressTracker.CheckEvent(event); loop != nil {
			log.Printf("Progress loop detected: %v", loop)
			if p.config.LoopDetectionMode == "kill" {
				p.terminate("progress loop")
				return true
			}
		}
	}

	// Check cost monitor
	if p.costMonitor != nil {
		if violation := p.costMonitor.CheckEvent(event); violation != nil {
			log.Printf("Budget exceeded: %v", violation)
			if p.config.BudgetExceededMode == "kill" {
				p.terminate("budget exceeded")
				return true
			}
		}
	}

	return false
}

// terminate shuts down the pipeline.
func (p *EventReactionPipeline) terminate(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.active {
		log.Printf("EventReactionPipeline: Terminating due to: %s", reason)
		p.active = false
	}
}

// fallbackToNonStreaming handles providers that don't support streaming.
func (p *EventReactionPipeline) fallbackToNonStreaming(ctx context.Context, systemPrompt string, messages []ChatMessage, model string, tools []ToolDefinition) (<-chan StreamEvent, error) {
	log.Printf("Provider does not support streaming - using fallback mode")

	stream := make(chan StreamEvent, 1)

	go func() {
		defer close(stream)

		var result string
		var err error

		if tools != nil {
			// For tools, we need to fall back to the non-tool version since Complete doesn't support tools
			result, err = p.provider.Complete(ctx, systemPrompt, messages, model)
		} else {
			result, err = p.provider.Complete(ctx, systemPrompt, messages, model)
		}

		if err != nil {
			stream <- StreamEvent{
				Type:  "error",
				Error: fmt.Sprintf("Non-streaming completion failed: %v", err),
			}
			return
		}

		// Simulate streaming by sending the complete response as a single delta
		stream <- StreamEvent{
			Type:    "delta",
			Content: result,
		}

		// Send done event
		stream <- StreamEvent{
			Type: "done",
		}
	}()

	return stream, nil
}