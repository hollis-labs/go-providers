package provider

import (
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/providertest"
	"github.com/hollis-labs/go-providers/registry"
)

type runtimeMode struct {
	id   runtimes.ID
	mode runtimes.Mode
}

// typedEventsEvidence says, for each native mode that declares typed events,
// how the claim is backed. A fixture is a captured session (providertest
// fixture stem) that the mode's own adapter must parse into typed events.
// ownedElsewhere names the session runtime outside this package that produces
// the mode's typed events; for those the mode's adapter must produce none from
// fixture, so the claim is never mistaken for this package's.
var typedEventsEvidence = map[runtimeMode]struct {
	fixture        string
	ownedElsewhere string
}{
	{runtimes.Claude, runtimes.ModeSubprocessPerTurn}:      {fixture: "claude/print_turn1"},
	{runtimes.Claude, runtimes.ModeStreamingStdio}:         {fixture: "claude/stream_two_turns"},
	{runtimes.Codex, runtimes.ModeSubprocessPerTurn}:       {fixture: "codex/exec_turn1"},
	{runtimes.OpenCode, runtimes.ModeSubprocessPerTurn}:    {fixture: "opencode/run_turn1"},
	{runtimes.Antigravity, runtimes.ModeSubprocessPerTurn}: {fixture: "antigravity/print_turn1"},
	// The app-server's JSON-RPC notifications become typed events in
	// agentkit's jsonrpc-stdio session runtime, not in CodexAdapter.
	{runtimes.Codex, runtimes.ModeJSONRPCStdio}: {
		fixture:        "codex/app_server_turn",
		ownedElsewhere: "agentkit's jsonrpc-stdio session runtime",
	},
	// opencode serve streams over HTTP SSE, which agentkit's http-sse
	// runtime reads; OpencodeAdapter.ParseLineEvents returns nil in serve
	// mode.
	{runtimes.OpenCode, runtimes.ModeHTTPSSE}: {
		fixture:        "opencode/run_turn1",
		ownedElsewhere: "agentkit's http-sse session runtime",
	},
}

// fixtureStdout returns the lines a captured session wrote to stdout: a
// replay's stdout lines, or a transcript's sent frames.
func fixtureStdout(t *testing.T, stem string) [][]byte {
	t.Helper()
	var lines [][]byte
	for _, step := range providertest.FixtureSteps(t, stem) {
		switch {
		case step.Stdout != nil:
			lines = append(lines, []byte(*step.Stdout))
		case step.Send != nil:
			lines = append(lines, step.Send)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("fixture %s has no stdout", stem)
	}
	return lines
}

// adapterImplements reports whether a implements the optional interface
// behind capability c. checked is false for a capability with no interface
// here (resume, approvals).
func adapterImplements(a CLIAdapter, c runtimes.Capability) (checked, ok bool) {
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

// TestDeclaredCapabilitiesMatchAdapters checks the capabilities a descriptor
// declares for each native mode against that mode's own adapter,
// NewAdapter(id, mode) (D-47's rule: a declared capability is checked against
// what is implemented). Resume and approvals have no interface here and are
// not checked.
//
// Every mode of a runtime shares one Go type, so an interface check cannot
// tell modes apart. For typed events, the claim the registry makes most often
// per mode, the check is behavioral: the mode's captured session, parsed by
// the mode's adapter, must yield typed events (typedEventsEvidence). The
// classifiers, preflight and resume-keeps-id are methods that do not depend
// on the mode, so the interface check on the mode's adapter is the per-mode
// check; their behavior is pinned by each adapter's own fixture tests.
func TestDeclaredCapabilitiesMatchAdapters(t *testing.T) {
	for _, d := range registry.All() {
		for _, m := range d.NativeModes() {
			a, err := NewAdapter(d.ID, m)
			if err != nil {
				t.Errorf("%s/%s is a native mode with no adapter: %v", d.ID, m, err)
				continue
			}
			for _, c := range d.Capabilities(m) {
				if checked, ok := adapterImplements(a, c); checked && !ok {
					t.Errorf("%s/%s declares %s but %T does not implement it", d.ID, m, c, a)
				}
				if c == runtimes.CapTypedEvents {
					checkTypedEvents(t, runtimeMode{d.ID, m}, a)
				}
			}
		}
	}
	for key := range typedEventsEvidence {
		d, ok := registry.Lookup(string(key.id))
		if !ok || !containsCapability(d.Capabilities(key.mode), runtimes.CapTypedEvents) {
			t.Errorf("typedEventsEvidence lists %s/%s, which does not declare typed events", key.id, key.mode)
		}
	}
}

// checkTypedEvents backs one mode's typed-events claim with evidence.
func checkTypedEvents(t *testing.T, key runtimeMode, a CLIAdapter) {
	t.Helper()
	evidence, ok := typedEventsEvidence[key]
	if !ok {
		t.Errorf("%s/%s declares typed events with no evidence: add its captured fixture, or the runtime that owns them, to typedEventsEvidence", key.id, key.mode)
		return
	}
	parser, ok := a.(EventParser)
	if !ok {
		return // reported as a missing interface
	}
	n := 0
	for _, line := range fixtureStdout(t, evidence.fixture) {
		evs, err := parser.ParseLineEvents(line)
		if err != nil && evidence.ownedElsewhere == "" {
			t.Errorf("%s/%s: %T.ParseLineEvents(%s line): %v", key.id, key.mode, a, evidence.fixture, err)
		}
		n += len(evs)
	}
	switch {
	case evidence.ownedElsewhere == "" && n == 0:
		t.Errorf("%s/%s declares typed events but %T parses none from %s", key.id, key.mode, a, evidence.fixture)
	case evidence.ownedElsewhere != "" && n > 0:
		t.Errorf("%s/%s: %T parses %d typed events from %s, but typedEventsEvidence says %s owns them; back the claim with the fixture instead", key.id, key.mode, a, n, evidence.fixture, evidence.ownedElsewhere)
	}
}

func containsCapability(caps []runtimes.Capability, c runtimes.Capability) bool {
	for _, have := range caps {
		if have == c {
			return true
		}
	}
	return false
}

// TestImplementedCapabilitiesAreDeclared is the reverse check, per runtime: an
// interface a runtime's adapter implements must be declared in at least one of
// its native modes. It stays per runtime because the modes share one type; per
// mode it would flag claude/pty, whose output is a terminal, for the event
// parser the type carries for claude's structured modes.
func TestImplementedCapabilitiesAreDeclared(t *testing.T) {
	for _, d := range registry.All() {
		modes := d.NativeModes()
		if len(modes) == 0 {
			continue
		}
		declared := map[runtimes.Capability]bool{}
		for _, m := range modes {
			for _, c := range d.Capabilities(m) {
				declared[c] = true
			}
		}
		for _, m := range modes {
			a, err := NewAdapter(d.ID, m)
			if err != nil {
				continue // reported by TestDeclaredCapabilitiesMatchAdapters
			}
			for _, c := range runtimes.Capabilities() {
				if checked, ok := adapterImplements(a, c); checked && ok && !declared[c] {
					t.Errorf("%T (%s) implements %s but %s declares it in no native mode", a, m, c, d.ID)
				}
			}
		}
	}
}
