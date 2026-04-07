package provider

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxRetryAfter is the maximum retry-after duration we'll respect from the server.
// This is intentionally much higher than MaxDelay to honour server guidance.
const MaxRetryAfter = 60 * time.Second

// RetryConfig controls exponential backoff behaviour.
type RetryConfig struct {
	MaxRetries   int           // default 3
	InitialDelay time.Duration // default 1s
	MaxDelay     time.Duration // default 8s
	Multiplier   float64       // default 2.0
}

// DefaultRetryConfig returns the standard retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     8 * time.Second,
		Multiplier:   2.0,
	}
}

// APIError represents an HTTP error from an LLM API that may be retryable.
type APIError struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration // parsed from Retry-After header, 0 if absent
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("API error %d: %s (retry-after: %s)", e.StatusCode, e.Message, e.RetryAfter)
	}
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// RetryableStatusCode returns true for status codes that should be retried.
func RetryableStatusCode(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == 529 || // Anthropic overloaded
		(statusCode >= 500 && statusCode < 600)
}

// IsRetryableError checks if an error is a retryable APIError.
func IsRetryableError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return RetryableStatusCode(apiErr.StatusCode)
	}
	return false
}

// ParseRetryAfter parses the Retry-After header value.
// It supports seconds (integer) and HTTP-date formats.
func ParseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}

	// Try integer seconds first.
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(secs) * time.Second
	}

	// Try HTTP-date format.
	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
	} {
		if t, err := time.Parse(layout, header); err == nil {
			d := time.Until(t)
			if d < 0 {
				return 0
			}
			return d
		}
	}

	return 0
}

// BackoffDelay calculates the delay for a given attempt using exponential backoff
// with jitter. If retryAfter is non-zero it is used instead, capped at MaxRetryAfter
// (60s) to respect server guidance. Computed backoff is capped at MaxDelay (8s).
// Jitter subtracts 0-25% of the delay to prevent thundering herd.
func (c RetryConfig) BackoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	var delay time.Duration
	if retryAfter > 0 {
		// Respect the server's retry-after, capped at 60s (not MaxDelay).
		delay = retryAfter
		if delay > MaxRetryAfter {
			delay = MaxRetryAfter
		}
	} else {
		delay = time.Duration(float64(c.InitialDelay) * math.Pow(c.Multiplier, float64(attempt)))
		if delay > c.MaxDelay {
			delay = c.MaxDelay
		}
	}

	// Apply jitter: subtract 0-25% of the delay.
	jitter := time.Duration(rand.Int63n(int64(delay) / 4))
	delay -= jitter

	return delay
}

// IsTokenRateLimit returns true if the error is a 429 caused by exceeding
// the input token rate limit (as opposed to a transient request rate limit).
// Token-rate 429s cannot be fixed by retrying the same payload.
func IsTokenRateLimit(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		return false
	}
	lower := strings.ToLower(apiErr.Message)
	return strings.Contains(lower, "input tokens") || strings.Contains(lower, "input_tokens")
}

// StatusCallback is called during retries to report status to the caller.
type StatusCallback func(message string)
