package provider

import "errors"

// ErrProviderSessionLost reports that a resume turn named a provider
// session the CLI no longer has. The stored session id is dead: resending
// with it fails the same way, and dropping it starts a new conversation
// without the old history. Session layers wrap it so callers can match it
// with errors.Is and decide whether to resend.
var ErrProviderSessionLost = errors.New("provider: provider session lost")

// SessionLostClassifier is an optional CLIAdapter extension for CLIs that
// report an unknown resume id only on stderr, with no structured stdout
// line for ParseLine to see. The session layer that owns the stored id
// captures a bounded stderr tail for a failed turn and asks the adapter
// whether that failure means the id is dead.
type SessionLostClassifier interface {
	// IsSessionLost reports whether stderrTail — the last bytes the
	// turn's process wrote to stderr — shows that the resume id passed to
	// BuildArgs is unknown to the CLI.
	IsSessionLost(stderrTail []byte) bool
}

// SessionResumeVerifier is an optional CLIAdapter extension for CLIs whose
// resume keeps the session id: resuming a known session reports that same
// id, so a resume turn that reports a different one did not continue the
// requested session. Antigravity needs this because it answers an unknown
// conversation id by silently starting a new conversation (a stderr warning,
// exit 0), where claude and opencode fail the turn instead and are covered
// by SessionLostClassifier. The session layer compares the id it passed to
// BuildArgs with the first session id the turn reports.
type SessionResumeVerifier interface {
	ResumeKeepsSessionID() bool
}

// ErrProviderNotAuthenticated reports that the CLI has no usable login.
// Session layers wrap it so callers can tell "sign in first" from other
// launch failures.
var ErrProviderNotAuthenticated = errors.New("provider: provider CLI not authenticated")

// AuthFailureClassifier is an optional CLIAdapter extension that recognizes
// a login failure from the tail of a failed turn's stderr.
type AuthFailureClassifier interface {
	IsNotAuthenticated(stderrTail []byte) bool
}

// Preflighter is an optional CLIAdapter extension: a cheap check, run before
// a runtime starts a session, that fails with a typed error instead of
// letting the CLI hit the failure itself (for example, an interactive login
// that would open a browser).
type Preflighter interface {
	Preflight() error
}
