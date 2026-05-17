package provider

import "os"

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
//	        mode := pf.Mode
//	        if mode == 0 {
//	            mode = 0o644
//	        }
//	        os.WriteFile(filepath.Join(bootDir, pf.RelPath), []byte(content), mode)
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
	//
	// Render is normally pure, but adapters MAY perform environment
	// setup keyed on PlantContext.BootDir (e.g. seeding user-global
	// CLI state so the spawned process doesn't prompt on first run).
	// Such side effects MUST be gated on `ctx.BootDir != ""` so callers
	// that invoke Render for content-only purposes (unit tests, dry
	// runs) don't pollute global state. The content return value
	// remains the file payload either way.
	Render func(ctx PlantContext) (string, error)
	// Mode is the file mode for the planted file. Zero value falls back
	// to 0o644. Set to 0o600 (or stricter) for sensitive files like auth
	// tokens or MCP loopback URLs (which contain a localhost port that
	// an attacker with read access could probe). The caller's WriteFile
	// must honor this value (with 0o644 fallback when zero).
	Mode os.FileMode
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
	// BootDir is the absolute path to the per-task tempdir the app is
	// materializing. Apps populate it from their bootdir factory.
	// Empty during pure render-only paths (e.g. unit tests of file
	// content) — adapter Render closures that key environment seeding
	// on the bootdir (see PlantedFile.Render) MUST gate the side effect
	// on `BootDir != ""` so render stays pure when the field is unset.
	BootDir string

	// MuxCommand is the absolute path to a Mux (or other aggregating)
	// stdio MCP binary the spawned agent should also reach, alongside
	// the per-task loopback. When non-empty, the planted MCP config
	// gains a second server entry (named "mux") whose stdio child is
	// `MuxCommand MuxArgs...` with `MuxEnv` (KEY=VALUE pairs) merged
	// onto the spawned process env.
	//
	// Why this exists: the per-task loopback URL only carries the
	// task-restricted clockwork tool surface. Spawned agents need
	// access to the broader portfolio (Vanta memory, cross-task
	// clockwork, cerberus) that the user's interactive shell already
	// reaches via Mux. Loopback stays put (role-aware tool surface);
	// the Mux entry adds the aggregated surface in parallel.
	//
	// Empty MuxCommand → no Mux entry is emitted (back-compat
	// preserved; existing planted config stays byte-identical for
	// callers that don't populate this field).
	MuxCommand string

	// MuxArgs is the argv passed to MuxCommand when the planted MCP
	// config invokes it as a stdio child. Typical shape mirrors the
	// user's interactive Mux entry, e.g.:
	//
	//	["mcp", "--proxy", "--servers", "vanta,clockwork,cerberus",
	//	 "--token", "local-dev",
	//	 "--scopes", "session.write,message.write"]
	//
	// Empty / nil is permitted; the planted entry then carries an
	// empty args array. Apps should populate this from their own
	// bootstrap config so token/scopes line up with operator policy.
	MuxArgs []string

	// MuxEnv carries optional KEY=VALUE env pairs the planted Mux
	// entry should set when claude/opencode spawn the stdio child.
	// Usually empty (the spawned agent inherits the parent process
	// env, which already carries auth tokens, $HOME, etc). Provided
	// for the rare case where Mux needs an isolated env (e.g. a
	// scoped API key that shouldn't leak through to the parent).
	//
	// Each entry is "KEY=VALUE" exactly as the renderer encodes it
	// into the planted config's env map (claude/opencode both expect
	// an object-shaped env). Malformed entries (no "=") are emitted
	// verbatim; the spawned CLI's MCP client decides what to do.
	MuxEnv []string

	// MCPServers carries additional MCP servers to plant beyond the
	// per-task loopback (MCPLoopbackURL) and the mux aggregator (Mux*).
	//
	// Consumed by the codex config.toml renderer. codex has no .mcp.json
	// sidecar — every MCP server it sees must be co-rendered into the
	// single config.toml — so a consumer's own server (e.g. Nanite's
	// `nanite mcp`) cannot be added by writing a separate file; it has to
	// ride here so config.toml stays single-owner. claude and opencode
	// keep their MCP servers in a dedicated .mcp.json / opencode.json a
	// consumer can extend directly, so this field does not affect them.
	//
	// Empty / nil → no extra entries (back-compat: planted config stays
	// byte-identical for callers that don't populate this field).
	MCPServers []MCPServerSpec
}

// MCPServerSpec describes one additional MCP server to plant into a
// provider's MCP configuration (see PlantContext.MCPServers). Exactly one
// transport must be set: HTTPURL for a streamable-HTTP server, or Command
// for a stdio server.
type MCPServerSpec struct {
	// Name is the server's key in the planted config — its
	// [mcp_servers.<Name>] table. Required. Must match [A-Za-z0-9_-]+.
	// The names "loopback" and "mux" are reserved (those entries come
	// from MCPLoopbackURL and the Mux* fields); reusing one is an error.
	Name string

	// HTTPURL is the streamable-HTTP endpoint. Set this XOR Command.
	HTTPURL string

	// Command is the stdio server executable. Set this XOR HTTPURL.
	Command string

	// Args is the stdio server argv, excluding Command. Command only.
	Args []string

	// Env is the stdio server environment as "KEY=VALUE" strings.
	// Command only.
	Env []string
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
