//go:build !windows

package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestClaudeBootDirSpec_TrustPreAccept_Smoke is a real-spawn integration
// test for the v0.8.2 trust pre-acceptance fix.
//
// Surfaced incident (clockwork S2.5 plan-execute smoke retry, 2026-05-08
// post-v0.8.1): the long-lived PTY claude spawn no longer dies on arg
// validation, but instead stalls indefinitely on the first-run workspace
// trust dialog ("Quick safety check: Is this a project you created or
// one you trust?"). Per `claude --help`, the dialog auto-skips only in
// non-interactive mode (-p / piped stdout). PTY = TTY = dialog fires.
// `--dangerously-skip-permissions` covers per-tool permission checks, not
// this gate.
//
// Probe results (see implementer-report under
// agent-workspaces/execution/go-providers/2026-05-08-claude-pty-trust/):
// trust state lives in `~/.claude.json`'s `projects[<realpath(cwd)>].
// hasTrustDialogAccepted` field. Per-cwd `.claude/settings.json` does
// NOT honor any trust field (probed: hasTrustDialogAccepted,
// trustDialogAccepted, trusted, workspaceTrust — all leave the dialog
// firing). Path keying must use realpath because claude resolves cwd via
// EvalSymlinks on macOS (/var/folders → /private/var/folders).
//
// What this test asserts:
//   - Plant the lib's BootDirSpec into a fresh tempdir, populating
//     PlantContext.BootDir so the .claude/settings.json Render closure
//     fires the seedClaudeWorkspaceTrust side effect.
//   - Spawn claude in PTY mode (NewClaudeAdapterDevPTY args).
//   - Read PTY output for ~4s; assert the trust dialog sentinel does NOT
//     appear (verified to fire reliably in <1s in baseline runs).
//
// Gated on CLAUDE_PTY_SMOKE=1; skips when the claude binary is not on
// PATH so CI doesn't auto-run it. Skipping keeps `go test ./...` clean
// for contributors without a working claude install.
//
// Best-effort cleanup: removes the projects[bootDir] entry the test
// added, so contributor ~/.claude.json doesn't accumulate stale entries
// across smoke runs.
func TestClaudeBootDirSpec_TrustPreAccept_Smoke(t *testing.T) {
	if os.Getenv("CLAUDE_PTY_SMOKE") != "1" {
		t.Skip("set CLAUDE_PTY_SMOKE=1 to run this real-spawn smoke test")
	}

	a := NewClaudeAdapterDevPTY()
	binary, ok := a.Detect()
	if !ok {
		t.Skip("claude binary not found on PATH")
	}

	bootDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(bootDir)
	if err != nil {
		resolved = bootDir
	}

	// Plant the spec into bootDir, exercising the lib's PlantedFiles loop
	// the way clockwork does — this is the path that fires the trust seed.
	spec := a.BootDirSpec()
	pctx := PlantContext{
		SystemPrompt: "You are a smoke test agent. Respond exactly with the literal text TEST_OK_2026 and nothing else.",
		BootContent:  "Read @./CLAUDE.md and respond with the exact text from your system prompt.",
		AgentName:    "smoke",
		BootDir:      bootDir,
	}
	for _, pf := range spec.PlantedFiles {
		path := filepath.Join(bootDir, pf.RelPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if pf.Render == nil {
			continue
		}
		content, rerr := pf.Render(pctx)
		if rerr != nil {
			t.Fatalf("render %s: %v", pf.RelPath, rerr)
		}
		if werr := os.WriteFile(path, []byte(content), 0o644); werr != nil {
			t.Fatalf("write %s: %v", path, werr)
		}
	}

	// Best-effort cleanup of the projects[bootDir] entry so the
	// developer's ~/.claude.json doesn't accumulate stale paths.
	t.Cleanup(func() {
		removeBootDirTrustEntry(t, resolved)
	})

	args := a.BuildArgs("", "", "")
	cmd := exec.Command(binary, args...) //nolint:gosec
	cmd.Dir = bootDir
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}

	var bufMu sync.Mutex
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&safeBuf{buf: &buf, mu: &bufMu}, ptmx)
	}()

	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		close(exited)
	}()

	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-exited
		}
	})

	// PTY cursor-rendered output strips spaces between glyphs when ANSI
	// is naïvely stripped. Check whitespace-stripped sentinels — these
	// are the literal substrings observed in the surfaced incident's
	// session.log capture.
	dialogSentinels := []string{
		"Quicksafetycheck",
		"Isthisaprojectyoucreated",
		"trustthisfolder",
		"Yes,Itrustthisfolder",
	}

	deadline := time.After(4 * time.Second)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			// No dialog detected within the window — pass.
			return
		case err := <-exited:
			t.Fatalf("claude PTY spawn died inside trust-window: %v", err)
		case <-tick.C:
			bufMu.Lock()
			s := buf.String()
			bufMu.Unlock()
			compact := stripWhitespaceForSmokeTest(s)
			for _, needle := range dialogSentinels {
				if strings.Contains(compact, needle) {
					t.Fatalf("trust dialog fired (sentinel %q in PTY output) — fix did not pre-accept trust\n--- PTY output ---\n%s\n---", needle, truncateForSmokeTest(s, 4000))
				}
			}
		}
	}
}

type safeBuf struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func stripWhitespaceForSmokeTest(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func truncateForSmokeTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

// removeBootDirTrustEntry is best-effort cleanup for the smoke test.
// Removes projects[<resolved>] from ~/.claude.json without touching other
// keys. Logs but does not fail on errors — the entry is harmless if left
// behind (references a non-existent tempdir path).
func removeBootDirTrustEntry(t *testing.T, resolved string) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Logf("cleanup: home dir: %v", err)
		return
	}
	cfgPath := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Logf("cleanup: read %s: %v", cfgPath, err)
		return
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Logf("cleanup: parse %s: %v", cfgPath, err)
		return
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		return
	}
	if _, ok := projects[resolved]; !ok {
		return
	}
	delete(projects, resolved)

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Logf("cleanup: marshal: %v", err)
		return
	}
	tmp, err := os.CreateTemp(home, ".claude.json.cleanup-*")
	if err != nil {
		t.Logf("cleanup: temp: %v", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		t.Logf("cleanup: write temp: %v", err)
		return
	}
	_ = tmp.Chmod(0o600)
	_ = tmp.Close()
	if err := os.Rename(tmpName, cfgPath); err != nil {
		_ = os.Remove(tmpName)
		t.Logf("cleanup: rename: %v", err)
	}
}
