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
// blocks ("thinking"). Empty Phase means the adapter did not classify
// the fragment.
type Delta struct {
	Text  string
	Phase string
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
// model. Signature is Anthropic's interleaved-thinking-2025-05-14 signed
// signature; preserve verbatim if the consumer plans to round-trip the
// block to a subsequent turn.
type Thinking struct {
	Text      string
	Signature string
}

func (Thinking) eventTag() {}

// Usage carries token-usage data for the turn. Emitted alongside or in
// place of Done depending on the adapter.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	StopReason          string
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
