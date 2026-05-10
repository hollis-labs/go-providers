// Command opencode_bootdir demonstrates the BootDirSpec plant-and-spawn
// pattern for the opencode CLI.
//
// opencode treats its config dir (not cwd) as the source of truth for
// agents.json + opencode.json + agents/<name>.md. This example plants
// those three files plus boot.md + .mcp.json into a fresh bootDir,
// sets OPENCODE_CONFIG_DIR=<bootDir>, and invokes
// `opencode run --agent <name> --dir <projectDir>` with cwd =
// projectDir.
//
// Usage:
//
//	go run ./examples/opencode_bootdir -project /path/to/project
//
// If the opencode binary is not detectable, the example prints the
// boot dir layout and the would-be spawn args, then exits 0.
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
	projectDir := flag.String("project", ".", "absolute path the agent should run against (cwd + --dir)")
	agentName := flag.String("agent", "example", "opencode agent profile name (must match agents.json entry)")
	prompt := flag.String("prompt", "Say hello in one short sentence.", "user prompt to send")
	flag.Parse()

	absProject, err := filepath.Abs(*projectDir)
	if err != nil {
		log.Fatalf("resolve project dir: %v", err)
	}

	// 1. Construct the adapter, naming the agent that the planted
	//    agents.json entry will declare.
	adapter := provider.NewOpencodeAdapter()
	adapter.Agent = *agentName
	adapter.Dir = absProject

	// 2. Materialize the BootDirSpec.
	bootDir, err := os.MkdirTemp("", "go-providers-opencode-*")
	if err != nil {
		log.Fatalf("mkdir bootdir: %v", err)
	}
	defer os.RemoveAll(bootDir)

	plantCtx := provider.PlantContext{
		SystemPrompt:   "You are a terse assistant. One sentence only.",
		BootContent:    "",
		AgentName:      *agentName,
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

	// 3. Apply EnvAmendments — opencode wants OPENCODE_CONFIG_DIR set
	//    so it loads agents.json + opencode.json from the bootDir.
	for _, amend := range spec.EnvAmendments {
		// {{.BootDir}} / {{.ProjectDir}} substitution
		amend = strings.ReplaceAll(amend, "{{.BootDir}}", bootDir)
		amend = strings.ReplaceAll(amend, "{{.ProjectDir}}", absProject)
		eq := strings.IndexByte(amend, '=')
		if eq <= 0 {
			continue
		}
		os.Setenv(amend[:eq], amend[eq+1:])
	}

	cwd := spec.SpawnWorkdir(bootDir, absProject)
	projectArg := strings.ReplaceAll(spec.ProjectDirArg, "{{.ProjectDir}}", absProject)

	// 4. Detect the binary; print dry-run summary if absent.
	cliPath, ok := adapter.Detect()
	if !ok {
		fmt.Println("opencode binary not detected (set OPENCODE_CLI_PATH or install `opencode`).")
		fmt.Println("dry-run summary:")
		fmt.Printf("  bootDir                = %s\n", bootDir)
		fmt.Printf("  cwd (per spec)         = %s\n", cwd)
		fmt.Printf("  OPENCODE_CONFIG_DIR    = %s\n", bootDir)
		fmt.Printf("  project-dir flag       = %s\n", projectArg)
		fmt.Printf("  spawn args             = %v\n", adapter.BuildArgs(*prompt, plantCtx.SystemPrompt, ""))
		fmt.Printf("  spec.Notes             = %s\n", spec.Notes)
		fmt.Println("\nplanted files:")
		for _, pf := range spec.PlantedFiles {
			fmt.Printf("  %s\n", filepath.Join(bootDir, pf.RelPath))
		}
		return
	}

	// 5. Spawn. cwd = projectDir per the spec; chdir before
	//    constructing the bridge.
	if err := os.Chdir(cwd); err != nil {
		log.Fatalf("chdir to projectDir: %v", err)
	}

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
