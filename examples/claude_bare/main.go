// Command claude_bare demonstrates bare-mode Claude Code dispatch via
// go-providers: BootDirSpec materialization, BareInjectionPaths wiring,
// and SubprocessBridge streaming.
//
// Bare mode disables the Claude CLI's auto-discovery of CLAUDE.md,
// .mcp.json, and .claude/settings.json from cwd / parent dirs and from
// the user-global config. The caller injects each one explicitly via
// the four bare-mode flags (--append-system-prompt-file, --mcp-config,
// --settings, --add-dir). This example wires those flags from the
// planted BootDirSpec layout.
//
// Auth: bare mode requires either ANTHROPIC_API_KEY in env or an
// apiKeyHelper executable referenced from the planted settings.json.
// Pass -apikey-helper /path/to/bin to thread it through.
//
// Usage:
//
//	go run ./examples/claude_bare -project /path/to/project
//	go run ./examples/claude_bare -project . -apikey-helper /usr/local/bin/my-claude-helper
//
// If the claude binary is not detectable, the example prints the boot
// dir layout and the would-be spawn args, then exits 0.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
)

func main() {
	projectDir := flag.String("project", ".", "absolute path the agent should have tool access to (--add-dir)")
	apiKeyHelper := flag.String("apikey-helper", "", "optional path to an apiKeyHelper executable (threaded into planted settings.json)")
	prompt := flag.String("prompt", "Say hello in one short sentence.", "user prompt to send")
	flag.Parse()

	absProject, err := filepath.Abs(*projectDir)
	if err != nil {
		log.Fatalf("resolve project dir: %v", err)
	}

	// 1. Construct a bare-mode adapter.
	adapter := provider.NewClaudeAdapterDevBare()
	if *apiKeyHelper != "" {
		adapter.ApiKeyHelperPath = *apiKeyHelper
	}

	// 2. Materialize the BootDirSpec into a fresh tempdir.
	bootDir, err := os.MkdirTemp("", "go-providers-claude-bare-*")
	if err != nil {
		log.Fatalf("mkdir bootdir: %v", err)
	}
	defer os.RemoveAll(bootDir)

	plantCtx := provider.PlantContext{
		SystemPrompt:   "You are a terse assistant. One sentence only.",
		BootContent:    "", // boot.md left empty for this example
		AgentName:      "example",
		MCPLoopbackURL: "", // no MCP server in this example
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

	// 3. Wire bare-mode flag values from the planted layout.
	inj := adapter.BareInjectionPaths(bootDir, absProject)
	adapter.MCPConfigPath = inj.MCPConfigPath
	adapter.AppendSystemPromptFile = inj.AppendSystemPromptFile
	adapter.SettingsPath = inj.SettingsPath
	adapter.ProjectDir = inj.ProjectDir

	// 4. Detect the binary; if missing, print the dry-run summary.
	cliPath, ok := adapter.Detect()
	if !ok {
		fmt.Println("claude binary not detected (set CLAUDE_CLI_PATH or install `claude`).")
		fmt.Println("dry-run summary:")
		fmt.Printf("  bootDir         = %s\n", bootDir)
		fmt.Printf("  cwd (per spec)  = %s\n", spec.SpawnWorkdir(bootDir, absProject))
		fmt.Printf("  --add-dir       = %s\n", inj.ProjectDir)
		fmt.Printf("  --mcp-config    = %s\n", inj.MCPConfigPath)
		fmt.Printf("  --append-system-prompt-file = %s\n", inj.AppendSystemPromptFile)
		fmt.Printf("  --settings      = %s\n", inj.SettingsPath)
		fmt.Printf("  spawn args      = %v\n", adapter.BuildArgs(*prompt, "", ""))
		fmt.Println("\nplanted files:")
		for _, pf := range spec.PlantedFiles {
			fmt.Printf("  %s\n", filepath.Join(bootDir, pf.RelPath))
		}
		return
	}

	// 5. Spawn via SubprocessBridge and drain the event stream.
	bridge := provider.NewSubprocessBridge(adapter, cliPath)
	stream, err := bridge.StreamChat(context.Background(), llmtypes.ChatRequest{
		Model: "claude-sonnet-4-5",
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
