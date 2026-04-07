package provider

// CLIAdapter abstracts the differences between CLI tools (Claude, Codex, Gemini)
// so the PTY bridge and subprocess bridge can spawn and parse any of them generically.
type CLIAdapter interface {
	// Name returns the adapter identifier (e.g. "claude", "codex", "gemini").
	Name() string

	// BuildArgs constructs CLI arguments for a prompt. cliSessionID is empty
	// on the first turn; non-empty triggers resume behavior.
	BuildArgs(prompt, systemPrompt, cliSessionID string) []string

	// ParseLine parses one line of structured output into StreamEvents.
	ParseLine(line []byte) ([]StreamEvent, error)

	// Detect checks if this adapter's CLI binary is available.
	// Returns the resolved binary path and true if found.
	Detect() (path string, ok bool)
}

// CLIConfig describes how to invoke a CLI tool. Used for future
// user-configurable adapters beyond the built-in three.
type CLIConfig struct {
	Command    string   `json:"command"`     // binary name or path
	Args       []string `json:"args"`        // default args (prepended)
	OutputMode string   `json:"output_mode"` // "stream-json", "jsonl", "text"
	Env        []string `json:"env"`         // additional env vars
}
