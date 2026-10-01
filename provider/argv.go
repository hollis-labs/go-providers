package provider

import (
	"fmt"
	"slices"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
	"github.com/hollis-labs/go-providers/registry"
)

// Each runtime's argv is built in exactly one place: the convention builders
// below. A ProviderProjection resolves the convention against launch roots
// (the prepared path); an adapter's BuildArgs resolves the same convention
// built from the adapter's own fields (the adapter path). Both go through
// LaunchConvention.ResolveTurn, and they differ only in where path-valued
// arguments come from: the boot-dir layout, or the adapter's fields.

// pathArgs are a convention's path-valued arguments, by role.
type pathArgs struct {
	mcp          []ArgTemplate // --mcp-config <file>
	instructions []ArgTemplate // Claude bare: --append-system-prompt-file <file>
	settings     []ArgTemplate // Claude bare: --settings <file>
	projectDirs  []ArgTemplate // the project directory flag
	skillsDir    []ArgTemplate // Claude bare: --add-dir <skills root>
}

// fieldArg is flag and an adapter field's value as two arguments, or nothing
// when the field is empty. The flag is the layout row's, so both paths name
// the same flag.
func fieldArg(flag, value string) []ArgTemplate {
	if value == "" {
		return nil
	}
	return []ArgTemplate{lit(flag), lit(value)}
}

func lit(v string) ArgTemplate { return ArgTemplate{Kind: ArgLiteral, Value: v} }

func lits(vs ...string) []ArgTemplate {
	out := make([]ArgTemplate, len(vs))
	for i, v := range vs {
		out[i] = lit(v)
	}
	return out
}

var (
	argPrompt = ArgTemplate{Kind: ArgPrompt}
	argExtra  = ArgTemplate{Kind: ArgExtra}
)

// layoutProjectDirs is the layout's project-dir argument for a shape, if it
// has one.
func layoutProjectDirs(id runtimes.ID, shape layout.Shape) []ArgTemplate {
	if dir, ok := layoutProjectDirArg(id, shape); ok {
		return []ArgTemplate{dir}
	}
	return nil
}

// fieldProjectDirs is the project-dir flag of a shape with each of dirs.
func fieldProjectDirs(id runtimes.ID, shape layout.Shape, dirs ...string) []ArgTemplate {
	e, ok := layoutEntryOK(id, shape, layout.ProjectDir)
	if !ok {
		return nil
	}
	var out []ArgTemplate
	for _, d := range dirs {
		out = append(out, fieldArg(e.Flag, d)...)
	}
	return out
}

// executableFor is the runtime's binary from its registry descriptor.
func executableFor(id runtimes.ID) string {
	d, ok := registry.Lookup(string(id))
	if !ok {
		panic(fmt.Sprintf("provider: runtime %s is not in the registry", id))
	}
	return d.Binary
}

func newConvention(id runtimes.ID, shape layout.Shape, args []ArgTemplate) LaunchConvention {
	cwd, configRoot, env := layoutLaunchBase(id, shape)
	return LaunchConvention{
		Executable: executableFor(id),
		Mode:       shape.Mode,
		Variant:    shape.Variant,
		CWD:        cwd,
		ConfigRoot: configRoot,
		Argv:       args,
		Env:        env,
	}
}

// resolveAdapterTurn is every adapter's BuildArgs: its convention resolved
// for one turn, with the adapter's ExtraArgs at the convention's extra slot.
// Built from fields, the convention has no root or file arguments, so
// resolving it cannot fail.
func resolveAdapterTurn(c LaunchConvention, prompt, systemPrompt, sessionID string, extra []string) []string {
	b, err := c.ResolveTurn(ProjectionRoots{}, TurnInput{Prompt: prompt, SystemPrompt: systemPrompt, ResumeID: sessionID}, extra)
	if err != nil {
		panic("provider: resolve adapter argv: " + err.Error())
	}
	return b.Argv
}

// --- Claude ---------------------------------------------------------------

func claudeProjectionShape(a *ClaudeAdapter) layout.Shape {
	switch {
	case a.Bare:
		return shapeBare
	case a.PTY:
		return shapePTY
	case a.InputMode == "stream-json":
		return shapeStreaming
	default:
		return shapePerTurn
	}
}

// claudeLayoutPaths are Claude's path arguments in the boot-dir layout.
func claudeLayoutPaths(shape layout.Shape, withSkills bool) pathArgs {
	const pid = runtimes.Claude
	p := pathArgs{
		mcp:         []ArgTemplate{layoutFileArg(pid, shape, layout.MCP)},
		projectDirs: layoutProjectDirs(pid, shape),
	}
	if shape == shapeBare {
		p.instructions = []ArgTemplate{layoutFileArg(pid, shape, layout.Instructions)}
		p.settings = []ArgTemplate{layoutFileArg(pid, shape, layout.NativeConfig)}
		// --bare reads no cwd skills; the boot root must be an --add-dir for
		// projected skills to be discovered (probe C4, C5). Only added when
		// skills are actually projected, so argv is otherwise unchanged.
		if _, flag := skillRootFor(pid, shape); withSkills && flag != "" {
			p.skillsDir = []ArgTemplate{{Kind: ArgRoot, Root: RootBoot, Value: flag, OmitEmpty: true}}
		}
	}
	return p
}

// fieldPaths are Claude's path arguments from the adapter's fields.
func (a *ClaudeAdapter) fieldPaths(shape layout.Shape) pathArgs {
	const pid = runtimes.Claude
	p := pathArgs{
		mcp:         fieldArg(layoutEntry(pid, shape, layout.MCP).Flag, a.MCPConfigPath),
		projectDirs: fieldProjectDirs(pid, shape, a.ProjectDir),
	}
	if shape == shapeBare {
		p.instructions = fieldArg(layoutEntry(pid, shape, layout.Instructions).Flag, a.AppendSystemPromptFile)
		p.settings = fieldArg(layoutEntry(pid, shape, layout.NativeConfig).Flag, a.SettingsPath)
		_, flag := skillRootFor(pid, shape)
		p.skillsDir = fieldArg(flag, a.SkillsDir)
	}
	return p
}

// claudeConvention is Claude's argv in every shape:
//
//	print     [--resume id] -p --output-format stream-json --verbose
//	          [--input-format m] [--model m] [--mcp-config f]
//	          [--strict-mcp-config] [--system-prompt=s] [extra]
//	          [--add-dir project] [--dangerously-skip-permissions]
//	          -- <prompt>
//	bare      [--resume id] -p --output-format stream-json --verbose
//	          --bare [--model m] [--mcp-config f] [--strict-mcp-config]
//	          [--append-system-prompt-file f] [--settings f] [extra]
//	          [--add-dir project] [--add-dir skills]
//	          [--dangerously-skip-permissions] -- <prompt>
//	streaming [--resume id] -p --input-format stream-json --output-format
//	          stream-json --verbose [--model m] [--mcp-config f]
//	          [--strict-mcp-config] [extra] [--add-dir project]
//	          [--dangerously-skip-permissions]
//	pty       [--resume id] [--model m] [--mcp-config f]
//	          [--strict-mcp-config] [extra] [--add-dir project]
//	          [--dangerously-skip-permissions]
//
// -p is the boolean --print and the prompt is claude's positional argument.
// Turn text is untrusted, so the prompt comes last, after "--": a turn
// starting with '-', or equal to --dangerously-skip-permissions, is text,
// not a flag (CW-20261001-0069). "--" also ends the variadic --add-dir list,
// whose values otherwise run until the next option; extra arguments go
// before the directories so they cannot join that list. The system prompt
// uses the inline --system-prompt=s form so a value starting with '-' stays
// its value. --strict-mcp-config (MCPExclusive) comes right after
// --mcp-config, so it ends that variadic list too: an extra that starts with
// a non-flag token cannot join it as another config file. The system prompt
// is passed on a first turn only: a resumed session already has it. Streaming and PTY take
// no prompt or system prompt in argv; turns arrive on stdin.
func claudeConvention(a *ClaudeAdapter, shape layout.Shape, p pathArgs) LaunchConvention {
	args := []ArgTemplate{{Kind: ArgResume, Value: "--resume"}}
	var model []ArgTemplate
	if a.Model != "" {
		model = lits("--model", a.Model)
	}
	// mcp is the MCP config argument and, for an exclusive launch, the flag
	// that makes it the only MCP config claude reads. The flag is added with
	// nothing planted too: claude then loads no MCP servers.
	mcp := p.mcp
	if a.MCPExclusive {
		mcp = append(slices.Clone(p.mcp), lit(claudeStrictMCPConfigFlag))
	}
	switch shape {
	case shapePTY:
		// The TUI rejects the print-mode flags.
		args = append(args, model...)
		args = append(args, mcp...)
	case shapeStreaming:
		args = append(args, lits("-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")...)
		args = append(args, model...)
		args = append(args, mcp...)
	case shapeBare:
		args = append(args, lits("-p", "--output-format", "stream-json", "--verbose", "--bare")...)
		args = append(args, model...)
		args = append(args, mcp...)
		args = append(args, p.instructions...)
		args = append(args, p.settings...)
	default:
		args = append(args, lits("-p", "--output-format", "stream-json", "--verbose")...)
		if a.InputMode != "" {
			args = append(args, lits("--input-format", a.InputMode)...)
		}
		args = append(args, model...)
		args = append(args, mcp...)
		args = append(args, ArgTemplate{Kind: ArgSystemPrompt, Value: "--system-prompt=", FirstTurnOnly: true})
	}
	args = append(args, argExtra)
	args = append(args, p.projectDirs...)
	args = append(args, p.skillsDir...)
	if a.SkipPermissions {
		args = append(args, lit("--dangerously-skip-permissions"))
	}
	if shape == shapePerTurn || shape == shapeBare {
		args = append(args, argPrompt)
	}
	return newConvention(runtimes.Claude, shape, args)
}

// --- Codex ----------------------------------------------------------------

func codexShape(a *CodexAdapter) layout.Shape {
	if a.Mode == "app-server" {
		return shapeJSONRPC
	}
	return shapePerTurn
}

// codexConvention is Codex's argv:
//
//	exec       exec [-c model="m"] [extra] --json --skip-git-repo-check
//	           [--cd project] [resume <thread>] -- <prompt>
//	app-server app-server [-c model="m"] [extra]
//
// exec runs one turn; a turn that resumes a thread adds the resume
// subcommand and the thread id just before the prompt (CW-20261001-0109).
// Every exec option stays in front of `resume`, where codex-cli 0.159.2
// applies it to the resumed turn: `exec resume` itself takes no --cd or -s,
// and a resumed thread without --cd runs in the process cwd (the boot dir),
// not the cwd it started in. Measured live: with --cd before `resume` the
// resumed turn's shell ran in the project, and a -c sandbox_mode override
// before it took effect. exec's system prompt is the planted AGENTS.md. The
// prompt is untrusted turn text, so it comes last, after "--"
// (CW-20261001-0069): a turn equal to --dangerously-bypass-approvals-and-sandbox
// is text, not a flag. Every option, the extras included, goes before "--";
// --json ends any variadic list among the extras (exec's --image is one). --skip-git-repo-check is
// required because the boot dir codex runs in is a fresh tempdir, never a git
// repo, and codex refuses to run non-interactively outside a trusted or git
// directory ("Not inside a trusted directory and --skip-git-repo-check was not
// specified", codex-cli 0.147.0). It only widens where codex will start; the
// planted config.toml's approval_policy and sandbox_mode still govern what it
// may do. app-server takes its prompt, system prompt and thread over JSON-RPC
// (thread/start, thread/resume, turn/start), not argv. The model is a config
// override because app-server has no --model flag.
func codexConvention(a *CodexAdapter, shape layout.Shape, p pathArgs) LaunchConvention {
	var model []ArgTemplate
	if a.Model != "" {
		model = lits("-c", fmt.Sprintf("model=%q", a.Model))
	}
	if shape == shapeJSONRPC {
		args := append([]ArgTemplate{lit("app-server")}, model...)
		return newConvention(runtimes.Codex, shape, append(args, argExtra))
	}
	args := append([]ArgTemplate{lit("exec")}, model...)
	args = append(args, argExtra, lit("--json"), lit("--skip-git-repo-check"))
	args = append(args, p.projectDirs...)
	args = append(args, ArgTemplate{Kind: ArgResume, Value: "resume"}, argPrompt)
	return newConvention(runtimes.Codex, shape, args)
}

// --- OpenCode -------------------------------------------------------------

func opencodeShape(a *OpencodeAdapter) layout.Shape {
	if a.Mode == "serve-http" {
		return shapeHTTPSSE
	}
	return shapePerTurn
}

// opencodeConvention is OpenCode's argv:
//
//	run   run --format json --agent <agent> [--model m] [--dir project]
//	      [--session id] [extra] -- <prompt>
//	serve serve --port 0 --hostname 127.0.0.1 [extra]
//
// run's message is a trailing variadic positional, after "--" because turn
// text is untrusted (CW-20261001-0069: a turn equal to --auto is text, not
// the flag), so extra arguments go before it or they would join the prompt. --agent stays in argv even when the
// agent is empty, so the shape is uniform; opencode resolves an empty name to
// its default agent. OpenCode has no system-prompt flag; the system prompt
// leads the prompt text. serve takes turns over HTTP.
func opencodeConvention(a *OpencodeAdapter, shape layout.Shape, agent string, p pathArgs) LaunchConvention {
	if shape == shapeHTTPSSE {
		args := []ArgTemplate{lit(layoutEntry(runtimes.OpenCode, shape, layout.Runtime).Flag)}
		args = append(args, lits("--port", "0", "--hostname", "127.0.0.1")...)
		return newConvention(runtimes.OpenCode, shape, append(args, argExtra))
	}
	args := lits("run", "--format", "json", "--agent", agent)
	if a.Model != "" {
		args = append(args, lits("--model", a.Model)...)
	}
	args = append(args, p.projectDirs...)
	args = append(args, ArgTemplate{Kind: ArgResume, Value: "--session"}, argExtra)
	args = append(args, ArgTemplate{Kind: ArgPrompt, WithSystem: true})
	return newConvention(runtimes.OpenCode, shape, args)
}

// --- Antigravity ----------------------------------------------------------

// antigravityConvention is agy's argv:
//
//	--output-format stream-json [--dangerously-skip-permissions | --mode m]
//	[--model m] [--effort e] [--agent a] [extra] [--add-dir dir]...
//	[--conversation id] -p=<prompt>
//
// agy's -p takes a value, and a bare -p followed by another flag swallows that
// flag as the prompt, so the prompt is passed inline and last. Extra arguments
// go before the directories, so a flag always follows an --add-dir value. agy
// has no system-prompt flag; the system prompt leads the prompt text.
func antigravityConvention(a *AntigravityAdapter, p pathArgs) LaunchConvention {
	args := lits("--output-format", "stream-json")
	switch a.Permission {
	case "bypass":
		args = append(args, lit("--dangerously-skip-permissions"))
	case "accept-edits", "plan":
		args = append(args, lits("--mode", a.Permission)...)
	}
	if a.Model != "" {
		args = append(args, lits("--model", a.Model)...)
	}
	if a.Effort != "" {
		args = append(args, lits("--effort", a.Effort)...)
	}
	if a.Agent != "" {
		args = append(args, lits("--agent", a.Agent)...)
	}
	args = append(args, argExtra)
	args = append(args, p.projectDirs...)
	args = append(args, ArgTemplate{Kind: ArgResume, Value: "--conversation"})
	args = append(args, ArgTemplate{Kind: ArgPromptInline, Value: "-p=", WithSystem: true})
	return newConvention(runtimes.Antigravity, shapePerTurn, args)
}
