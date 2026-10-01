package registry

import (
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// The rules only a built-in must meet: a runtimes.ID, layout rows exactly when
// there is a native mode, naming only native modes it supports, and projection
// facts exactly when there are layout rows, covering every Feature.
func TestValidateBuiltinRules(t *testing.T) {
	base := func() Descriptor {
		d, _ := Lookup("codex")
		return d
	}
	cases := map[string]struct {
		mutate func(*Descriptor)
		want   string
	}{
		"not a runtimes.ID": {func(d *Descriptor) { d.ID = "gemini" }, "not a runtimes.ID"},
		"ACP-only with layout rows": {func(d *Descriptor) {
			d.Modes = []ModeSupport{{Mode: runtimes.ModeACPStdio}}
			d.DefaultMode = runtimes.ModeACPStdio
		}, "layout rows exactly when"},
		"native without layout rows": {func(d *Descriptor) { d.ID = runtimes.Copilot; d.Aliases = nil }, "layout rows exactly when"},
		"layout row names an unsupported mode": {func(d *Descriptor) {
			d.Modes = []ModeSupport{{Mode: runtimes.ModeJSONRPCStdio}}
			d.DefaultMode = runtimes.ModeJSONRPCStdio
		}, "not a native mode"},
		"layout without projection facts":          {func(d *Descriptor) { d.Projection = nil }, "projection facts must be set exactly"},
		"projection facts without a version":       {func(d *Descriptor) { d.Projection.TestedVersion = "" }, "no tested version"},
		"projection facts missing a feature":       {func(d *Descriptor) { delete(d.Projection.Features, FeatureTrust) }, `feature "trust"`},
		"projection facts with an unknown feature": {func(d *Descriptor) { d.Projection.Features["telepathy"] = SupportProjected }, "outside registry.Features"},
		"MCP exclusivity for a mode the runtime lacks": {func(d *Descriptor) {
			d.Projection.MCPExclusive = map[runtimes.Mode]MCPExclusivity{runtimes.ModeACPStdio: MCPExclusivityFlag}
		}, "MCP exclusivity names mode"},
		"MCP exclusivity with no value": {func(d *Descriptor) {
			d.Projection.MCPExclusive[runtimes.ModeSubprocessPerTurn] = MCPExclusivityNone
		}, "is not flag, projected-layout or absent"},
		"MCP exclusivity with an unknown value": {func(d *Descriptor) {
			d.Projection.MCPExclusive[runtimes.ModeSubprocessPerTurn] = "telepathy"
		}, "is not flag, projected-layout or absent"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := base()
			c.mutate(&d)
			err := validate(d, true)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("validate = %v, want an error containing %q", err, c.want)
			}
		})
	}
	for _, d := range All() {
		if err := validate(d, true); err != nil {
			t.Errorf("built-in %s: %v", d.ID, err)
		}
	}
}

func TestRegisterPanicsOnABadBuiltin(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("register accepted a duplicate built-in")
		}
	}()
	d, _ := Lookup("pi")
	register(d)
}

// Only a flag and a projected layout are mechanisms: a mode measured to have
// none, and one never measured, both leave a launch able to load the user's
// servers.
func TestMCPExclusivityExclusive(t *testing.T) {
	for x, want := range map[MCPExclusivity]bool{
		MCPExclusivityFlag:            true,
		MCPExclusivityProjectedLayout: true,
		MCPExclusivityAbsent:          false,
		MCPExclusivityNone:            false,
		"telepathy":                   false,
	} {
		if got := x.Exclusive(); got != want {
			t.Errorf("%q.Exclusive() = %v, want %v", x, got, want)
		}
	}
}
