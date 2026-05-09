package provider

import (
	"context"
	"os"
	"time"
)

// ptySessionKeyType is the context key for passing a CLI session ID
// into the PTY bridge for --resume support.
type ptySessionKeyType struct{}

// WithCLISessionID returns a context carrying the given CLI session ID.
// The PTY bridge reads this to decide whether to use --resume.
func WithCLISessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ptySessionKeyType{}, id)
}

// CLISessionIDFromContext extracts the CLI session ID from the context, if set.
func CLISessionIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ptySessionKeyType{}).(string)
	return id, ok && id != ""
}

// sandboxDirKeyType is the context key for passing a sandbox directory
// path into the PTY bridge.
type sandboxDirKeyType struct{}

// WithSandboxDir returns a context carrying the given sandbox directory path.
// The PTY bridge reads this to set cmd.Dir.
func WithSandboxDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, sandboxDirKeyType{}, dir)
}

// SandboxDirFromContext extracts the sandbox directory from the context, if set.
func SandboxDirFromContext(ctx context.Context) (string, bool) {
	dir, ok := ctx.Value(sandboxDirKeyType{}).(string)
	return dir, ok && dir != ""
}

// ProcessCallback is called by PTY/subprocess bridges after spawning a CLI process
// and again when the process exits. This enables external process tracking without
// the provider package importing the chat package.
type ProcessCallback func(proc *os.Process, started bool)

type processCallbackKeyType struct{}

// WithProcessCallback returns a context carrying a process lifecycle callback.
func WithProcessCallback(ctx context.Context, cb ProcessCallback) context.Context {
	return context.WithValue(ctx, processCallbackKeyType{}, cb)
}

// ProcessCallbackFromContext extracts the process callback from the context, if set.
func ProcessCallbackFromContext(ctx context.Context) (ProcessCallback, bool) {
	cb, ok := ctx.Value(processCallbackKeyType{}).(ProcessCallback)
	return cb, ok && cb != nil
}

// ActivityCallback is called by PTY/subprocess bridges when output is received
// from a CLI process. Used by the process tracker to detect hung processes.
type ActivityCallback func(pid int)

type activityCallbackKeyType struct{}

// WithActivityCallback returns a context carrying an activity callback.
func WithActivityCallback(ctx context.Context, cb ActivityCallback) context.Context {
	return context.WithValue(ctx, activityCallbackKeyType{}, cb)
}

// ActivityCallbackFromContext extracts the activity callback from the context, if set.
func ActivityCallbackFromContext(ctx context.Context) (ActivityCallback, bool) {
	cb, ok := ctx.Value(activityCallbackKeyType{}).(ActivityCallback)
	return cb, ok && cb != nil
}

// DefaultWaitDelay is the grace period the spawner gives a child process between
// SIGTERM and SIGKILL when the spawn context is cancelled. Tuned for CLI agents
// that may need a moment to flush stream output before exiting.
const DefaultWaitDelay = 5 * time.Second

type waitDelayKeyType struct{}

// WithWaitDelay returns a context carrying a custom grace period for child
// process termination. When the context is cancelled, the spawner sends SIGTERM
// and waits up to d for the process to exit before sending SIGKILL.
func WithWaitDelay(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, waitDelayKeyType{}, d)
}

// WaitDelayFromContext returns the configured grace period, or DefaultWaitDelay
// if none was set. Always returns a usable duration; never returns zero.
func WaitDelayFromContext(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(waitDelayKeyType{}).(time.Duration); ok && d > 0 {
		return d
	}
	return DefaultWaitDelay
}
