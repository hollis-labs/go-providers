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
