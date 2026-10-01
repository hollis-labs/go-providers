package provider

import (
	"errors"
	"fmt"
	"slices"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
	"github.com/hollis-labs/go-providers/registry"
)

// claudeStrictMCPConfigFlag is the Claude flag that keeps a launch to the MCP
// servers it passes with --mcp-config (CW-20261001-0225). It is spelled here
// and nowhere else: a host asks for MCPExclusive.
const claudeStrictMCPConfigFlag = "--strict-mcp-config"

// ErrMCPExclusiveUnsupported is returned by a projection asked for MCP
// exclusivity (ProjectionOptions.MCPExclusive) in a mode with no mechanism:
// one measured to have none (registry.MCPExclusivityAbsent), one not measured
// (registry.MCPExclusivityNone), or one the registry does not know. The error
// says which. Launching anyway would load the user's own MCP servers next to
// the planted ones, past any allow-list the host applies to the planted ones,
// so the request is refused, never ignored.
var ErrMCPExclusiveUnsupported = errors.New("provider: MCP exclusivity is not supported")

// layoutExclusiveEnv names, for a runtime whose exclusivity is its projected
// layout (registry.MCPExclusivityProjectedLayout), the environment variable
// the projection must set to the config root the runtime reads its MCP servers
// from. The claim holds only where the launch really sets it.
var layoutExclusiveEnv = map[runtimes.ID]string{runtimes.Codex: "CODEX_HOME"}

// CheckMCPExclusive reports whether proj really keeps its launch to the MCP
// servers it plants, by the mechanism the registry declares for proj's mode
// (see requireMCPExclusive). The built-in adapters run it themselves when
// ProjectionOptions.MCPExclusive is set. A host that asked for exclusivity
// from an adapter it did not get from here, a custom ProjectionProvider that
// may ignore the option, calls it on the projection it got back: a failure
// wraps ErrMCPExclusiveUnsupported and names the provider and mode.
//
// What it verifies, exactly. A flag mode: the flag literal is in the argv
// ahead of the prompt template and of any literal "--", because after either
// the CLI reads it as prompt text. A projected-layout mode: the convention's
// last delta for the config-root variable (CODEX_HOME) is an EnvSet, with
// EnvProviderWins, of the boot root, so no earlier or later delta in the
// convention changes it, and a caller's own value cannot take its place. It
// checks the convention, not the CLI's parser, and not what a host merges into
// the environment afterward: a host must keep the variable (agentkit checks
// the merged environment).
func CheckMCPExclusive(proj ProviderProjection) error {
	return requireMCPExclusive(proj, ProjectionOptions{MCPExclusive: true})
}

// checkMCPExclusive is requireMCPExclusive behind a variable, so a test can
// hold that every adapter's ProviderProjection runs it. Nothing else assigns it.
var checkMCPExclusive = requireMCPExclusive

// carriesStrictFlag reports whether argv has the strict flag ahead of anything
// that ends the options: the prompt template (it resolves to "--" and the
// prompt) or a literal "--". Behind either, the flag is positional text and
// does nothing.
func carriesStrictFlag(argv []ArgTemplate) bool {
	for _, a := range argv {
		switch {
		case a.Kind == ArgPrompt, a.Kind == ArgPromptInline, a.Kind == ArgLiteral && a.Value == "--":
			return false
		case a.Kind == ArgLiteral && a.Value == claudeStrictMCPConfigFlag:
			return true
		}
	}
	return false
}

// setsBootRoot reports whether the last delta for name sets it, with provider
// precedence, to the boot root. Only the last counts, since each delta applies
// over the ones before it; RootKind(value) == RootBoot also rules out an empty
// value and a path that is not the launch's own.
func setsBootRoot(deltas []EnvDelta, name string) bool {
	last := -1
	for i, d := range deltas {
		if d.Name == name {
			last = i
		}
	}
	if last < 0 {
		return false
	}
	d := deltas[last]
	return d.Operation == EnvSet && d.Precedence == EnvProviderWins && RootKind(d.Value) == RootBoot
}

// requireMCPExclusive refuses a projection that was asked to be exclusive and
// is not. It trusts neither side alone: the registry must declare a mechanism
// for the mode, and the projection's own launch convention must carry it.
func requireMCPExclusive(proj ProviderProjection, opts ProjectionOptions) error {
	if !opts.MCPExclusive {
		return nil
	}
	shape := layout.Shape{Mode: proj.Mode, Variant: proj.Variant}
	how := registry.MCPExclusivityNone
	if d, ok := registry.Lookup(string(proj.Provider)); ok {
		how = d.MCPExclusivity(proj.Mode)
	}
	switch how {
	case registry.MCPExclusivityFlag:
		if !carriesStrictFlag(proj.Launch.Argv) {
			return fmt.Errorf("%w: %s/%s: the registry declares a flag, but the launch convention does not carry %s ahead of the prompt", ErrMCPExclusiveUnsupported, proj.Provider, shape, claudeStrictMCPConfigFlag)
		}
		return nil
	case registry.MCPExclusivityProjectedLayout:
		env := layoutExclusiveEnv[proj.Provider]
		if env == "" || !setsBootRoot(proj.Launch.Env, env) {
			return fmt.Errorf("%w: %s/%s: the registry declares the projected layout, but the launch convention's last %s is not a provider-wins set of the boot root", ErrMCPExclusiveUnsupported, proj.Provider, shape, env)
		}
		return nil
	case registry.MCPExclusivityAbsent:
		return fmt.Errorf("%w: %s/%s was measured and has no switch that limits only its MCP servers (whole-config isolation also drops every other setting, so it is not offered)", ErrMCPExclusiveUnsupported, proj.Provider, shape)
	default:
		return fmt.Errorf("%w: %s/%s %s", ErrMCPExclusiveUnsupported, proj.Provider, shape, unmeasuredReason(proj))
	}
}

// unmeasuredReason says why a mode declares no MCP exclusivity at all: the
// runtime is not in the registry, the mode is not a native one (an ACP mode),
// or it is native and nobody could measure it (Antigravity needs a login).
func unmeasuredReason(proj ProviderProjection) string {
	d, ok := registry.Lookup(string(proj.Provider))
	switch {
	case !ok:
		return "is not a runtime in the registry, so nothing is known about its MCP servers"
	case !slices.Contains(d.NativeModes(), proj.Mode):
		return "is not a native mode, so no MCP exclusivity was measured for it"
	default:
		return "was not measured: no mechanism that keeps a launch to its own MCP servers is declared for it"
	}
}

// requireProjected is what every ProviderProjection ends with: the caller's
// requirements, MCP exclusivity first, then the required features.
func requireProjected(proj ProviderProjection, opts ProjectionOptions) (ProviderProjection, error) {
	if err := checkMCPExclusive(proj, opts); err != nil {
		return ProviderProjection{}, err
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}
