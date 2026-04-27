package provider

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
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
		case "delta":
			sb.WriteString(ev.Content)
		case "usage":
			usage = ev.Usage
		case "error":
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

	if dir, ok := SandboxDirFromContext(ctx); ok {
		cmd.Dir = dir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
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

	go func() {
		defer close(ch)
		defer stdout.Close()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		var seenDelta, seenToolUse, terminalSent bool

		// emitGuardIfNeeded emits the no-silent-drop error event exactly once,
		// when the CLI produced only tool_use blocks and no text deltas.
		// Returns true if the guard was emitted on this call.
		emitGuardIfNeeded := func() bool {
			if seenToolUse && !seenDelta && !terminalSent && ctx.Err() == nil {
				ch <- StreamEvent{Type: "error", Error: "CLI bridge cannot forward tool calls"}
				terminalSent = true
				return true
			}
			return false
		}

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: "error", Error: "context cancelled"}
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				return
			default:
			}

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

			for _, ev := range events {
				switch ev.Type {
				case "delta":
					seenDelta = true
				case "tool_use":
					seenToolUse = true
				case "done":
					// Inject the no-silent-drop guard *before* the terminal
					// done event so consumers that stop reading at "done"
					// still see the error.
					emitGuardIfNeeded()
					terminalSent = true
				case "error":
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

		if err := cmd.Wait(); err != nil {
			if ctx.Err() == nil {
				log.Printf("subprocess[%s]: process exited: %v", s.adapter.Name(), err)
			}
		}

		// Notify process tracker that process has exited.
		if cb, ok := ProcessCallbackFromContext(ctx); ok && cmd.Process != nil {
			cb(cmd.Process, false)
		}
	}()

	return ch, nil
}
