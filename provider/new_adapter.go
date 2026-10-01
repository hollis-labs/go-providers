package provider

import (
	"errors"
	"fmt"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// ErrNoAdapter is returned by NewAdapter for a runtime and mode no adapter in
// this package drives natively: an ACP mode, an unknown runtime, or a mode the
// runtime does not have.
var ErrNoAdapter = errors.New("provider: no native adapter for the runtime and mode")

type adapterKey struct {
	id   runtimes.ID
	mode runtimes.Mode
}

// adapterConstructors is the one table of which adapter, in which shape,
// serves each native (runtime, mode). TestNewAdapterCoversTheRegistry holds it
// to the registry's native modes.
var adapterConstructors = map[adapterKey]func() CLIAdapter{
	{runtimes.Claude, runtimes.ModeStreamingStdio}:      func() CLIAdapter { return NewClaudeAdapterStreamingStdio() },
	{runtimes.Claude, runtimes.ModeSubprocessPerTurn}:   func() CLIAdapter { return NewClaudeAdapter() },
	{runtimes.Claude, runtimes.ModePTY}:                 func() CLIAdapter { return NewClaudeAdapterPTY() },
	{runtimes.Codex, runtimes.ModeJSONRPCStdio}:         func() CLIAdapter { return NewCodexAdapterAppServer() },
	{runtimes.Codex, runtimes.ModeSubprocessPerTurn}:    func() CLIAdapter { return NewCodexAdapter() },
	{runtimes.OpenCode, runtimes.ModeSubprocessPerTurn}: func() CLIAdapter { return NewOpencodeAdapter() },
	{runtimes.OpenCode, runtimes.ModeHTTPSSE}:           func() CLIAdapter { return NewOpencodeAdapterServeHTTP() },
	{runtimes.Antigravity, runtimes.ModeSubprocessPerTurn}: func() CLIAdapter {
		return NewAntigravityAdapter()
	},
}

// NewAdapter returns a fresh adapter for runtime id driven natively in mode,
// in that mode's shape (Claude's streaming adapter for streaming-stdio, Codex's
// app-server adapter for jsonrpc-stdio, ...). It is the constructor every host
// shares, so none keeps its own per-runtime switch; a host then sets the
// adapter's fields (Binary, ExtraArgs, posture) rather than wrapping it. An
// ACP mode is ErrNoAdapter: ACP runtimes are driven by an ACP client, not a
// CLIAdapter.
func NewAdapter(id runtimes.ID, mode runtimes.Mode) (CLIAdapter, error) {
	newAdapter, ok := adapterConstructors[adapterKey{id, mode}]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoAdapter, id, mode)
	}
	return newAdapter(), nil
}
