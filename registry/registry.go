package registry

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
)

// Descriptor is everything the libraries know about one agent CLI runtime.
// Values returned by [Lookup] and [All] are copies; editing one does not
// change the registry.
type Descriptor struct {
	// ID is the canonical runtime id.
	ID runtimes.ID `json:"id"`
	// Aliases are other names [Lookup] accepts, for example "claude-code".
	// They are lookup spellings only; nothing reports them back.
	Aliases []string `json:"aliases,omitempty"`

	// Binary is the executable name looked up on PATH.
	Binary string `json:"binary"`
	// EnvOverride names the environment variable that, when set, is the
	// binary's path, used as-is without a PATH search (CLAUDE_CLI_PATH,
	// AGY_CLI_PATH, ...).
	EnvOverride string `json:"env_override"`
	// LookupDirs are directories searched after PATH and the user install
	// directories but before /usr/local/bin, for a runtime whose installer
	// uses its own. A leading "~/" is the user's home directory.
	LookupDirs []string `json:"lookup_dirs,omitempty"`

	// Modes are the modes the runtime can be driven in, each with the
	// capabilities it declares in that mode.
	Modes []ModeSupport `json:"modes"`
	// DefaultMode is the mode a launch uses when the caller names none. It
	// is one of Modes, and native where the runtime has a native mode.
	DefaultMode runtimes.Mode `json:"default_mode"`

	// Posture maps a permission posture onto the runtime's own launch
	// flags. Nil means the runtime has no posture mapping yet; every
	// descriptor's is nil until CW-20260930-0138. The hook's signature is
	// provisional: see [PostureFunc].
	Posture PostureFunc `json:"-"`
}

// ModeSupport is one mode a runtime can be driven in and the capabilities it
// declares in that mode. A capability that is not listed is absent.
type ModeSupport struct {
	Mode         runtimes.Mode         `json:"mode"`
	Capabilities []runtimes.Capability `json:"capabilities,omitempty"`
}

// PostureFunc returns the argv a runtime needs, in mode, to run under
// posture.
//
// The string parameter is a placeholder, not the settled type. Per D-72 the
// posture vocabulary is go-permission's Mode (default, acceptEdits, plan,
// yolo), and this parameter becomes that type when CW-20260930-0138 lands. That
// task may also change what the hook returns: whether a posture is argv,
// config or both is decided there, together with the one argv owner
// (CW-20260930-0135).
type PostureFunc func(posture string, mode runtimes.Mode) ([]string, error)

// Supports reports whether d can be driven in mode m.
func (d Descriptor) Supports(m runtimes.Mode) bool {
	_, ok := d.mode(m)
	return ok
}

// Capabilities returns what d declares in mode m: nil when d does not support
// m or declares nothing there.
func (d Descriptor) Capabilities(m runtimes.Mode) []runtimes.Capability {
	ms, _ := d.mode(m)
	return slices.Clone(ms.Capabilities)
}

// Has reports whether d declares capability c in mode m.
func (d Descriptor) Has(m runtimes.Mode, c runtimes.Capability) bool {
	ms, _ := d.mode(m)
	return slices.Contains(ms.Capabilities, c)
}

// NativeModes returns d's modes that are not ACP, in declared order. A runtime
// with none is ACP-only.
func (d Descriptor) NativeModes() []runtimes.Mode {
	var out []runtimes.Mode
	for _, ms := range d.Modes {
		if !ms.Mode.ACP() {
			out = append(out, ms.Mode)
		}
	}
	return out
}

// Layout returns the rows of package layout's table for d, in table order:
// where d reads its instructions, skills, MCP and native config. It is empty
// for an ACP-only runtime.
func (d Descriptor) Layout() []layout.Entry {
	var out []layout.Entry
	for _, e := range layout.Table() {
		if e.Provider == d.ID {
			out = append(out, e)
		}
	}
	return out
}

// HasLayout reports whether d has a boot-dir layout, which is exactly when it
// has a native mode.
func (d Descriptor) HasLayout() bool { return len(d.Layout()) > 0 }

func (d Descriptor) mode(m runtimes.Mode) (ModeSupport, bool) {
	for _, ms := range d.Modes {
		if ms.Mode == m {
			return ms, true
		}
	}
	return ModeSupport{}, false
}

func (d Descriptor) clone() Descriptor {
	d.Aliases = slices.Clone(d.Aliases)
	d.LookupDirs = slices.Clone(d.LookupDirs)
	modes := make([]ModeSupport, len(d.Modes))
	for i, ms := range d.Modes {
		modes[i] = ModeSupport{Mode: ms.Mode, Capabilities: slices.Clone(ms.Capabilities)}
	}
	d.Modes = modes
	return d
}

var (
	mu      sync.RWMutex
	entries []Descriptor
	byName  = map[string]int{}
)

// Lookup returns the descriptor whose id or alias is name, ignoring case and
// surrounding space.
func Lookup(name string) (Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	i, ok := byName[normalize(name)]
	if !ok {
		return Descriptor{}, false
	}
	return entries[i].clone(), true
}

// All returns every registered descriptor: the built-in runtimes in
// runtimes.IDs order, then any added by [RegisterForTest] in the order they
// were added.
func All() []Descriptor {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Descriptor, len(entries))
	for i, d := range entries {
		out[i] = d.clone()
	}
	return out
}

// register adds a built-in descriptor. It is called only from this package's
// init; a descriptor that fails validation is a programming error.
func register(d Descriptor) {
	if err := validate(d, true); err != nil {
		panic("registry: " + err.Error())
	}
	mu.Lock()
	defer mu.Unlock()
	if err := addLocked(d); err != nil {
		panic("registry: " + err.Error())
	}
}

// TB is the part of *testing.T (and *testing.B) [RegisterForTest] uses.
type TB interface {
	Helper()
	Cleanup(func())
	Fatalf(format string, args ...any)
}

// RegisterForTest adds d to the registry for the rest of the calling test and
// removes it when the test's cleanup runs. It exists so shared fake CLIs
// (CW-20260930-0140) can stand in as a runtime; production code has no way to
// register one.
//
// d's id need not be in the runtimes vocabulary, and d needs no layout rows,
// but neither its id nor any alias may collide with a registered descriptor,
// and its modes and capabilities must be valid. A test that registers must not
// run in parallel with another that enumerates [All] and expects only the
// built-ins.
func RegisterForTest(t TB, d Descriptor) {
	t.Helper()
	if err := validate(d, false); err != nil {
		t.Fatalf("registry.RegisterForTest: %v", err)
		return
	}
	d = d.clone()
	mu.Lock()
	err := addLocked(d)
	mu.Unlock()
	if err != nil {
		t.Fatalf("registry.RegisterForTest: %v", err)
		return
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		removeLocked(d.ID)
	})
}

func normalize(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func names(d Descriptor) []string {
	return append([]string{string(d.ID)}, d.Aliases...)
}

func addLocked(d Descriptor) error {
	for _, n := range names(d) {
		if i, dup := byName[normalize(n)]; dup {
			return fmt.Errorf("%s: name %q is already registered by %s", d.ID, n, entries[i].ID)
		}
	}
	entries = append(entries, d)
	for _, n := range names(d) {
		byName[normalize(n)] = len(entries) - 1
	}
	return nil
}

func removeLocked(id runtimes.ID) {
	i := slices.IndexFunc(entries, func(d Descriptor) bool { return d.ID == id })
	if i < 0 {
		return
	}
	entries = slices.Delete(entries, i, i+1)
	clear(byName)
	for j, d := range entries {
		for _, n := range names(d) {
			byName[normalize(n)] = j
		}
	}
}

// validate checks d's own shape. A built-in must also use a runtimes.ID and
// have layout rows exactly when it has a native mode, and those rows may name
// only modes it supports.
func validate(d Descriptor, builtin bool) error {
	switch {
	case normalize(string(d.ID)) == "" || normalize(string(d.ID)) != string(d.ID):
		return fmt.Errorf("id %q must be non-empty, lowercase and trimmed", d.ID)
	case builtin && !d.ID.Valid():
		return fmt.Errorf("%s: not a runtimes.ID", d.ID)
	case d.Binary == "":
		return fmt.Errorf("%s: no binary", d.ID)
	case d.EnvOverride == "":
		return fmt.Errorf("%s: no env override variable", d.ID)
	case len(d.Modes) == 0:
		return fmt.Errorf("%s: no modes", d.ID)
	}
	for _, a := range d.Aliases {
		if normalize(a) == "" {
			return fmt.Errorf("%s: empty alias", d.ID)
		}
	}
	seen := map[runtimes.Mode]bool{}
	for _, ms := range d.Modes {
		if !ms.Mode.Valid() {
			return fmt.Errorf("%s: mode %q is not a runtimes.Mode", d.ID, ms.Mode)
		}
		if seen[ms.Mode] {
			return fmt.Errorf("%s: mode %s listed twice", d.ID, ms.Mode)
		}
		seen[ms.Mode] = true
		caps := map[runtimes.Capability]bool{}
		for _, c := range ms.Capabilities {
			if !c.Valid() || caps[c] {
				return fmt.Errorf("%s/%s: capability %q is unknown or repeated", d.ID, ms.Mode, c)
			}
			caps[c] = true
		}
	}
	if !seen[d.DefaultMode] {
		return fmt.Errorf("%s: default mode %q is not one of its modes", d.ID, d.DefaultMode)
	}
	native := d.NativeModes()
	if len(native) > 0 && d.DefaultMode.ACP() {
		return fmt.Errorf("%s: default mode %s is ACP but the runtime has a native mode", d.ID, d.DefaultMode)
	}
	if !builtin {
		return nil
	}
	rows := d.Layout()
	if (len(native) > 0) != (len(rows) > 0) {
		return fmt.Errorf("%s: %d native modes but %d layout rows; a runtime has layout rows exactly when it has a native mode", d.ID, len(native), len(rows))
	}
	for _, e := range rows {
		if e.Mode != "" && !slices.Contains(native, e.Mode) {
			return fmt.Errorf("%s: layout row %s/%s names mode %s, which is not a native mode of the runtime", d.ID, e.Shape(), e.Concern, e.Mode)
		}
	}
	return nil
}
