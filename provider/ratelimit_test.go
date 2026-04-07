package provider

import (
	"sync"
	"testing"
	"time"
)

func TestTokenRateTracker_Available_FullBudget(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	if avail := tr.Available(); avail != 30000 {
		t.Errorf("expected 30000 available, got %d", avail)
	}
}

func TestTokenRateTracker_Available_AfterRecord(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.Record(10000)
	if avail := tr.Available(); avail != 20000 {
		t.Errorf("expected 20000 available, got %d", avail)
	}
}

func TestTokenRateTracker_Available_NeverNegative(t *testing.T) {
	tr := NewTokenRateTracker(1000)
	tr.Record(2000)
	if avail := tr.Available(); avail != 0 {
		t.Errorf("expected 0 available, got %d", avail)
	}
}

func TestTokenRateTracker_SlidingWindowExpiry(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	// Inject an old record directly (61 seconds ago).
	tr.mu.Lock()
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-61 * time.Second),
		tokens: 20000,
	})
	tr.mu.Unlock()

	// The old record should be expired.
	if avail := tr.Available(); avail != 30000 {
		t.Errorf("expected 30000 after expiry, got %d", avail)
	}
}

func TestTokenRateTracker_SlidingWindowPartialExpiry(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	tr.mu.Lock()
	// Old record: expired.
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-61 * time.Second),
		tokens: 15000,
	})
	// Recent record: still active.
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-10 * time.Second),
		tokens: 5000,
	})
	tr.mu.Unlock()

	if avail := tr.Available(); avail != 25000 {
		t.Errorf("expected 25000 (only recent record counted), got %d", avail)
	}
}

func TestTokenRateTracker_WaitTime_FitsNow(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.Record(10000)
	wait := tr.WaitTime(15000)
	if wait != 0 {
		t.Errorf("expected 0 wait, got %s", wait)
	}
}

func TestTokenRateTracker_WaitTime_NeedsToWait(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	// Record at 50 seconds ago — will expire in ~10 seconds.
	tr.mu.Lock()
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-50 * time.Second),
		tokens: 25000,
	})
	tr.mu.Unlock()

	wait := tr.WaitTime(10000)
	// Need 5000 more tokens freed. The 25K record expires in ~10s.
	if wait < 8*time.Second || wait > 12*time.Second {
		t.Errorf("expected wait around 10s, got %s", wait)
	}
}

func TestTokenRateTracker_WaitTime_ExceedsLimit(t *testing.T) {
	tr := NewTokenRateTracker(30000)

	// Record something recent.
	tr.mu.Lock()
	tr.window = append(tr.window, tokenRecord{
		at:     time.Now().Add(-5 * time.Second),
		tokens: 10000,
	})
	tr.mu.Unlock()

	// Request that exceeds the entire limit.
	wait := tr.WaitTime(50000)
	// Should return time until the full window clears (around 55s).
	if wait < 50*time.Second || wait > 60*time.Second {
		t.Errorf("expected wait around 55s for oversized request, got %s", wait)
	}
}

func TestTokenRateTracker_WaitTime_EmptyWindow(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	wait := tr.WaitTime(20000)
	if wait != 0 {
		t.Errorf("expected 0 wait on empty window, got %s", wait)
	}
}

func TestTokenRateTracker_UpdateLimit(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.UpdateLimit(50000)
	if avail := tr.Available(); avail != 50000 {
		t.Errorf("expected 50000 after UpdateLimit, got %d", avail)
	}
}

func TestTokenRateTracker_UpdateLimit_IgnoresZero(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.UpdateLimit(0)
	if avail := tr.Available(); avail != 30000 {
		t.Errorf("expected 30000 (unchanged), got %d", avail)
	}
}

func TestTokenRateTracker_Remaining(t *testing.T) {
	tr := NewTokenRateTracker(30000)
	tr.Record(12000)
	avail, limit := tr.Remaining()
	if avail != 18000 {
		t.Errorf("expected 18000 available, got %d", avail)
	}
	if limit != 30000 {
		t.Errorf("expected 30000 limit, got %d", limit)
	}
}

func TestTokenRateTracker_ConcurrentAccess(t *testing.T) {
	tr := NewTokenRateTracker(100000)
	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent Record calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Record(100)
		}()
	}

	// Concurrent Available calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Available()
		}()
	}

	// Concurrent WaitTime calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.WaitTime(500)
		}()
	}

	// Concurrent UpdateLimit calls.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.UpdateLimit(100000)
		}()
	}

	wg.Wait()

	// Verify no panic occurred and state is consistent.
	avail := tr.Available()
	if avail < 0 || avail > 100000 {
		t.Errorf("available out of range after concurrent access: %d", avail)
	}
}
