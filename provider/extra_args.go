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
// extras are flags and their values. An extra that carries its own "--" is a
// whole argv tail and does not belong at the slot; such a caller keeps
// appending it to BuildArgs' output.
type ExtraArgsBuilder interface {
	BuildArgsWithExtras(prompt, systemPrompt, cliSessionID string, extras []string) []string
}

// adapterExtras is an adapter's own ExtraArgs followed by a caller's extras,
// in a new slice so neither is aliased.
func adapterExtras(own, extras []string) []string {
	return slices.Concat(own, extras)
}
