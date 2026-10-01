package provider

import (
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/registry"
)

// The capabilities a descriptor declares for its native modes are a claim
// about this package's adapters; a declaration must be backed by the optional
// interface that implements it, and an implemented interface must be declared
// in at least one native mode (D-47's rule: a declared capability is checked
// against what is implemented). Resume and approvals have no interface here
// and are not checked.
func TestDeclaredCapabilitiesMatchAdapters(t *testing.T) {
	adapters := map[runtimes.ID]CLIAdapter{
		runtimes.Claude:      NewClaudeAdapter(),
		runtimes.Codex:       NewCodexAdapter(),
		runtimes.OpenCode:    NewOpencodeAdapter(),
		runtimes.Antigravity: NewAntigravityAdapter(),
	}
	implements := func(a CLIAdapter, c runtimes.Capability) (checked, ok bool) {
		switch c {
		case runtimes.CapTypedEvents:
			_, ok = a.(EventParser)
		case runtimes.CapSessionLostClassifier:
			_, ok = a.(SessionLostClassifier)
		case runtimes.CapAuthClassifier:
			_, ok = a.(AuthFailureClassifier)
		case runtimes.CapPreflight:
			_, ok = a.(Preflighter)
		case runtimes.CapResumeKeepsID:
			v, is := a.(SessionResumeVerifier)
			ok = is && v.ResumeKeepsSessionID()
		default:
			return false, false
		}
		return true, ok
	}
	for _, d := range registry.All() {
		if len(d.NativeModes()) == 0 {
			continue
		}
		a, ok := adapters[d.ID]
		if !ok {
			t.Errorf("%s has native modes but no adapter in this package", d.ID)
			continue
		}
		declared := map[runtimes.Capability]bool{}
		for _, m := range d.NativeModes() {
			for _, c := range d.Capabilities(m) {
				declared[c] = true
				if checked, ok := implements(a, c); checked && !ok {
					t.Errorf("%s/%s declares %s but %T does not implement it", d.ID, m, c, a)
				}
			}
		}
		for _, c := range runtimes.Capabilities() {
			if checked, ok := implements(a, c); checked && ok && !declared[c] {
				t.Errorf("%T implements %s but %s declares it in no native mode", a, c, d.ID)
			}
		}
	}
}
