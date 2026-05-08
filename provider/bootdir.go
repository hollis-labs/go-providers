package provider

// BootDirSpec declares the per-task tempdir layout convention for an
// adapter. The lib owns the spec; apps materialize the directory
// (write files, set env, choose cwd) by iterating over it.
//
// Apps invoke this via the BootDirProvider optional interface:
//
//	if bp, ok := adapter.(BootDirProvider); ok {
//	    spec := bp.BootDirSpec()
//	    for _, pf := range spec.PlantedFiles {
//	        content, _ := pf.Render(plantCtx)
//	        os.WriteFile(filepath.Join(bootDir, pf.RelPath), []byte(content), 0o644)
//	    }
//	    env := append(env, spec.EnvAmendments...)
//	    cwd := spec.SpawnWorkdir(bootDir, projectDir)
//	}
//
// EnvAmendments and ProjectDirArg may contain {{.BootDir}} and
// {{.ProjectDir}} placeholders; apps perform the substitution at spawn
// time. The lib does not perform IO.
type BootDirSpec struct {
	// PlantedFiles is a list of files the app should write into the
	// boot dir before spawning. Filenames are relative to the boot
	// dir root; nested paths (e.g. ".claude/settings.json") are
	// allowed and the app must create intermediate directories.
	PlantedFiles []PlantedFile

	// EnvAmendments are KEY=VALUE pairs the app should append to the
	// spawned process env. Templates support {{.BootDir}} and
	// {{.ProjectDir}} substitution at app-side for paths that depend
	// on the runtime locations (e.g. OPENCODE_CONFIG_DIR={{.BootDir}}).
	EnvAmendments []string

	// CwdPreference declares where the spawned process should be
	// invoked. CwdBootDir means cwd = bootDir (claude, codex);
	// CwdProjectDir means cwd = projectDir, with the boot dir
	// referenced via env (opencode).
	CwdPreference CwdPreference

	// ProjectDirArg is the spawn-args flag pattern for granting
	// project access. Empty string when no flag is needed (e.g.
	// opencode uses cwd; codex uses --dir or similar; claude uses
	// --add-dir). Templates support {{.ProjectDir}} substitution.
	// Example: "--add-dir {{.ProjectDir}}".
	ProjectDirArg string

	// Notes carries free-form documentation about TBD items, probes
	// required, or per-adapter caveats. Empty for adapters with a
	// fully-locked spec.
	Notes string
}

// PlantedFile describes one file the app writes into the boot dir.
type PlantedFile struct {
	// RelPath is the path relative to the boot dir root. May contain
	// directory separators (the app must create intermediate dirs).
	RelPath string
	// Render produces the file content for the given PlantContext.
	// Returning an error aborts boot dir setup (the app should fail
	// the spawn). A nil Render is permitted — apps may skip the file
	// or render their own content.
	Render func(ctx PlantContext) (string, error)
}

// PlantContext carries the values render functions commonly need.
// Apps populate it from their own boot configuration; not every
// field is meaningful for every adapter.
type PlantContext struct {
	// SystemPrompt is the agent's identity / role / invariants.
	SystemPrompt string
	// BootContent is the task-specific kickoff content (the body of
	// boot.md, referenced via the kickoff message "Boot @./boot.md").
	BootContent string
	// AgentName is the adapter-specific agent profile name. Used by
	// opencode for its agents.json and opencode.json layouts.
	AgentName string
	// MCPLoopbackURL is the MCP server URL the agent should connect
	// to for tool brokering. Empty if no MCP server is provisioned.
	MCPLoopbackURL string
	// ProjectDir is the absolute path to the project the agent
	// should operate against. Empty when no project access is granted.
	ProjectDir string
}

// CwdPreference declares the spawned process's working directory.
type CwdPreference int

const (
	// CwdBootDir means the process is invoked with cwd = boot dir.
	// The boot dir is also where adapter-specific config files
	// (CLAUDE.md, AGENTS.md) live so the CLI auto-loads them.
	CwdBootDir CwdPreference = iota
	// CwdProjectDir means the process is invoked with cwd = project
	// dir; the boot dir is referenced via env vars or flags.
	CwdProjectDir
)

// SpawnWorkdir resolves the cwd path for a spawn given a populated
// PlantContext. Convenience helper for the app-side spawn loop.
func (s BootDirSpec) SpawnWorkdir(bootDir, projectDir string) string {
	switch s.CwdPreference {
	case CwdProjectDir:
		if projectDir != "" {
			return projectDir
		}
		return bootDir
	default:
		return bootDir
	}
}

// BootDirProvider is the optional CLIAdapter extension that exposes
// the boot dir layout convention. Apps type-assert at boot time:
//
//	if bp, ok := adapter.(BootDirProvider); ok {
//	    spec := bp.BootDirSpec()
//	    ...
//	}
//
// Adapters that have not yet probed their CLI's auto-load convention
// return a zero-value BootDirSpec with a non-empty Notes field
// describing what needs to be verified.
type BootDirProvider interface {
	BootDirSpec() BootDirSpec
}
