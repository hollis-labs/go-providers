package layout

import "github.com/hollis-labs/agent-contracts-leaf/runtimes"

// Variant names a runtime-specific launch variant: a flag that changes where
// the harness looks for its files without changing the transport mode. The
// empty Variant is the runtime's ordinary launch.
type Variant string

const (
	// VariantBare is Claude Code's --bare: it skips cwd discovery of
	// CLAUDE.md, settings and skills, so each must be passed explicitly.
	VariantBare Variant = "bare"
)

// Shape is one launch shape of a runtime: the transport mode it is driven in
// and, where the runtime has one, a launch variant. A row applies to a Shape
// when each of its Mode and Variant is empty or equal to the Shape's. The zero
// Shape selects only the rows that hold in every mode.
type Shape struct {
	Mode    runtimes.Mode `json:"mode,omitempty"`
	Variant Variant       `json:"variant,omitempty"`
}

// String renders s as mode or mode+variant, "all" for the zero Shape.
func (s Shape) String() string {
	switch {
	case s.Mode == "" && s.Variant == "":
		return "all"
	case s.Variant == "":
		return string(s.Mode)
	case s.Mode == "":
		return "+" + string(s.Variant)
	default:
		return string(s.Mode) + "+" + string(s.Variant)
	}
}

// Root names the directory an Entry is relative to. The values mirror the
// provider.RootKind strings that are meaningful for placement.
type Root string

const (
	RootBoot    Root = "boot"
	RootProject Root = "project"
	RootConfig  Root = "config"
	RootHome    Root = "home"
	RootState   Root = "state"
)

// Concern says what an Entry is for.
type Concern string

const (
	Instructions Concern = "instructions"
	Boot         Concern = "boot"
	MCP          Concern = "mcp"
	NativeConfig Concern = "native-config"
	Auth         Concern = "auth"
	Agents       Concern = "agents"
	Skills       Concern = "skills"
	ProjectDir   Concern = "project-dir"
	Runtime      Concern = "runtime"
)

// Form is the on-disk shape of a skill.
type Form string

const (
	// FormDir is the Agent Skills layout: <name>/SKILL.md. It is the only
	// form any of the three harnesses reads (probe C1, X1, O1).
	FormDir Form = "dir"
	// FormFlat is <name>.md. No harness reads it; it is never emitted.
	FormFlat Form = "flat"
)

// AgentPlaceholder in a Rel is replaced by the agent name (OpenCode only).
const AgentPlaceholder = "{agent}"

// Entry is one row of the table: where a thing lives, relative to which root,
// and the flag, environment and working directory that make the harness find
// it.
type Entry struct {
	Provider runtimes.ID   `json:"provider"`
	Mode     runtimes.Mode `json:"mode,omitempty"`    // "" = every mode
	Variant  Variant       `json:"variant,omitempty"` // "" = every variant
	Concern  Concern       `json:"concern"`

	Root     Root   `json:"root"`                // relative to which launch root
	Rel      string `json:"rel,omitempty"`       // path under Root; for skills, the directory that holds <name>/SKILL.md
	Form     Form   `json:"form,omitempty"`      // skills only
	FileMode uint32 `json:"file_mode,omitempty"` // 0 = default

	// Flag is the argv flag that carries the path (or the Root itself for
	// skills and project-dir rows). Empty when the harness finds the file by
	// convention alone.
	Flag string `json:"flag,omitempty"`
	// Env are environment variables the harness needs, value = a Root name.
	Env map[string]string `json:"env,omitempty"`
	// CWD is the root the process must be started in for the row to hold.
	CWD Root `json:"cwd,omitempty"`

	// Probe lists Step 0 probe ids (for example "C2") of this provider whose
	// measured result justifies the row.
	Probe []string `json:"probe,omitempty"`
	// Unprobed explains why the row has no probe id: it is a path the
	// package writes but Step 0 does not measure. Exactly one of Probe and
	// Unprobed is set.
	Unprobed string `json:"unprobed,omitempty"`
	// Note is human-readable context rendered into docs/LAYOUT.md.
	Note string `json:"note,omitempty"`
}

// Table returns a copy of the one table, in stable order.
func Table() []Entry {
	out := make([]Entry, len(table))
	for i, e := range table {
		out[i] = e.clone()
	}
	return out
}

// Shape returns the row's own Mode and Variant.
func (e Entry) Shape() Shape { return Shape{Mode: e.Mode, Variant: e.Variant} }

// appliesTo reports whether the row holds for shape s, and how specific the
// match is: one point each for a Mode and a Variant the row pins.
func (e Entry) appliesTo(s Shape) (specificity int, ok bool) {
	if e.Mode != "" {
		if e.Mode != s.Mode {
			return 0, false
		}
		specificity++
	}
	if e.Variant != "" {
		if e.Variant != s.Variant {
			return 0, false
		}
		specificity++
	}
	return specificity, true
}

// For returns the rows that apply to runtime r in shape s: rows whose Mode and
// Variant are each empty or equal to s's. The zero Shape returns only the
// every-mode rows.
func For(r runtimes.ID, s Shape) []Entry {
	var out []Entry
	for _, e := range table {
		if _, ok := e.appliesTo(s); e.Provider == r && ok {
			out = append(out, e.clone())
		}
	}
	return out
}

// Find returns the row for (r, s, c). The most specific applicable row wins:
// one pinning both Mode and Variant beats one pinning either, which beats an
// every-mode row. Among rows of equal specificity the first in Table order
// (the primary) wins.
func Find(r runtimes.ID, s Shape, c Concern) (Entry, bool) {
	var best *Entry
	bestSpec := -1
	for i := range table {
		e := &table[i]
		if e.Provider != r || e.Concern != c {
			continue
		}
		if spec, ok := e.appliesTo(s); ok && spec > bestSpec {
			best, bestSpec = e, spec
		}
	}
	if best == nil {
		return Entry{}, false
	}
	return best.clone(), true
}

// SkillRoot returns the row saying which root, and which relative prefix under
// it, a skill package (<name>/SKILL.md) is placed under for runtime r in shape
// s. Flag, Env and CWD on the row are what the launch must also carry for the
// harness to scan it.
func SkillRoot(r runtimes.ID, s Shape) (Entry, bool) { return Find(r, s, Skills) }

// Runtimes returns the runtimes the table has rows for, in canonical
// (runtimes.IDs) order. A runtime without rows has no boot-dir layout: it is
// launched only over ACP.
func Runtimes() []runtimes.ID {
	have := map[runtimes.ID]bool{}
	for _, e := range table {
		have[e.Provider] = true
	}
	var out []runtimes.ID
	for _, id := range runtimes.IDs() {
		if have[id] {
			out = append(out, id)
		}
	}
	return out
}

func (e Entry) clone() Entry {
	if e.Env != nil {
		env := make(map[string]string, len(e.Env))
		for k, v := range e.Env {
			env[k] = v
		}
		e.Env = env
	}
	e.Probe = append([]string(nil), e.Probe...)
	if len(e.Probe) == 0 {
		e.Probe = nil
	}
	return e
}
