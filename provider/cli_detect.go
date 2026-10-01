package provider

import (
	"fmt"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

// detect resolves runtime id's executable: the adapter's pinned binary when
// set, else through its registry descriptor — the env override
// (CLAUDE_CLI_PATH and so on) as-is, then PATH, then the common install
// directories, which a process supervisor's minimal PATH often leaves out,
// and the runtime's own.
func detect(id runtimes.ID, pinned string) (string, bool) {
	if pinned != "" {
		return pinned, true
	}
	d, ok := registry.Lookup(string(id))
	if !ok {
		panic(fmt.Sprintf("provider: no registry descriptor for built-in runtime %s", id))
	}
	p, err := d.LookPath()
	if err != nil {
		return "", false
	}
	return p, true
}
