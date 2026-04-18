package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const defaultWindowDuration = 60 * time.Second

// pacingHeartbeat is the cadence for PacingWait status-callback emissions
// while blocked waiting for rate-limit budget. Declared as a var so tests
// can shorten it.
var pacingHeartbeat = 10 * time.Second

// ErrRequestExceedsRateBudget signals that the caller's estimated request
// size is larger than the current per-minute rate-limit window. Waiting
// cannot fix this — the caller must reduce the request (e.g. compact
// history) before retrying. Callers should check with errors.Is.
var ErrRequestExceedsRateBudget = errors.New("request exceeds per-minute rate budget")

// PacingWait blocks for the given duration while emitting periodic status
// updates via onStatus, and returns early with ctx.Err() if the context is
// cancelled. It is intended to replace the inline
// select { case <-ctx.Done(): ...; case <-time.After(wait): } pattern used
// by provider adapters during rate-limit pacing, where a silent wait is
// misclassified as a hang by upstream stall watchdogs.
//
// onStatus may be nil. When non-nil, it is called once immediately with
// the full wait duration, then again every pacingHeartbeat with the
// remaining time. A zero or negative wait returns immediately without
// any callback.
func PacingWait(ctx context.Context, wait time.Duration, onStatus func(string)) error {
	if wait <= 0 {
		return nil
	}
	notify := func(remaining time.Duration) {
		if onStatus == nil {
			return
		}
		secs := int(remaining.Seconds() + 0.5)
		if secs < 1 {
			secs = 1
		}
		onStatus(fmt.Sprintf("Rate-limit pacing: waiting %ds for provider budget...", secs))
	}

	deadline := time.Now().Add(wait)
	notify(wait)

	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(pacingHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case now := <-ticker.C:
			remaining := deadline.Sub(now)
			if remaining <= 0 {
				return nil
			}
			notify(remaining)
		}
	}
}

// tokenRecord represents a single token usage event within the sliding window.
type tokenRecord struct {
	at     time.Time
	tokens int
}

// TokenRateTracker tracks token usage over a sliding 60-second window
// and provides pacing information to avoid per-minute rate limits.
type TokenRateTracker struct {
	mu     sync.Mutex
	window []tokenRecord
	limit  int
}

// NewTokenRateTracker creates a new tracker with the given per-minute token limit.
func NewTokenRateTracker(defaultLimit int) *TokenRateTracker {
	return &TokenRateTracker{
		limit: defaultLimit,
	}
}

// expire removes records older than 60 seconds. Must be called with mu held.
func (t *TokenRateTracker) expire() {
	cutoff := time.Now().Add(-defaultWindowDuration)
	i := 0
	for i < len(t.window) && t.window[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		t.window = t.window[i:]
	}
}

// usedTokens returns the total tokens in the current window. Must be called with mu held.
func (t *TokenRateTracker) usedTokens() int {
	total := 0
	for _, r := range t.window {
		total += r.tokens
	}
	return total
}

// Record records that tokens were sent at the current time.
func (t *TokenRateTracker) Record(tokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire()
	t.window = append(t.window, tokenRecord{at: time.Now(), tokens: tokens})
}

// Available returns the remaining token budget in the current 60-second window.
func (t *TokenRateTracker) Available() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire()
	avail := t.limit - t.usedTokens()
	if avail < 0 {
		return 0
	}
	return avail
}

// WaitTime returns how long to wait before a request of the given size can be sent.
// Returns 0 if the request fits within the current budget.
func (t *TokenRateTracker) WaitTime(requestTokens int) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire()

	used := t.usedTokens()
	if used+requestTokens <= t.limit {
		return 0
	}

	// We need to wait until enough old records expire to make room.
	// Walk through the window oldest-first, accumulating freed tokens
	// until we have enough headroom.
	need := used + requestTokens - t.limit
	freed := 0
	now := time.Now()
	for _, r := range t.window {
		freed += r.tokens
		if freed >= need {
			// This record expires at r.at + 60s; wait until then.
			expiresAt := r.at.Add(defaultWindowDuration)
			wait := expiresAt.Sub(now)
			if wait < 0 {
				return 0
			}
			return wait
		}
	}

	// Even expiring everything wouldn't be enough — the request exceeds the
	// per-minute limit entirely. Return the time until the full window clears.
	if len(t.window) > 0 {
		last := t.window[len(t.window)-1]
		expiresAt := last.at.Add(defaultWindowDuration)
		wait := expiresAt.Sub(now)
		if wait < 0 {
			return 0
		}
		return wait
	}
	return 0
}

// UpdateLimit updates the per-minute token limit, typically calibrated from
// API response headers (x-ratelimit-limit-input-tokens).
func (t *TokenRateTracker) UpdateLimit(limit int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if limit > 0 {
		t.limit = limit
	}
}

// Remaining returns the available tokens and the current limit, useful for logging.
func (t *TokenRateTracker) Remaining() (available int, limit int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expire()
	avail := t.limit - t.usedTokens()
	if avail < 0 {
		avail = 0
	}
	return avail, t.limit
}
