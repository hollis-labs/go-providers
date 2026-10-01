package provider

import (
	"bytes"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// AntigravityAdapter implements CLIAdapter for the Antigravity CLI (`agy`),
// verified against agy 1.2.7.
//
// Shape: one subprocess per turn,
//
//	agy --output-format stream-json [flags] [--conversation <id>] -p=<prompt>
//
// Every turn ends with a `result` line and the process exits, so the turn
// boundary is reliable. `--conversation <id>` resumes a conversation across
// processes (turn 2 recalls turn 1 and keeps the id). The prompt is passed
// inline as `-p=<prompt>` because agy's -p takes a value: a bare -p followed
// by another flag swallows that flag as the prompt, and the inline form keeps
// arguments a session layer appends after BuildArgs (such as --add-dir) from
// being read as the prompt.
//
// Not used: `--input-format stream-json`. It keeps one process across turns
// and reads NDJSON frames of the form
//
//	{"event":"user","message":{"content":"<text>"}}
//
// (`message` must be an object and `content` carries the text; `text` is
// rejected and unknown events are ignored with a warning). Memory carries
// across frames, but in 1.2.7 the per-turn `result` line is often held back
// until the next frame arrives or stdin closes (seen at 20s, 25s and more
// than 150s, never released by a timeout), so a driver cannot tell when a
// turn ended. Revisit if a later agy emits `result` promptly.
//
// Headless behaviour worth knowing:
//   - An unknown --conversation id does not fail: agy warns on stderr
//     (`conversation "<id>" not found`), exits 0 and silently starts a new
//     conversation. ResumeKeepsSessionID lets the session layer detect that
//     by comparing ids; IsSessionLost recognizes the warning.
//   - Tool actions that need approval are auto-denied under the default
//     request-review mode: the turn completes, the tool step reports ERROR
//     (or DONE with nothing done, under accept-edits) and the result lists
//     them in denied_actions, which ParseLineEvents surfaces as
//     events.PermissionDenied. Permission "bypass" approves everything.
//   - --sandbox is advisory: a command it blocked succeeded when the model
//     retried it. Do not rely on it as a boundary.
//   - Authentication comes from the macOS Keychain (go-keyring), not from
//     ~/.gemini/oauth_creds.json, which belongs to the retired Gemini CLI.
//     Without a usable login print mode tries a silent sign-in, then prints a
//     URL, opens a browser and reads an authorization code from stdin. There
//     is no Preflight (no reliable check exists that cannot raise a Keychain
//     prompt); a turn's stdin is at EOF (go-runner never sets one), so the
//     code prompt ends at once instead of blocking, and IsNotAuthenticated
//     classifies the failure afterwards. Residual
//     risk: credentials that exist but are expired or revoked may still
//     reach the browser step.
//   - agy has no config-dir variable; its global config (~/.gemini/config)
//     is shared with the desktop app and HOME also locates the credentials.
//     BootDirSpec therefore projects into the workspace customization root
//     <boot>/.agents, with cwd = boot and the project attached by --add-dir.
type AntigravityAdapter struct {
	// Model is passed as --model (see `agy models`). Empty keeps agy's default.
	Model string

	// Agent is passed as --agent. Empty keeps agy's default agent.
	Agent string

	// Effort is passed as --effort: low, medium or high.
	Effort string

	// Permission picks the headless approval posture:
	//   - "" keeps agy's request-review default: anything needing approval
	//     is auto-denied.
	//   - "bypass" passes --dangerously-skip-permissions.
	//   - "accept-edits" and "plan" pass --mode <value>. Under accept-edits
	//     file edits are approved and commands are still denied.
	Permission string

	// AddDirs are extra --add-dir directories, before the prompt.
	AddDirs []string
}

func NewAntigravityAdapter() *AntigravityAdapter { return &AntigravityAdapter{} }

func (a *AntigravityAdapter) Name() string { return "antigravity" }

func (a *AntigravityAdapter) BuildArgs(prompt, systemPrompt, cliSessionID string) []string {
	args := []string{"--output-format", "stream-json"}
	switch a.Permission {
	case "bypass":
		args = append(args, "--dangerously-skip-permissions")
	case "accept-edits", "plan":
		args = append(args, "--mode", a.Permission)
	}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if a.Effort != "" {
		args = append(args, "--effort", a.Effort)
	}
	if a.Agent != "" {
		args = append(args, "--agent", a.Agent)
	}
	for _, d := range a.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if cliSessionID != "" {
		args = append(args, "--conversation", cliSessionID)
	}
	return append(args, "-p="+prependOpencodeSystemPrompt(prompt, systemPrompt))
}

func (a *AntigravityAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	return parseAntigravityStreamLine(line), nil
}

// Detect resolves AGY_CLI_PATH, then agy on PATH and the usual install
// directories (the installer puts it in ~/.local/bin).
func (a *AntigravityAdapter) Detect() (string, bool) {
	return detect(runtimes.Antigravity)
}

// agy authenticates from the macOS Keychain, not from
// ~/.gemini/oauth_creds.json (that file belongs to the retired Gemini CLI), and
// the Keychain service name is not known statically, so there is no check that
// is both reliable and guaranteed not to raise a Keychain prompt. The adapter
// therefore has no Preflight: a login failure is reported after the fact
// through IsNotAuthenticated.

// ResumeKeepsSessionID implements SessionResumeVerifier: agy resumes a known
// conversation under the same id and replaces an unknown one silently.
func (a *AntigravityAdapter) ResumeKeepsSessionID() bool { return true }

// IsSessionLost implements SessionLostClassifier for the warning agy prints
// when --conversation names an unknown id. The turn itself succeeds.
func (a *AntigravityAdapter) IsSessionLost(stderrTail []byte) bool {
	return bytes.Contains(stderrTail, []byte(`conversation "`)) && bytes.Contains(stderrTail, []byte(`" not found`))
}

// IsNotAuthenticated implements AuthFailureClassifier. It matches the lines
// agy prints when it has no usable login: the interactive sign-in prompt
// ("Authentication required. Please visit the URL"), the missing-credentials
// error ("not authenticated: no stored credentials found"), and the outcomes of
// the sign-in wait ("Waiting for authentication", "authentication failed or
// timed out"). It deliberately does not match "trying silent auth", which agy
// logs on every healthy run before it finds its Keychain credentials.
func (a *AntigravityAdapter) IsNotAuthenticated(stderrTail []byte) bool {
	for _, marker := range antigravityAuthMarkers {
		if bytes.Contains(stderrTail, []byte(marker)) {
			return true
		}
	}
	return false
}

var antigravityAuthMarkers = []string{
	"Authentication required. Please visit the URL",
	"not authenticated: no stored credentials found",
	"authentication failed or timed out",
	"Waiting for authentication",
}
