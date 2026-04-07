package provider

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProgressLoop represents a detected processing loop.
type ProgressLoop struct {
	Type        string        // "content_loop", "tool_loop", "state_loop"
	Description string
	Iterations  int
	Duration    time.Duration
	Event       StreamEvent
}

func (pl *ProgressLoop) Error() string {
	return fmt.Sprintf("Progress loop (%s): %s (iterations: %d, duration: %v)",
		pl.Type, pl.Description, pl.Iterations, pl.Duration)
}

// ProgressTracker monitors stream events for potential processing loops.
// It detects when the same content, tool usage, or state patterns repeat
// beyond reasonable thresholds.
type ProgressTracker struct {
	maxIterations    int
	detectionWindow  time.Duration
	detectionMode    string // "log" or "kill"

	// Tracking state
	mu                sync.RWMutex
	contentHistory    []contentRecord
	toolHistory       []toolRecord
	stateHistory      []stateRecord
	windowStart       time.Time
}

type contentRecord struct {
	hash      string
	content   string
	timestamp time.Time
	count     int
}

type toolRecord struct {
	toolName  string
	inputHash string
	timestamp time.Time
	count     int
}

type stateRecord struct {
	stateHash string
	timestamp time.Time
	count     int
}

// NewProgressTracker creates a new progress tracker with the given configuration.
func NewProgressTracker(maxIterations int, detectionWindow time.Duration, detectionMode string) *ProgressTracker {
	return &ProgressTracker{
		maxIterations:   maxIterations,
		detectionWindow: detectionWindow,
		detectionMode:   detectionMode,
		contentHistory:  make([]contentRecord, 0),
		toolHistory:     make([]toolRecord, 0),
		stateHistory:    make([]stateRecord, 0),
		windowStart:     time.Now(),
	}
}

// CheckEvent examines a stream event for progress loop patterns.
func (pt *ProgressTracker) CheckEvent(event StreamEvent) *ProgressLoop {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	now := time.Now()

	// Clean old records outside the detection window
	pt.cleanOldRecords(now)

	switch event.Type {
	case "delta":
		return pt.checkContentLoop(event, now)
	case "tool_use":
		return pt.checkToolLoop(event, now)
	default:
		// For other events, check general state patterns
		return pt.checkStateLoop(event, now)
	}
}

// checkContentLoop detects loops in content generation.
func (pt *ProgressTracker) checkContentLoop(event StreamEvent, now time.Time) *ProgressLoop {
	content := strings.TrimSpace(event.Content)
	if len(content) == 0 {
		return nil
	}

	// Hash the content for comparison
	hash := hashContent(content)

	// Look for existing record
	for i := range pt.contentHistory {
		record := &pt.contentHistory[i]
		if record.hash == hash {
			record.count++
			record.timestamp = now

			// Check if we've exceeded the iteration threshold
			if record.count > pt.maxIterations {
				return &ProgressLoop{
					Type:        "content_loop",
					Description: fmt.Sprintf("Repeated content detected: %.50s...", content),
					Iterations:  record.count,
					Duration:    now.Sub(pt.windowStart),
					Event:       event,
				}
			}
			return nil
		}
	}

	// Add new record
	pt.contentHistory = append(pt.contentHistory, contentRecord{
		hash:      hash,
		content:   content,
		timestamp: now,
		count:     1,
	})

	return nil
}

// checkToolLoop detects loops in tool usage.
func (pt *ProgressTracker) checkToolLoop(event StreamEvent, now time.Time) *ProgressLoop {
	if event.ToolUse == nil {
		return nil
	}

	toolName := event.ToolUse.Name
	inputHash := hashInput(event.ToolUse.Input)

	// Look for existing tool usage pattern
	for i := range pt.toolHistory {
		record := &pt.toolHistory[i]
		if record.toolName == toolName && record.inputHash == inputHash {
			record.count++
			record.timestamp = now

			// Check if we've exceeded the iteration threshold
			if record.count > pt.maxIterations {
				return &ProgressLoop{
					Type:        "tool_loop",
					Description: fmt.Sprintf("Repeated tool usage: %s with same inputs", toolName),
					Iterations:  record.count,
					Duration:    now.Sub(pt.windowStart),
					Event:       event,
				}
			}
			return nil
		}
	}

	// Add new record
	pt.toolHistory = append(pt.toolHistory, toolRecord{
		toolName:  toolName,
		inputHash: inputHash,
		timestamp: now,
		count:     1,
	})

	return nil
}

// checkStateLoop detects loops in general state patterns.
func (pt *ProgressTracker) checkStateLoop(event StreamEvent, now time.Time) *ProgressLoop {
	// Create a state hash based on the event type and content
	stateHash := hashState(event)

	// Look for existing state pattern
	for i := range pt.stateHistory {
		record := &pt.stateHistory[i]
		if record.stateHash == stateHash {
			record.count++
			record.timestamp = now

			// Use a higher threshold for state loops since they're less specific
			threshold := pt.maxIterations * 2
			if record.count > threshold {
				return &ProgressLoop{
					Type:        "state_loop",
					Description: fmt.Sprintf("Repeated state pattern detected: %s", event.Type),
					Iterations:  record.count,
					Duration:    now.Sub(pt.windowStart),
					Event:       event,
				}
			}
			return nil
		}
	}

	// Add new record
	pt.stateHistory = append(pt.stateHistory, stateRecord{
		stateHash: stateHash,
		timestamp: now,
		count:     1,
	})

	return nil
}

// cleanOldRecords removes records outside the detection window.
func (pt *ProgressTracker) cleanOldRecords(now time.Time) {
	cutoff := now.Add(-pt.detectionWindow)

	// Clean content history
	newContentHistory := make([]contentRecord, 0, len(pt.contentHistory))
	for _, record := range pt.contentHistory {
		if record.timestamp.After(cutoff) {
			newContentHistory = append(newContentHistory, record)
		}
	}
	pt.contentHistory = newContentHistory

	// Clean tool history
	newToolHistory := make([]toolRecord, 0, len(pt.toolHistory))
	for _, record := range pt.toolHistory {
		if record.timestamp.After(cutoff) {
			newToolHistory = append(newToolHistory, record)
		}
	}
	pt.toolHistory = newToolHistory

	// Clean state history
	newStateHistory := make([]stateRecord, 0, len(pt.stateHistory))
	for _, record := range pt.stateHistory {
		if record.timestamp.After(cutoff) {
			newStateHistory = append(newStateHistory, record)
		}
	}
	pt.stateHistory = newStateHistory

	// Update window start if this is the first time in a new window
	if len(pt.contentHistory) == 0 && len(pt.toolHistory) == 0 && len(pt.stateHistory) == 0 {
		pt.windowStart = now
	}
}

// hashContent creates a hash of the content for comparison.
func hashContent(content string) string {
	// Normalize whitespace for more reliable comparison
	normalized := strings.Join(strings.Fields(content), " ")
	hash := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", hash)[:16] // Use first 16 chars for efficiency
}

// hashInput creates a hash of tool input parameters.
func hashInput(input map[string]any) string {
	// Create a deterministic representation of the input
	var parts []string
	for key, value := range input {
		parts = append(parts, fmt.Sprintf("%s:%v", key, value))
	}

	// Sort for consistency
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			if parts[i] > parts[j] {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}

	combined := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash)[:16]
}

// hashState creates a hash of the general event state.
func hashState(event StreamEvent) string {
	state := fmt.Sprintf("%s:%s", event.Type, event.Content)
	if event.ToolUse != nil {
		state += fmt.Sprintf(":tool:%s", event.ToolUse.Name)
	}
	if event.Error != "" {
		state += fmt.Sprintf(":error:%s", event.Error)
	}

	hash := sha256.Sum256([]byte(state))
	return fmt.Sprintf("%x", hash)[:16]
}