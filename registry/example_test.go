package registry_test

import (
	"fmt"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

func ExampleLookup() {
	d, _ := registry.Lookup("claude-code")
	fmt.Println(d.ID, d.Binary, d.EnvOverride, d.DefaultMode)
	fmt.Println(d.Has(runtimes.ModeStreamingStdio, runtimes.CapResume))
	// Output:
	// claude claude CLAUDE_CLI_PATH streaming-stdio
	// true
}

func ExampleAll() {
	for _, d := range registry.All() {
		fmt.Println(d.ID, d.DefaultMode, d.HasLayout())
	}
	// Output:
	// claude streaming-stdio true
	// codex jsonrpc-stdio true
	// opencode subprocess-per-turn true
	// copilot acp-stdio false
	// pi acp-stdio false
	// antigravity subprocess-per-turn true
}
