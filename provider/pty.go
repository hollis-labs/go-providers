//go:build !windows

package provider

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"syscall"

	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/creack/pty"
	pevents "github.com/hollis-labs/go-providers/provider/events"
)

// PTYBridge is a provider that wraps CLI tools in pseudo-terminals.
// It spawns the CLI as a child process, reads its structured output,
// and maps events to llmtypes.StreamEvent values.
type PTYBridge struct {
	adapter CLIAdapter
	cliPath string // resolved path to the CLI binary
}

// NewPTYBridge creates a PTY bridge for Claude CLI. Returns nil if the
// claude binary is not found in PATH. Preserved for backwards compatibility.
func NewPTYBridge() *PTYBridge {
	adapter := NewClaudeAdapter()
	path, ok := adapter.Detect()
	if !ok {
		return nil
	}
	return &PTYBridge{adapter: adapter, cliPath: path}
}

// NewPTYBridgeWithAdapter creates a PTY bridge for any CLI adapter.
func NewPTYBridgeWithAdapter(adapter CLIAdapter, cliPath string) *PTYBridge {
	return &PTYBridge{adapter: adapter, cliPath: cliPath}
}

func (p *PTYBridge) StreamChat(ctx context.Context, in llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	return p.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
}

func (p *PTYBridge) Complete(ctx context.Context, in llmtypes.ChatRequest) (string, error) {
	result, err := p.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage returns the concatenated text output from the CLI.
// Usage may be nil because the wrapped CLI is not required to surface it.
func (p *PTYBridge) CompleteWithUsage(ctx context.Context, in llmtypes.ChatRequest) (llmtypes.CompleteResult, error) {
	// Complete is always single-turn — strip any resume session ID.
	ctx = context.WithValue(ctx, ptySessionKeyType{}, "")
	ch, err := p.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
	if err != nil {
		return llmtypes.CompleteResult{}, err
	}

	var sb strings.Builder
	var usage *llmtypes.Usage
	for ev := range ch {
		switch ev.Type {
		case llmtypes.EventDelta:
			sb.WriteString(ev.Content)
		case llmtypes.EventUsage:
			usage = ev.Usage
		case llmtypes.EventError:
			return llmtypes.CompleteResult{}, fmt.Errorf("claude cli error: %s", ev.Error)
		}
	}
	return llmtypes.CompleteResult{Text: sb.String(), Usage: usage}, nil
}

func (p *PTYBridge) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{
		SupportsStreamJSON:          true,
		SupportsPreToolHooks:        false,
		SupportsPostToolHooks:       false,
		SupportsSystemPromptCaching: false, // CLI manages its own caching
		SupportsToolCalling:         true,  // CLI handles tools internally
		SupportsBatch:               false,
		SupportsImageInput:          false, // CLI stdin limitation
		MaxTokens:                   0,     // CLI manages its own limits
	}
}

// streamCLI spawns the Claude CLI in a PTY and streams parsed events.
func (p *PTYBridge) streamCLI(ctx context.Context, systemPrompt string, messages []llmtypes.ChatMessage) (<-chan llmtypes.StreamEvent, error) {
	// Extract the last user message as the prompt.
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

	// Delegate arg construction to the adapter.
	cliSessionID, _ := CLISessionIDFromContext(ctx)
	args := p.adapter.BuildArgs(prompt, systemPrompt, cliSessionID)

	// Avoid logging full CLI arguments to prevent leaking user prompts or other sensitive data.
	log.Printf("pty[%s]: launching CLI with %d args", p.adapter.Name(), len(args))

	cmd := exec.CommandContext(ctx, p.cliPath, args...)
	// On context cancellation, send SIGTERM and let the process flush stream
	// output, then SIGKILL after WaitDelay if it hasn't exited. Replaces the
	// stdlib default of immediate SIGKILL.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = WaitDelayFromContext(ctx)

	// Run in the sandbox directory if one was provided.
	if dir, ok := SandboxDirFromContext(ctx); ok {
		cmd.Dir = dir
	}

	// Start in a PTY.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	// Notify process tracker if one is attached.
	if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
		cb(cmd.Process, true)
	}

	ch := make(chan llmtypes.StreamEvent, 64)
	activityCb, hasActivity := ActivityCallbackFromContext(ctx)
	typedCb, hasTyped := EventsCallbackFromContext(ctx)
	_, hasEventParser := p.adapter.(EventParser)

	var bridgeState *eventsBridgeState
	stopHeartbeat := func() {}
	if hasTyped {
		bridgeState = newEventsBridgeState()
		stopHeartbeat = startHeartbeat(ctx, typedCb, bridgeState, HeartbeatIntervalFromContext(ctx))
	}

	go func() {
		defer close(ch)
		defer ptmx.Close()
		defer stopHeartbeat()

		scanner := bufio.NewScanner(ptmx)
		// Set 1MB buffer for large tool results.
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		var seenDelta, seenToolUse, terminalSent bool

		// emitGuardIfNeeded emits the no-silent-drop error event exactly once,
		// when the CLI produced only tool_use blocks and no text deltas.
		// Returns true if the guard was emitted on this call.
		emitGuardIfNeeded := func() bool {
			if seenToolUse && !seenDelta && !terminalSent && ctx.Err() == nil {
				const msg = "CLI bridge cannot forward tool calls"
				ch <- llmtypes.StreamEvent{Type: llmtypes.EventError, Error: msg}
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

			events, err := p.adapter.ParseLine(line)
			if err != nil {
				log.Printf("pty[%s]: parse error: %v (line: %s)", p.adapter.Name(), err, string(line))
				continue
			}

			if len(events) > 0 && hasActivity && cmd.Process != nil {
				activityCb(cmd.Process.Pid)
			}

			if hasTyped {
				var typed []pevents.Event
				if hasEventParser {
					if t, perr := p.adapter.(EventParser).ParseLineEvents(line); perr == nil {
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
				case llmtypes.EventDelta:
					seenDelta = true
				case llmtypes.EventToolUse:
					seenToolUse = true
				case llmtypes.EventDone:
					// If the adapter produced only tool_use blocks with no
					// deltas, replace its llmtypes.EventDone with a terminal llmtypes.EventError
					// so consumers see the failure. The contract on
					// llmtypes.IsTurnComplete is "exactly one terminal event" — don't
					// forward the adapter's llmtypes.EventDone after the guard fires.
					if emitGuardIfNeeded() {
						continue
					}
					terminalSent = true
				case llmtypes.EventError:
					// Upstream already signalled failure — don't pile on.
					terminalSent = true
				}
				ch <- ev
			}
		}

		// Scanner finished — process has exited or PTY closed.
		if err := scanner.Err(); err != nil {
			// PTY read errors on process exit are expected (EIO).
			if !strings.Contains(err.Error(), "input/output error") {
				log.Printf("pty: scanner error: %v", err)
			}
		}

		// Post-loop guard: stream ended without a terminal event. Still emit
		// the error so callers don't hang on an empty result.
		emitGuardIfNeeded()

		// Wait for process to finish. cmd.Cancel + cmd.WaitDelay handle
		// SIGTERM-then-SIGKILL on context cancellation.
		waitErr := cmd.Wait()

		// Always emit a terminal event so consumers see an explicit boundary
		// before the channel closes. Order of preference:
		//   1. Adapter already emitted llmtypes.EventDone or llmtypes.EventError (terminalSent).
		//   2. Context was cancelled — surface as llmtypes.EventError.
		//   3. Process exited non-zero — surface as llmtypes.EventError with exit info.
		//   4. Clean exit with no adapter terminal — synthesize llmtypes.EventDone.
		switch {
		case terminalSent:
			// Adapter took care of the boundary.
		case ctx.Err() != nil:
			msg := fmt.Sprintf("context cancelled: %v", ctx.Err())
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventError, Error: msg}
			if hasTyped {
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Error{Err: ctx.Err(), Message: msg}})
			}
		case waitErr != nil:
			msg := fmt.Sprintf("process exited: %v", waitErr)
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventError, Error: msg}
			if hasTyped {
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Error{Err: waitErr, Message: msg}})
			}
		default:
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
			if hasTyped {
				emitTyped(ctx, typedCb, bridgeState, []pevents.Event{pevents.Done{}})
			}
		}

		if waitErr != nil && ctx.Err() == nil {
			log.Printf("pty: process exited: %v", waitErr)
		}

		// Notify process tracker that process has exited.
		if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
			cb(cmd.Process, false)
		}
	}()

	return ch, nil
}
