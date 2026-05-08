package provider

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"syscall"

	pevents "github.com/hollis-labs/go-providers/provider/events"
)

// SubprocessBridge is a provider that wraps CLI tools using standard pipes
// (stdin/stdout) instead of pseudo-terminals. It works on all platforms
// including Windows where PTY support is unavailable.
//
// The trade-off vs PTYBridge: some CLIs detect non-TTY stdout and may
// alter their output format or disable interactive features. For CLIs
// that support explicit output format flags (e.g. --output-format stream-json),
// this is generally not an issue.
type SubprocessBridge struct {
	adapter CLIAdapter
	cliPath string
}

// NewSubprocessBridge creates a subprocess bridge for any CLI adapter.
func NewSubprocessBridge(adapter CLIAdapter, cliPath string) *SubprocessBridge {
	return &SubprocessBridge{adapter: adapter, cliPath: cliPath}
}

func (s *SubprocessBridge) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	return s.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
}

func (s *SubprocessBridge) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := s.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage returns the concatenated text output from the CLI.
// Usage may be nil because the wrapped CLI is not required to surface it.
func (s *SubprocessBridge) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	ctx = context.WithValue(ctx, ptySessionKeyType{}, "")
	ch, err := s.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
	if err != nil {
		return CompleteResult{}, err
	}

	var sb strings.Builder
	var usage *Usage
	for ev := range ch {
		switch ev.Type {
		case EventDelta:
			sb.WriteString(ev.Content)
		case EventUsage:
			usage = ev.Usage
		case EventError:
			return CompleteResult{}, fmt.Errorf("cli error: %s", ev.Error)
		}
	}
	return CompleteResult{Text: sb.String(), Usage: usage}, nil
}

func (s *SubprocessBridge) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		SupportsStreamJSON:          true,
		SupportsPreToolHooks:        false,
		SupportsPostToolHooks:       false,
		SupportsSystemPromptCaching: false,
		SupportsToolCalling:         true,
		SupportsBatch:               false,
		SupportsImageInput:          false,
		MaxTokens:                   0,
	}
}

// streamCLI spawns the CLI as a subprocess with piped stdout and streams parsed events.
func (s *SubprocessBridge) streamCLI(ctx context.Context, systemPrompt string, messages []ChatMessage) (<-chan StreamEvent, error) {
	var prompt string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != "" {
			prompt = messages[i].Content
			break
		}
	}
	if prompt == "" {
		return nil, fmt.Errorf("no user message found")
	}

	cliSessionID, _ := CLISessionIDFromContext(ctx)
	args := s.adapter.BuildArgs(prompt, systemPrompt, cliSessionID)

	log.Printf("subprocess[%s]: launching CLI with %d args", s.adapter.Name(), len(args))

	cmd := exec.CommandContext(ctx, s.cliPath, args...)
	// On context cancellation, send SIGTERM and let the process flush stream
	// output, then SIGKILL after WaitDelay if it hasn't exited. Replaces the
	// stdlib default of immediate SIGKILL. On Windows, SIGTERM is not
	// meaningful and stdlib falls back to TerminateProcess after WaitDelay.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = WaitDelayFromContext(ctx)

	if dir, ok := SandboxDirFromContext(ctx); ok {
		cmd.Dir = dir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	typedCb, hasTyped := EventsCallbackFromContext(ctx)
	_, hasEventParser := s.adapter.(EventParser)

	// When a typed-events callback is configured, capture stderr so the
	// bridge can emit events.SubprocessStderr per line. Otherwise leave
	// stderr at its default (os.DevNull) for backward compatibility.
	var stderr io.ReadCloser
	if hasTyped {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("stderr pipe: %w", err)
		}
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start subprocess: %w", err)
	}

	// Notify process tracker if one is attached.
	if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
		cb(cmd.Process, true)
	}

	ch := make(chan StreamEvent, 64)
	activityCb, hasActivity := ActivityCallbackFromContext(ctx)

	var bridgeState *eventsBridgeState
	stopHeartbeat := func() {}
	if hasTyped {
		bridgeState = newEventsBridgeState()
		stopHeartbeat = startHeartbeat(ctx, typedCb, bridgeState, HeartbeatIntervalFromContext(ctx))
	}

	stderrDone := make(chan struct{})
	if hasTyped && stderr != nil {
		go func() {
			defer close(stderrDone)
			defer stderr.Close()
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.SubprocessStderr{Line: line}})
			}
		}()
	} else {
		close(stderrDone)
	}

	go func() {
		defer close(ch)
		defer stdout.Close()
		defer stopHeartbeat()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		var seenDelta, seenToolUse, terminalSent bool

		// emitGuardIfNeeded emits the no-silent-drop error event exactly once,
		// when the CLI produced only tool_use blocks and no text deltas.
		// Returns true if the guard was emitted on this call.
		emitGuardIfNeeded := func() bool {
			if seenToolUse && !seenDelta && !terminalSent && ctx.Err() == nil {
				const msg = "CLI bridge cannot forward tool calls"
				ch <- StreamEvent{Type: EventError, Error: msg}
				if hasTyped {
					emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Error{Message: msg}})
				}
				terminalSent = true
				return true
			}
			return false
		}

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			events, err := s.adapter.ParseLine(line)
			if err != nil {
				log.Printf("subprocess[%s]: parse error: %v (line: %s)", s.adapter.Name(), err, string(line))
				continue
			}

			if len(events) > 0 && hasActivity && cmd.Process != nil {
				activityCb(cmd.Process.Pid)
			}

			if hasTyped {
				var typed []pevents.Event
				if hasEventParser {
					if t, perr := s.adapter.(EventParser).ParseLineEvents(line); perr == nil {
						typed = t
					}
				}
				if typed == nil {
					typed = translateStreamEvents(events)
				}
				emitTyped(ctx, typedCb, bridgeState, typed)
			}

			for _, ev := range events {
				switch ev.Type {
				case EventDelta:
					seenDelta = true
				case EventToolUse:
					seenToolUse = true
				case EventDone:
					// If the adapter produced only tool_use blocks with no
					// deltas, replace its EventDone with a terminal EventError
					// so consumers see the failure. The contract on
					// IsTurnComplete is "exactly one terminal event" — don't
					// forward the adapter's EventDone after the guard fires.
					if emitGuardIfNeeded() {
						continue
					}
					terminalSent = true
				case EventError:
					// Upstream already signalled failure — don't pile on.
					terminalSent = true
				}
				ch <- ev
			}
		}

		if err := scanner.Err(); err != nil {
			log.Printf("subprocess[%s]: scanner error: %v", s.adapter.Name(), err)
		}

		// Post-loop guard: stream ended without a terminal event. Still emit
		// the error so callers don't hang on an empty result.
		emitGuardIfNeeded()

		// Wait for process to finish. cmd.Cancel + cmd.WaitDelay handle
		// SIGTERM-then-SIGKILL on context cancellation.
		waitErr := cmd.Wait()

		// Drain stderr BEFORE emitting any terminal event so consumers
		// don't observe SubprocessStderr typed events arriving after a
		// turn-terminal Done/Error. The legacy StreamEvent channel is
		// not coupled to stderr, but the typed surface is — and the
		// terminal contract says no further typed events fire after
		// Done/Error. (No-op when stderr capture isn't active —
		// stderrDone is closed at start in that path.)
		<-stderrDone

		// Always emit a terminal event so consumers see an explicit boundary
		// before the channel closes. See PTYBridge.streamCLI for the same
		// protocol; both bridges guarantee one of EventDone or EventError.
		switch {
		case terminalSent:
			// Adapter took care of the boundary.
		case ctx.Err() != nil:
			msg := fmt.Sprintf("context cancelled: %v", ctx.Err())
			ch <- StreamEvent{Type: EventError, Error: msg}
			if hasTyped {
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Error{Err: ctx.Err(), Message: msg}})
			}
		case waitErr != nil:
			msg := fmt.Sprintf("process exited: %v", waitErr)
			ch <- StreamEvent{Type: EventError, Error: msg}
			if hasTyped {
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Error{Err: waitErr, Message: msg}})
			}
		default:
			ch <- StreamEvent{Type: EventDone}
			if hasTyped {
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Done{}})
			}
		}

		if waitErr != nil && ctx.Err() == nil {
			log.Printf("subprocess[%s]: process exited: %v", s.adapter.Name(), waitErr)
		}

		// Notify process tracker that process has exited.
		if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
			cb(cmd.Process, false)
		}
	}()

	return ch, nil
}
