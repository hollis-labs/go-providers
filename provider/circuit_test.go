package provider

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_TripsAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3)

	if cb.IsOpen() {
		t.Fatal("expected circuit to start closed")
	}

	// First two failures should not trip.
	if tripped := cb.RecordFailure(); tripped {
		t.Fatal("should not trip on first failure")
	}
	if cb.IsOpen() {
		t.Fatal("expected circuit to remain closed after 1 failure")
	}

	if tripped := cb.RecordFailure(); tripped {
		t.Fatal("should not trip on second failure")
	}
	if cb.IsOpen() {
		t.Fatal("expected circuit to remain closed after 2 failures")
	}

	// Third failure should trip.
	if tripped := cb.RecordFailure(); !tripped {
		t.Fatal("expected circuit to trip on third failure")
	}
	if !cb.IsOpen() {
		t.Fatal("expected circuit to be open after threshold")
	}
}

func TestCircuitBreaker_SuccessResetsCounter(t *testing.T) {
	cb := NewCircuitBreaker(3)

	cb.RecordFailure()
	cb.RecordFailure()
	// Two failures, then a success should reset.
	cb.RecordSuccess()

	if cb.IsOpen() {
		t.Fatal("expected circuit to remain closed after success")
	}

	// Now need 3 more failures to trip.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("expected circuit to remain closed after 2 new failures")
	}

	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected circuit to trip after 3 consecutive failures")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(3)

	// Trip the circuit.
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected circuit to be open")
	}

	// Reset should close it.
	cb.Reset()
	if cb.IsOpen() {
		t.Fatal("expected circuit to be closed after reset")
	}

	// Should need full threshold again to trip.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("expected circuit to remain closed after 2 failures post-reset")
	}
}

func TestCircuitBreaker_SuccessClosesOpenCircuit(t *testing.T) {
	cb := NewCircuitBreaker(2)

	cb.RecordFailure()
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected circuit to be open")
	}

	cb.RecordSuccess()
	if cb.IsOpen() {
		t.Fatal("expected success to close open circuit")
	}
}

func TestCircuitBreaker_DefaultThreshold(t *testing.T) {
	cb := NewCircuitBreaker(0)

	// Default threshold should be 3.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("expected circuit to remain closed after 2 failures with default threshold")
	}

	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected circuit to trip at default threshold of 3")
	}
}

func TestCircuitBreaker_FurtherFailuresAfterTrip(t *testing.T) {
	cb := NewCircuitBreaker(2)

	// Trip it.
	cb.RecordFailure()
	tripped := cb.RecordFailure()
	if !tripped {
		t.Fatal("expected trip on second failure")
	}

	// Further failures should not report tripping again.
	tripped = cb.RecordFailure()
	if tripped {
		t.Fatal("should not report tripping again when already open")
	}
	if !cb.IsOpen() {
		t.Fatal("circuit should still be open")
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(100)

	var wg sync.WaitGroup
	// Run 100 goroutines recording failures concurrently.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.RecordFailure()
		}()
	}
	wg.Wait()

	if !cb.IsOpen() {
		t.Fatal("expected circuit to be open after 100 concurrent failures with threshold 100")
	}

	cb.Reset()
	if cb.IsOpen() {
		t.Fatal("expected circuit to be closed after reset")
	}
}

func TestCircuitBreaker_State(t *testing.T) {
	cb := NewCircuitBreaker(1)

	if cb.State() != CircuitClosed {
		t.Fatalf("expected CircuitClosed, got %d", cb.State())
	}

	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("expected CircuitOpen, got %d", cb.State())
	}
}

func TestCircuitBreaker_HalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(1)
	cb.cooldown = 10 * time.Millisecond // very short for testing

	// Trip the circuit.
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("expected circuit to be open")
	}

	// Wait for cooldown to expire.
	time.Sleep(20 * time.Millisecond)

	// Should transition to half-open (IsOpen returns false to allow probe).
	if cb.IsOpen() {
		t.Fatal("expected circuit to transition to half-open after cooldown")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected CircuitHalfOpen, got %d", cb.State())
	}

	// A success in half-open should close the circuit.
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected CircuitClosed after success in half-open, got %d", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenProbeFailure(t *testing.T) {
	cb := NewCircuitBreaker(1)
	cb.cooldown = 10 * time.Millisecond

	// Trip the circuit.
	cb.RecordFailure()

	// Wait for cooldown.
	time.Sleep(20 * time.Millisecond)

	// Transition to half-open.
	cb.IsOpen() // triggers transition

	// Probe fails — should go back to open.
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected CircuitOpen after failed probe, got %d", cb.State())
	}
}
