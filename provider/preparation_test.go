package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPrepareRuntime_CodexAuthUsesResolverAndNoAmbientFallback(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	ambient := filepath.Join(home, ".codex")
	if err := os.MkdirAll(ambient, 0o700); err != nil {
		t.Fatalf("mkdir ambient: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ambient, "auth.json"), []byte(`{"token":"ambient-secret"}`), 0o600); err != nil {
		t.Fatalf("write ambient auth: %v", err)
	}

	boot := t.TempDir()
	proj, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}

	secret := []byte(`{"token":"fake-runtime-secret"}`)
	result, err := PrepareRuntime(context.Background(), RuntimePreparationRequest{
		Projection: proj,
		Roots:      ProjectionRoots{BootRoot: boot},
		Policy: PreparationPolicy{
			AllowCredentials: true,
			AllowCleanup:     true,
		},
		CredentialResolver: CredentialResolverFunc(func(_ context.Context, req CredentialRequest) (Credential, error) {
			if req.Provider != ProviderCodex || req.Effect != EffectCodexAuthJSON || req.Destination != "auth.json" {
				t.Fatalf("unexpected credential request: %#v", req)
			}
			return Credential{Bytes: secret, Mode: 0o600, Source: "fake-test-store"}, nil
		}),
		RequiredEffects: []ProviderEffectKind{EffectCodexAuthJSON},
	})
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	if len(result.Effects) != 1 {
		t.Fatalf("effects: %#v", result.Effects)
	}
	authPath := filepath.Join(boot, "auth.json")
	got, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(got) != string(secret) {
		t.Fatalf("auth.json content came from wrong source: %s", got)
	}
	if strings.Contains(mustJSON(t, result), "fake-runtime-secret") || strings.Contains(mustJSON(t, result), "ambient-secret") {
		t.Fatalf("result leaked secret: %s", mustJSON(t, result))
	}
	if result.Effects[0].Redacted != RedactedSecret || !result.Effects[0].Secret {
		t.Fatalf("secret metadata not redacted: %#v", result.Effects[0])
	}
	st, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode: want 0600, got %#o", st.Mode().Perm())
	}

	if err := result.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("auth.json was not cleaned up: %v", err)
	}
}

func TestPrepareRuntime_RequiredUnauthorizedOrUnavailableFailsBeforeCredentialLookup(t *testing.T) {
	proj, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	called := false
	_, err = PrepareRuntime(context.Background(), RuntimePreparationRequest{
		Projection: proj,
		Roots:      ProjectionRoots{BootRoot: t.TempDir()},
		CredentialResolver: CredentialResolverFunc(func(context.Context, CredentialRequest) (Credential, error) {
			called = true
			return Credential{Bytes: []byte("secret")}, nil
		}),
		RequiredEffects: []ProviderEffectKind{EffectCodexAuthJSON},
	})
	if err == nil {
		t.Fatal("expected unauthorized error")
	}
	if called {
		t.Fatal("credential resolver was called despite unauthorized policy")
	}
	assertPreparationCode(t, err, "unauthorized_effect")

	_, err = PrepareRuntime(context.Background(), RuntimePreparationRequest{
		Projection:      proj,
		Roots:           ProjectionRoots{BootRoot: t.TempDir()},
		Policy:          PreparationPolicy{AllowCredentials: true},
		RequiredEffects: []ProviderEffectKind{EffectCodexAuthJSON},
	})
	assertPreparationCode(t, err, "unavailable_effect")

	_, err = PrepareRuntime(context.Background(), RuntimePreparationRequest{
		Projection: proj,
		Roots:      ProjectionRoots{BootRoot: t.TempDir()},
		Policy:     PreparationPolicy{AllowCredentials: true},
		CredentialResolver: CredentialResolverFunc(func(context.Context, CredentialRequest) (Credential, error) {
			return Credential{}, errors.New("resolver failed with token fake-runtime-secret")
		}),
		RequiredEffects: []ProviderEffectKind{EffectCodexAuthJSON},
	})
	if err == nil {
		t.Fatal("expected resolver error")
	}
	if strings.Contains(err.Error(), "fake-runtime-secret") {
		t.Fatalf("diagnostic leaked resolver secret: %v", err)
	}
	assertPreparationCode(t, err, "unavailable_effect")
}

func TestPrepareRuntime_CodexAuthIdempotentAndCleanupPreservesModifiedFile(t *testing.T) {
	boot := t.TempDir()
	proj, err := NewCodexAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	req := RuntimePreparationRequest{
		Projection: proj,
		Roots:      ProjectionRoots{BootRoot: boot},
		Policy:     PreparationPolicy{AllowCredentials: true, AllowCleanup: true},
		CredentialResolver: CredentialResolverFunc(func(context.Context, CredentialRequest) (Credential, error) {
			return Credential{Bytes: []byte(`{"token":"fake-runtime-secret"}`)}, nil
		}),
		RequiredEffects: []ProviderEffectKind{EffectCodexAuthJSON},
	}
	first, err := PrepareRuntime(context.Background(), req)
	if err != nil {
		t.Fatalf("first PrepareRuntime: %v", err)
	}
	second, err := PrepareRuntime(context.Background(), req)
	if err != nil {
		t.Fatalf("second PrepareRuntime: %v", err)
	}
	authPath := filepath.Join(boot, "auth.json")
	if got, _ := os.ReadFile(authPath); string(got) != `{"token":"fake-runtime-secret"}` {
		t.Fatalf("unexpected auth after repeated prepare: %s", got)
	}
	if err := first.Cleanup(context.Background()); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup after idempotent prepare should remove owned file: %v", err)
	}

	if _, err := PrepareRuntime(context.Background(), req); err != nil {
		t.Fatalf("third PrepareRuntime: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"token":"user-replaced-secret"}`), 0o600); err != nil {
		t.Fatalf("replace auth: %v", err)
	}
	if err := second.Cleanup(context.Background()); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	got, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("modified auth should survive cleanup: %v", err)
	}
	if string(got) != `{"token":"user-replaced-secret"}` {
		t.Fatalf("cleanup clobbered modified auth: %s", got)
	}
}

func TestPrepareRuntime_ClaudeTrustSyntheticHomeConcurrentCleanup(t *testing.T) {
	home := t.TempDir()
	boots := []string{t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()}
	proj, err := NewClaudeAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	existing := map[string]any{
		"oauthAccount": "user@example.com",
		"projects": map[string]any{
			"/existing": map[string]any{
				"hasTrustDialogAccepted": true,
				"allowedTools":           []any{"Edit"},
			},
		},
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), out, 0o600); err != nil {
		t.Fatalf("seed claude config: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]RuntimePreparationResult, len(boots))
	errs := make([]error, len(boots))
	for i, boot := range boots {
		wg.Add(1)
		go func(i int, boot string) {
			defer wg.Done()
			results[i], errs[i] = PrepareRuntime(context.Background(), RuntimePreparationRequest{
				Projection: proj,
				Roots:      ProjectionRoots{BootRoot: boot},
				HomeDir:    home,
				Policy:     PreparationPolicy{AllowHostMutation: true, AllowCleanup: true},
				RequiredEffects: []ProviderEffectKind{
					EffectClaudeWorkspaceTrust,
				},
			})
		}(i, boot)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("PrepareRuntime[%d]: %v", i, err)
		}
	}

	cfg := readClaudeConfig(t, home)
	if cfg["oauthAccount"] != "user@example.com" {
		t.Fatalf("top-level config clobbered: %#v", cfg)
	}
	projects := cfg["projects"].(map[string]any)
	if _, ok := projects["/existing"].(map[string]any); !ok {
		t.Fatalf("existing project dropped: %#v", projects)
	}
	for _, boot := range boots {
		resolved, _ := filepath.EvalSymlinks(boot)
		entry, ok := projects[resolved].(map[string]any)
		if !ok {
			t.Fatalf("missing trust entry for %s in %#v", resolved, projects)
		}
		if entry["hasTrustDialogAccepted"] != true || entry["hasCompletedProjectOnboarding"] != true {
			t.Fatalf("trust flags missing for %s: %#v", resolved, entry)
		}
	}
	st, err := os.Stat(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("stat claude config: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("claude config mode: want 0600, got %#o", st.Mode().Perm())
	}

	for _, result := range results {
		if err := result.Cleanup(context.Background()); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
	cfg = readClaudeConfig(t, home)
	projects = cfg["projects"].(map[string]any)
	for _, boot := range boots {
		resolved, _ := filepath.EvalSymlinks(boot)
		if _, ok := projects[resolved]; ok {
			t.Fatalf("trust entry survived cleanup for %s", resolved)
		}
	}
	existingEntry := projects["/existing"].(map[string]any)
	if existingEntry["allowedTools"] == nil {
		t.Fatalf("cleanup clobbered unowned config: %#v", existingEntry)
	}
}

func TestPrepareRuntime_ClaudeTrustCleanupPreservesUnownedProjectEntry(t *testing.T) {
	home := t.TempDir()
	boot := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(boot)
	existing := map[string]any{
		"projects": map[string]any{
			resolved: map[string]any{
				"hasTrustDialogAccepted":        false,
				"hasCompletedProjectOnboarding": false,
				"allowedTools":                  []any{"Bash"},
			},
		},
	}
	out, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), out, 0o600); err != nil {
		t.Fatalf("seed claude config: %v", err)
	}
	proj, err := NewClaudeAdapter().ProviderProjection(PlantContext{}, ProjectionOptions{})
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	result, err := PrepareRuntime(context.Background(), RuntimePreparationRequest{
		Projection:      proj,
		Roots:           ProjectionRoots{BootRoot: boot},
		HomeDir:         home,
		Policy:          PreparationPolicy{AllowHostMutation: true, AllowCleanup: true},
		RequiredEffects: []ProviderEffectKind{EffectClaudeWorkspaceTrust},
	})
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	if err := result.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	cfg := readClaudeConfig(t, home)
	entry := cfg["projects"].(map[string]any)[resolved].(map[string]any)
	if entry["allowedTools"] == nil || entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("cleanup should preserve unowned project entry: %#v", entry)
	}
}

func TestBootDirSpec_CodexAuthLegacyOptIn(t *testing.T) {
	home := t.TempDir()
	setHomeForTest(t, home)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"token":"ambient-secret"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	auth := NewCodexAdapter().BootDirSpec().PlantedFiles[3]
	content, err := auth.Render(PlantContext{})
	if err != nil {
		t.Fatalf("pure Render: %v", err)
	}
	if content != "" {
		t.Fatalf("pure Render read ambient auth: %q", content)
	}
	content, err = auth.Render(PlantContext{LegacyAllowHostEffects: true})
	if err != nil {
		t.Fatalf("legacy Render: %v", err)
	}
	if content != `{"token":"ambient-secret"}` {
		t.Fatalf("legacy Render did not read ambient auth: %q", content)
	}
}

func assertPreparationCode(t *testing.T, err error, want string) {
	t.Helper()
	var prep *PreparationError
	if !errors.As(err, &prep) {
		t.Fatalf("want PreparationError, got %T %v", err, err)
	}
	for _, d := range prep.Diagnostics {
		if d.Code == want {
			return
		}
	}
	t.Fatalf("diagnostics missing code %q: %#v", want, prep.Diagnostics)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func readClaudeConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read claude config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse claude config: %v", err)
	}
	return cfg
}
