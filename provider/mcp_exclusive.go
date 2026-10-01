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
func CheckMCPExclusive(proj ProviderProjection) error {
	return requireMCPExclusive(proj, ProjectionOptions{MCPExclusive: true})
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
		if !slices.ContainsFunc(proj.Launch.Argv, func(a ArgTemplate) bool {
			return a.Kind == ArgLiteral && a.Value == claudeStrictMCPConfigFlag
		}) {
			return fmt.Errorf("%w: %s/%s: the registry declares a flag, but the launch convention does not carry it", ErrMCPExclusiveUnsupported, proj.Provider, shape)
		}
		return nil
	case registry.MCPExclusivityProjectedLayout:
		env := layoutExclusiveEnv[proj.Provider]
		if env == "" || !slices.ContainsFunc(proj.Launch.Env, func(e EnvDelta) bool { return e.Name == env && e.Operation == EnvSet }) {
			return fmt.Errorf("%w: %s/%s: the registry declares the projected layout, but the launch convention does not set its config root", ErrMCPExclusiveUnsupported, proj.Provider, shape)
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
	if err := requireMCPExclusive(proj, opts); err != nil {
		return ProviderProjection{}, err
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}
