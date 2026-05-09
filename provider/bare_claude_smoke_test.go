//go:build !windows

package provider

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeAdapter_BareSpawn_Smoke is a real-spawn regression test for the
// v0.9.0 bare-mode work. It plants a minimal CLAUDE.md + .mcp.json into a
// tempdir and spawns claude with the bare-mode arg shape, asserting:
//
//   - exit 0 inside a 30s window
//   - stream-json output (at minimum a `system`/`init` event present in stdout)
//   - response contains the prompted sentinel "TEST_OK_BARE"
//   - no `Quicksafetycheck` / trust-dialog sentinel in output (bare skips it)
//   - no `remoteControl` operator-config sentinel in output
//
// Gated on CLAUDE_BARE_SMOKE=1 so CI doesn't auto-run it. Skips when the
// claude binary is not on PATH or ANTHROPIC_API_KEY is not set. Bare auth
// can also flow via an apiKeyHelper passed through --settings, but this
// smoke test does not plant SettingsPath, so it requires the env-var path.
// (OAuth and keychain are never read in bare mode regardless of which auth
// route is used.)
//
// Note: this is a real network-egress test against the Anthropic API.
// Token cost is one short turn ("respond TEST_OK_BARE").
func TestClaudeAdapter_BareSpawn_Smoke(t *testing.T) {
	if os.Getenv("CLAUDE_BARE_SMOKE") != "1" {
		t.Skip("set CLAUDE_BARE_SMOKE=1 to run this real-spawn smoke test")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; this smoke test takes the env-var auth path (apiKeyHelper via --settings is also valid for bare mode but isn't planted here)")
	}

	a := NewClaudeAdapterDevBare()
	binary, ok := a.Detect()
	if !ok {
		t.Skip("claude binary not found on PATH")
	}

	bootDir := t.TempDir()

	// Plant a minimal CLAUDE.md (system-prompt content) and an empty
	// .mcp.json (no MCP servers). These mirror what BootDirSpec would
	// plant; we don't go through the full PlantedFiles iteration here
	// because the smoke test is about arg shape + spawn behavior, not
	// the planting layer.
	claudeMD := []byte("Be terse. When asked to respond TEST_OK_BARE, reply with exactly TEST_OK_BARE.\n")
	if err := os.WriteFile(filepath.Join(bootDir, "CLAUDE.md"), claudeMD, 0o644); err != nil {
		t.Fatalf("plant CLAUDE.md: %v", err)
	}
	// Empty {} fails claude's MCP schema validation (mcpServers required).
	// renderMCPJSON("") emits the same content via the BootDirSpec path.
	mcpJSON := []byte(`{"mcpServers":{}}` + "\n")
	if err := os.WriteFile(filepath.Join(bootDir, ".mcp.json"), mcpJSON, 0o644); err != nil {
		t.Fatalf("plant .mcp.json: %v", err)
	}

	// Populate adapter bare-mode fields from the planted layout.
	inj := a.BareInjectionPaths(bootDir, "")
	a.MCPConfigPath = inj.MCPConfigPath
	a.AppendSystemPromptFile = inj.AppendSystemPromptFile
	// Skip SettingsPath — empty .claude/settings.json isn't planted here
	// and apiKeyHelper isn't needed (env-var auth).

	args := a.BuildArgs("respond TEST_OK_BARE", "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // adapter-sourced
	cmd.Dir = bootDir
	// Inherit env (ANTHROPIC_API_KEY in particular).
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.Logf("spawning: %s %s", binary, strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(exited)
	}()

	select {
	case err := <-exited:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Fatalf("claude --bare exited %d\nstdout:\n%s\nstderr:\n%s",
					exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			t.Fatalf("claude --bare wait error: %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String())
		}
	case <-ctx.Done():
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		t.Fatalf("claude --bare timed out after 30s\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := stdout.String()
	combined := out + "\n" + stderr.String()

	// Stream-json output should be present (init system event marker).
	if !strings.Contains(out, `"type":"system"`) {
		t.Errorf("expected stream-json system event in stdout; got:\n%s", out)
	}

	// Response should contain the sentinel.
	if !strings.Contains(out, "TEST_OK_BARE") {
		t.Errorf("expected TEST_OK_BARE in response; got:\n%s", out)
	}

	// Trust-dialog sentinels must NOT appear (bare bypasses the dialog).
	for _, marker := range []string{"Quicksafetycheck", "Quick safety check", "Is this a project you created", "trust this folder"} {
		if strings.Contains(combined, marker) {
			t.Errorf("trust-dialog marker %q leaked into bare-mode output: %s", marker, combined)
		}
	}

	// Operator-config bleed sentinels must NOT appear.
	for _, marker := range []string{"remoteControl", "remote-control"} {
		if strings.Contains(combined, marker) {
			t.Errorf("operator-config marker %q leaked into bare-mode output: %s", marker, combined)
		}
	}
}

// TestClaudeAdapter_BareSpawn_PopulatedMCP_Smoke is a real-spawn regression
// test for the v0.9.1 bare-mode MCP-shape fix (CW-20260509-0003). It plants a
// .mcp.json containing a populated HTTP loopback entry via BootDirSpec's
// renderer (renderMCPJSON("http://...")), spawns claude --bare, and asserts:
//
//   - exit 0 inside a 30s window (validation must not reject the planted shape)
//   - stream-json output present
//   - response contains the prompted sentinel "TEST_OK_BARE"
//   - the v0.9.0 validator-failure sentinels MUST NOT appear in stderr:
//     "Invalid MCP configuration", "command: expected string, received undefined"
//
// The loopback URL points to a port we don't listen on. Claude eagerly probes
// MCP servers at session init but treats a failed connection as non-fatal
// (init event reports `mcp_servers:[{name:"loopback",status:"failed"}]` and
// the session continues to API call). The prompt is plain text — no tool
// invocation is required, so the unreachable MCP server doesn't gate exit 0.
//
// Gated on CLAUDE_BARE_SMOKE=1 so CI doesn't auto-run it. Skips when the
// claude binary is not on PATH or ANTHROPIC_API_KEY is not set.
func TestClaudeAdapter_BareSpawn_PopulatedMCP_Smoke(t *testing.T) {
	if os.Getenv("CLAUDE_BARE_SMOKE") != "1" {
		t.Skip("set CLAUDE_BARE_SMOKE=1 to run this real-spawn smoke test")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; bare smoke takes the env-var auth path")
	}

	a := NewClaudeAdapterDevBare()
	binary, ok := a.Detect()
	if !ok {
		t.Skip("claude binary not found on PATH")
	}

	bootDir := t.TempDir()
	projectDir := t.TempDir()

	// Plant the full BootDirSpec layout end-to-end so we exercise
	// renderMCPJSON's populated branch via PlantedFile.Render rather than
	// hand-writing the .mcp.json content. The loopback port is intentionally
	// chosen to be unreachable; claude treats MCP connect failure as
	// non-fatal during session init.
	spec := a.BootDirSpec()
	pctx := PlantContext{
		SystemPrompt:   "Be terse. When asked to respond TEST_OK_BARE, reply with exactly TEST_OK_BARE.",
		BootContent:    "ignored in this smoke test",
		AgentName:      "smoke",
		MCPLoopbackURL: "http://127.0.0.1:65530/mcp",
		BootDir:        bootDir,
		ProjectDir:     projectDir,
	}
	for _, pf := range spec.PlantedFiles {
		content, err := pf.Render(pctx)
		if err != nil {
			t.Fatalf("render planted file %q: %v", pf.RelPath, err)
		}
		dst := filepath.Join(bootDir, pf.RelPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", pf.RelPath, err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			t.Fatalf("write planted file %q: %v", pf.RelPath, err)
		}
	}

	// Note: BootDirSpec's .claude/settings.json render closure writes a
	// trust-seed entry into ~/.claude.json keyed by realpath(bootDir).
	// Per the v0.8.2 changelog, stale entries pointing at removed bootdirs
	// are harmless — the bootdir is a tempdir torn down by t.TempDir().
	// No cleanup is performed here; it would duplicate the
	// TestClaudeBootDirSpec_TrustPreAccept_Smoke pattern with no benefit.

	inj := a.BareInjectionPaths(bootDir, projectDir)
	a.MCPConfigPath = inj.MCPConfigPath
	a.AppendSystemPromptFile = inj.AppendSystemPromptFile
	a.SettingsPath = inj.SettingsPath
	a.ProjectDir = inj.ProjectDir

	args := a.BuildArgs("respond TEST_OK_BARE", "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // adapter-sourced
	cmd.Dir = bootDir
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.Logf("spawning: %s %s", binary, strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}

	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(exited)
	}()

	select {
	case err := <-exited:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Fatalf("claude --bare (populated MCP) exited %d\nstdout:\n%s\nstderr:\n%s",
					exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			t.Fatalf("claude --bare wait error: %v\nstdout:\n%s\nstderr:\n%s",
				err, stdout.String(), stderr.String())
		}
	case <-ctx.Done():
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		t.Fatalf("claude --bare timed out after 30s\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}

	out := stdout.String()
	combined := out + "\n" + stderr.String()

	// The v0.9.0 validator-failure sentinels must NOT appear — this is the
	// load-bearing assertion for CW-20260509-0003.
	for _, marker := range []string{
		"Invalid MCP configuration",
		"Invalid MCP server config",
		"command: expected string, received undefined",
	} {
		if strings.Contains(combined, marker) {
			t.Errorf("validator-failure marker %q leaked into populated-MCP bare-mode output: %s", marker, combined)
		}
	}

	if !strings.Contains(out, `"type":"system"`) {
		t.Errorf("expected stream-json system event in stdout; got:\n%s", out)
	}
	if !strings.Contains(out, "TEST_OK_BARE") {
		t.Errorf("expected TEST_OK_BARE in response; got:\n%s", out)
	}
}

