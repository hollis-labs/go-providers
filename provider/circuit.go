package provider

import (
	"sync"
	"time"
)

// CircuitState represents the circuit breaker state.
type CircuitState int

const (
	// CircuitClosed means normal operation — requests are allowed.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the breaker has tripped — requests should be paused.
	CircuitOpen
	// CircuitHalfOpen means the breaker is probing — one request is allowed to test recovery.
	CircuitHalfOpen
)

// DefaultCooldown is the duration after which an open circuit transitions to half-open.
const DefaultCooldown = 30 * time.Second

// CircuitBreaker tracks consecutive failures and trips when a threshold is reached.
// It supports closed → open → half-open → closed state transitions.
// It is safe for concurrent use.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            CircuitState
	consecutiveFails int
	threshold        int           // trips after this many consecutive failures
	openedAt         time.Time     // when the circuit last opened
	cooldown         time.Duration // how long to wait before probing (half-open)
}

// NewCircuitBreaker creates a circuit breaker that trips after `threshold` consecutive failures.
func NewCircuitBreaker(threshold int) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 3
	}
	return &CircuitBreaker{
		state:     CircuitClosed,
		threshold: threshold,
		cooldown:  DefaultCooldown,
	}
}

// RecordFailure records a failure. Returns true if the circuit just tripped open.
func (cb *CircuitBreaker) RecordFailure() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails++

	// Half-open probe failed — go back to open.
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitOpen
		cb.openedAt = time.Now()
		return false // already tripped
	}

	if cb.consecutiveFails >= cb.threshold && cb.state == CircuitClosed {
		cb.state = CircuitOpen
		cb.openedAt = time.Now()
		return true
	}
	return false
}

// RecordSuccess records a success, resetting the consecutive failure counter.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0
	// A success in any state closes the circuit.
	cb.state = CircuitClosed
}

// Reset reopens the circuit (moves from open back to closed) and resets counters.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitClosed
	cb.consecutiveFails = 0
}

// IsOpen returns true if the circuit breaker is blocking requests.
// An open circuit transitions to half-open after the cooldown period,
// allowing one probe request through.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitOpen {
		if time.Since(cb.openedAt) >= cb.cooldown {
			// Cooldown expired — transition to half-open, allow one probe.
			cb.state = CircuitHalfOpen
			return false
		}
		return true
	}
	return false
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.state
}
