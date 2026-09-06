// Command codex_bootdir demonstrates pure projection plus explicit runtime
// preparation for the OpenAI Codex CLI.
//
// Codex auto-loads AGENTS.md from cwd as its system prompt. This
// example projects AGENTS.md, boot.md and config.toml into a fresh
// tempdir. It prepares auth.json only when the caller provides
// -auth-json, then invokes `codex exec --json --cd <projectDir>`.
//
// Usage:
//
//	go run ./examples/codex_bootdir -project /path/to/project
//
// If the codex binary is not detectable, the example prints the boot
// dir layout and the would-be spawn args, then exits 0.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
)

func main() {
	projectDir := flag.String("project", ".", "absolute path the agent should have access to (--cd)")
	authJSON := flag.String("auth-json", "", "optional caller-approved codex auth.json source to prepare into the boot dir")
	prompt := flag.String("prompt", "Say hello in one short sentence.", "user prompt to send")
	flag.Parse()

	absProject, err := filepath.Abs(*projectDir)
	if err != nil {
		log.Fatalf("resolve project dir: %v", err)
	}

	// 1. Construct the adapter.
	adapter := provider.NewCodexAdapter()

	// 2. Pure projection: no credential reads, no HOME writes, no spawn.
	bootDir, err := os.MkdirTemp("", "go-providers-codex-*")
	if err != nil {
		log.Fatalf("mkdir bootdir: %v", err)
	}
	defer os.RemoveAll(bootDir)

	plantCtx := provider.PlantContext{
		SystemPrompt:   "You are a terse assistant. One sentence only.",
		BootContent:    "",
		AgentName:      "example",
		MCPLoopbackURL: "",
		ProjectDir:     absProject,
	}

	proj, err := adapter.ProviderProjection(plantCtx, provider.ProjectionOptions{
		RequiredFeatures: []provider.ProviderFeature{
			provider.FeatureInstructions,
			provider.FeatureNativeConfig,
			provider.FeatureMCP,
		},
	})
	if err != nil {
		log.Fatalf("project codex layout: %v", err)
	}
	for _, f := range proj.Files {
		if f.Role == "credential-placeholder" {
			continue
		}
		if err := writeProjectedFile(bootDir, f); err != nil {
			log.Fatalf("write %s: %v", f.RelPath, err)
		}
	}

	// 3. Explicit runtime preparation: auth is copied only from the
	//    caller-approved path. No ambient CODEX_HOME/HOME fallback exists.
	var prepared provider.RuntimePreparationResult
	if *authJSON != "" {
		prepared, err = provider.PrepareRuntime(context.Background(), provider.RuntimePreparationRequest{
			Projection: proj,
			Roots:      provider.ProjectionRoots{BootRoot: bootDir, ProjectRoot: absProject},
			Policy: provider.PreparationPolicy{
				AllowCredentials: true,
				AllowCleanup:     true,
			},
			CredentialResolver: provider.CredentialResolverFunc(func(_ context.Context, req provider.CredentialRequest) (provider.Credential, error) {
				if req.Effect != provider.EffectCodexAuthJSON {
					return provider.Credential{}, fmt.Errorf("unsupported credential request %q", req.Effect)
				}
				b, err := os.ReadFile(*authJSON)
				if err != nil {
					return provider.Credential{}, err
				}
				return provider.Credential{Bytes: b, Mode: 0o600, Source: *authJSON}, nil
			}),
			RequiredEffects: []provider.ProviderEffectKind{provider.EffectCodexAuthJSON},
		})
		if err != nil {
			log.Fatalf("prepare codex runtime: %v", err)
		}
		defer func() {
			if err := prepared.Cleanup(context.Background()); err != nil {
				log.Printf("cleanup prepared resources: %v", err)
			}
		}()
	}

	binding, err := proj.ResolveLaunch(provider.ProjectionRoots{
		BootRoot:    bootDir,
		ProjectRoot: absProject,
	}, *prompt)
	if err != nil {
		log.Fatalf("resolve launch: %v", err)
	}

	// 4. Detect the binary; print dry-run summary if absent.
	cliPath, ok := adapter.Detect()
	if !ok {
		fmt.Println("codex binary not detected (set CODEX_CLI_PATH or install `codex`).")
		fmt.Println("dry-run summary:")
		fmt.Printf("  bootDir              = %s\n", bootDir)
		fmt.Printf("  cwd                  = %s\n", binding.CWD)
		fmt.Printf("  configDir            = %s\n", binding.ConfigDir)
		fmt.Printf("  argv                 = %v\n", binding.Argv)
		fmt.Printf("  env                  = %v\n", binding.Env)
		fmt.Printf("  prepared effects     = %v\n", prepared.Effects)
		fmt.Printf("  pending effects      = %v\n", proj.Effects)
		fmt.Println("\nplanted files:")
		for _, f := range proj.Files {
			if f.Role == "credential-placeholder" && *authJSON == "" {
				fmt.Printf("  %s (credential placeholder; pass -auth-json to prepare)\n", filepath.Join(bootDir, f.RelPath))
				continue
			}
			fmt.Printf("  %s\n", filepath.Join(bootDir, f.RelPath))
		}
		return
	}

	// 5. Spawn with the resolved launch binding.
	cmd := exec.CommandContext(context.Background(), cliPath, binding.Argv...)
	cmd.Dir = binding.CWD
	cmd.Env = provider.ApplyEnvDeltas(os.Environ(), binding.Env)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("stderr pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("start codex: %v", err)
	}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Fprintln(os.Stderr, scanner.Text())
		}
	}()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		events, err := adapter.ParseLine(scanner.Bytes())
		if err != nil {
			log.Fatalf("parse codex output: %v", err)
		}
		for _, ev := range events {
			switch ev.Type {
			case llmtypes.EventDelta:
				fmt.Print(ev.Content)
			case llmtypes.EventError:
				fmt.Fprintf(os.Stderr, "\nerror: %s\n", ev.Error)
			case llmtypes.EventDone:
				fmt.Println()
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		log.Fatalf("codex exited: %v", err)
	}
}

func writeProjectedFile(root string, f provider.ProjectedFile) error {
	mode := f.Mode
	if mode == 0 {
		mode = 0o644
	}
	dst := filepath.Join(root, filepath.FromSlash(f.RelPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, f.Content, mode.Perm())
}
