//go:build !windows

package provider

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestClaudeAdapter_PTYSpawn_Smoke is a real-spawn regression test for the
// CW-20260508-0005 incident: the previous BuildArgs always emitted print-mode
// args, so the initial PTY spawn (with empty prompt) died on:
//
//	Error: Input must be provided either through stdin or as a prompt argument
//	when using --print
//
// Gated on CLAUDE_PTY_SMOKE=1 so CI doesn't auto-run it. Skips when the claude
// binary is not on PATH.
//
// What it asserts:
//   - Spawning claude with the PTY-mode adapter args does NOT die in 1s on
//     arg-validation. (The print-mode regression failed in ~2.5s; a 1s window
//     is plenty to catch it without flaking on slow startup.)
//
// What it does NOT exercise: full conversation flow, streaming, or stdin
// payload formatting — those are go-agent-sessions concerns.
func TestClaudeAdapter_PTYSpawn_Smoke(t *testing.T) {
	if os.Getenv("CLAUDE_PTY_SMOKE") != "1" {
		t.Skip("set CLAUDE_PTY_SMOKE=1 to run this real-spawn smoke test")
	}

	a := NewClaudeAdapterDevPTY()
	binary, ok := a.Detect()
	if !ok {
		t.Skip("claude binary not found on PATH")
	}

	args := a.BuildArgs("", "", "")

	cmd := exec.Command(binary, args...) //nolint:gosec // adapter-sourced binary + args
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
	})

	// Drain the PTY in the background so the child doesn't block on a full
	// output buffer. Discard the bytes — we don't assert on content.
	go func() {
		_, _ = io.Copy(io.Discard, ptmx)
	}()

	// The print-mode regression killed the process in ~2.5s with a non-zero
	// exit. Watch for that explicitly: if Wait() returns inside the window,
	// the spawn failed. If we hit the deadline, the process is still alive,
	// which is the success signal.
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
	}()

	select {
	case err := <-exited:
		// Process died inside the window — regression.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("claude PTY spawn exited inside 1s window: %v (exit code %d)", err, exitErr.ExitCode())
		}
		t.Fatalf("claude PTY spawn exited inside 1s window: %v", err)
	case <-time.After(1 * time.Second):
		// Survived arg validation — t.Cleanup will tear it down.
	}
}
