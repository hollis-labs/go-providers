package provider

import "slices"

// ExtraArgsBuilder is an optional CLIAdapter extension for a caller that adds
// its own flags to every turn: a session runtime's per-session arguments, for
// instance. BuildArgsWithExtras is BuildArgs with extras placed at the launch
// convention's extra-argument slot, after the adapter's own ExtraArgs, the
// same place LaunchConvention.ResolveTurn puts them on the prepared path.
//
// A caller that splices extras into BuildArgs' output cannot know where a
// convention takes them. Before "--" is right for most, but a codex exec
// resume turn is `exec … resume <id> -- <prompt>`, and codex takes exec
// options only in front of the resume subcommand: -s, --cd and --add-dir
// after `resume <id>` fail to parse (codex-cli 0.159.2, CW-20261001-0197).
// The slot is in front of it.
//
// # What extras may contain
//
// extras are flags and their values, as the CLI takes them in front of its
// prompt. Two limits follow. Neither is checked in code: the method returns
// only an argv, so a check could do no more than panic or silently drop an
// extra, and extras come from a trusted caller. The adapters' ExtraArgs fields
// have the same limits.
//
// No "--". An extra that carries its own "--" is a whole argv tail and does
// not belong at the slot; such a caller keeps appending it to BuildArgs'
// output. Passed here anyway, it lands at the slot, and a CLI reads
// everything after it as positional text, so the flags the convention puts
// behind the slot are not applied:
//
//	codex exec    exec -- tail --json --skip-git-repo-check --cd /p -- hi
//	claude print  ... --mcp-config f -- tail --add-dir /p --dangerously-skip-permissions -- hi
//
// agy, whose prompt is the inline -p=<prompt> with no "--" of its own, loses
// the print flag as well.
//
// The last extra must not be a flag that takes a value. The slot sits
// directly in front of the next argument the convention emits, and a flag
// there takes it as its value. On a Claude print resume turn with no
// ProjectDir, extras ending in --model give
//
//	--resume id -p --output-format stream-json --verbose --mcp-config f --model -- <prompt>
//
// where the CLI may read "--" as the model, and then a prompt that starts with
// "-" is read as a flag (the CW-20261001-0069 class; not checked against the
// binary). The same extras swallow opencode run's "--" and agy's --add-dir.
// An extra that starts with a non-flag token joins the variadic flag in front
// of the slot: on Claude, "--mcp-config f plainword" reads plainword as a
// second config file; with MCPExclusive on, --strict-mcp-config ends that list
// first, so it becomes a stray positional (a PTY initial prompt). Pass a
// value-taking flag with its value.
type ExtraArgsBuilder interface {
	BuildArgsWithExtras(prompt, systemPrompt, cliSessionID string, extras []string) []string
}

// adapterExtras is an adapter's own ExtraArgs followed by a caller's extras,
// in a new slice so neither is aliased.
func adapterExtras(own, extras []string) []string {
	return slices.Concat(own, extras)
}
