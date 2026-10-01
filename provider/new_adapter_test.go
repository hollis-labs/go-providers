package provider

import (
	"errors"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

// Every native mode the registry lists has a constructor, the adapter it
// builds names the runtime and projects that mode, and nothing else has one.
func TestNewAdapterCoversTheRegistry(t *testing.T) {
	native := 0
	for _, d := range registry.All() {
		for _, ms := range d.Modes {
			a, err := NewAdapter(d.ID, ms.Mode)
			if ms.Mode.ACP() {
				if !errors.Is(err, ErrNoAdapter) {
					t.Errorf("%s/%s: err = %v, want ErrNoAdapter for an ACP mode", d.ID, ms.Mode, err)
				}
				continue
			}
			native++
			if err != nil {
				t.Fatalf("%s/%s: %v", d.ID, ms.Mode, err)
			}
			if r, ok := registry.Lookup(a.Name()); !ok || r.ID != d.ID {
				t.Errorf("%s/%s: adapter named %q", d.ID, ms.Mode, a.Name())
			}
			proj, err := a.(ProjectionProvider).ProviderProjection(PlantContext{AgentName: "a"}, ProjectionOptions{})
			if err != nil {
				t.Fatalf("%s/%s: projection: %v", d.ID, ms.Mode, err)
			}
			if proj.Mode != ms.Mode || proj.Variant != "" {
				t.Errorf("%s/%s: adapter projects %s+%q", d.ID, ms.Mode, proj.Mode, proj.Variant)
			}
		}
	}
	if native != len(adapterConstructors) {
		t.Errorf("%d native registry modes, %d constructors", native, len(adapterConstructors))
	}
	if _, err := NewAdapter("gemini", runtimes.ModeSubprocessPerTurn); !errors.Is(err, ErrNoAdapter) {
		t.Errorf("unknown runtime: %v", err)
	}
}

// Each call is a fresh adapter: a host's field edits never leak.
func TestNewAdapterIsFresh(t *testing.T) {
	a, _ := NewAdapter(runtimes.Codex, runtimes.ModeJSONRPCStdio)
	a.(*CodexAdapter).Binary = "/mutated"
	b, _ := NewAdapter(runtimes.Codex, runtimes.ModeJSONRPCStdio)
	if b.(*CodexAdapter).Binary != "" {
		t.Fatal("NewAdapter returned a shared adapter")
	}
}
