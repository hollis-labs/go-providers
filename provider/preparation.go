package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PreparationPolicy declares which runtime effects the caller authorizes.
// PrepareRuntime fails before process start when a required effect is not
// authorized or cannot be prepared.
type PreparationPolicy struct {
	AllowCredentials  bool
	AllowHostMutation bool
	AllowCleanup      bool
}

// CredentialRequest is passed to caller-owned credential resolution. The
// provider package never falls back to ambient HOME or provider env vars.
type CredentialRequest struct {
	Provider    ProviderID         `json:"provider"`
	Mode        ProviderMode       `json:"mode"`
	Effect      ProviderEffectKind `json:"effect"`
	Destination string             `json:"destination,omitempty"`
}

// Credential carries secret bytes resolved at the execution edge.
type Credential struct {
	Bytes  []byte
	Mode   os.FileMode
	Source string
}

// CredentialResolver resolves provider credentials from caller-controlled
// state. Implementations should not include secret values in returned errors.
type CredentialResolver interface {
	ResolveProviderCredential(context.Context, CredentialRequest) (Credential, error)
}

// CredentialResolverFunc adapts a function to CredentialResolver.
type CredentialResolverFunc func(context.Context, CredentialRequest) (Credential, error)

// ResolveProviderCredential implements CredentialResolver.
func (f CredentialResolverFunc) ResolveProviderCredential(ctx context.Context, req CredentialRequest) (Credential, error) {
	return f(ctx, req)
}

// RuntimePreparationRequest asks go-providers to perform the explicit effects
// needed by a previously computed pure projection.
type RuntimePreparationRequest struct {
	Projection         ProviderProjection
	Roots              ProjectionRoots
	HomeDir            string
	Policy             PreparationPolicy
	CredentialResolver CredentialResolver
	RequiredEffects    []ProviderEffectKind
}

// PreparationDiagnostic is safe to log: it carries effect names and paths, not
// secret values.
type PreparationDiagnostic struct {
	Effect  ProviderEffectKind `json:"effect"`
	Code    string             `json:"code"`
	Message string             `json:"message"`
}

// PreparationError reports why required runtime preparation could not finish.
type PreparationError struct {
	Diagnostics []PreparationDiagnostic
}

func (e *PreparationError) Error() string {
	parts := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		parts = append(parts, d.Message)
	}
	return strings.Join(parts, "; ")
}

// CleanupObligation describes whether a prepared effect has caller-visible
// cleanup work.
type CleanupObligation string

const (
	CleanupNone          CleanupObligation = "none"
	CleanupSessionScoped CleanupObligation = "session-scoped"
	CleanupBestEffort    CleanupObligation = "best-effort"
)

// PreparedEffect describes work completed by PrepareRuntime without exposing
// secret content.
type PreparedEffect struct {
	Kind        ProviderEffectKind `json:"kind"`
	Destination string             `json:"destination,omitempty"`
	Mode        os.FileMode        `json:"mode,omitempty"`
	Secret      bool               `json:"secret,omitempty"`
	Redacted    string             `json:"redacted,omitempty"`
	Cleanup     CleanupObligation  `json:"cleanup"`
}

// RuntimePreparationResult is the non-secret execution-edge preparation
// result. Call Cleanup when the session-scoped prepared resources are no
// longer needed.
type RuntimePreparationResult struct {
	Effects     []PreparedEffect        `json:"effects"`
	Diagnostics []PreparationDiagnostic `json:"diagnostics,omitempty"`

	cleanup []func(context.Context) error
}

// Cleanup removes session-scoped resources prepared by PrepareRuntime. It is
// idempotent and preserves files/config entries whose content no longer match
// what this preparation owned.
func (r RuntimePreparationResult) Cleanup(ctx context.Context) error {
	var errs []error
	for _, fn := range r.cleanup {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PrepareRuntime performs explicitly requested runtime effects. It does not
// start a provider process.
func PrepareRuntime(ctx context.Context, req RuntimePreparationRequest) (RuntimePreparationResult, error) {
	var result RuntimePreparationResult
	if len(req.RequiredEffects) == 0 {
		return result, nil
	}

	available := map[ProviderEffectKind]ProviderEffect{}
	for _, effect := range req.Projection.Effects {
		available[effect.Kind] = effect
	}

	var diagnostics []PreparationDiagnostic
	for _, kind := range req.RequiredEffects {
		effect, ok := available[kind]
		if !ok {
			diagnostics = append(diagnostics, PreparationDiagnostic{
				Effect:  kind,
				Code:    "unknown_effect",
				Message: fmt.Sprintf("%s/%s did not declare required preparation effect %q", req.Projection.Provider, req.Projection.Mode, kind),
			})
			continue
		}
		prepared, cleanup, diag := prepareOneEffect(ctx, req, effect)
		if diag.Code != "" {
			diagnostics = append(diagnostics, diag)
			continue
		}
		result.Effects = append(result.Effects, prepared)
		if cleanup != nil {
			result.cleanup = append(result.cleanup, cleanup)
		}
	}
	if len(diagnostics) > 0 {
		result.Diagnostics = diagnostics
		return result, &PreparationError{Diagnostics: diagnostics}
	}
	return result, nil
}

func prepareOneEffect(ctx context.Context, req RuntimePreparationRequest, effect ProviderEffect) (PreparedEffect, func(context.Context) error, PreparationDiagnostic) {
	switch effect.Kind {
	case EffectClaudeWorkspaceTrust:
		return prepareClaudeWorkspaceTrust(req)
	case EffectCodexAuthJSON:
		return prepareCodexAuthJSON(ctx, req, effect)
	default:
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  effect.Kind,
			Code:    "unavailable_effect",
			Message: fmt.Sprintf("no runtime preparation adapter is available for %q", effect.Kind),
		}
	}
}

func prepareClaudeWorkspaceTrust(req RuntimePreparationRequest) (PreparedEffect, func(context.Context) error, PreparationDiagnostic) {
	if !req.Policy.AllowHostMutation {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectClaudeWorkspaceTrust,
			Code:    "unauthorized_effect",
			Message: "claude workspace trust requires PreparationPolicy.AllowHostMutation",
		}
	}
	if req.HomeDir == "" || req.Roots.BootRoot == "" {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectClaudeWorkspaceTrust,
			Code:    "unavailable_effect",
			Message: "claude workspace trust requires HomeDir and Roots.BootRoot",
		}
	}
	if err := seedClaudeWorkspaceTrust(req.HomeDir, req.Roots.BootRoot); err != nil {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectClaudeWorkspaceTrust,
			Code:    "preparation_failed",
			Message: fmt.Sprintf("claude workspace trust preparation failed for %s", filepath.Join(req.HomeDir, ".claude.json")),
		}
	}
	cleanup := func(context.Context) error { return nil }
	obligation := CleanupBestEffort
	if req.Policy.AllowCleanup {
		homeDir := req.HomeDir
		bootRoot := req.Roots.BootRoot
		cleanup = func(context.Context) error {
			return removeClaudeWorkspaceTrust(homeDir, bootRoot)
		}
		obligation = CleanupSessionScoped
	}
	return PreparedEffect{
		Kind:        EffectClaudeWorkspaceTrust,
		Destination: filepath.Join(req.HomeDir, ".claude.json"),
		Mode:        0o600,
		Cleanup:     obligation,
	}, cleanup, PreparationDiagnostic{}
}

func prepareCodexAuthJSON(ctx context.Context, req RuntimePreparationRequest, effect ProviderEffect) (PreparedEffect, func(context.Context) error, PreparationDiagnostic) {
	if !req.Policy.AllowCredentials {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "unauthorized_effect",
			Message: "codex auth.json requires PreparationPolicy.AllowCredentials",
		}
	}
	if req.CredentialResolver == nil {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "unavailable_effect",
			Message: "codex auth.json requires a caller-provided CredentialResolver",
		}
	}
	if req.Roots.BootRoot == "" {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "unavailable_effect",
			Message: "codex auth.json requires Roots.BootRoot",
		}
	}
	dst := filepath.Join(req.Roots.BootRoot, filepath.FromSlash(firstNonEmpty(effect.Destination, "auth.json")))
	cred, err := req.CredentialResolver.ResolveProviderCredential(ctx, CredentialRequest{
		Provider:    req.Projection.Provider,
		Mode:        req.Projection.Mode,
		Effect:      EffectCodexAuthJSON,
		Destination: effect.Destination,
	})
	if err != nil {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "unavailable_effect",
			Message: "codex auth.json credential resolver failed",
		}
	}
	if len(cred.Bytes) == 0 {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "unavailable_effect",
			Message: "codex auth.json credential resolver returned no credential bytes",
		}
	}
	mode := cred.Mode
	if mode == 0 {
		mode = 0o600
	}
	if mode.Perm()&0o077 != 0 {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "invalid_mode",
			Message: "codex auth.json credentials require a restrictive file mode",
		}
	}
	if err := writeFilePrivateAtomic(dst, cred.Bytes, mode); err != nil {
		return PreparedEffect{}, nil, PreparationDiagnostic{
			Effect:  EffectCodexAuthJSON,
			Code:    "preparation_failed",
			Message: fmt.Sprintf("codex auth.json preparation failed for %s", dst),
		}
	}
	owned := append([]byte(nil), cred.Bytes...)
	cleanup := func(context.Context) error { return nil }
	obligation := CleanupBestEffort
	if req.Policy.AllowCleanup {
		cleanup = func(context.Context) error {
			return removeFileIfContentMatches(dst, owned)
		}
		obligation = CleanupSessionScoped
	}
	return PreparedEffect{
		Kind:        EffectCodexAuthJSON,
		Destination: dst,
		Mode:        mode,
		Secret:      true,
		Redacted:    RedactedSecret,
		Cleanup:     obligation,
	}, cleanup, PreparationDiagnostic{}
}

// RedactedSecret is the only representation PrepareRuntime returns for secret
// values.
const RedactedSecret = "<redacted>"

func writeFilePrivateAtomic(dst string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", dst, err)
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, dst, err)
	}
	cleanupTmp = false
	return nil
}

func removeFileIfContentMatches(path string, owned []byte) error {
	current, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !bytes.Equal(current, owned) {
		return nil
	}
	return os.Remove(path)
}
