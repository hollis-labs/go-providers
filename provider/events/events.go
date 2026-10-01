// Package events defines the normalized in-loop event taxonomy emitted by
// CLI/PTY adapters when an Events callback is wired into the spawn context.
//
// This is a parallel observation surface to the existing
// provider.StreamEvent channel returned by Provider.StreamChat. The legacy
// channel remains the canonical turn driver; the typed events here are
// intended for richer in-loop tooling activity (per-tool result tracking,
// thinking blocks with signatures, sub-agent spawn detection, stderr lines,
// heartbeats, optional arg-fingerprinting).
//
// Each adapter's ParseLineEvents (when implemented) translates the wire
// format (claude stream-json, opencode JSON, codex JSON, etc.) into these
// types. Adapters that don't implement ParseLineEvents get their typed
// events translated from the existing StreamEvent output by the bridge.
package events

import "time"

// Event is a normalized in-loop event from a CLI-spawned agent.
//
// Concrete types implement the unexported eventTag method so the type is
// closed: callers exhaustively switch on the concrete type rather than on
// a string tag. New event kinds are added by introducing new exported
// types in this package.
type Event interface {
	eventTag()
}

// Delta carries an incremental text fragment from the agent.
//
// Phase distinguishes streaming narration ("narration") from the
// terminal-result text ("final") and from text emitted inside thinking
// blocks ("thought", llmtypes.PhaseThinking). Empty Phase means the
// adapter did not classify the fragment.
type Delta struct {
	Text  string
	Phase string
	// BlockID identifies the content block the fragment belongs to: the
	// same for every fragment of one block, different for the next, so a
	// consumer can separate consecutive blocks without provider rules.
	// Opaque; empty when the adapter cannot tell blocks apart.
	BlockID string
}

func (Delta) eventTag() {}

// ToolUse carries a tool invocation from the agent.
//
// Args contains the full arguments by default. When the spawn context
// has WithToolArgFingerprint(true), Args is replaced with a map of the
// original argument keys to SHA-256 hex digests of their JSON-marshalled
// values; Fingerprint is then true.
type ToolUse struct {
	ID          string
	Name        string
	Args        map[string]any
	Fingerprint bool
}

func (ToolUse) eventTag() {}

// ToolResult carries the result of a tool invocation. ID matches the
// preceding ToolUse.ID. ContentPreview is truncated; full content (when
// the adapter forwards it) flows through the normal stdout / Delta path.
type ToolResult struct {
	ID             string
	IsError        bool
	ContentPreview string
}

func (ToolResult) eventTag() {}

// Thinking carries a completed thinking block from a reasoning-capable
// model. Signature is the underlying model's signed thinking signature
// (e.g. claude's interleaved-thinking-2025-05-14 signature surfaced via
// the claude PTY adapter); preserve verbatim if the consumer plans to
// round-trip the block to a subsequent turn.
type Thinking struct {
	Text      string
	Signature string
	// BlockID identifies the thinking block, as Delta.BlockID does.
	BlockID string
}

func (Thinking) eventTag() {}

// Usage carries token-usage data for the turn. Emitted alongside or in
// place of Done depending on the adapter.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	// StopReason is normalised with llmtypes.NormalizeStopReason.
	StopReason string
	// CostUSD is the cost of the work this event covers, as a per-event
	// delta (see llmtypes.Usage.CostUSD). Zero means no cost was reported.
	CostUSD float64
}

func (Usage) eventTag() {}

// Done is a terminal success event marking the end of a turn.
type Done struct {
	StopReason string
}

func (Done) eventTag() {}

// Error is a terminal failure event. Err is the underlying Go error when
// available (for example, from stderr-only failures or context cancellation);
// Message is the adapter-reported diagnostic.
type Error struct {
	Err     error
	Message string
}

func (Error) eventTag() {}

// SessionID carries an adapter-assigned CLI session identifier (e.g.
// claude --resume id, opencode session id). Informational; not a turn
// boundary.
type SessionID struct {
	ID string
}

func (SessionID) eventTag() {}

// SubagentSpawn is a special-cased ToolUse for known sub-agent tools
// (claude's "Task", etc.). The adapter recognises the tool name and
// emits this in addition to the underlying ToolUse so consumers tracking
// nested agent fan-out don't have to duplicate the name table.
type SubagentSpawn struct {
	Tool string
	Args map[string]any
}

func (SubagentSpawn) eventTag() {}

// SubprocessStderr carries one line of stderr from the spawned process.
// Lib-side: captured by the bridge from the child process's stderr pipe
// (subprocess transport only — PTY transports merge stderr into stdout
// at the kernel level, so SubprocessStderr is not emitted under PTY).
type SubprocessStderr struct {
	Line string
}

func (SubprocessStderr) eventTag() {}

// Heartbeat is synthesized by the bridge on a configurable interval when
// no other events have fired. Consumers can use it as a "process is
// alive but idle" signal for UX layers that show working indicators.
type Heartbeat struct {
	LastActivityAt time.Time
}

func (Heartbeat) eventTag() {}

// SessionLost reports that a resume turn did not continue the requested
// provider session: the CLI started a new one instead (Antigravity answers
// an unknown conversation id that way, without failing the turn).
// Non-terminal: the turn itself runs on in the new session. Emitted by the
// session layer, which is the only place that knows the requested id.
type SessionLost struct {
	RequestedID string
	ActualID    string
	Reason      string
}

func (SessionLost) eventTag() {}

// AuthFailed reports that the CLI is not signed in or its credentials were
// refused, as an AuthFailureClassifier recognised from its output. Emitted by
// the session layer, which runs the classifier. The turn usually fails too.
type AuthFailed struct {
	Message string
}

func (AuthFailed) eventTag() {}

// PermissionDenied reports a tool action the CLI refused because it needed
// an approval that headless mode cannot ask for. The turn still completes,
// so without this event the refusal is a silent no-op. Non-terminal.
type PermissionDenied struct {
	Action      string
	DisplayName string
}

func (PermissionDenied) eventTag() {}
