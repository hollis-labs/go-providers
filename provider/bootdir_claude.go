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
					return claudeSettingsStub(), nil
				},
			},
			{
				RelPath: ".mcp.json",
				Render: func(ctx PlantContext) (string, error) {
					return renderMCPJSON(ctx.MCPLoopbackURL), nil
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
	if ctx.MCPLoopbackURL != "" {
		b.WriteString("## MCP\n\nTools for this task are available via the MCP server configured in .mcp.json (")
		b.WriteString(ctx.MCPLoopbackURL)
		b.WriteString(").\n")
	}
	return b.String()
}

func claudeSettingsStub() string {
	// Minimal stub that prevents the CLI from reading the user's global
	// ~/.claude.json or ~/.claude/settings.json. Apps that need a
	// custom approvedTools list or mcpServers map can post-process
	// the planted file before spawn.
	stub := map[string]any{
		"mcpServers":    map[string]any{},
		"approvedTools": []string{},
	}
	out, _ := json.MarshalIndent(stub, "", "  ")
	return string(out) + "\n"
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

func renderMCPJSON(loopbackURL string) string {
	if loopbackURL == "" {
		// Empty loopback — emit a minimal valid MCP config (empty
		// servers map). `{}` works for auto-discovery (non-bare) but
		// fails claude's schema validation when referenced via
		// --mcp-config (bare mode), which requires `mcpServers` to be
		// a record. Emitting `{"mcpServers":{}}` is valid for both.
		return `{"mcpServers":{}}` + "\n"
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"loopback": map[string]any{
				"url": loopbackURL,
			},
		},
	}
	out, _ := json.MarshalIndent(cfg, "", "  ")
	return string(out) + "\n"
}
