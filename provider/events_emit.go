package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/go-providers/provider/events"
)

// EventsCallback is invoked once per typed event produced by a CLI/PTY
// bridge. It is called from the bridge's parser goroutine; consumers
// must keep work in the callback short or hand off to their own
// goroutine. Callbacks must be safe for the bridge to call concurrently
// from a single goroutine (no internal synchronization needed by the
// caller for ordering, but locking is the caller's responsibility if
// the callback is shared across multiple bridges).
type EventsCallback func(ev events.Event)

type eventsCallbackKeyType struct{}

// WithEvents returns a context carrying a typed-events callback. When
// the spawned bridge sees this, it emits typed events.Event values
// alongside the existing llmtypes.StreamEvent channel — both surfaces fire on
// the same parser line. If unset, no typed events are emitted; the
// existing llmtypes.StreamEvent channel behavior is preserved.
//
// The callback fires from the bridge's parser goroutine; treat it as
// you would an io.Writer's Write — quick, non-blocking work.
func WithEvents(ctx context.Context, cb EventsCallback) context.Context {
	if cb == nil {
		return ctx
	}
	return context.WithValue(ctx, eventsCallbackKeyType{}, cb)
}

// EventsCallbackFromContext extracts the typed-events callback from ctx,
// if set.
func EventsCallbackFromContext(ctx context.Context) (EventsCallback, bool) {
	cb, ok := ctx.Value(eventsCallbackKeyType{}).(EventsCallback)
	return cb, ok && cb != nil
}

type toolArgFingerprintKeyType struct{}

// WithToolArgFingerprint returns a context that, when set to true,
// instructs CLI/PTY bridges to replace tool-call argument values with
// SHA-256 hex digests in any emitted events.ToolUse. Argument keys are
// preserved; values are hashed (after JSON marshalling). Default off
// preserves the existing full-args behavior.
//
// Use this when logs may be shared across trust boundaries and full
// argument values may contain sensitive data. Single-user portfolio
// deployments can leave it unset.
func WithToolArgFingerprint(ctx context.Context, on bool) context.Context {
	if !on {
		return ctx
	}
	return context.WithValue(ctx, toolArgFingerprintKeyType{}, true)
}

// ToolArgFingerprintFromContext reports whether the tool-arg
// fingerprinting flag is set in ctx.
func ToolArgFingerprintFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(toolArgFingerprintKeyType{}).(bool)
	return v
}

// EventParser is an optional CLIAdapter extension. Adapters that
// implement it produce typed events directly from the wire format,
// preserving information that would otherwise be lost when translating
// from llmtypes.StreamEvent (notably tool_result blocks, thinking signatures,
// and adapter-specific phase information). Adapters that do not
// implement it have their typed events translated from the legacy
// []llmtypes.StreamEvent return by translateStreamEvents.
type EventParser interface {
	// ParseLineEvents parses one line of structured output into typed
	// events. Returning a nil slice with nil error is permissible
	// (line was informational and produced no event).
	ParseLineEvents(line []byte) ([]events.Event, error)
}

// translateStreamEvents converts a slice of legacy llmtypes.StreamEvent into
// typed events.Event values for adapters that don't implement
// EventParser. Lossy by design: tool_result blocks and thinking
// signatures only flow through if the adapter natively emits them on
// llmtypes.StreamEvent, which most adapters do not. EventParser is the
// preferred path for richer events.
func translateStreamEvents(in []llmtypes.StreamEvent) []events.Event {
	out := make([]events.Event, 0, len(in))
	for _, ev := range in {
		switch ev.Type {
		case llmtypes.EventDelta:
			out = append(out, events.Delta{Text: ev.Content})
		case llmtypes.EventToolUse:
			if ev.ToolUse != nil {
				out = append(out, events.ToolUse{
					ID:   ev.ToolUse.ID,
					Name: ev.ToolUse.Name,
					Args: ev.ToolUse.Input,
				})
			}
		case llmtypes.EventUsage:
			if ev.Usage != nil {
				out = append(out, events.Usage{
					InputTokens:         ev.Usage.InputTokens,
					OutputTokens:        ev.Usage.OutputTokens,
					CacheCreationTokens: ev.Usage.CacheCreationTokens,
					CacheReadTokens:     ev.Usage.CacheReadTokens,
					StopReason:          ev.Usage.StopReason,
				})
			}
		case llmtypes.EventDone:
			out = append(out, events.Done{})
		case llmtypes.EventError:
			out = append(out, events.Error{Message: ev.Error})
		case llmtypes.EventSessionID:
			out = append(out, events.SessionID{ID: ev.SessionID})
		case llmtypes.EventThinking:
			if ev.ThinkingBlock != nil {
				out = append(out, events.Thinking{
					Text:      ev.ThinkingBlock.Thinking,
					Signature: ev.ThinkingBlock.Signature,
				})
			}
		}
	}
	return out
}

// fingerprintArgs replaces argument values with SHA-256 hex digests of
// their JSON-marshalled form. Keys are preserved. Returns a new map;
// the input is not modified. Empty input returns nil.
func fingerprintArgs(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		b, err := json.Marshal(v)
		if err != nil {
			// fall back to a marker so the key is still observable
			out[k] = "sha256:marshal_error"
			continue
		}
		sum := sha256.Sum256(b)
		out[k] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return out
}

// applyToolArgFingerprint walks a slice of typed events and replaces
// ToolUse / SubagentSpawn args with fingerprinted maps when the spawn
// context requests it. Idempotent — already-fingerprinted ToolUses
// (Fingerprint=true) are passed through unchanged.
func applyToolArgFingerprint(in []events.Event) []events.Event {
	for i, ev := range in {
		switch e := ev.(type) {
		case events.ToolUse:
			if e.Fingerprint {
				continue
			}
			e.Args = fingerprintArgs(e.Args)
			e.Fingerprint = true
			in[i] = e
		case events.SubagentSpawn:
			e.Args = fingerprintArgs(e.Args)
			in[i] = e
		}
	}
	return in
}

// emitTyped fires the EventsCallback (if configured) for each typed
// event produced from one parsed line. Optionally applies
// fingerprinting and tracks LastActivityAt for the heartbeat goroutine.
//
// Caller passes either []events.Event (when ParseLineEvents was used)
// or []llmtypes.StreamEvent (translated). bridgeState is shared with the
// heartbeat goroutine; nil bridgeState disables activity tracking.
func emitTyped(
	ctx context.Context,
	cb EventsCallback,
	bridgeState *eventsBridgeState,
	typed []events.Event,
) {
	if cb == nil || len(typed) == 0 {
		return
	}
	if ToolArgFingerprintFromContext(ctx) {
		typed = applyToolArgFingerprint(typed)
	}
	if bridgeState != nil {
		bridgeState.mark(time.Now())
	}
	for _, ev := range typed {
		cb(ev)
	}
}

// eventsBridgeState coordinates the heartbeat goroutine and parser
// goroutine. Only used when EventsCallback is set.
type eventsBridgeState struct {
	mu             sync.Mutex
	lastActivityAt time.Time
	stopped        bool
}

func newEventsBridgeState() *eventsBridgeState {
	return &eventsBridgeState{lastActivityAt: time.Now()}
}

func (s *eventsBridgeState) mark(t time.Time) {
	s.mu.Lock()
	if t.After(s.lastActivityAt) {
		s.lastActivityAt = t
	}
	s.mu.Unlock()
}

func (s *eventsBridgeState) snapshot() (lastActivityAt time.Time, stopped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivityAt, s.stopped
}

func (s *eventsBridgeState) stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

// startHeartbeat runs a goroutine that fires events.Heartbeat through
// cb every interval if no activity has been recorded in that window.
// Returns a stop function; caller must call stop in the bridge's
// teardown path. Interval <= 0 disables the heartbeat.
func startHeartbeat(
	ctx context.Context,
	cb EventsCallback,
	state *eventsBridgeState,
	interval time.Duration,
) func() {
	if cb == nil || state == nil || interval <= 0 {
		return func() {}
	}
	tick := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-tick.C:
				lastAt, stopped := state.snapshot()
				if stopped {
					return
				}
				if now.Sub(lastAt) >= interval {
					cb(events.Heartbeat{LastActivityAt: lastAt})
				}
			}
		}
	}()
	return func() {
		state.stop()
		close(done)
	}
}

// DefaultHeartbeatInterval is the default cadence for events.Heartbeat
// emission when no other events have fired. Apps can override via
// WithHeartbeatInterval.
const DefaultHeartbeatInterval = 5 * time.Second

type heartbeatIntervalKeyType struct{}

// WithHeartbeatInterval returns a context that adjusts the heartbeat
// cadence. Non-positive values disable heartbeats entirely.
func WithHeartbeatInterval(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, heartbeatIntervalKeyType{}, d)
}

// HeartbeatIntervalFromContext returns the configured heartbeat
// interval, or DefaultHeartbeatInterval if none was set. Returns the
// raw configured value (which may be 0 or negative — bridges interpret
// non-positive as "disabled").
func HeartbeatIntervalFromContext(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(heartbeatIntervalKeyType{}).(time.Duration); ok {
		return d
	}
	return DefaultHeartbeatInterval
}
