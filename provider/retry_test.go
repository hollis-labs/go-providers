package provider

import (
	"errors"
	"testing"
	"time"
)

func TestRetryableStatusCode(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{429, true},  // rate limit
		{500, true},  // server error
		{502, true},  // bad gateway
		{503, true},  // service unavailable
		{529, true},  // Anthropic overloaded
		{599, true},  // upper bound of 5xx
		{600, false}, // not 5xx
	}
	for _, tt := range tests {
		got := RetryableStatusCode(tt.code)
		if got != tt.want {
			t.Errorf("RetryableStatusCode(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	t.Run("APIError 429", func(t *testing.T) {
		err := &APIError{StatusCode: 429, Message: "rate limited"}
		if !IsRetryableError(err) {
			t.Error("expected 429 to be retryable")
		}
	})

	t.Run("APIError 400", func(t *testing.T) {
		err := &APIError{StatusCode: 400, Message: "bad request"}
		if IsRetryableError(err) {
			t.Error("expected 400 to not be retryable")
		}
	})

	t.Run("non-API error", func(t *testing.T) {
		err := errors.New("connection refused")
		if IsRetryableError(err) {
			t.Error("expected non-API error to not be retryable")
		}
	})

	t.Run("wrapped APIError", func(t *testing.T) {
		inner := &APIError{StatusCode: 503, Message: "unavailable"}
		err := errors.Join(errors.New("provider call failed"), inner)
		if !IsRetryableError(err) {
			t.Error("expected wrapped 503 to be retryable")
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{"0", 0},
		{"120", 120 * time.Second},
		{"not-a-number", 0},
	}
	for _, tt := range tests {
		got := ParseRetryAfter(tt.header)
		if got != tt.want {
			t.Errorf("ParseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	cfg := DefaultRetryConfig()

	// With jitter, delay is in range [base*0.75, base]. Test within that range.
	tests := []struct {
		attempt    int
		retryAfter time.Duration
		wantMin    time.Duration // base * 0.75
		wantMax    time.Duration // base (no jitter)
	}{
		{0, 0, 750 * time.Millisecond, 1 * time.Second},   // base 1s
		{1, 0, 1500 * time.Millisecond, 2 * time.Second},  // base 2s
		{2, 0, 3 * time.Second, 4 * time.Second},           // base 4s
		{3, 0, 6 * time.Second, 8 * time.Second},           // base 8s (capped)
		{4, 0, 6 * time.Second, 8 * time.Second},           // base 16s capped to 8s
		{0, 3 * time.Second, 2250 * time.Millisecond, 3 * time.Second},  // retry-after 3s
		{0, 20 * time.Second, 15 * time.Second, 20 * time.Second},       // retry-after 20s (not capped at MaxDelay anymore)
	}
	for _, tt := range tests {
		got := cfg.BackoffDelay(tt.attempt, tt.retryAfter)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("BackoffDelay(attempt=%d, retryAfter=%v) = %v, want [%v, %v]",
				tt.attempt, tt.retryAfter, got, tt.wantMin, tt.wantMax)
		}
	}
}

func TestIsTokenRateLimit(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"token rate limit",
			&APIError{StatusCode: 429, Message: `exceed your organization's rate limit of 30,000 input tokens per minute`},
			true,
		},
		{
			"request rate limit",
			&APIError{StatusCode: 429, Message: `rate limit exceeded, please retry`},
			false,
		},
		{
			"non-429",
			&APIError{StatusCode: 500, Message: `input tokens server error`},
			false,
		},
		{
			"non-API error",
			errors.New("connection refused"),
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTokenRateLimit(tt.err); got != tt.want {
				t.Errorf("IsTokenRateLimit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIErrorMessage(t *testing.T) {
	t.Run("without retry-after", func(t *testing.T) {
		err := &APIError{StatusCode: 429, Message: "rate limited"}
		got := err.Error()
		want := "API error 429: rate limited"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("with retry-after", func(t *testing.T) {
		err := &APIError{StatusCode: 429, Message: "rate limited", RetryAfter: 5 * time.Second}
		got := err.Error()
		if got != "API error 429: rate limited (retry-after: 5s)" {
			t.Errorf("got %q", got)
		}
	})
}
