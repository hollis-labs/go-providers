package layout

import "sort"

// Provider names an agent CLI family.
type Provider string

const (
	Claude      Provider = "claude"
	Codex       Provider = "codex"
	OpenCode    Provider = "opencode"
	Antigravity Provider = "antigravity"
)

// Mode is the string value of a provider.ProviderMode. The empty Mode means
// "every mode of the provider".
type Mode string

// Modes mirror the provider.ProviderMode constants.
const (
	ModeClaudePrint          Mode = "claude-print"
	ModeClaudeBare           Mode = "claude-bare"
	ModeClaudePTY            Mode = "claude-pty"
	ModeClaudeStreamingStdio Mode = "claude-streaming-stdio"
	ModeCodexExec            Mode = "codex-exec"
	ModeCodexAppServer       Mode = "codex-app-server"
	ModeOpenCodeRun          Mode = "opencode-run"
	ModeOpenCodeServeHTTP    Mode = "opencode-serve-http"
	ModeAntigravityPrint     Mode = "antigravity-print"
)

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
	Provider Provider `json:"provider"`
	Mode     Mode     `json:"mode,omitempty"` // "" = every mode
	Concern  Concern  `json:"concern"`

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

	// Aliases are other spellings of the same thing (informational).
	Aliases []string `json:"aliases,omitempty"`
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

// For returns the rows that apply to provider p in mode m: rows for the exact
// mode and rows with an empty Mode. An empty m returns only the every-mode
// rows plus nothing mode-specific.
func For(p Provider, m Mode) []Entry {
	var out []Entry
	for _, e := range table {
		if e.Provider == p && (e.Mode == "" || e.Mode == m) {
			out = append(out, e.clone())
		}
	}
	return out
}

// Find returns the row for (p, m, c). A row for the exact mode wins over an
// every-mode row; among rows of equal specificity the first in Table order
// (the primary) wins.
func Find(p Provider, m Mode, c Concern) (Entry, bool) {
	var fallback *Entry
	for i := range table {
		e := &table[i]
		if e.Provider != p || e.Concern != c {
			continue
		}
		if e.Mode == m && m != "" {
			return e.clone(), true
		}
		if e.Mode == "" && fallback == nil {
			fallback = e
		}
	}
	if fallback != nil {
		return fallback.clone(), true
	}
	return Entry{}, false
}

// SkillRoot returns the row saying which root, and which relative prefix under
// it, a skill package (<name>/SKILL.md) is placed under for provider p in mode
// m. Flag, Env and CWD on the row are what the launch must also carry for the
// harness to scan it.
func SkillRoot(p Provider, m Mode) (Entry, bool) { return Find(p, m, Skills) }

// Providers returns the providers in the table, sorted.
func Providers() []Provider {
	seen := map[Provider]bool{}
	var out []Provider
	for _, e := range table {
		if !seen[e.Provider] {
			seen[e.Provider] = true
			out = append(out, e.Provider)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
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
	e.Aliases = append([]string(nil), e.Aliases...)
	e.Probe = append([]string(nil), e.Probe...)
	if len(e.Aliases) == 0 {
		e.Aliases = nil
	}
	if len(e.Probe) == 0 {
		e.Probe = nil
	}
	return e
}
