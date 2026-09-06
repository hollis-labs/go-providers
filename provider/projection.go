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
)

// ProviderID names a provider layout family without importing any runtime
// package.
type ProviderID string

const (
	ProviderClaude   ProviderID = "claude"
	ProviderCodex    ProviderID = "codex"
	ProviderOpencode ProviderID = "opencode"
)

// ProviderMode names the provider runtime shape whose filesystem and launch
// conventions are being projected.
type ProviderMode string

const (
	ModeClaudePrint          ProviderMode = "claude-print"
	ModeClaudeBare           ProviderMode = "claude-bare"
	ModeClaudePTY            ProviderMode = "claude-pty"
	ModeClaudeStreamingStdio ProviderMode = "claude-streaming-stdio"
	ModeCodexExec            ProviderMode = "codex-exec"
	ModeCodexAppServer       ProviderMode = "codex-app-server"
	ModeOpencodeRun          ProviderMode = "opencode-run"
	ModeOpencodeServeHTTP    ProviderMode = "opencode-serve-http"
)

// ProviderFeature is a named provider capability that callers may require
// before accepting a projection.
type ProviderFeature string

const (
	FeatureInstructions ProviderFeature = "instructions"
	FeatureNativeConfig ProviderFeature = "native-config"
	FeatureMCP          ProviderFeature = "mcp"
	FeatureSkillTrees   ProviderFeature = "skill-trees"
	FeatureHooks        ProviderFeature = "hooks"
	FeatureCommands     ProviderFeature = "commands"
	FeatureSubagents    ProviderFeature = "subagents"
	FeatureCredential   ProviderFeature = "credential"
	FeatureTrust        ProviderFeature = "trust"
)

// CapabilitySupport records whether a feature is projected by this package,
// known to the provider but left to another phase, or unsupported.
type CapabilitySupport string

const (
	SupportProjected   CapabilitySupport = "projected"
	SupportExplicit    CapabilitySupport = "explicit-effect"
	SupportUnsupported CapabilitySupport = "unsupported"
)

// ProviderCapabilityRow is the exported capability matrix for provider
// projection. TestedVersion is the executable version used for the M06
// contract fixtures in this worktree.
type ProviderCapabilityRow struct {
	Provider      ProviderID        `json:"provider"`
	Mode          ProviderMode      `json:"mode"`
	TestedVersion string            `json:"tested_version"`
	Features      map[string]string `json:"features"`
	Notes         string            `json:"notes,omitempty"`
}

// ProviderCapabilityMatrix returns a deterministic provider/version matrix for
// the pure projection contracts in this package.
func ProviderCapabilityMatrix() []ProviderCapabilityRow {
	claudeFeatures := featureMap(map[ProviderFeature]CapabilitySupport{
		FeatureInstructions: SupportProjected,
		FeatureNativeConfig: SupportProjected,
		FeatureMCP:          SupportProjected,
		FeatureSkillTrees:   SupportProjected,
		FeatureHooks:        SupportExplicit,
		FeatureCommands:     SupportExplicit,
		FeatureSubagents:    SupportExplicit,
		FeatureCredential:   SupportExplicit,
		FeatureTrust:        SupportExplicit,
	})
	codexFeatures := featureMap(map[ProviderFeature]CapabilitySupport{
		FeatureInstructions: SupportProjected,
		FeatureNativeConfig: SupportProjected,
		FeatureMCP:          SupportProjected,
		FeatureSkillTrees:   SupportProjected,
		FeatureHooks:        SupportExplicit,
		FeatureCommands:     SupportUnsupported,
		FeatureSubagents:    SupportExplicit,
		FeatureCredential:   SupportExplicit,
		FeatureTrust:        SupportUnsupported,
	})
	opencodeFeatures := featureMap(map[ProviderFeature]CapabilitySupport{
		FeatureInstructions: SupportProjected,
		FeatureNativeConfig: SupportProjected,
		FeatureMCP:          SupportProjected,
		FeatureSkillTrees:   SupportProjected,
		FeatureHooks:        SupportUnsupported,
		FeatureCommands:     SupportExplicit,
		FeatureSubagents:    SupportExplicit,
		FeatureCredential:   SupportExplicit,
		FeatureTrust:        SupportUnsupported,
	})
	return []ProviderCapabilityRow{
		claudeCapabilityRow(ModeClaudePrint, claudeFeatures),
		claudeCapabilityRow(ModeClaudeBare, claudeFeatures),
		claudeCapabilityRow(ModeClaudePTY, claudeFeatures),
		claudeCapabilityRow(ModeClaudeStreamingStdio, claudeFeatures),
		{
			Provider:      ProviderCodex,
			Mode:          ModeCodexExec,
			TestedVersion: "0.153.4",
			Features:      codexFeatures,
			Notes:         "Codex reads config from CODEX_HOME/config.toml; auth.json is a preparation effect, not a pure render input.",
		},
		{
			Provider:      ProviderCodex,
			Mode:          ModeCodexAppServer,
			TestedVersion: "0.153.4",
			Features:      codexFeatures,
			Notes:         "Project root is supplied to the JSON-RPC thread layer rather than via --cd.",
		},
		{
			Provider:      ProviderOpencode,
			Mode:          ModeOpencodeRun,
			TestedVersion: "1.15.6",
			Features:      opencodeFeatures,
			Notes:         "OpenCode uses OPENCODE_CONFIG_DIR for projected config and project cwd for work.",
		},
		{
			Provider:      ProviderOpencode,
			Mode:          ModeOpencodeServeHTTP,
			TestedVersion: "1.15.6",
			Features:      opencodeFeatures,
			Notes:         "OpenCode serve-http uses the same projected config and moves turn delivery to the HTTP runtime.",
		},
	}
}

func claudeCapabilityRow(mode ProviderMode, features map[string]string) ProviderCapabilityRow {
	return ProviderCapabilityRow{
		Provider:      ProviderClaude,
		Mode:          mode,
		TestedVersion: "2.1.263",
		Features:      features,
		Notes:         "Claude project files are rooted at the boot directory; auth and trust preparation are explicit runtime effects.",
	}
}

func featureMap(in map[ProviderFeature]CapabilitySupport) map[string]string {
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
	RequiredFeatures []ProviderFeature
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
	ArgLiteral ArgKind = "literal"
	ArgPrompt  ArgKind = "prompt"
	ArgRoot    ArgKind = "root"
	ArgFile    ArgKind = "file"
)

// ArgTemplate stores argv as structured values so paths with spaces or unicode
// are never parsed from a shell string.
type ArgTemplate struct {
	Kind      ArgKind  `json:"kind"`
	Value     string   `json:"value,omitempty"`
	Root      RootKind `json:"root,omitempty"`
	RelPath   string   `json:"rel_path,omitempty"`
	OmitEmpty bool     `json:"omit_empty,omitempty"`
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
	Executable string        `json:"executable"`
	Mode       ProviderMode  `json:"mode"`
	CWD        RootKind      `json:"cwd"`
	ConfigRoot RootKind      `json:"config_root,omitempty"`
	Argv       []ArgTemplate `json:"argv"`
	Env        []EnvDelta    `json:"env"`
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
)

// ProviderEffect names runtime preparation that pure projection intentionally
// does not perform.
type ProviderEffect struct {
	Kind        ProviderEffectKind `json:"kind"`
	Destination string             `json:"destination,omitempty"`
	Reason      string             `json:"reason"`
}

// ProjectionDiagnostic reports unsupported or deferred features.
type ProjectionDiagnostic struct {
	Feature ProviderFeature `json:"feature,omitempty"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
}

// ProviderProjection is the pure output of provider layout projection.
type ProviderProjection struct {
	Provider    ProviderID             `json:"provider"`
	Mode        ProviderMode           `json:"mode"`
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
	mode := claudeProjectionMode(a)
	files := []ProjectedFile{
		{RelPath: "CLAUDE.md", Content: []byte(renderClaudeMD(ctx)), Role: "instructions"},
		{RelPath: "boot.md", Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: ".mcp.json", Content: []byte(renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: 0o600, Role: "mcp"},
	}
	doc, err := a.SettingsDocument()
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, ProjectedFile{
		RelPath: ".claude/settings.json",
		Content: []byte(marshalClaudeSettings(doc)),
		Role:    "native-config",
	})
	skillFiles, err := projectSkillPackages(".claude/skills", opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: ProviderClaude,
		Mode:     mode,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   claudeLaunchConvention(a, mode),
		Effects: []ProviderEffect{
			{Kind: EffectClaudeCredentialHelper, Destination: ".claude/settings.json", Reason: "apiKeyHelper may execute at runtime; projection only serializes the configured path"},
			{Kind: EffectClaudeWorkspaceTrust, Reason: "workspace trust seeding mutates host state and is handled by explicit preparation"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

// ProviderProjection renders a pure projection for a Codex adapter.
func (a *CodexAdapter) ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error) {
	mode := ModeCodexExec
	if a.Mode == "app-server" {
		mode = ModeCodexAppServer
	}
	config, err := a.ConfigDocument(ctx)
	if err != nil {
		return ProviderProjection{}, err
	}
	files := []ProjectedFile{
		{RelPath: "AGENTS.md", Content: []byte(AgentsMD(AgentInfo{Name: ctx.AgentName, SystemPrompt: ctx.SystemPrompt}, ctx.MCPLoopbackURL)), Role: "instructions"},
		{RelPath: "boot.md", Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: "config.toml", Content: []byte(config), Mode: 0o600, Role: "native-config"},
		{RelPath: "auth.json", Mode: 0o600, Role: "credential-placeholder"},
		{RelPath: ".mcp.json", Content: []byte(renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: 0o600, Role: "mcp-mirror"},
	}
	skillFiles, err := projectSkillPackages(".agents/skills", opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: ProviderCodex,
		Mode:     mode,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   codexLaunchConvention(mode),
		Effects: []ProviderEffect{
			{Kind: EffectCodexAuthJSON, Destination: "auth.json", Reason: "auth.json contains credentials and must be resolved by explicit runtime preparation"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

// ProviderProjection renders a pure projection for an OpenCode adapter.
func (a *OpencodeAdapter) ProviderProjection(ctx PlantContext, opts ProjectionOptions) (ProviderProjection, error) {
	mode := ModeOpencodeRun
	if a.Mode == "serve-http" {
		mode = ModeOpencodeServeHTTP
	}
	agentName := ctx.AgentName
	if agentName == "" {
		agentName = a.Agent
	}
	if agentName == "" {
		agentName = "default"
	}
	files := []ProjectedFile{
		{RelPath: "agents/" + agentName + ".md", Content: []byte(renderOpencodeAgentMD(agentName, ctx)), Role: "instructions"},
		{RelPath: "agents.json", Content: []byte(renderOpencodeAgentsJSON(agentName)), Role: "native-config"},
		{RelPath: "opencode.json", Content: []byte(renderOpencodeJSON(agentName, ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Role: "native-config"},
		{RelPath: "boot.md", Content: []byte(ctx.BootContent), Role: "boot"},
		{RelPath: ".mcp.json", Content: []byte(renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx))), Mode: 0o600, Role: "mcp-mirror"},
	}
	skillFiles, err := projectSkillPackages(".opencode/skills", opts.Skills)
	if err != nil {
		return ProviderProjection{}, err
	}
	files = append(files, skillFiles...)
	proj := ProviderProjection{
		Provider: ProviderOpencode,
		Mode:     mode,
		Version:  opts.Version,
		Files:    sortProjectedFiles(files),
		Launch:   opencodeLaunchConvention(a, mode, agentName),
		Effects: []ProviderEffect{
			{Kind: EffectOpencodeProviderAuth, Reason: "provider credentials are resolved by OpenCode or explicit runtime preparation"},
		},
	}
	return requireProjectedFeatures(proj, opts.RequiredFeatures)
}

func claudeProjectionMode(a *ClaudeAdapter) ProviderMode {
	switch {
	case a.Bare:
		return ModeClaudeBare
	case a.PTY:
		return ModeClaudePTY
	case a.InputMode == "stream-json":
		return ModeClaudeStreamingStdio
	default:
		return ModeClaudePrint
	}
}

func claudeLaunchConvention(a *ClaudeAdapter, mode ProviderMode) LaunchConvention {
	args := []ArgTemplate{}
	switch mode {
	case ModeClaudePTY:
		args = append(args, ArgTemplate{Kind: ArgFile, Root: RootBoot, RelPath: ".mcp.json", Value: "--mcp-config", OmitEmpty: true})
	case ModeClaudeStreamingStdio:
		args = append(args,
			ArgTemplate{Kind: ArgLiteral, Value: "-p"},
			ArgTemplate{Kind: ArgLiteral, Value: "--input-format"},
			ArgTemplate{Kind: ArgLiteral, Value: "stream-json"},
			ArgTemplate{Kind: ArgLiteral, Value: "--output-format"},
			ArgTemplate{Kind: ArgLiteral, Value: "stream-json"},
			ArgTemplate{Kind: ArgLiteral, Value: "--verbose"},
		)
		args = append(args, ArgTemplate{Kind: ArgFile, Root: RootBoot, RelPath: ".mcp.json", Value: "--mcp-config", OmitEmpty: true})
	case ModeClaudeBare:
		args = append(args,
			ArgTemplate{Kind: ArgLiteral, Value: "-p"},
			ArgTemplate{Kind: ArgPrompt},
			ArgTemplate{Kind: ArgLiteral, Value: "--output-format"},
			ArgTemplate{Kind: ArgLiteral, Value: "stream-json"},
			ArgTemplate{Kind: ArgLiteral, Value: "--verbose"},
			ArgTemplate{Kind: ArgLiteral, Value: "--bare"},
			ArgTemplate{Kind: ArgFile, Root: RootBoot, RelPath: ".mcp.json", Value: "--mcp-config", OmitEmpty: true},
			ArgTemplate{Kind: ArgFile, Root: RootBoot, RelPath: "CLAUDE.md", Value: "--append-system-prompt-file", OmitEmpty: true},
			ArgTemplate{Kind: ArgFile, Root: RootBoot, RelPath: ".claude/settings.json", Value: "--settings", OmitEmpty: true},
			ArgTemplate{Kind: ArgRoot, Root: RootProject, Value: "--add-dir", OmitEmpty: true},
		)
	default:
		args = append(args,
			ArgTemplate{Kind: ArgLiteral, Value: "-p"},
			ArgTemplate{Kind: ArgPrompt},
			ArgTemplate{Kind: ArgLiteral, Value: "--output-format"},
			ArgTemplate{Kind: ArgLiteral, Value: "stream-json"},
			ArgTemplate{Kind: ArgLiteral, Value: "--verbose"},
			ArgTemplate{Kind: ArgFile, Root: RootBoot, RelPath: ".mcp.json", Value: "--mcp-config", OmitEmpty: true},
		)
	}
	if a.SkipPermissions {
		args = append(args, ArgTemplate{Kind: ArgLiteral, Value: "--dangerously-skip-permissions"})
	}
	return LaunchConvention{
		Executable: "claude",
		Mode:       mode,
		CWD:        RootBoot,
		ConfigRoot: RootBoot,
		Argv:       args,
	}
}

func codexLaunchConvention(mode ProviderMode) LaunchConvention {
	args := []ArgTemplate{}
	if mode == ModeCodexAppServer {
		args = []ArgTemplate{{Kind: ArgLiteral, Value: "app-server"}}
	} else {
		args = []ArgTemplate{
			{Kind: ArgLiteral, Value: "exec"},
			{Kind: ArgPrompt},
			{Kind: ArgLiteral, Value: "--json"},
			{Kind: ArgLiteral, Value: "--skip-git-repo-check"},
			{Kind: ArgRoot, Root: RootProject, Value: "--cd", OmitEmpty: true},
		}
	}
	return LaunchConvention{
		Executable: "codex",
		Mode:       mode,
		CWD:        RootBoot,
		ConfigRoot: RootBoot,
		Argv:       args,
		Env: []EnvDelta{{
			Name:       "CODEX_HOME",
			Value:      string(RootBoot),
			Operation:  EnvSet,
			Precedence: EnvProviderWins,
		}},
	}
}

func opencodeLaunchConvention(a *OpencodeAdapter, mode ProviderMode, agentName string) LaunchConvention {
	args := []ArgTemplate{}
	if mode == ModeOpencodeServeHTTP {
		args = []ArgTemplate{
			{Kind: ArgLiteral, Value: "serve"},
			{Kind: ArgLiteral, Value: "--port"},
			{Kind: ArgLiteral, Value: "0"},
			{Kind: ArgLiteral, Value: "--hostname"},
			{Kind: ArgLiteral, Value: "127.0.0.1"},
		}
	} else {
		args = []ArgTemplate{
			{Kind: ArgLiteral, Value: "run"},
			{Kind: ArgLiteral, Value: "--agent"},
			{Kind: ArgLiteral, Value: firstNonEmpty(agentName, "default")},
		}
		if a.Model != "" {
			args = append(args, ArgTemplate{Kind: ArgLiteral, Value: "--model"}, ArgTemplate{Kind: ArgLiteral, Value: a.Model})
		}
		args = append(args,
			ArgTemplate{Kind: ArgRoot, Root: RootProject, Value: "--dir", OmitEmpty: true},
			ArgTemplate{Kind: ArgPrompt},
		)
	}
	return LaunchConvention{
		Executable: "opencode",
		Mode:       mode,
		CWD:        RootProject,
		ConfigRoot: RootBoot,
		Argv:       args,
		Env: []EnvDelta{{
			Name:       "OPENCODE_CONFIG_DIR",
			Value:      string(RootBoot),
			Operation:  EnvSet,
			Precedence: EnvProviderWins,
		}},
	}
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// ResolveLaunch resolves the projection's structured launch convention using
// the supplied roots. It performs no IO and does not start a process.
func (p ProviderProjection) ResolveLaunch(roots ProjectionRoots, prompt string) (LaunchBinding, error) {
	if roots.ConfigRoot == "" {
		roots.ConfigRoot = roots.BootRoot
	}
	cwd, err := rootValue(roots, p.Launch.CWD)
	if err != nil {
		return LaunchBinding{}, err
	}
	if cwd == "" && p.Launch.CWD == RootProject {
		cwd = roots.BootRoot
	}
	configRoot, err := rootValue(roots, p.Launch.ConfigRoot)
	if err != nil {
		return LaunchBinding{}, err
	}
	argv := make([]string, 0, len(p.Launch.Argv))
	for _, tmpl := range p.Launch.Argv {
		values, err := resolveArgTemplate(roots, tmpl, prompt)
		if err != nil {
			return LaunchBinding{}, err
		}
		argv = append(argv, values...)
	}
	env := make([]EnvDelta, 0, len(p.Launch.Env))
	for _, delta := range p.Launch.Env {
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

func resolveArgTemplate(roots ProjectionRoots, tmpl ArgTemplate, prompt string) ([]string, error) {
	switch tmpl.Kind {
	case ArgLiteral:
		if tmpl.Value == "" && tmpl.OmitEmpty {
			return nil, nil
		}
		return []string{tmpl.Value}, nil
	case ArgPrompt:
		if prompt == "" && tmpl.OmitEmpty {
			return nil, nil
		}
		return []string{prompt}, nil
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

func requireProjectedFeatures(proj ProviderProjection, required []ProviderFeature) (ProviderProjection, error) {
	if len(required) == 0 {
		return proj, nil
	}
	row, ok := capabilityRow(proj.Provider, proj.Mode)
	if !ok {
		return proj, fmt.Errorf("no capability matrix row for %s/%s", proj.Provider, proj.Mode)
	}
	var diagnostics []ProjectionDiagnostic
	for _, f := range required {
		status := CapabilitySupport(row.Features[string(f)])
		if status == SupportProjected {
			continue
		}
		msg := fmt.Sprintf("%s/%s does not project required feature %q", proj.Provider, proj.Mode, f)
		if status == SupportExplicit {
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

func capabilityRow(provider ProviderID, mode ProviderMode) (ProviderCapabilityRow, bool) {
	for _, row := range ProviderCapabilityMatrix() {
		if row.Provider == provider && row.Mode == mode {
			return row, true
		}
	}
	// Claude non-bare modes share the same projection support as bare mode.
	if provider == ProviderClaude {
		for _, row := range ProviderCapabilityMatrix() {
			if row.Provider == ProviderClaude {
				row.Mode = mode
				return row, true
			}
		}
	}
	// OpenCode serve-http shares filesystem projection with run mode.
	if provider == ProviderOpencode && mode == ModeOpencodeServeHTTP {
		for _, row := range ProviderCapabilityMatrix() {
			if row.Provider == ProviderOpencode {
				row.Mode = mode
				return row, true
			}
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
