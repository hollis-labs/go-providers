package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSeedClaudeWorkspaceTrust_NewConfig verifies that when ~/.claude.json
// does not exist, the helper creates it with just the projects map seeded.
func TestSeedClaudeWorkspaceTrust_NewConfig(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()

	if err := seedClaudeWorkspaceTrust(homeDir, bootDir); err != nil {
		t.Fatalf("seedClaudeWorkspaceTrust: %v", err)
	}

	cfgPath := filepath.Join(homeDir, ".claude.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read %s: %v", cfgPath, err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v", cfgPath, err)
	}
	projects, ok := cfg["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects not a map: %T", cfg["projects"])
	}
	resolved, _ := filepath.EvalSymlinks(bootDir)
	entry, ok := projects[resolved].(map[string]any)
	if !ok {
		t.Fatalf("projects[%s] not a map; full=%v", resolved, projects)
	}
	if got, _ := entry["hasTrustDialogAccepted"].(bool); !got {
		t.Errorf("hasTrustDialogAccepted: want true, got %v", entry["hasTrustDialogAccepted"])
	}
	if got, _ := entry["hasCompletedProjectOnboarding"].(bool); !got {
		t.Errorf("hasCompletedProjectOnboarding: want true, got %v", entry["hasCompletedProjectOnboarding"])
	}

	st, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat %s: %v", cfgPath, err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("permissions: want 0600, got %v", st.Mode().Perm())
	}
}

// TestSeedClaudeWorkspaceTrust_PreservesExistingKeys confirms the helper
// does not clobber unrelated top-level keys or other projects entries when
// adding a new bootdir entry. This matches the boot prompt's "narrow scope"
// mitigation: only seed the trust fields, don't touch other keys.
func TestSeedClaudeWorkspaceTrust_PreservesExistingKeys(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()
	cfgPath := filepath.Join(homeDir, ".claude.json")

	existing := map[string]any{
		"oauthAccount":  "user@example.com",
		"anonymousId":   "abc-123",
		"someOtherKey":  []any{"a", "b"},
		"projects": map[string]any{
			"/Users/me/repoA": map[string]any{
				"hasTrustDialogAccepted": true,
				"allowedTools":           []any{"Edit", "Bash"},
			},
		},
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(cfgPath, out, 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	if err := seedClaudeWorkspaceTrust(homeDir, bootDir); err != nil {
		t.Fatalf("seedClaudeWorkspaceTrust: %v", err)
	}

	raw, _ := os.ReadFile(cfgPath)
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg["oauthAccount"] != "user@example.com" {
		t.Errorf("oauthAccount clobbered: %v", cfg["oauthAccount"])
	}
	if cfg["anonymousId"] != "abc-123" {
		t.Errorf("anonymousId clobbered: %v", cfg["anonymousId"])
	}

	projects, _ := cfg["projects"].(map[string]any)
	repoA, _ := projects["/Users/me/repoA"].(map[string]any)
	if repoA == nil {
		t.Fatalf("existing projects entry dropped: %v", projects)
	}
	tools, _ := repoA["allowedTools"].([]any)
	if len(tools) != 2 {
		t.Errorf("repoA.allowedTools clobbered: %v", repoA["allowedTools"])
	}

	resolved, _ := filepath.EvalSymlinks(bootDir)
	if _, ok := projects[resolved].(map[string]any); !ok {
		t.Fatalf("bootdir entry not added: %v", projects)
	}
}

// TestSeedClaudeWorkspaceTrust_PreservesExistingProjectKeys confirms that
// re-seeding an existing projects entry preserves unrelated keys (e.g.
// allowedTools, lastSessionId that claude itself wrote on a prior trusted
// run) and only updates the trust fields.
func TestSeedClaudeWorkspaceTrust_PreservesExistingProjectKeys(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(bootDir)
	cfgPath := filepath.Join(homeDir, ".claude.json")

	existing := map[string]any{
		"projects": map[string]any{
			resolved: map[string]any{
				"hasTrustDialogAccepted": false, // Will be flipped to true.
				"allowedTools":           []any{"Edit"},
				"lastSessionId":          "abc-xyz",
			},
		},
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	_ = os.WriteFile(cfgPath, out, 0o600)

	if err := seedClaudeWorkspaceTrust(homeDir, bootDir); err != nil {
		t.Fatalf("seedClaudeWorkspaceTrust: %v", err)
	}

	raw, _ := os.ReadFile(cfgPath)
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	projects, _ := cfg["projects"].(map[string]any)
	entry, _ := projects[resolved].(map[string]any)
	if got, _ := entry["hasTrustDialogAccepted"].(bool); !got {
		t.Errorf("hasTrustDialogAccepted: want true, got %v", entry["hasTrustDialogAccepted"])
	}
	tools, _ := entry["allowedTools"].([]any)
	if len(tools) != 1 || tools[0] != "Edit" {
		t.Errorf("allowedTools clobbered: %v", entry["allowedTools"])
	}
	if entry["lastSessionId"] != "abc-xyz" {
		t.Errorf("lastSessionId clobbered: %v", entry["lastSessionId"])
	}
}

// TestSeedClaudeWorkspaceTrust_NonObjectProjectsErrors confirms that when
// `projects` is present but is not a JSON object (e.g. someone or some
// future claude version stores it as an array or string), the helper
// refuses to overwrite rather than silently discarding the field.
// Mirrors the don't-overwrite-malformed-config invariant for nested shape.
func TestSeedClaudeWorkspaceTrust_NonObjectProjectsErrors(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()
	cfgPath := filepath.Join(homeDir, ".claude.json")

	existing := map[string]any{
		"oauthAccount": "user@example.com",
		// Deliberately wrong shape — array instead of object.
		"projects": []any{"/some/path", "/other/path"},
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(cfgPath, out, 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	err := seedClaudeWorkspaceTrust(homeDir, bootDir)
	if err == nil {
		t.Fatal("expected error on non-object projects, got nil")
	}

	raw, _ := os.ReadFile(cfgPath)
	var rt map[string]any
	if perr := json.Unmarshal(raw, &rt); perr != nil {
		t.Fatalf("config corrupted by failed seed: %v", perr)
	}
	if rt["oauthAccount"] != "user@example.com" {
		t.Errorf("oauthAccount lost: %v", rt["oauthAccount"])
	}
	arr, _ := rt["projects"].([]any)
	if len(arr) != 2 {
		t.Errorf("projects array clobbered: %v", rt["projects"])
	}
}

// TestSeedClaudeWorkspaceTrust_NonObjectEntryErrors confirms that when
// `projects[<resolved>]` is present but not a JSON object (e.g. a string
// "trusted" or some future shape), the helper refuses to overwrite it.
func TestSeedClaudeWorkspaceTrust_NonObjectEntryErrors(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(bootDir)
	cfgPath := filepath.Join(homeDir, ".claude.json")

	existing := map[string]any{
		"projects": map[string]any{
			resolved: "trusted", // Wrong shape — string instead of object.
		},
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	_ = os.WriteFile(cfgPath, out, 0o600)

	err := seedClaudeWorkspaceTrust(homeDir, bootDir)
	if err == nil {
		t.Fatal("expected error on non-object entry, got nil")
	}

	raw, _ := os.ReadFile(cfgPath)
	var rt map[string]any
	_ = json.Unmarshal(raw, &rt)
	projects, _ := rt["projects"].(map[string]any)
	if projects[resolved] != "trusted" {
		t.Errorf("non-object entry clobbered: %v", projects[resolved])
	}
}

// TestSeedClaudeWorkspaceTrust_MalformedConfigErrors confirms a corrupt
// existing ~/.claude.json fails the helper rather than overwriting it.
// Otherwise an unrelated parse failure could nuke the user's config.
func TestSeedClaudeWorkspaceTrust_MalformedConfigErrors(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()
	cfgPath := filepath.Join(homeDir, ".claude.json")
	if err := os.WriteFile(cfgPath, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	err := seedClaudeWorkspaceTrust(homeDir, bootDir)
	if err == nil {
		t.Fatalf("expected error on malformed config, got nil")
	}

	// Original content must be preserved.
	raw, _ := os.ReadFile(cfgPath)
	if string(raw) != "not json" {
		t.Errorf("malformed config was overwritten: %q", raw)
	}
}

// TestSeedClaudeWorkspaceTrust_RejectsEmpty confirms guard returns errors
// for empty inputs rather than silently writing nothing.
func TestSeedClaudeWorkspaceTrust_RejectsEmpty(t *testing.T) {
	if err := seedClaudeWorkspaceTrust("", t.TempDir()); err == nil {
		t.Error("expected error for empty homeDir")
	}
	if err := seedClaudeWorkspaceTrust(t.TempDir(), ""); err == nil {
		t.Error("expected error for empty bootDir")
	}
}

// setHomeForTest redirects os.UserHomeDir() to homeDir across platforms.
// On unix, $HOME is the source of truth. On Windows, os.UserHomeDir reads
// %USERPROFILE%. Setting both keeps these tests cross-platform — without
// the Windows env, the closure under test would write into the real user
// profile on Windows (or fail unexpectedly).
func setHomeForTest(t *testing.T, homeDir string) {
	t.Helper()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
}

// TestClaudeBootDirSpec_SettingsJSON_NoSideEffectWhenBootDirEmpty pins the
// purity guarantee documented on PlantedFile.Render: when ctx.BootDir is
// empty (e.g. unit tests rendering for content only), the settings.json
// closure does NOT seed ~/.claude.json. This protects developer machines
// from pollution during `go test ./...`.
func TestClaudeBootDirSpec_SettingsJSON_NoSideEffectWhenBootDirEmpty(t *testing.T) {
	homeDir := t.TempDir()
	setHomeForTest(t, homeDir)
	cfgPath := filepath.Join(homeDir, ".claude.json")

	spec := NewClaudeAdapter().BootDirSpec()
	settings, err := spec.PlantedFiles[2].Render(PlantContext{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if settings == "" {
		t.Error("Render returned empty settings.json")
	}

	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("HOME/.claude.json was created despite empty BootDir; err=%v", err)
	}
}

// TestClaudeBootDirSpec_SettingsJSON_SeedsTrustWhenBootDirSet confirms the
// closure seeds ~/.claude.json when ctx.BootDir is provided. End-to-end
// behavior validation that the spec actually wires the helper.
func TestClaudeBootDirSpec_SettingsJSON_SeedsTrustWhenBootDirSet(t *testing.T) {
	homeDir := t.TempDir()
	bootDir := t.TempDir()
	setHomeForTest(t, homeDir)

	spec := NewClaudeAdapter().BootDirSpec()
	settings, err := spec.PlantedFiles[2].Render(PlantContext{BootDir: bootDir})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if settings == "" {
		t.Error("Render returned empty settings.json")
	}

	cfgPath := filepath.Join(homeDir, ".claude.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	projects, _ := cfg["projects"].(map[string]any)
	resolved, _ := filepath.EvalSymlinks(bootDir)
	if _, ok := projects[resolved].(map[string]any); !ok {
		t.Errorf("projects[%s] not seeded; got %v", resolved, projects)
	}
}
