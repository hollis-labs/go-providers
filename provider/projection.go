package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"
	"github.com/hollis-labs/go-providers/registry"
)

// The launch shapes the built-in adapters project. Claude print, Codex exec,
// OpenCode run and Antigravity print are all subprocess-per-turn; Claude's
// --bare is a launch variant of it, not a mode of its own.
var (
	shapePerTurn   = layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}
	shapeBare      = layout.Shape{Mode: runtimes.ModeSubprocessPerTurn, Variant: layout.VariantBare}
	shapePTY       = layout.Shape{Mode: runtimes.ModePTY}
	shapeStreaming = layout.Shape{Mode: runtimes.ModeStreamingStdio}
	shapeJSONRPC   = layout.Shape{Mode: runtimes.ModeJSONRPCStdio}
	shapeHTTPSSE   = layout.Shape{Mode: runtimes.ModeHTTPSSE}
)

// ProviderCapabilityRow is the exported capability matrix for provider
// projection. TestedVersion is the executable version used for the M06
// contract fixtures in this worktree.
type ProviderCapabilityRow struct {
	Provider      runtimes.ID       `json:"provider"`
	Mode          runtimes.Mode     `json:"mode"`
	Variant       layout.Variant    `json:"variant,omitempty"`
	TestedVersion string            `json:"tested_version"`
	Features      map[string]string `json:"features"`
	Notes         string            `json:"notes,omitempty"`
}

// Shape returns the row's Mode and Variant.
func (r ProviderCapabilityRow) Shape() layout.Shape {
	return layout.Shape{Mode: r.Mode, Variant: r.Variant}
}

// ProviderCapabilityMatrix returns a deterministic provider/version matrix for
// the pure projection contracts in this package. Its rows come from the
// registry: one per native mode of every runtime with a layout, each followed
// by a row for every launch variant its layout rows name in that mode (Claude's
// bare), carrying the descriptor's projection facts. ACP-only runtimes have no
// row: nothing is projected for them.
func ProviderCapabilityMatrix() []ProviderCapabilityRow {
	var rows []ProviderCapabilityRow
	for _, d := range registry.All() {
		facts := d.Projection
		if facts == nil {
			continue
		}
		for _, shape := range projectionShapes(d) {
			rows = append(rows, ProviderCapabilityRow{
				Provider:      d.ID,
				Mode:          shape.Mode,
				Variant:       shape.Variant,
				TestedVersion: facts.TestedVersion,
				Features:      featureMap(facts.Features),
				Notes:         facts.Note(shape.Mode),
			})
		}
	}
	return rows
}

// projectionShapes lists d's native modes, each followed by the variants its
// layout rows pin in that mode.
func projectionShapes(d registry.Descriptor) []layout.Shape {
	var out []layout.Shape
	for _, m := range d.NativeModes() {
		out = append(out, layout.Shape{Mode: m})
		seen := map[layout.Variant]bool{}
		for _, e := range d.Layout() {
			if e.Mode == m && e.Variant != "" && !seen[e.Variant] {
				seen[e.Variant] = true
				out = append(out, e.Shape())
			}
		}
	}
	return out
}

func featureMap(in map[registry.Feature]registry.Support) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[string(k)] = string(v)
	}
	return out
}

// ProjectionOptions carries provider-neutral content and caller requirements
// for a pure provider projection.
type ProjectionOptions struct {
	Version          string
	Skills           []SkillPackage
	RequiredFeatures []registry.Feature
}

// ProjectionProvider is implemented by adapters that can render provider-owned
// pure projection values.
type ProjectionProvider interface {
	ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error)
}

// ProjectionRoots keeps launch roots distinct from cwd. Empty roots are allowed
// during dry-run rendering; ResolveLaunch only requires the roots it needs.
type ProjectionRoots struct {
	ProjectRoot string
	BootRoot    string
	ConfigRoot  string
	StateRoot   string
	ScratchRoot string
	CWD         string
}

// SkillPackage is a provider-neutral, already-resolved skill tree. The
// provider projection chooses its native destination prefix.
type SkillPackage struct {
	Name  string
	Files []SkillFile
	// Hash optionally pins the package content as "sha256:<hex>" (see
	// TreeHash). When set, projection fails if the files do not hash to it.
	Hash string
}

// SkillFile is one file inside a SkillPackage. RelPath is package-relative.
type SkillFile struct {
	RelPath string
	Content []byte
	Mode    os.FileMode
}

// ProjectedFile is a pure provider-owned file description. Agentkit can
// translate this value into its neutral artifact type without go-providers
// importing agentkit.
type ProjectedFile struct {
	RelPath string      `json:"rel_path"`
	Content []byte      `json:"content,omitempty"`
	Mode    os.FileMode `json:"mode,omitempty"`
	Role    string      `json:"role,omitempty"`
}

// RootKind identifies a launch root used by argv/env/cwd conventions.
type RootKind string

const (
	RootBoot    RootKind = "boot"
	RootProject RootKind = "project"
	RootConfig  RootKind = "config"
	RootState   RootKind = "state"
	RootScratch RootKind = "scratch"
	RootCWD     RootKind = "cwd"
)

// ArgKind describes how an argv entry is resolved.
type ArgKind string

const (
	// ArgLiteral is Value as one argument.
	ArgLiteral ArgKind = "literal"
	// ArgPrompt is the turn's prompt as one argument.
	ArgPrompt ArgKind = "prompt"
	// ArgRoot is a launch root, after Value when Value names a flag.
	ArgRoot ArgKind = "root"
	// ArgFile is a file under a launch root, after Value when Value names a
	// flag.
	ArgFile ArgKind = "file"
	// ArgPromptInline is the turn's prompt joined to Value in one argument,
	// for a flag whose value must not stand apart (agy's -p=<prompt>).
	ArgPromptInline ArgKind = "prompt-inline"
	// ArgSystemPrompt is Value followed by the turn's system prompt. It is
	// omitted when the turn has none.
	ArgSystemPrompt ArgKind = "system-prompt"
	// ArgResume is Value followed by the id of the session the turn
	// resumes. It is omitted when the turn resumes nothing.
	ArgResume ArgKind = "resume"
	// ArgExtra is where a caller's extra arguments go. Each convention puts
	// it where an extra argument can neither swallow the prompt nor be read
	// as one of a variadic flag's values.
	ArgExtra ArgKind = "extra"
)

// ArgTemplate stores argv as structured values so paths with spaces or unicode
// are never parsed from a shell string.
type ArgTemplate struct {
	Kind      ArgKind  `json:"kind"`
	Value     string   `json:"value,omitempty"`
	Root      RootKind `json:"root,omitempty"`
	RelPath   string   `json:"rel_path,omitempty"`
	OmitEmpty bool     `json:"omit_empty,omitempty"`
	// WithSystem, on ArgPrompt and ArgPromptInline, puts the turn's system
	// prompt in front of the prompt, for a CLI with no system-prompt flag.
	WithSystem bool `json:"with_system,omitempty"`
	// FirstTurnOnly omits the argument on a turn that resumes a session.
	FirstTurnOnly bool `json:"first_turn_only,omitempty"`
}

// TurnInput is what one turn adds to a launch convention's argv.
type TurnInput struct {
	// Prompt is the turn's prompt.
	Prompt string
	// SystemPrompt is passed where the convention has a place for it.
	SystemPrompt string
	// ResumeID is the CLI session the turn resumes; empty starts a new one.
	ResumeID string
}

// EnvOperation describes how an environment delta is applied.
type EnvOperation string

const (
	EnvSet     EnvOperation = "set"
	EnvPrepend EnvOperation = "prepend"
	EnvAppend  EnvOperation = "append"
	EnvUnset   EnvOperation = "unset"
)

// EnvPrecedence describes whether the provider projection or caller should win
// when both mention the same key.
type EnvPrecedence string

const (
	EnvProviderWins EnvPrecedence = "provider-wins"
	EnvCallerWins   EnvPrecedence = "caller-wins"
)

// EnvDelta is a structured environment amendment.
type EnvDelta struct {
	Name       string        `json:"name"`
	Value      string        `json:"value,omitempty"`
	Operation  EnvOperation  `json:"operation"`
	Precedence EnvPrecedence `json:"precedence"`
	Separator  string        `json:"separator,omitempty"`
}

// LaunchConvention is the provider's spawn contract as values.
type LaunchConvention struct {
	Executable string         `json:"executable"`
	Mode       runtimes.Mode  `json:"mode"`
	Variant    layout.Variant `json:"variant,omitempty"`
	CWD        RootKind       `json:"cwd"`
	ConfigRoot RootKind       `json:"config_root,omitempty"`
	Argv       []ArgTemplate  `json:"argv"`
	Env        []EnvDelta     `json:"env"`
}

// LaunchBinding is a resolved spawn contract. It does not start a process.
type LaunchBinding struct {
	CWD       string     `json:"cwd"`
	ConfigDir string     `json:"config_dir,omitempty"`
	Argv      []string   `json:"argv"`
	Env       []EnvDelta `json:"env"`
}

// ProviderEffectKind names runtime preparation that pure projection
// intentionally does not perform.
type ProviderEffectKind string

const (
	EffectClaudeCredentialHelper ProviderEffectKind = "claude-credential-helper"
	EffectClaudeWorkspaceTrust   ProviderEffectKind = "claude-workspace-trust"
	EffectCodexAuthJSON          ProviderEffectKind = "codex-auth-json"
	EffectOpencodeProviderAuth   ProviderEffectKind = "opencode-provider-auth"
	EffectAntigravityAuth        ProviderEffectKind = "antigravity-auth"
)

// EffectClass says what a ProviderEffect touches when it is carried out.
type EffectClass string

const (
	// EffectClassCredential writes or exposes a credential.
	EffectClassCredential EffectClass = "credential"
	// EffectClassHostConfig changes host state outside the boot dir, such
	// as Claude's workspace-trust record.
	EffectClassHostConfig EffectClass = "host-config"
)

// Class reports what an effect of kind k touches. An unknown kind is a
// credential: the class that is handled most carefully.
func (k ProviderEffectKind) Class() EffectClass {
	switch k {
	case EffectClaudeWorkspaceTrust:
		return EffectClassHostConfig
	default:
		return EffectClassCredential
	}
}

// Secret reports whether an effect of kind k puts secret bytes at its
// destination, so a host must redact them. Claude's credential helper
// serializes only the helper's path, and agy's credentials are never
// projected. An unknown kind is secret.
func (k ProviderEffectKind) Secret() bool {
	switch k {
	case EffectClaudeCredentialHelper, EffectClaudeWorkspaceTrust, EffectAntigravityAuth:
		return false
	default:
		return true
	}
}

// ProviderEffect names runtime preparation that pure projection intentionally
// does not perform.
type ProviderEffect struct {
	Kind        ProviderEffectKind `json:"kind"`
	Destination string             `json:"destination,omitempty"`
	Reason      string             `json:"reason"`
}

// ProjectionDiagnostic reports unsupported or deferred features.
type ProjectionDiagnostic struct {
	Feature registry.Feature `json:"feature,omitempty"`
	Code    string           `json:"code"`
	Message string           `json:"message"`
}

// ProviderProjection is the pure output of provider layout projection.
type ProviderProjection struct {
	Provider    runtimes.ID            `json:"provider"`
	Mode        runtimes.Mode          `json:"mode"`
	Variant     layout.Variant         `json:"variant,omitempty"`
	Version     string                 `json:"version,omitempty"`
	Files       []ProjectedFile        `json:"files"`
	Launch      LaunchConvention       `json:"launch"`
	Effects     []ProviderEffect       `json:"effects,omitempty"`
	Diagnostics []ProjectionDiagnostic `json:"diagnostics,omitempty"`
}

// UnsupportedFeatureError is returned when a required feature is not projected.
type UnsupportedFeatureError struct {
	Diagnostics []ProjectionDiagnostic
}

func (e *UnsupportedFeatureError) Error() string {
	parts := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		parts = append(parts, d.Message)
	}
	return strings.Join(parts, "; ")
}

// ProviderProjection renders a pure projection for a Claude adapter.
func (a *ClaudeAdapter) ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error) {
	shape := claudeProjectionShape(a)
	pid := runtimes.Claude
	files := []ProjectedFile{
		{RelPath: layoutRel(pid, shape, layout.Instructions, ""), Content: []byte(renderClaudeMD(ctx)), Role: "instructions"},
		{RelPath: layoutRel(pid, shape, layout.Boot, ""), Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: layoutRel(pid, shape, layout.MCP, ""), Content: []byte(renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: layoutFileMode(pid, shape, layout.MCP), Role: "mcp"},
	}
	doc, err := a.SettingsDocument()
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, ProjectedFile{
		RelPath: layoutRel(pid, shape, layout.NativeConfig, ""),
		Content: []byte(marshalClaudeSettings(doc)),
		Role:    "native-config",
	})
	skillPrefix, _ := skillRootFor(pid, shape)
	skillFiles, err := projectSkillPackages(skillPrefix, opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: runtimes.Claude,
		Mode:     shape.Mode,
		Variant:  shape.Variant,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   claudeConvention(a, shape, claudeLayoutPaths(shape, len(opts.Skills) > 0)),
		Effects: []ProviderEffect{
			{Kind: EffectClaudeCredentialHelper, Destination: layoutRel(pid, shape, layout.NativeConfig, ""), Reason: "apiKeyHelper may execute at runtime; projection only serializes the configured path"},
			{Kind: EffectClaudeWorkspaceTrust, Reason: "workspace trust seeding mutates host state and is handled by explicit preparation"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

// ProviderProjection renders a pure projection for a Codex adapter.
func (a *CodexAdapter) ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error) {
	shape := codexShape(a)
	config, err := a.ConfigDocument(ctx)
	if err != nil {
		return ProviderProjection{}, err
	}
	pid := runtimes.Codex
	files := []ProjectedFile{
		{RelPath: layoutRel(pid, shape, layout.Instructions, ""), Content: []byte(AgentsMD(AgentInfo{Name: ctx.AgentName, SystemPrompt: ctx.SystemPrompt}, ctx.MCPLoopbackURL)), Role: "instructions"},
		{RelPath: layoutRel(pid, shape, layout.Boot, ""), Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: layoutRel(pid, shape, layout.NativeConfig, ""), Content: []byte(config), Mode: layoutFileMode(pid, shape, layout.NativeConfig), Role: "native-config"},
		{RelPath: layoutRel(pid, shape, layout.Auth, ""), Mode: layoutFileMode(pid, shape, layout.Auth), Role: "credential-placeholder"},
		{RelPath: layoutRel(pid, shape, layout.MCP, ""), Content: []byte(renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: layoutFileMode(pid, shape, layout.MCP), Role: "mcp-mirror"},
	}
	skillPrefix, _ := skillRootFor(pid, shape)
	skillFiles, err := projectSkillPackages(skillPrefix, opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: runtimes.Codex,
		Mode:     shape.Mode,
		Variant:  shape.Variant,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   codexConvention(a, shape, pathArgs{projectDirs: layoutProjectDirs(runtimes.Codex, shape)}),
		Effects: []ProviderEffect{
			{Kind: EffectCodexAuthJSON, Destination: layoutRel(pid, shape, layout.Auth, ""), Reason: "auth.json contains credentials and must be resolved by explicit runtime preparation"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

// ProviderProjection renders a pure projection for an Antigravity adapter.
func (a *AntigravityAdapter) ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error) {
	const pid = runtimes.Antigravity
	shape := shapePerTurn
	files := []ProjectedFile{
		{RelPath: layoutRel(pid, shape, layout.Instructions, ""), Content: []byte(AgentsMD(AgentInfo{Name: ctx.AgentName, SystemPrompt: ctx.SystemPrompt}, ctx.MCPLoopbackURL)), Role: "instructions"},
		{RelPath: layoutRel(pid, shape, layout.Boot, ""), Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: layoutRel(pid, shape, layout.NativeConfig, ""), Content: []byte(renderAntigravityPluginJSON()), Role: "native-config"},
		{RelPath: layoutRel(pid, shape, layout.MCP, ""), Content: []byte(renderAntigravityMCPConfig(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: layoutFileMode(pid, shape, layout.MCP), Role: "mcp"},
	}
	skillPrefix, _ := skillRootFor(pid, shape)
	skillFiles, err := projectSkillPackages(skillPrefix, opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: pid,
		Mode:     shape.Mode,
		Variant:  shape.Variant,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   antigravityConvention(a, pathArgs{projectDirs: layoutProjectDirs(pid, shape)}),
		Effects: []ProviderEffect{
			{Kind: EffectAntigravityAuth, Reason: "agy authenticates from OAuth credentials under ~/.gemini, shared with the desktop app; they are never projected or relocated"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

// ProviderProjection renders a pure projection for an OpenCode adapter.
func (a *OpencodeAdapter) ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error) {
	shape := opencodeShape(a)
	agentName := ctx.AgentName
	if agentName == "" {
		agentName = a.Agent
	}
	if agentName == "" {
		agentName = "default"
	}
	pid := runtimes.OpenCode
	files := []ProjectedFile{
		{RelPath: layoutRel(pid, shape, layout.Instructions, agentName), Content: []byte(renderOpencodeAgentMD(agentName, ctx)), Role: "instructions"},
		{RelPath: layoutRel(pid, shape, layout.NativeConfig, agentName), Content: []byte(renderOpencodeJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Role: "native-config"},
		{RelPath: layoutRel(pid, shape, layout.Boot, agentName), Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: layoutRel(pid, shape, layout.MCP, agentName), Content: []byte(renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: layoutFileMode(pid, shape, layout.MCP), Role: "mcp-mirror"},
	}
	skillPrefix, _ := skillRootFor(pid, shape)
	skillFiles, err := projectSkillPackages(skillPrefix, opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: runtimes.OpenCode,
		Mode:     shape.Mode,
		Variant:  shape.Variant,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   opencodeConvention(a, shape, agentName, pathArgs{projectDirs: layoutProjectDirs(pid, shape)}),
		Effects: []ProviderEffect{
			{Kind: EffectOpencodeProviderAuth, Reason: "provider credentials are resolved by OpenCode or explicit runtime preparation"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// ResolveLaunch resolves the projection's structured launch convention using
// the supplied roots, for a first turn with prompt. It performs no IO and
// does not start a process.
func (p ProviderProjection) ResolveLaunch(roots ProjectionRoots, prompt string) (LaunchBinding, error) {
	return p.Launch.ResolveTurn(roots, TurnInput{Prompt: prompt}, nil)
}

// ResolveTurn resolves the projection's launch convention for one turn; see
// [LaunchConvention.ResolveTurn].
func (p ProviderProjection) ResolveTurn(roots ProjectionRoots, in TurnInput, extra []string) (LaunchBinding, error) {
	return p.Launch.ResolveTurn(roots, in, extra)
}

// ResolveTurn resolves the convention for one turn: roots fill the path
// arguments, in fills the prompt, system prompt and resume id, and extra goes
// at the convention's ArgExtra (after everything, if it has none). It is the
// one place a runtime's argv is produced: an adapter's BuildArgs resolves the
// same convention, built from the adapter's own fields. It performs no IO and
// does not start a process.
func (c LaunchConvention) ResolveTurn(roots ProjectionRoots, in TurnInput, extra []string) (LaunchBinding, error) {
	if roots.ConfigRoot == "" {
		roots.ConfigRoot = roots.BootRoot
	}
	cwd, err := rootValue(roots, c.CWD)
	if err != nil {
		return LaunchBinding{}, err
	}
	if cwd == "" && c.CWD == RootProject {
		cwd = roots.BootRoot
	}
	configRoot, err := rootValue(roots, c.ConfigRoot)
	if err != nil {
		return LaunchBinding{}, err
	}
	argv := make([]string, 0, len(c.Argv)+len(extra))
	placedExtra := false
	for _, tmpl := range c.Argv {
		if tmpl.Kind == ArgExtra {
			argv = append(argv, extra...)
			placedExtra = true
			continue
		}
		values, err := resolveArgTemplate(roots, tmpl, in)
		if err != nil {
			return LaunchBinding{}, err
		}
		argv = append(argv, values...)
	}
	if !placedExtra {
		argv = append(argv, extra...)
	}
	env := make([]EnvDelta, 0, len(c.Env))
	for _, delta := range c.Env {
		resolved := delta
		if RootKind(delta.Value) != "" {
			if v, err := rootValue(roots, RootKind(delta.Value)); err == nil && v != "" {
				resolved.Value = v
			}
		}
		env = append(env, resolved)
	}
	return LaunchBinding{CWD: cwd, ConfigDir: configRoot, Argv: argv, Env: env}, nil
}

func resolveArgTemplate(roots ProjectionRoots, tmpl ArgTemplate, in TurnInput) ([]string, error) {
	if tmpl.FirstTurnOnly && in.ResumeID != "" {
		return nil, nil
	}
	switch tmpl.Kind {
	case ArgLiteral:
		if tmpl.Value == "" && tmpl.OmitEmpty {
			return nil, nil
		}
		return []string{tmpl.Value}, nil
	case ArgPrompt:
		prompt := in.Prompt
		if tmpl.WithSystem {
			prompt = prefixSystemPrompt(in.Prompt, in.SystemPrompt)
		}
		if prompt == "" && tmpl.OmitEmpty {
			return nil, nil
		}
		return []string{prompt}, nil
	case ArgPromptInline:
		prompt := in.Prompt
		if tmpl.WithSystem {
			prompt = prefixSystemPrompt(in.Prompt, in.SystemPrompt)
		}
		return []string{tmpl.Value + prompt}, nil
	case ArgSystemPrompt:
		return flagValue(tmpl.Value, in.SystemPrompt), nil
	case ArgResume:
		return flagValue(tmpl.Value, in.ResumeID), nil
	case ArgRoot:
		value, err := rootValue(roots, tmpl.Root)
		if err != nil {
			return nil, err
		}
		if value == "" && tmpl.OmitEmpty {
			return nil, nil
		}
		if tmpl.Value == "" {
			return []string{value}, nil
		}
		return []string{tmpl.Value, value}, nil
	case ArgFile:
		root, err := rootValue(roots, tmpl.Root)
		if err != nil {
			return nil, err
		}
		if root == "" && tmpl.OmitEmpty {
			return nil, nil
		}
		value := filepath.Join(root, filepath.FromSlash(tmpl.RelPath))
		if tmpl.Value == "" {
			return []string{value}, nil
		}
		return []string{tmpl.Value, value}, nil
	default:
		return nil, fmt.Errorf("unsupported argv template kind %q", tmpl.Kind)
	}
}

// flagValue is [flag, value], or [value] when there is no flag, and nothing
// when value is empty.
func flagValue(flag, value string) []string {
	switch {
	case value == "":
		return nil
	case flag == "":
		return []string{value}
	default:
		return []string{flag, value}
	}
}

// prefixSystemPrompt puts a system prompt in front of the prompt, for a CLI
// that takes no system prompt of its own.
func prefixSystemPrompt(prompt, systemPrompt string) string {
	if systemPrompt == "" {
		return prompt
	}
	return "System: " + systemPrompt + "\n\n" + prompt
}

func rootValue(roots ProjectionRoots, kind RootKind) (string, error) {
	switch kind {
	case "":
		return "", nil
	case RootBoot:
		return roots.BootRoot, nil
	case RootProject:
		return roots.ProjectRoot, nil
	case RootConfig:
		return roots.ConfigRoot, nil
	case RootState:
		return roots.StateRoot, nil
	case RootScratch:
		return roots.ScratchRoot, nil
	case RootCWD:
		return roots.CWD, nil
	default:
		return "", fmt.Errorf("unsupported root kind %q", kind)
	}
}

// ApplyEnvDeltas applies structured environment amendments without shell
// parsing. The returned slice is sorted by key for deterministic tests.
func ApplyEnvDeltas(base []string, deltas []EnvDelta) []string {
	env := map[string]string{}
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		env[k] = v
	}
	for _, d := range deltas {
		if d.Name == "" {
			continue
		}
		if d.Precedence == EnvCallerWins {
			if _, exists := env[d.Name]; exists {
				continue
			}
		}
		sep := d.Separator
		if sep == "" {
			sep = string(os.PathListSeparator)
		}
		switch d.Operation {
		case EnvUnset:
			delete(env, d.Name)
		case EnvPrepend:
			if cur := env[d.Name]; cur != "" {
				env[d.Name] = d.Value + sep + cur
			} else {
				env[d.Name] = d.Value
			}
		case EnvAppend:
			if cur := env[d.Name]; cur != "" {
				env[d.Name] = cur + sep + d.Value
			} else {
				env[d.Name] = d.Value
			}
		default:
			env[d.Name] = d.Value
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func requireProjectedFeatures(proj ProviderProjection, required []registry.Feature) (ProviderProjection, error) {
	if len(required) == 0 {
		return proj, nil
	}
	shape := layout.Shape{Mode: proj.Mode, Variant: proj.Variant}
	row, ok := capabilityRow(proj.Provider, shape)
	if !ok {
		return proj, fmt.Errorf("no capability matrix row for %s/%s", proj.Provider, shape)
	}
	var diagnostics []ProjectionDiagnostic
	for _, f := range required {
		status := registry.Support(row.Features[string(f)])
		if status == registry.SupportProjected {
			continue
		}
		msg := fmt.Sprintf("%s/%s does not project required feature %q", proj.Provider, shape, f)
		if status == registry.SupportExplicit {
			msg += "; it requires explicit runtime preparation"
		}
		diagnostics = append(diagnostics, ProjectionDiagnostic{Feature: f, Code: "unsupported_feature", Message: msg})
	}
	if len(diagnostics) == 0 {
		return proj, nil
	}
	proj.Diagnostics = append(proj.Diagnostics, diagnostics...)
	return proj, &UnsupportedFeatureError{Diagnostics: diagnostics}
}

func capabilityRow(provider runtimes.ID, shape layout.Shape) (ProviderCapabilityRow, bool) {
	for _, row := range ProviderCapabilityMatrix() {
		if row.Provider == provider && row.Shape() == shape {
			return row, true
		}
	}
	return ProviderCapabilityRow{}, false
}

func projectSkillPackages(prefix string, packages []SkillPackage) ([]ProjectedFile, error) {
	var out []ProjectedFile
	for _, pkg := range packages {
		if !validSkillName(pkg.Name) {
			return nil, fmt.Errorf("skill package %q: invalid name (want lowercase alphanumeric with single hyphen separators)", pkg.Name)
		}
		if pkg.Hash != "" {
			got, err := pkg.TreeHash()
			if err != nil {
				return nil, err
			}
			if got != pkg.Hash {
				return nil, fmt.Errorf("skill package %q: content hash %s does not match pinned %s", pkg.Name, got, pkg.Hash)
			}
		}
		seen := map[string]bool{}
		for _, f := range pkg.Files {
			rel, err := cleanPackageRelPath(f.RelPath)
			if err != nil {
				return nil, fmt.Errorf("skill package %q: %w", pkg.Name, err)
			}
			if seen[rel] {
				return nil, fmt.Errorf("skill package %q: duplicate file %q", pkg.Name, rel)
			}
			seen[rel] = true
			mode := f.Mode
			if mode == 0 {
				mode = 0o644
			}
			out = append(out, ProjectedFile{
				RelPath: path.Join(prefix, pkg.Name, rel),
				Content: append([]byte(nil), f.Content...),
				Mode:    mode,
				Role:    "skill",
			})
		}
		if !seen["SKILL.md"] {
			return nil, fmt.Errorf("skill package %q: missing SKILL.md", pkg.Name)
		}
	}
	return out, nil
}

var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validSkillName(name string) bool {
	return len(name) >= 1 && len(name) <= 64 && skillNameRE.MatchString(name)
}

func cleanPackageRelPath(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty skill file path")
	}
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("skill file path %q contains backslash", rel)
	}
	if path.IsAbs(rel) {
		return "", fmt.Errorf("skill file path %q is absolute", rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("skill file path %q escapes package root", rel)
	}
	return clean, nil
}

func sortProjectedFiles(files []ProjectedFile) []ProjectedFile {
	out := append([]ProjectedFile(nil), files...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].RelPath < out[j].RelPath
	})
	return out
}

// MarshalProjectionFixture is a test/helper convenience for stable golden
// fixtures.
func MarshalProjectionFixture(p ProviderProjection) string {
	out, _ := json.MarshalIndent(p, "", "  ")
	return string(out) + "\n"
}
