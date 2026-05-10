// Command codex_bootdir demonstrates the BootDirSpec plant-and-spawn
// pattern for the OpenAI Codex CLI.
//
// Codex auto-loads AGENTS.md from cwd as its system prompt. This
// example plants AGENTS.md + boot.md + .mcp.json into a fresh tempdir,
// invokes `codex exec --json` with cwd = bootDir and project access via
// `--cd <projectDir>`, and streams the response.
//
// Usage:
//
//	go run ./examples/codex_bootdir -project /path/to/project
//
// If the codex binary is not detectable, the example prints the boot
// dir layout and the would-be spawn args, then exits 0.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
)

func main() {
	projectDir := flag.String("project", ".", "absolute path the agent should have access to (--cd)")
	prompt := flag.String("prompt", "Say hello in one short sentence.", "user prompt to send")
	flag.Parse()

	absProject, err := filepath.Abs(*projectDir)
	if err != nil {
		log.Fatalf("resolve project dir: %v", err)
	}

	// 1. Construct the adapter.
	adapter := provider.NewCodexAdapter()

	// 2. Materialize the BootDirSpec.
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
		BootDir:        bootDir,
	}

	spec := adapter.BootDirSpec()
	for _, pf := range spec.PlantedFiles {
		content, err := pf.Render(plantCtx)
		if err != nil {
			log.Fatalf("render %s: %v", pf.RelPath, err)
		}
		dst := filepath.Join(bootDir, pf.RelPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			log.Fatalf("mkdir for %s: %v", pf.RelPath, err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			log.Fatalf("write %s: %v", pf.RelPath, err)
		}
	}

	// 3. Codex needs no per-flag injection — cwd carries AGENTS.md and
	//    --cd carries project access. Resolve cwd from the spec.
	cwd := spec.SpawnWorkdir(bootDir, absProject)
	projectArg := strings.ReplaceAll(spec.ProjectDirArg, "{{.ProjectDir}}", absProject)

	// 4. Detect the binary; print dry-run summary if absent.
	cliPath, ok := adapter.Detect()
	if !ok {
		fmt.Println("codex binary not detected (set CODEX_CLI_PATH or install `codex`).")
		fmt.Println("dry-run summary:")
		fmt.Printf("  bootDir              = %s\n", bootDir)
		fmt.Printf("  cwd (per spec)       = %s\n", cwd)
		fmt.Printf("  project-dir flag     = %s\n", projectArg)
		fmt.Printf("  spawn args (per turn) = %v\n", adapter.BuildArgs(*prompt, "", ""))
		fmt.Printf("  spec.Notes           = %s\n", spec.Notes)
		fmt.Println("\nplanted files:")
		for _, pf := range spec.PlantedFiles {
			fmt.Printf("  %s\n", filepath.Join(bootDir, pf.RelPath))
		}
		return
	}

	// 5. Spawn. Note: NewSubprocessBridge spawns at the adapter's cwd;
	//    apps wanting cwd = bootDir wrap exec with their own chdir or
	//    use os.Chdir before constructing the bridge. For this example
	//    we chdir into the bootDir.
	if err := os.Chdir(cwd); err != nil {
		log.Fatalf("chdir to bootdir: %v", err)
	}

	// Append --cd <projectDir> by injecting via BuildArgs prompt? No —
	// codex's BuildArgs returns ["exec", prompt, "--json"]. The
	// project-dir arg is a positional-style flag the caller appends.
	// SubprocessBridge does not splice extra args, so for codex the
	// app-side spawn typically uses os/exec directly when --cd is
	// needed. The bridge path below works for codex when project
	// access is implicit (cwd is the project root); the dry-run summary
	// above documents the full --cd flow for the non-implicit case.

	bridge := provider.NewSubprocessBridge(adapter, cliPath)
	stream, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: *prompt},
		},
	})
	if err != nil {
		log.Fatalf("StreamChat: %v", err)
	}

	for ev := range stream {
		switch ev.Type {
		case llmtypes.EventDelta:
			fmt.Print(ev.Content)
		case llmtypes.EventError:
			fmt.Fprintf(os.Stderr, "\nerror: %s\n", ev.Error)
			os.Exit(1)
		case llmtypes.EventDone:
			fmt.Println()
			return
		}
	}
}
