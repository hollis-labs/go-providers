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
	"time"

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
	// Complete is always single-turn — strip any resume session ID.
	ctx = context.WithValue(ctx, ptySessionKeyType{}, "")
	ch, err := p.streamCLI(ctx, in.EffectiveSystemPrompt(), in.Messages)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "delta":
			sb.WriteString(ev.Content)
		case "error":
			return "", fmt.Errorf("claude cli error: %s", ev.Error)
		}
	}
	return sb.String(), nil
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

		var seenDelta, seenToolUse bool

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: "error", Error: "context cancelled"}
				p.killProcess(cmd)
				return
			default:
			}

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
				case "delta":
					seenDelta = true
				case "tool_use":
					seenToolUse = true
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

		// No-silent-drop guard: if the CLI produced only tool_use blocks and no
		// text deltas, the stream would close silently with no usable content.
		// Emit an explicit error so callers can surface it rather than hang.
		if seenToolUse && !seenDelta && ctx.Err() == nil {
			ch <- StreamEvent{Type: "error", Error: "CLI bridge cannot forward tool calls"}
		}

		// Wait for process to finish.
		if err := cmd.Wait(); err != nil {
			if ctx.Err() == nil {
				// Only log if not a context cancellation.
				log.Printf("pty: process exited: %v", err)
			}
		}

		// Notify process tracker that process has exited.
		if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
			cb(cmd.Process, false)
		}
	}()

	return ch, nil
}

// killProcess sends SIGTERM, then SIGKILL after a grace period.
func (p *PTYBridge) killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}
