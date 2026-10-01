package registry

import (
	"maps"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
)

// Feature is something a runtime's boot-dir projection can provide, which a
// caller may require before accepting a projection.
type Feature string

const (
	FeatureInstructions Feature = "instructions"
	FeatureNativeConfig Feature = "native-config"
	FeatureMCP          Feature = "mcp"
	FeatureSkillTrees   Feature = "skill-trees"
	FeatureHooks        Feature = "hooks"
	FeatureCommands     Feature = "commands"
	FeatureSubagents    Feature = "subagents"
	FeatureCredential   Feature = "credential"
	FeatureTrust        Feature = "trust"
)

// Features lists every Feature, in declaration order.
func Features() []Feature {
	return []Feature{
		FeatureInstructions, FeatureNativeConfig, FeatureMCP, FeatureSkillTrees,
		FeatureHooks, FeatureCommands, FeatureSubagents, FeatureCredential, FeatureTrust,
	}
}

// Support records whether go-providers projects a feature for a runtime,
// leaves it to explicit runtime preparation, or cannot provide it.
type Support string

const (
	SupportProjected   Support = "projected"
	SupportExplicit    Support = "explicit-effect"
	SupportUnsupported Support = "unsupported"
)

// ProjectionFacts is what go-providers' pure boot-dir projection does for a
// runtime: the harness version its projection contracts were checked
// against, the support of every Feature, and a note per mode.
type ProjectionFacts struct {
	// TestedVersion is the CLI version the projection contracts were
	// checked against.
	TestedVersion string `json:"tested_version"`
	// Features gives every Feature's support.
	Features map[Feature]Support `json:"features"`
	// Notes holds a note per mode; the "" key applies to every mode
	// without its own.
	Notes map[runtimes.Mode]string `json:"notes,omitempty"`
	// MCPExclusive records, per native mode, how a launch keeps the runtime
	// to the MCP servers the launch provides instead of also loading the
	// user's own (CW-20261001-0225). A mode with no entry has no measured
	// mechanism, and a host that needs exclusivity must refuse it. Like a
	// capability, it is declared only where hack/probe-mcp-exclusive.sh
	// measured it: over-claiming is the unsafe direction.
	MCPExclusive map[runtimes.Mode]MCPExclusivity `json:"mcp_exclusive,omitempty"`
}

// MCPExclusivity is how a launch in one mode is kept to its own MCP servers.
type MCPExclusivity string

const (
	// MCPExclusivityNone: no mechanism was measured for the mode. The runtime
	// loads the user's own MCP servers next to the launch's, or it was not
	// possible to tell, so a launch in this mode cannot be made exclusive.
	MCPExclusivityNone MCPExclusivity = ""
	// MCPExclusivityFlag: the adapter's MCPExclusive field adds a CLI flag
	// that stops the runtime loading any MCP server the launch did not pass.
	MCPExclusivityFlag MCPExclusivity = "flag"
	// MCPExclusivityLayout: the planted layout already excludes the user's
	// own servers, because the runtime reads MCP config only from the config
	// root the layout sets (CODEX_HOME). There is nothing to ask for, and it
	// holds only for a launch that sets that root.
	MCPExclusivityLayout MCPExclusivity = "layout"
)

// MCPExclusivity returns how a launch in mode m is kept to its own MCP
// servers: MCPExclusivityNone when no mechanism was measured for m.
func (f ProjectionFacts) MCPExclusivity(m runtimes.Mode) MCPExclusivity {
	return f.MCPExclusive[m]
}

// Note is the note for mode m: its own, or the one for every mode.
func (f ProjectionFacts) Note(m runtimes.Mode) string {
	if n, ok := f.Notes[m]; ok {
		return n
	}
	return f.Notes[""]
}

func (f *ProjectionFacts) clone() *ProjectionFacts {
	if f == nil {
		return nil
	}
	c := *f
	c.Features = maps.Clone(f.Features)
	c.Notes = maps.Clone(f.Notes)
	c.MCPExclusive = maps.Clone(f.MCPExclusive)
	return &c
}
