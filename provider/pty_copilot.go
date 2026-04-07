package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CopilotAdapter implements CLIAdapter for GitHub Copilot CLI.
// Supports both standalone `copilot` binary and `gh copilot` extension.
// No structured JSON output is available, so ParseLine treats each line as a text delta.
type CopilotAdapter struct {
	// ghMode is true when the detected binary is `gh` (needs "copilot" subcommand prefix).
	ghMode bool
}

func NewCopilotAdapter() *CopilotAdapter { return &CopilotAdapter{} }

func (a *CopilotAdapter) Name() string { return "copilot" }

func (a *CopilotAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	if a.ghMode {
		return []string{"copilot", "explain", prompt}
	}
	return []string{"explain", prompt}
}

func (a *CopilotAdapter) ParseLine(line []byte) ([]StreamEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	text := string(line)

	// Copilot produces some ANSI formatting and progress indicators;
	// skip lines that are purely decorative.
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "Synthesizing") {
		return nil, nil
	}

	return []StreamEvent{{Type: "delta", Content: text + "\n"}}, nil
}

func (a *CopilotAdapter) Detect() (string, bool) {
	if p := os.Getenv("COPILOT_CLI_PATH"); p != "" {
		a.ghMode = filepath.Base(p) == "gh"
		return p, true
	}
	// Check for standalone copilot binary first (e.g. /opt/homebrew/bin/copilot).
	if p, err := lookPathExpanded("copilot"); err == nil {
		a.ghMode = false
		return p, true
	}
	// Fall back to gh copilot extension.
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return "", false
	}
	out, err := exec.Command(ghPath, "copilot", "--help").CombinedOutput()
	if err != nil || len(out) == 0 {
		return "", false
	}
	a.ghMode = true
	return ghPath, true
}
