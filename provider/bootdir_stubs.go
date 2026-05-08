package provider

// Stub BootDirSpec implementations for adapters whose per-task
// tempdir convention has not yet been probed against the installed
// CLI. Each returns a zero-value BootDirSpec with a non-empty Notes
// field describing what an app needs to verify before relying on
// the spec.
//
// Apps that boot one of these adapters should not iterate
// PlantedFiles unconditionally — they should check Notes and either
// fall back to bespoke planting code or skip the spec until the
// adapter file is filled in.
//
// Tracked as portfolio follow-ups under the agent-boot-unification
// initiative.

// BootDirSpec for the Google Gemini CLI. TBD.
func (a *GeminiAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		Notes: "TBD: probe gemini --help for cwd-load convention (GEMINI_SYSTEM_MD env var path), config-dir support, and project-dir flag. Until probed, apps should plant bespoke files for gemini and not rely on this spec.",
	}
}

// BootDirSpec for the GitHub Copilot CLI. TBD.
func (a *CopilotAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		Notes: "TBD: probe copilot --help for system-prompt loading convention and project-dir flag. Until probed, apps should plant bespoke files for copilot and not rely on this spec.",
	}
}

// BootDirSpec for the Aider CLI. TBD.
func (a *AiderAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		Notes: "TBD: aider's --message-file plus repo-aware cwd model needs probing for the per-task tempdir convention. Apps should pass --message-file directly today.",
	}
}

// BootDirSpec for the JetBrains Junie CLI. TBD.
func (a *JunieAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		Notes: "TBD: junie's system-prompt loading convention needs probing against the installed CLI version.",
	}
}

// BootDirSpec for the Kiro CLI. TBD.
func (a *KiroAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		Notes: "TBD: kiro's system-prompt loading and project-dir conventions need probing.",
	}
}

// BootDirSpec for the Qwen CLI. TBD.
func (a *QwenAdapter) BootDirSpec() BootDirSpec {
	return BootDirSpec{
		Notes: "TBD: qwen's system-prompt loading and project-dir conventions need probing.",
	}
}
