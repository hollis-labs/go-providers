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
	// TestedVersion is the CLI version the projection contracts (the layout
	// rows) were checked against. It is not the version MCPExclusive was
	// measured at: hack/probe-mcp-exclusive.sh records that in the V line of
	// provider/testdata/mcp-exclusive, and it is newer.
	TestedVersion string `json:"tested_version"`
	// Features gives every Feature's support.
	Features map[Feature]Support `json:"features"`
	// Notes holds a note per mode; the "" key applies to every mode
	// without its own.
	Notes map[runtimes.Mode]string `json:"notes,omitempty"`
	// MCPExclusive records, per native mode, what was measured about keeping a
	// launch to the MCP servers it provides instead of also loading the
	// user's own (CW-20261001-0225). A mode with no entry was not measured,
	// and a host that needs exclusivity must refuse it. Like a capability, a
	// mechanism is declared only where hack/probe-mcp-exclusive.sh measured it:
	// over-claiming is the unsafe direction.
	MCPExclusive map[runtimes.Mode]MCPExclusivity `json:"mcp_exclusive,omitempty"`
}

// MCPExclusivity is what was measured about keeping a launch in one mode to
// its own MCP servers. Only MCPExclusivityFlag and MCPExclusivityProjectedLayout
// are mechanisms; see Exclusive.
type MCPExclusivity string

const (
	// MCPExclusivityNone: nothing is declared for the mode. No mechanism was
	// measured, or the mode is not one this registry measures at all (an ACP
	// mode, an unknown runtime). A launch in the mode cannot be made exclusive.
	MCPExclusivityNone MCPExclusivity = ""
	// MCPExclusivityAbsent: measured, and the runtime has no switch that
	// limits only its MCP servers. Whole-config isolation exists but also drops
	// every other setting, so it is not offered. A launch in the mode cannot be
	// made exclusive.
	MCPExclusivityAbsent MCPExclusivity = "absent"
	// MCPExclusivityFlag: the adapter's MCPExclusive field (or
	// ProjectionOptions.MCPExclusive) adds a CLI flag that keeps the runtime to
	// the MCP servers the launch passes. Claude's --strict-mcp-config, measured
	// on claude 2.1.286 to leave out the user-level top-level mcpServers of
	// ~/.claude.json and a .mcp.json in the working directory. Not measured,
	// so not claimed: claude.ai account connectors (they need a login), managed
	// and plugin servers. Anthropic's documentation (read 2026-10-01, not
	// measured here) says a deployed managed-mcp.json makes Claude exit at
	// startup when the flag is passed, and that before Claude Code v2.1.246 a
	// strict session still waited on approval for project servers it was not
	// loading.
	MCPExclusivityFlag MCPExclusivity = "flag"
	// MCPExclusivityProjectedLayout: the runtime reads its MCP servers only
	// from the config root the layout sets, so a launch that sets that root
	// loads none of the user's. It holds only where ProviderProjection's launch
	// convention sets the root (Codex: CODEX_HOME=<boot>; CheckMCPExclusive
	// verifies it) and nothing later overrides it. BuildArgs from adapter
	// fields, run in a host's own environment, is not that and is not covered.
	// Measured on codex-cli 0.159.3 for exec, app-server and `mcp list`.
	// Project-level servers stayed out because Codex did not trust the
	// project: the planted config.toml has no trust entry for it. A trusted
	// project's .codex/config.toml was not measured. Codex account connectors
	// and plugins were not measured either.
	MCPExclusivityProjectedLayout MCPExclusivity = "projected-layout"
)

// Exclusive reports whether x is a mechanism that keeps a launch to its own
// MCP servers: MCPExclusivityFlag or MCPExclusivityProjectedLayout. The
// others, MCPExclusivityNone and MCPExclusivityAbsent, are not.
func (x MCPExclusivity) Exclusive() bool {
	return x == MCPExclusivityFlag || x == MCPExclusivityProjectedLayout
}

// MCPExclusivity returns what was measured about keeping a launch in mode m to
// its own MCP servers: MCPExclusivityNone when the mode was not measured.
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
