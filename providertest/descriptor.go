package providertest

import (
	"sync"
	"testing"
)

// Descriptor is the part of a runtime's facts the fake needs: the binary
// name it stands in for and the variable that overrides the CLI path.
//
// It is keyed on the plain runtime id string until the go-providers
// runtime descriptor registry lands; the fake then reads these facts from
// the registry and RegisterDescriptor goes through its test-only hook.
type Descriptor struct {
	// ID is the canonical runtime id, e.g. "claude".
	ID string
	// Aliases are other names [New] accepts for the runtime.
	Aliases []string
	// Binary is the executable name the fake is installed under.
	Binary string
	// EnvVar overrides the CLI path for the runtime's adapter, e.g.
	// CLAUDE_CLI_PATH. Empty when the runtime has none.
	EnvVar string
}

var builtinDescriptors = []Descriptor{
	{ID: "claude", Aliases: []string{"claude-code"}, Binary: "claude", EnvVar: "CLAUDE_CLI_PATH"},
	{ID: "codex", Binary: "codex", EnvVar: "CODEX_CLI_PATH"},
	{ID: "opencode", Binary: "opencode", EnvVar: "OPENCODE_CLI_PATH"},
	{ID: "antigravity", Aliases: []string{"agy"}, Binary: "agy", EnvVar: "AGY_CLI_PATH"},
	{ID: "copilot", Binary: "copilot", EnvVar: "COPILOT_CLI_PATH"},
	// Pi is reached through the pi-acp bridge (npx -y pi-acp by default).
	{ID: "pi", Aliases: []string{"pi-acp"}, Binary: "pi-acp", EnvVar: "PIACP_CLI_PATH"},
}

var (
	descriptorsMu sync.RWMutex
	registered    = map[string]Descriptor{}
)

// LookupDescriptor returns the descriptor for a runtime id or alias. A
// descriptor registered by [RegisterDescriptor] wins over a built-in one.
func LookupDescriptor(id string) (Descriptor, bool) {
	descriptorsMu.RLock()
	defer descriptorsMu.RUnlock()
	for _, d := range registered {
		if matchesID(d, id) {
			return d, true
		}
	}
	for _, d := range builtinDescriptors {
		if matchesID(d, id) {
			return d, true
		}
	}
	return Descriptor{}, false
}

// RegisterDescriptor makes d available to [New] until t finishes. Use it
// for a runtime the built-in table does not know, or to override one.
func RegisterDescriptor(t testing.TB, d Descriptor) {
	t.Helper()
	if d.ID == "" || d.Binary == "" {
		t.Fatalf("providertest: descriptor needs an ID and a Binary: %+v", d)
	}
	descriptorsMu.Lock()
	prev, had := registered[d.ID]
	registered[d.ID] = d
	descriptorsMu.Unlock()
	t.Cleanup(func() {
		descriptorsMu.Lock()
		defer descriptorsMu.Unlock()
		if had {
			registered[d.ID] = prev
		} else {
			delete(registered, d.ID)
		}
	})
}

func matchesID(d Descriptor, id string) bool {
	if d.ID == id {
		return true
	}
	for _, a := range d.Aliases {
		if a == id {
			return true
		}
	}
	return false
}
