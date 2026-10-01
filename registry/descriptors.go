package registry

import "github.com/hollis-labs/agent-contracts-leaf/runtimes"

// The capabilities each mode declares are what the code that drives it does
// today, not what the runtime could do: go-providers' adapters for the native
// modes (EventParser, SessionLostClassifier, AuthFailureClassifier,
// SessionResumeVerifier, Preflighter), and what go-agent-wrapper's ACP
// adapters record as measured live for the ACP modes. An unmeasured
// capability is not declared: over-claiming is the unsafe direction.
// TestDeclaredCapabilitiesMatchAdapters in package provider checks the native
// side.
func init() {
	const (
		resume        = runtimes.CapResume
		resumeKeepsID = runtimes.CapResumeKeepsID
		typedEvents   = runtimes.CapTypedEvents
		authClass     = runtimes.CapAuthClassifier
		lostClass     = runtimes.CapSessionLostClassifier
		approvals     = runtimes.CapApprovals
	)
	caps := func(c ...runtimes.Capability) []runtimes.Capability { return c }
	const (
		projected   = SupportProjected
		explicit    = SupportExplicit
		unsupported = SupportUnsupported
	)

	register(Descriptor{
		ID:          runtimes.Claude,
		Aliases:     []string{"claude-code", "claudecode"},
		Binary:      "claude",
		EnvOverride: "CLAUDE_CLI_PATH",
		Modes: []ModeSupport{
			// An unknown --resume id is classified from stderr ("No
			// conversation found with session ID") in both modes, measured
			// in providertest/fixtures/claude/print_resume_unknown_id and
			// stream_resume_unknown_id (CW-20261001-0047).
			{runtimes.ModeStreamingStdio, caps(resume, typedEvents, lostClass)},
			{runtimes.ModeSubprocessPerTurn, caps(resume, typedEvents, lostClass)},
			// The TUI: resumable with --resume, but its output is a
			// terminal stream, not typed events, and a lost id there is
			// not measured.
			{runtimes.ModePTY, caps(resume)},
			// Through the claude-agent-acp bridge (go-agent-wrapper
			// claudeacp), which brings its own binary. No approvals: live
			// runs executed shell tools without ever sending
			// session/request_permission.
			{runtimes.ModeACPStdio, caps(typedEvents)},
		},
		DefaultMode: runtimes.ModeStreamingStdio,
		Posture:     claudePosture,
		Projection: &ProjectionFacts{
			TestedVersion: "2.1.285",
			Features: map[Feature]Support{
				FeatureInstructions: projected,
				FeatureNativeConfig: projected,
				FeatureMCP:          projected,
				FeatureSkillTrees:   projected,
				FeatureHooks:        explicit,
				FeatureCommands:     explicit,
				FeatureSubagents:    explicit,
				FeatureCredential:   explicit,
				FeatureTrust:        explicit,
			},
			Notes: map[runtimes.Mode]string{
				"": "Claude project files are rooted at the boot directory; auth and trust preparation are explicit runtime effects.",
			},
		},
	})

	register(Descriptor{
		ID:          runtimes.Codex,
		Binary:      "codex",
		EnvOverride: "CODEX_CLI_PATH",
		Modes: []ModeSupport{
			// app-server. The default per D-74. It asks the host to
			// approve, and it resumes: thread/resume was measured live
			// (providertest/fixtures/codex/app_server_resume, a codex-cli
			// 0.159.2 capture), and agentkit's turn.CodexAppServerSession
			// sends it when ResumeThreadID is set (agentkit v0.11.0), with
			// a lost or mismatched thread returned as a SessionLostError.
			// The wrapper does not yet map its SessionIDPreset onto
			// ResumeThreadID: apps drive turn.CodexAppServerCache
			// themselves. No session-lost-classifier: the loss arrives as
			// a JSON-RPC error, which agentkit's turn package classifies;
			// CodexAdapter.IsSessionLost reads exec's stderr and never
			// sees it.
			{runtimes.ModeJSONRPCStdio, caps(resume, typedEvents, approvals)},
			// codex exec --json. A turn given a thread id resumes it with
			// `exec … --cd <project> resume <id> -- <prompt>`, every exec
			// option in front of the subcommand (CW-20261001-0109).
			// Measured live on codex-cli 0.159.2: the resumed turn keeps
			// the thread id, runs in the --cd project, and takes -c
			// overrides given before `resume`. Without --cd it runs in the
			// process cwd, not the cwd it started in. Captured in
			// providertest/fixtures/codex/exec_turn2_resume. An unknown id
			// writes "no rollout found for thread id" to stderr and exits
			// 1 (exec_resume_unknown_id), which IsSessionLost classifies.
			{runtimes.ModeSubprocessPerTurn, caps(resume, typedEvents, lostClass)},
			// Through the codex-acp bridge (go-agent-wrapper codexacp);
			// session/request_permission has not been observed from it.
			{runtimes.ModeACPStdio, caps(typedEvents)},
		},
		DefaultMode: runtimes.ModeJSONRPCStdio,
		Posture:     codexPosture,
		Projection: &ProjectionFacts{
			TestedVersion: "0.154.0",
			Features: map[Feature]Support{
				FeatureInstructions: projected,
				FeatureNativeConfig: projected,
				FeatureMCP:          projected,
				FeatureSkillTrees:   projected,
				FeatureHooks:        explicit,
				FeatureCommands:     unsupported,
				FeatureSubagents:    explicit,
				FeatureCredential:   explicit,
				FeatureTrust:        unsupported,
			},
			Notes: map[runtimes.Mode]string{
				runtimes.ModeSubprocessPerTurn: "Codex reads config from CODEX_HOME/config.toml; auth.json is a preparation effect, not a pure render input.",
				runtimes.ModeJSONRPCStdio:      "Project root is supplied to the JSON-RPC thread layer rather than via --cd.",
			},
		},
	})

	register(Descriptor{
		ID:          runtimes.OpenCode,
		Aliases:     []string{"open-code"},
		Binary:      "opencode",
		EnvOverride: "OPENCODE_CLI_PATH",
		LookupDirs:  []string{"~/.opencode/bin"},
		Modes: []ModeSupport{
			// opencode run --format json --session <id>.
			{runtimes.ModeSubprocessPerTurn, caps(resume, typedEvents, lostClass)},
			// opencode serve: deferred as a default until its SSE and
			// permission behaviour is probed.
			{runtimes.ModeHTTPSSE, caps(typedEvents)},
			// opencode acp (go-agent-wrapper opencodeacp): session/load
			// resume confirmed live. No approvals: a live shell tool ran
			// without a session/request_permission.
			{runtimes.ModeACPStdio, caps(resume, typedEvents)},
		},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
		Posture:     opencodePosture,
		Projection: &ProjectionFacts{
			TestedVersion: "1.18.30",
			Features: map[Feature]Support{
				FeatureInstructions: projected,
				FeatureNativeConfig: projected,
				FeatureMCP:          projected,
				FeatureSkillTrees:   projected,
				FeatureHooks:        unsupported,
				FeatureCommands:     explicit,
				FeatureSubagents:    explicit,
				FeatureCredential:   explicit,
				FeatureTrust:        unsupported,
			},
			Notes: map[runtimes.Mode]string{
				runtimes.ModeSubprocessPerTurn: "OpenCode uses OPENCODE_CONFIG_DIR for projected config and project cwd for work.",
				runtimes.ModeHTTPSSE:           "OpenCode serve-http uses the same projected config and moves turn delivery to the HTTP runtime.",
			},
		},
	})

	// Copilot CLI is ACP-only: copilot --acp, over stdio or with --port N
	// over TCP (go-agent-wrapper copilotacp). Approvals were measured live
	// (a shell command produced a session/request_permission); resume has
	// not been, so it is not declared.
	register(Descriptor{
		ID:          runtimes.Copilot,
		Aliases:     []string{"copilot-cli"},
		Binary:      "copilot",
		EnvOverride: "COPILOT_CLI_PATH",
		Modes: []ModeSupport{
			{runtimes.ModeACPStdio, caps(typedEvents, approvals)},
			{runtimes.ModeACPTCP, caps(typedEvents, approvals)},
		},
		DefaultMode: runtimes.ModeACPStdio,
	})

	// Pi is ACP-only, through the pi-acp bridge (go-agent-wrapper piacp). The
	// binary is the bridge; when it is not installed the wrapper falls back to
	// npx -y pi-acp. session/load resume was confirmed live; pi-acp sent no
	// session/request_permission.
	register(Descriptor{
		ID:          runtimes.Pi,
		Aliases:     []string{"pi-acp"},
		Binary:      "pi-acp",
		EnvOverride: "PIACP_CLI_PATH",
		Modes: []ModeSupport{
			{runtimes.ModeACPStdio, caps(resume, typedEvents)},
		},
		DefaultMode: runtimes.ModeACPStdio,
	})

	register(Descriptor{
		ID:          runtimes.Antigravity,
		Aliases:     []string{"agy"},
		Binary:      "agy",
		EnvOverride: "AGY_CLI_PATH",
		Modes: []ModeSupport{
			// agy -p: an unknown conversation id silently starts a new
			// one, so resume-keeps-id is what detects it.
			{runtimes.ModeSubprocessPerTurn, caps(resume, resumeKeepsID, typedEvents, authClass, lostClass)},
		},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
		Posture:     antigravityPosture,
		Projection: &ProjectionFacts{
			TestedVersion: "1.2.7",
			Features: map[Feature]Support{
				FeatureInstructions: projected,
				FeatureNativeConfig: projected,
				FeatureMCP:          projected,
				FeatureSkillTrees:   projected,
				FeatureHooks:        explicit,
				FeatureCommands:     explicit,
				FeatureSubagents:    explicit,
				FeatureCredential:   explicit,
				FeatureTrust:        unsupported,
			},
			Notes: map[runtimes.Mode]string{
				"": "agy projects into the workspace customization root <boot>/.agents (cwd = boot, project via --add-dir); its global ~/.gemini/config is shared with the desktop app and not written. Credentials stay in ~/.gemini.",
			},
		},
	})
}
