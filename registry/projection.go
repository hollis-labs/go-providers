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
	return &c
}
