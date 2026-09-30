package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/hollis-labs/go-providers/layout"
)

// The built-in adapters derive every path, flag, environment variable and
// working directory from package layout, the single table of what each agent
// CLI reads. A missing row is a programming error in that table, caught by
// TestLayoutTableCoversBuiltInAdapters, so these helpers panic rather than
// thread an error through every projection.

func layoutProviderOf(id ProviderID) layout.Provider { return layout.Provider(id) }

// layoutEntry returns the table row for (provider, mode, concern).
func layoutEntry(id ProviderID, mode ProviderMode, c layout.Concern) layout.Entry {
	e, ok := layout.Find(layoutProviderOf(id), layout.Mode(mode), c)
	if !ok {
		panic(fmt.Sprintf("provider: layout table has no %s row for %s/%s", c, id, mode))
	}
	return e
}

// layoutEntryOK is layoutEntry for concerns a mode may legitimately lack.
func layoutEntryOK(id ProviderID, mode ProviderMode, c layout.Concern) (layout.Entry, bool) {
	return layout.Find(layoutProviderOf(id), layout.Mode(mode), c)
}

// layoutRel returns the boot-relative path of a file row, substituting the
// agent name for layout.AgentPlaceholder.
func layoutRel(id ProviderID, mode ProviderMode, c layout.Concern, agent string) string {
	e := layoutEntry(id, mode, c)
	if e.Root != layout.RootBoot {
		panic(fmt.Sprintf("provider: layout row %s for %s/%s is relative to %q, not the boot root", c, id, mode, e.Root))
	}
	return strings.ReplaceAll(e.Rel, layout.AgentPlaceholder, agent)
}

// layoutFileArg builds the ArgFile template for a file row that carries a flag.
func layoutFileArg(id ProviderID, mode ProviderMode, c layout.Concern) ArgTemplate {
	e := layoutEntry(id, mode, c)
	return ArgTemplate{Kind: ArgFile, Root: RootKind(e.Root), RelPath: e.Rel, Value: e.Flag, OmitEmpty: true}
}

// layoutProjectDirArg builds the ArgRoot template for the project-dir row,
// or reports false when the mode has none.
func layoutProjectDirArg(id ProviderID, mode ProviderMode) (ArgTemplate, bool) {
	e, ok := layoutEntryOK(id, mode, layout.ProjectDir)
	if !ok {
		return ArgTemplate{}, false
	}
	return ArgTemplate{Kind: ArgRoot, Root: RootKind(e.Root), Value: e.Flag, OmitEmpty: true}, true
}

// layoutLaunchBase returns the working directory, config root and environment
// deltas the provider's launch convention needs. They hang off the native
// config row: that is the file the harness finds through them.
func layoutLaunchBase(id ProviderID, mode ProviderMode) (cwd, configRoot RootKind, env []EnvDelta) {
	e := layoutEntry(id, mode, layout.NativeConfig)
	keys := sortedEnvKeys(e.Env)
	for _, k := range keys {
		env = append(env, EnvDelta{Name: k, Value: e.Env[k], Operation: EnvSet, Precedence: EnvProviderWins})
	}
	return RootKind(e.CWD), RootKind(e.Root), env
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// legacyRootTemplate maps a layout root to the BootDirSpec template variable.
func legacyRootTemplate(r string) string {
	switch layout.Root(r) {
	case layout.RootBoot:
		return "{{.BootDir}}"
	case layout.RootProject:
		return "{{.ProjectDir}}"
	default:
		panic(fmt.Sprintf("provider: layout root %q has no BootDirSpec template variable", r))
	}
}

// layoutLegacyEnv renders the native-config row's environment as BootDirSpec
// EnvAmendments.
func layoutLegacyEnv(id ProviderID, mode ProviderMode) []string {
	e := layoutEntry(id, mode, layout.NativeConfig)
	var out []string
	for _, k := range sortedEnvKeys(e.Env) {
		out = append(out, k+"="+legacyRootTemplate(e.Env[k]))
	}
	return out
}

// layoutLegacyCwd renders the native-config row's working directory as a
// CwdPreference.
func layoutLegacyCwd(id ProviderID, mode ProviderMode) CwdPreference {
	switch e := layoutEntry(id, mode, layout.NativeConfig); e.CWD {
	case layout.RootProject:
		return CwdProjectDir
	default:
		return CwdBootDir
	}
}

// layoutLegacyProjectDirArg renders the project-dir row as a BootDirSpec
// ProjectDirArg ("" when the mode has no such flag).
func layoutLegacyProjectDirArg(id ProviderID, mode ProviderMode) string {
	e, ok := layoutEntryOK(id, mode, layout.ProjectDir)
	if !ok {
		return ""
	}
	return e.Flag + " " + legacyRootTemplate(string(e.Root))
}

// skillRootFor returns the boot-relative prefix skill packages are placed
// under for (provider, mode), and the launch flag that must accompany it
// ("" when the harness scans the root without one).
func skillRootFor(id ProviderID, mode ProviderMode) (prefix, flag string) {
	e := layoutEntry(id, mode, layout.Skills)
	if e.Root != layout.RootBoot || e.Form != layout.FormDir {
		panic(fmt.Sprintf("provider: layout skills row for %s/%s must be boot-relative directory form", id, mode))
	}
	return e.Rel, e.Flag
}

// TreeHash returns the content pin of the package: "sha256:<hex>" over every
// file, sorted by cleaned package-relative path, each framed as
// path NUL decimal-length NUL content NUL. This is the definition go-agentdef
// uses for a skill tree (skills.go hashTree), so a hash computed by either
// module verifies in the other. Files named .DS_Store are skipped, as there.
func (p SkillPackage) TreeHash() (string, error) {
	type entry struct {
		rel     string
		content []byte
	}
	var files []entry
	seen := map[string]bool{}
	for _, f := range p.Files {
		rel, err := cleanPackageRelPath(f.RelPath)
		if err != nil {
			return "", fmt.Errorf("skill package %q: %w", p.Name, err)
		}
		if seen[rel] {
			return "", fmt.Errorf("skill package %q: duplicate file %q", p.Name, rel)
		}
		seen[rel] = true
		if rel == ".DS_Store" || strings.HasSuffix(rel, "/.DS_Store") {
			continue
		}
		files = append(files, entry{rel, f.Content})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.rel))
		h.Write([]byte{0})
		h.Write([]byte(strconv.Itoa(len(f.content))))
		h.Write([]byte{0})
		h.Write(f.content)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// layoutFileMode returns the file permission the table records for a row.
func layoutFileMode(id ProviderID, mode ProviderMode, c layout.Concern) os.FileMode {
	return os.FileMode(layoutEntry(id, mode, c).FileMode)
}
