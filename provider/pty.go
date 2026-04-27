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

	"github.com/creack/pty"
)

// PTYBridge is a provider that wraps CLI tools in pseudo-terminals.
// It spawns the CLI as a child process, reads its structured output,
// and maps events to Nanite's StreamEvent types.
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

func (p *PTYBridge) StreamChat(ctx context.Context, in ChatRequest) (<-chan StreamEvent, error) {
	return p.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
}

func (p *PTYBridge) Complete(ctx context.Context, in ChatRequest) (string, error) {
	result, err := p.CompleteWithUsage(ctx, in)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// CompleteWithUsage returns the concatenated text output from the CLI.
// Usage may be nil because the wrapped CLI is not required to surface it.
func (p *PTYBridge) CompleteWithUsage(ctx context.Context, in ChatRequest) (CompleteResult, error) {
	// Complete is always single-turn — strip any resume session ID.
	ctx = context.WithValue(ctx, ptySessionKeyType{}, "")
	ch, err := p.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
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
			return CompleteResult{}, fmt.Errorf("claude cli error: %s", ev.Error)
		}
	}
	return CompleteResult{Text: sb.String(), Usage: usage}, nil
}

func (p *PTYBridge) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
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
func (p *PTYBridge) streamCLI(ctx context.Context, systemPrompt string, messages []ChatMessage) (<-chan StreamEvent, error) {
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

	ch := make(chan StreamEvent, 64)
	activityCb, hasActivity := ActivityCallbackFromContext(ctx)

	go func() {
		defer close(ch)
		defer ptmx.Close()

		scanner := bufio.NewScanner(ptmx)
		// Set 1MB buffer for large tool results.
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		var seenDelta, seenToolUse, terminalSent bool

		// emitGuardIfNeeded emits the no-silent-drop error event exactly once,
		// when the CLI produced only tool_use blocks and no text deltas.
		// Returns true if the guard was emitted on this call.
		emitGuardIfNeeded := func() bool {
			if seenToolUse && !seenDelta && !terminalSent && ctx.Err() == nil {
				ch <- StreamEvent{Type: EventError, Error: "CLI bridge cannot forward tool calls"}
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

			for _, ev := range events {
				switch ev.Type {
				case EventDelta:
					seenDelta = true
				case EventToolUse:
					seenToolUse = true
				case EventDone:
					// Inject the no-silent-drop guard *before* the terminal
					// done event so consumers that stop reading at "done"
					// still see the error.
					emitGuardIfNeeded()
					terminalSent = true
				case EventError:
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
		//   1. Adapter already emitted EventDone or EventError (terminalSent).
		//   2. Context was cancelled — surface as EventError.
		//   3. Process exited non-zero — surface as EventError with exit info.
		//   4. Clean exit with no adapter terminal — synthesize EventDone.
		switch {
		case terminalSent:
			// Adapter took care of the boundary.
		case ctx.Err() != nil:
			ch <- StreamEvent{Type: EventError, Error: fmt.Sprintf("context cancelled: %v", ctx.Err())}
		case waitErr != nil:
			ch <- StreamEvent{Type: EventError, Error: fmt.Sprintf("process exited: %v", waitErr)}
		default:
			ch <- StreamEvent{Type: EventDone}
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
