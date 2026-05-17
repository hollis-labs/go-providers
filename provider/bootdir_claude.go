package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BootDirSpec for the Claude Code CLI.
//
// Layout:
//
//	<bootDir>/
//	├── CLAUDE.md           # system context (auto-loaded by Claude on cwd)
//	├── boot.md             # task kickoff content (referenced via Boot @./boot.md)
//	├── .claude/settings.json   # stub to avoid global ~/.claude.json bleed
//	└── .mcp.json           # MCP loopback config
//
// Spawn invariants: cwd = bootDir; project access via
// "--add-dir <projectDir>"; CLAUDE.md is auto-loaded by the CLI.
func (a *ClaudeAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: "CLAUDE.md",
				Render: func(ctx PlantContext) (string, error) {
					return renderClaudeMD(ctx), nil
				},
			},
			{
				RelPath: "boot.md",
				Render: func(ctx PlantContext) (string, error) {
					return ctx.BootContent, nil
				},
			},
			{
				RelPath: ".claude/settings.json",
				Render: func(ctx PlantContext) (string, error) {
					// Side effect, gated on PlantContext.BootDir: seed a
					// per-bootdir trust marker in ~/.claude.json so the
					// claude CLI doesn't fire its first-run workspace
					// trust dialog on PTY spawn. The stub itself only
					// covers tool/MCP overrides — trust state lives in
					// the user-global config keyed by the realpath of
					// cwd (probed empirically; see CHANGELOG v0.8.2).
					if ctx.BootDir != "" {
						home, err := os.UserHomeDir()
						if err != nil {
							return "", fmt.Errorf("claude bootdir trust seed: home dir: %w", err)
						}
						if err := seedClaudeWorkspaceTrust(home, ctx.BootDir); err != nil {
							return "", fmt.Errorf("claude bootdir trust seed: %w", err)
						}
					}
					// a.ApiKeyHelperPath threads through into the planted
					// settings.json so bare-mode claude can resolve auth
					// via the helper. Empty path leaves
					// the field unset — bare mode then requires
					// ANTHROPIC_API_KEY in env.
					//
					// a.PermissionMode / a.SkipPermissions thread through
					// as permissions.defaultMode in the planted settings
					// (see resolveClaudeDefaultMode for the precedence).
					mode, err := resolveClaudeDefaultMode(a.PermissionMode, a.SkipPermissions)
					if err != nil {
						return "", fmt.Errorf("claude bootdir settings: %w", err)
					}
					return claudeSettingsStub(a.ApiKeyHelperPath, mode), nil
				},
			},
			{
				RelPath: ".mcp.json",
				Render: func(ctx PlantContext) (string, error) {
					return renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx)), nil
				},
			},
		},
		CwdPreference: CwdBootDir,
		ProjectDirArg: "--add-dir {{.ProjectDir}}",
	}
}

// ClaudeBareInjection bundles the four CLI flag values for bare-mode
// invocation, derived from the planted-file layout in BootDirSpec.
type ClaudeBareInjection struct {
	// MCPConfigPath is the value for --mcp-config (the planted .mcp.json).
	MCPConfigPath string
	// AppendSystemPromptFile is the value for --append-system-prompt-file
	// (the planted CLAUDE.md), replacing the auto-discovered CLAUDE.md
	// auto-load that bare mode disables.
	AppendSystemPromptFile string
	// SettingsPath is the value for --settings (the planted
	// .claude/settings.json).
	SettingsPath string
	// ProjectDir is the value for --add-dir (the project root the agent
	// should have tool access to).
	ProjectDir string
}

// BareInjectionPaths derives the bare-mode CLI flag values from the
// claude BootDirSpec layout. Empty bootDir or projectDir produce the
// corresponding empty fields (no-op for that flag).
//
// Consumer flow:
//
//	adapter := provider.NewClaudeAdapterDevBare()
//	spec := adapter.BootDirSpec()
//	// app plants files from spec ...
//	inj := adapter.BareInjectionPaths(bootDir, projectDir)
//	adapter.MCPConfigPath          = inj.MCPConfigPath
//	adapter.AppendSystemPromptFile = inj.AppendSystemPromptFile
//	adapter.SettingsPath           = inj.SettingsPath
//	adapter.ProjectDir             = inj.ProjectDir
//	args := adapter.BuildArgs(prompt, "", sessionID)
func (a *ClaudeAdapter) BareInjectionPaths(bootDir, projectDir string) ClaudeBareInjection {
	var inj ClaudeBareInjection
	if bootDir != "" {
		inj.MCPConfigPath = filepath.Join(bootDir, ".mcp.json")
		inj.AppendSystemPromptFile = filepath.Join(bootDir, "CLAUDE.md")
		inj.SettingsPath = filepath.Join(bootDir, ".claude", "settings.json")
	}
	inj.ProjectDir = projectDir
	return inj
}

func renderClaudeMD(ctx PlantContext) string {
	var b strings.Builder
	if ctx.SystemPrompt != "" {
		b.WriteString(ctx.SystemPrompt)
		b.WriteString("\n\n")
	}
	if ctx.MCPLoopbackURL != "" || ctx.MuxCommand != "" {
		b.WriteString("## MCP\n\n")
		if ctx.MCPLoopbackURL != "" {
			b.WriteString("Tools for this task are available via the MCP server configured in .mcp.json (")
			b.WriteString(ctx.MCPLoopbackURL)
			b.WriteString(").\n")
		}
		if ctx.MuxCommand != "" {
			// Cross-task / portfolio-wide tooling (Vanta memory, cross-task
			// clockwork, cerberus) is exposed via a second MCP entry that
			// spawns the Mux aggregator over stdio. Spelled out so the
			// LLM can route tool calls correctly when both surfaces exist.
			b.WriteString("Additional cross-task tools (Vanta + portfolio surface) are available via the `mux` MCP entry in .mcp.json (")
			b.WriteString(ctx.MuxCommand)
			b.WriteString(").\n")
		}
	}
	return b.String()
}

// claudeSettingsStub renders the planted .claude/settings.json using
// the current Claude Code settings schema.
//
// apiKeyHelperPath, when non-empty, threads into the stub as
// `apiKeyHelper: <path>`. Bare-mode claude invokes the helper per
// request and consumes its first line of stdout as the bearer token.
// This knob exists so subscription users (no
// ANTHROPIC_API_KEY in env, authenticated via `claude` interactive →
// macOS keychain) can run bare-mode dispatches without manually
// setting up an API key — the helper reads the keychain entry and
// emits the OAuth access token (`sk-ant-oat01-...`), which
// authenticates against the API directly (empirically verified).
//
// defaultMode, when non-empty, emits `permissions.defaultMode:
// <defaultMode>` — the Claude Code settings-schema knob for the
// permission mode (default / acceptEdits / plan / bypassPermissions).
// It backstops the equivalent CLI flags for any consumer that reaches
// the planted settings.json. Callers resolve the value via
// resolveClaudeDefaultMode. The legacy `approvedTools` / `mcpServers`
// keys are intentionally NOT emitted: current Claude Code ignores
// both, so writing them pre-approved nothing (the cause of
// spawned-agent permission-prompt breakage).
//
// Apps that need a richer permissions policy (permissions.allow /
// deny rules) can post-process the planted file before spawn — the
// stub is the minimum-viable shape; apps own everything beyond.
func claudeSettingsStub(apiKeyHelperPath, defaultMode string) string {
	stub := map[string]any{}
	if apiKeyHelperPath != "" {
		stub["apiKeyHelper"] = apiKeyHelperPath
	}
	if defaultMode != "" {
		stub["permissions"] = map[string]any{"defaultMode": defaultMode}
	}
	out, _ := json.MarshalIndent(stub, "", "  ")
	return string(out) + "\n"
}

// claudePermissionModes is the Claude Code settings-schema vocabulary
// for `permissions.defaultMode`.
var claudePermissionModes = map[string]bool{
	"default":           true,
	"acceptEdits":       true,
	"plan":              true,
	"bypassPermissions": true,
}

// resolveClaudeDefaultMode derives the `permissions.defaultMode` value
// for the planted .claude/settings.json from a ClaudeAdapter's
// PermissionMode and SkipPermissions fields.
//
// Precedence:
//   - A non-empty PermissionMode wins. It must be one of the
//     claudePermissionModes vocabulary; an unrecognized value is an
//     error (surfaced through the PlantedFile Render).
//   - Otherwise SkipPermissions == true yields "bypassPermissions" —
//     the back-compat behavior from before PermissionMode existed.
//   - Otherwise the empty string: no `permissions` block is planted.
func resolveClaudeDefaultMode(permissionMode string, skipPermissions bool) (string, error) {
	if permissionMode != "" {
		if !claudePermissionModes[permissionMode] {
			return "", fmt.Errorf("invalid PermissionMode %q (want one of: default, acceptEdits, plan, bypassPermissions)", permissionMode)
		}
		return permissionMode, nil
	}
	if skipPermissions {
		return "bypassPermissions", nil
	}
	return "", nil
}

// seedClaudeWorkspaceTrust pre-accepts the workspace trust dialog for the
// given bootDir by writing an entry to ~/.claude.json's `projects` map.
//
// Why this is needed: when claude is spawned in PTY mode in a fresh tempdir,
// the CLI fires a "Quick safety check" trust dialog because the path is not
// yet in projects[]. The dialog is auto-skipped only in non-interactive mode
// (-p / piped stdout), per `claude --help`. PTY = TTY = dialog fires.
// `--dangerously-skip-permissions` covers per-tool permission checks, not
// this workspace-trust gate.
//
// Probe results (go-providers v0.8.2 ticket): per-cwd `.claude/settings.json`
// does not honor any trust field; the canonical store is ~/.claude.json's
// `projects[<realpath(cwd)>].hasTrustDialogAccepted`. Path keying must use
// the realpath because claude resolves cwd via realpath on macOS
// (/var/folders → /private/var/folders).
//
// homeDir is normally os.UserHomeDir(); tests override via t.Setenv("HOME").
// The write is best-effort atomic (temp file + rename in the same dir as
// ~/.claude.json). If ~/.claude.json doesn't exist, a new file with just
// the projects map is created. If it exists but is malformed JSON, this
// returns an error rather than overwriting the user's config.
//
// Concurrency: ~/.claude.json may be written by other claude processes.
// This function does read-modify-rename without a lock; in the rare case
// of a concurrent write, the bootdir's trust entry could be clobbered and
// the dialog would fire on next spawn. Mitigation is filed as a follow-up;
// the surface here is intentionally narrow (only one key under projects).
//
// Cleanup: this function does not remove the projects[bootdir] entry. The
// bootdir tempdir is removed by the consumer at session teardown; the
// stale projects entry references a non-existent path and is harmless.
// If accumulation becomes an issue, consumers can sweep entries whose path
// matches the bootdir prefix on startup.
func seedClaudeWorkspaceTrust(homeDir, bootDir string) error {
	if homeDir == "" {
		return fmt.Errorf("homeDir is empty")
	}
	if bootDir == "" {
		return fmt.Errorf("bootDir is empty")
	}
	resolved, err := filepath.EvalSymlinks(bootDir)
	if err != nil {
		// Fall back to the absolute path if EvalSymlinks fails (e.g. the
		// bootDir was just created and intermediate symlinks are racy).
		// claude will still match if no resolution is needed.
		abs, absErr := filepath.Abs(bootDir)
		if absErr != nil {
			return fmt.Errorf("resolve bootDir: %w (also: %v)", err, absErr)
		}
		resolved = abs
	}

	cfgPath := filepath.Join(homeDir, ".claude.json")

	cfg := map[string]any{}
	if raw, err := os.ReadFile(cfgPath); err == nil {
		if uerr := json.Unmarshal(raw, &cfg); uerr != nil {
			return fmt.Errorf("parse %s: %w", cfgPath, uerr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", cfgPath, err)
	}

	// Distinguish absent vs. wrong-shape: if `projects` is present but is
	// not a JSON object, refuse rather than silently discard. Same goes
	// for an existing per-path entry. Mirrors the don't-overwrite-malformed-
	// config invariant documented above.
	var projects map[string]any
	if raw, ok := cfg["projects"]; ok {
		m, mok := raw.(map[string]any)
		if !mok {
			return fmt.Errorf("%s: top-level `projects` is not a JSON object (%T) — refusing to overwrite", cfgPath, raw)
		}
		projects = m
	} else {
		projects = map[string]any{}
		cfg["projects"] = projects
	}

	// If the path already has an entry, preserve any other keys (e.g.
	// allowedTools, lastSessionId) that may have been written by claude
	// itself on a prior trusted run. We only assert the trust fields.
	var entry map[string]any
	if raw, ok := projects[resolved]; ok {
		m, mok := raw.(map[string]any)
		if !mok {
			return fmt.Errorf("%s: projects[%q] is not a JSON object (%T) — refusing to overwrite", cfgPath, resolved, raw)
		}
		entry = m
	} else {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true
	projects[resolved] = entry

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", cfgPath, err)
	}

	tmp, err := os.CreateTemp(homeDir, ".claude.json.seed-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", cfgPath, err)
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()
	// Verify the full payload is persisted before renaming. *os.File.Write
	// is documented to return a short-write error when n < len(p), but we
	// double-check explicitly: io.Writer's contract permits short writes
	// without errors, and a future swap of the temp-file backend (e.g. an
	// io.Writer wrapper for retry/throttle) shouldn't quietly drop bytes.
	n, err := tmp.Write(out)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if n != len(out) {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: short write %d of %d bytes", tmpName, n, len(out))
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, cfgPath); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, cfgPath, err)
	}
	cleanupTmp = false
	return nil
}

// muxEntry captures the planted Mux-stdio MCP entry inputs in a single
// value so the per-provider renderers can pass it through cleanly. Empty
// Command means "no Mux entry" (back-compat path); the renderers omit
// the entry entirely in that case.
//
// Args / Env mirror PlantContext.MuxArgs / PlantContext.MuxEnv with the
// same semantics — see PlantContext doc comments for shape and
// motivation.
type muxEntry struct {
	Command string
	Args    []string
	Env     []string
}

// present reports whether the entry should be emitted into the planted
// MCP config. False = back-compat path (loopback-only).
func (m muxEntry) present() bool {
	return m.Command != ""
}

// muxEntryFromContext extracts a muxEntry from a PlantContext. Pure
// translation; no validation. Empty Command propagates through and the
// renderers skip the entry entry-side.
func muxEntryFromContext(ctx PlantContext) muxEntry {
	return muxEntry{
		Command: ctx.MuxCommand,
		Args:    ctx.MuxArgs,
		Env:     ctx.MuxEnv,
	}
}

// muxEnvMap converts the KEY=VALUE entries from PlantContext.MuxEnv into
// a JSON-object-shaped map. Both claude (~/.claude.json) and opencode
// (opencode.json) expect an object-shaped env on stdio MCP entries, so
// this helper is shared. Entries without a "=" land as ("", value)
// which the spawned CLI is free to reject; better than silently
// dropping the operator's intent.
func muxEnvMap(env []string) map[string]any {
	if len(env) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(env))
	for _, kv := range env {
		key := kv
		val := ""
		if idx := indexEq(kv); idx >= 0 {
			key = kv[:idx]
			val = kv[idx+1:]
		}
		out[key] = val
	}
	return out
}

// indexEq returns the index of the first '=' in s, or -1 if absent.
// Inlined to avoid pulling strings just for this.
func indexEq(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return i
		}
	}
	return -1
}

// renderMCPJSON renders the planted .mcp.json content for claude (and,
// historically, codex via the shared renderer). The shape always
// declares `mcpServers` as a record. When loopbackURL is non-empty, a
// "loopback" HTTP entry is emitted; when mux.present() is true, a
// second "mux" stdio entry is emitted alongside.
//
// Branch matrix:
//
//	loopback="", mux=zero                     → {"mcpServers":{}} (legacy empty)
//	loopback set, mux=zero                    → loopback-only HTTP entry (legacy populated)
//	loopback="", mux set                      → mux-only stdio entry (rare)
//	loopback set, mux set                     → both entries side-by-side (CW-20260510-0110)
//
// Bare-mode validation requires an explicit transport discriminator
// (`type`) for non-stdio servers; without `type`, the validator defaults
// to the stdio shape and rejects with `command: expected string,
// received undefined` (probed empirically against claude 2.1.137).
// `type: "http"` matches the `claude mcp add --transport http` CLI
// keyword and is accepted by both bare-mode strict validation and
// non-bare auto-discovery. The stdio shape uses `type: "stdio"` (the
// canonical user-shell shape from ~/.claude.json — verified against the
// installed claude 2.1.x revision).
func renderMCPJSON(loopbackURL string, mux muxEntry) string {
	servers := map[string]any{}
	if loopbackURL != "" {
		servers["loopback"] = map[string]any{
			"type": "http",
			"url":  loopbackURL,
		}
	}
	if mux.present() {
		// Args is nil-safe: json.Marshal emits `null` for a nil slice
		// vs `[]` for an empty slice. Coerce nil→empty so the planted
		// entry is consistent (downstream MCP clients handle either,
		// but operators reading the file expect `[]`).
		args := mux.Args
		if args == nil {
			args = []string{}
		}
		servers["mux"] = map[string]any{
			"type":    "stdio",
			"command": mux.Command,
			"args":    args,
			"env":     muxEnvMap(mux.Env),
		}
	}
	if len(servers) == 0 {
		// Empty loopback + no Mux — emit minimal valid MCP config (empty
		// servers map). `{}` works for auto-discovery (non-bare) but
		// fails claude's schema validation when referenced via
		// --mcp-config (bare mode), which requires `mcpServers` to be
		// a record. Emitting `{"mcpServers":{}}` is valid for both.
		return `{"mcpServers":{}}` + "\n"
	}
	cfg := map[string]any{"mcpServers": servers}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}
