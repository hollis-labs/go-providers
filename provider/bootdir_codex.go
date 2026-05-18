package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BootDirSpec for the OpenAI Codex CLI.
//
// Layout:
//
//	<bootDir>/
//	├── AGENTS.md           # system context (auto-loaded by Codex on cwd)
//	├── boot.md             # task kickoff content
//	├── config.toml         # codex config (approval/sandbox policy + MCP
//	                        # loopback) — load-bearing
//	├── auth.json           # ChatGPT/API auth, copied from user's $CODEX_HOME
//	└── .mcp.json           # legacy claude-shape sidecar — NOT read by codex,
//	                        # kept for cross-tool inspection sanity (analogous
//	                        # to opencode's bootdir keeping the same plant)
//
// Spawn invariants:
//   - cwd = bootDir
//   - CODEX_HOME={{.BootDir}} env amendment isolates this dir's config.toml
//     and auth.json from the user's globals (~/.codex/). Without CODEX_HOME,
//     codex merges per-spawn config with ~/.codex/config.toml AND uses the
//     user's ~/.codex/auth.json — both bleed-throughs we want to avoid for
//     a per-task isolated boot.
//   - Project access is granted via codex's `--cd <dir>` flag in exec
//     mode. In app-server mode `--cd` is rejected by the daemon
//     (codex 0.130.0 exits 2); the consumer runtime grants project
//     access via JSON-RPC `thread/start` parameters instead, and
//     BootDirSpec emits ProjectDirArg = "" for that branch.
//   - AGENTS.md is auto-loaded by the CLI as the system prompt (when codex
//     finds it at cwd).
//
// MCP config: codex stores MCP servers in TOML at $CODEX_HOME/config.toml
// under [mcp_servers.<name>] blocks. The shape is:
//
//	[mcp_servers.loopback]
//	url = "http://127.0.0.1:NNNN/mcp"
//
// for streamable HTTP servers (verified against codex-cli 0.130.0). This is
// fundamentally different from claude's `.mcp.json` shape; the legacy
// `.mcp.json` file is left in the plant for cross-tool inspection but is
// IGNORED by codex.
//
// Auth: codex stores auth in $CODEX_HOME/auth.json (ChatGPT login or API
// key). When CODEX_HOME points at the bootdir, codex looks ONLY at
// <bootDir>/auth.json — so we copy the user's ~/.codex/auth.json into the
// bootdir at plant time. If the user isn't logged in (no source auth.json),
// the Render returns "" and the bootdir gets an empty auth.json file;
// codex will then fail at dispatch time with "Not logged in" which surfaces
// correctly through the stderr sidecar.
//
// File-mode plumbing: auth.json (OAuth tokens / API keys), config.toml
// (loopback URL), and .mcp.json (loopback URL sidecar) all carry per-task
// secret-ish content. They are declared with PlantedFile.Mode = 0o600 so
// the lib spec — not the caller — owns the policy. Apps that honor
// PlantedFile.Mode (with a 0o644 fallback when zero) automatically pick
// up the stricter perms.
//
// Mux: PlantContext carries optional MuxCommand/Args/Env fields populated
// when the caller wants spawned agents to also reach a Mux-aggregated MCP
// server (Vanta/Clockwork/Cerberus). Unlike Claude, Codex's load-bearing
// config is $CODEX_HOME/config.toml, so the mux entry must be emitted there
// as a stdio server block. The legacy .mcp.json sidecar is still planted for
// cross-tool inspection parity, but Codex itself ignores that file.
func (a *CodexAdapter) BootDirSpec() BootDirSpec {
	// In app-server mode the JSON-RPC daemon rejects `--cd <dir>` (codex
	// 0.130.0 exits 2 with "unexpected argument '--cd' found"). Project
	// access is granted via JSON-RPC `thread/start` parameters at the
	// runtime layer instead. Suppress the flag here to keep the long-lived
	// adapter spawnable; exec mode keeps the existing behavior.
	projectDirArg := "--cd {{.ProjectDir}}"
	if a.Mode == "app-server" {
		projectDirArg = ""
	}
	return BootDirSpec{
		PlantedFiles: []PlantedFile{
			{
				RelPath: "AGENTS.md",
				Render: func(ctx PlantContext) (string, error) {
					return AgentsMD(AgentInfo{
						Name:         ctx.AgentName,
						SystemPrompt: ctx.SystemPrompt,
					}, ctx.MCPLoopbackURL), nil
				},
			},
			{
				RelPath: "boot.md",
				Render: func(ctx PlantContext) (string, error) {
					return ctx.BootContent, nil
				},
			},
			{
				RelPath: "config.toml",
				Render: func(ctx PlantContext) (string, error) {
					approval, sandbox, err := resolveCodexExecPolicy(a.ApprovalPolicy, a.SandboxMode)
					if err != nil {
						return "", err
					}
					base := renderCodexConfigTOML(approval, sandbox, a.WritableRoots, ctx.MCPLoopbackURL, muxEntryFromContext(ctx))
					extra, err := renderCodexMCPServers(ctx.MCPServers)
					if err != nil {
						return "", err
					}
					return base + extra, nil
				},
				// Mode 0o600: config.toml embeds the per-task MCP loopback
				// URL. Treat as secret-ish (matches the .mcp.json policy).
				Mode: 0o600,
			},
			{
				RelPath: "auth.json",
				Render: func(ctx PlantContext) (string, error) {
					// Read the user's ~/.codex/auth.json (or $CODEX_HOME/auth.json
					// if CODEX_HOME is set in the parent env) and return its
					// content. The caller's WriteFile honors PlantedFile.Mode
					// (0o600 below) so the planted auth.json is not world-
					// readable.
					//
					// Empty content + (false, nil) if the user isn't logged in —
					// codex will surface "Not logged in" at dispatch time. A
					// non-NotExist read error (e.g. permission denied) bubbles
					// up so the operator sees an actionable message instead of
					// a misleading "Not logged in" downstream.
					content, _, err := readCodexAuthSource()
					if err != nil {
						return "", err
					}
					return content, nil
				},
				// Mode 0o600: auth.json carries OAuth tokens / API keys.
				Mode: 0o600,
			},
			{
				RelPath: ".mcp.json",
				Render: func(ctx PlantContext) (string, error) {
					// Legacy claude-shape sidecar. Codex does NOT read this
					// file (codex reads config.toml above via CODEX_HOME).
					// Kept for cross-tool inspection sanity (operator probing
					// the bootdir manually, parity with claude/opencode plant
					// shapes). Mux entry from PlantContext is included for
					// the same parity reason; it has no effect on codex itself
					// (see top-of-file Mux note).
					return renderMCPJSON(ctx.MCPLoopbackURL, muxEntryFromContext(ctx)), nil
				},
				// Mode 0o600: mirrors config.toml — the loopback URL is the
				// same shape and the same sensitivity.
				Mode: 0o600,
			},
		},
		EnvAmendments: []string{"CODEX_HOME={{.BootDir}}"},
		CwdPreference: CwdBootDir,
		ProjectDirArg: projectDirArg,
		Notes:         "codex MCP config lives in config.toml under [mcp_servers.<name>]; .mcp.json is legacy sidecar only. CODEX_HOME isolates per-task config + auth from ~/.codex/.",
	}
}

// codexApprovalPolicies is codex's `approval_policy` config vocabulary.
var codexApprovalPolicies = map[string]bool{
	"untrusted":  true,
	"on-failure": true,
	"on-request": true,
	"never":      true,
}

// codexSandboxModes is codex's `sandbox_mode` config vocabulary.
var codexSandboxModes = map[string]bool{
	"read-only":          true,
	"workspace-write":    true,
	"danger-full-access": true,
}

// resolveCodexExecPolicy validates a CodexAdapter's ApprovalPolicy / SandboxMode
// and applies the headless-safe defaults for empty values. The defaults are
// "never" / "workspace-write" — NOT codex's own interactive defaults — because
// a BootDirSpec materializes a headless per-task boot with no human at a TTY;
// see the CodexAdapter field godoc. An unrecognized value is an error,
// surfaced through the config.toml PlantedFile Render.
func resolveCodexExecPolicy(approvalPolicy, sandboxMode string) (approval, sandbox string, err error) {
	approval = approvalPolicy
	if approval == "" {
		approval = "never"
	}
	if !codexApprovalPolicies[approval] {
		return "", "", fmt.Errorf("invalid CodexAdapter.ApprovalPolicy %q (want one of: untrusted, on-failure, on-request, never)", approvalPolicy)
	}
	sandbox = sandboxMode
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	if !codexSandboxModes[sandbox] {
		return "", "", fmt.Errorf("invalid CodexAdapter.SandboxMode %q (want one of: read-only, workspace-write, danger-full-access)", sandboxMode)
	}
	return approval, sandbox, nil
}

// renderCodexConfigTOML emits the full codex config.toml: the approval/sandbox
// policy header, the optional `[sandbox_workspace_write]` table, and the
// per-task MCP server blocks.
//
// The policy header (`approval_policy` / `sandbox_mode`) is ALWAYS emitted —
// it is the fix for the headless-codex deadlock, where a codex with no planted
// approval policy falls back to its interactive default and blocks forever
// waiting for an approval no one can give. Top-level TOML keys must precede
// any `[table]` header, so the policy lines come first.
//
// writableRoots, when non-empty, emits a `[sandbox_workspace_write]` table
// with a `writable_roots` array — the directories codex's "workspace-write"
// sandbox may write to beyond the boot dir cwd. It is emitted right after the
// policy header (before the MCP tables) for readability; codex only honors it
// under sandbox_mode "workspace-write". Empty → the table is omitted and the
// rendered config is byte-identical to before this parameter existed.
//
// The loopback transport is "streamable_http" in codex's terminology and is
// configured via `url = "..."`. Stdio servers use `command = "..."` plus
// `args = [...]` and optional `[mcp_servers.<name>.env]` key/value pairs.
func renderCodexConfigTOML(approvalPolicy, sandboxMode string, writableRoots []string, loopbackURL string, mux muxEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "approval_policy = %q\n", approvalPolicy)
	fmt.Fprintf(&b, "sandbox_mode = %q\n", sandboxMode)
	if len(writableRoots) > 0 {
		b.WriteString("\n[sandbox_workspace_write]\n")
		fmt.Fprintf(&b, "writable_roots = %s\n", tomlStringArray(writableRoots))
	}
	if loopbackURL != "" {
		b.WriteString("\n[mcp_servers.loopback]\n")
		fmt.Fprintf(&b, "url = %q\n", loopbackURL)
	}
	if mux.present() {
		b.WriteString("\n[mcp_servers.mux]\n")
		fmt.Fprintf(&b, "command = %q\n", mux.Command)
		fmt.Fprintf(&b, "args = %s\n", tomlStringArray(mux.Args))
		if len(mux.Env) > 0 {
			b.WriteString("\n[mcp_servers.mux.env]\n")
			for _, kv := range mux.Env {
				key, value, ok := strings.Cut(kv, "=")
				if !ok {
					value = ""
				}
				if key == "" {
					continue
				}
				fmt.Fprintf(&b, "%q = %q\n", key, value)
			}
		}
	}
	return b.String()
}

func tomlStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// codexReservedMCPNames are the [mcp_servers.<name>] keys renderCodexConfigTOML
// already emits from MCPLoopbackURL / the Mux* fields. An MCPServerSpec may
// not reuse one — a duplicate TOML table is invalid and codex rejects it.
var codexReservedMCPNames = map[string]bool{"loopback": true, "mux": true}

// validMCPServerName reports whether name is a safe bare TOML table key:
// non-empty and limited to [A-Za-z0-9_-]. Anything else would need TOML
// quoting and is rejected up front to keep the planted config simple.
func validMCPServerName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// renderCodexMCPServers emits a [mcp_servers.<name>] block for each extra
// MCP server in PlantContext.MCPServers, in slice order, for appending after
// renderCodexConfigTOML's output (top-level keys already precede all tables,
// so appending more tables stays valid TOML).
//
// codex has no .mcp.json sidecar — a consumer's own MCP server must be
// co-rendered into the single config.toml. An invalid spec (empty/reserved/
// non-simple name, duplicate name, or not exactly one transport) returns an
// error so the misconfiguration fails the config.toml Render rather than
// planting a broken file.
func renderCodexMCPServers(servers []MCPServerSpec) (string, error) {
	var b strings.Builder
	seen := make(map[string]bool, len(servers))
	for _, s := range servers {
		switch {
		case !validMCPServerName(s.Name):
			return "", fmt.Errorf("MCPServerSpec: invalid name %q (want non-empty [A-Za-z0-9_-]+)", s.Name)
		case codexReservedMCPNames[s.Name]:
			return "", fmt.Errorf("MCPServerSpec %q: name is reserved — loopback/mux come from MCPLoopbackURL/Mux*", s.Name)
		case seen[s.Name]:
			return "", fmt.Errorf("MCPServerSpec %q: duplicate name", s.Name)
		}
		seen[s.Name] = true

		hasHTTP, hasStdio := s.HTTPURL != "", s.Command != ""
		if hasHTTP == hasStdio {
			return "", fmt.Errorf("MCPServerSpec %q: set exactly one of HTTPURL or Command", s.Name)
		}

		fmt.Fprintf(&b, "\n[mcp_servers.%s]\n", s.Name)
		if hasHTTP {
			fmt.Fprintf(&b, "url = %q\n", s.HTTPURL)
			continue
		}
		fmt.Fprintf(&b, "command = %q\n", s.Command)
		fmt.Fprintf(&b, "args = %s\n", tomlStringArray(s.Args))
		if len(s.Env) > 0 {
			fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", s.Name)
			for _, kv := range s.Env {
				key, value, ok := strings.Cut(kv, "=")
				if !ok {
					value = ""
				}
				if key == "" {
					continue
				}
				fmt.Fprintf(&b, "%q = %q\n", key, value)
			}
		}
	}
	return b.String(), nil
}

// readCodexAuthSource reads the user's codex auth.json bytes for replication
// into the bootdir. Honors $CODEX_HOME (matches codex's own discovery rule),
// falls back to ~/.codex/auth.json.
//
// Return values:
//   - (content, true,  nil) on success
//   - ("",      false, nil) when the source doesn't exist (best-effort silent
//     path: the bootdir gets an empty auth.json and codex surfaces "Not logged
//     in" at dispatch time)
//   - ("",      false, err) for other read errors (permission denied, IO
//     errors, etc.) so callers can surface actionable messages instead of
//     swallowing the cause and producing a misleading "Not logged in"
//     downstream
//
// The auth-seeding contract is best-effort for the missing-file case
// (downstream dispatch is well-handled by the stderr sidecar) but NOT for
// the unreadable-file case (a real misconfiguration the operator needs to
// see).
func readCodexAuthSource() (string, bool, error) {
	src := codexAuthSourcePath()
	if src == "" {
		return "", false, nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read codex auth.json at %s: %w", src, err)
	}
	return string(b), true, nil
}

// codexAuthSourcePath resolves the user's codex auth.json path, honoring
// $CODEX_HOME if set, falling back to ~/.codex/auth.json.
func codexAuthSourcePath() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}
