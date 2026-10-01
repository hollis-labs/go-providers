// Package layouttest lets other modules pin their own provider path tables
// against package layout from their own tests.
//
//	exec := layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}
//	bare := layout.Shape{Mode: runtimes.ModeSubprocessPerTurn, Variant: layout.VariantBare}
//	layouttest.AssertSkillPlacement(t, runtimes.Codex, exec, layout.RootBoot, "skills")
//	layouttest.AssertProjectDirFlag(t, runtimes.Claude, bare, argv)
//	layouttest.AssertEnv(t, runtimes.Codex, exec, map[string]string{"CODEX_HOME": "boot"})
//
// The helpers only compare against layout.Table(); they run no harness. Adopting
// them is each consumer's decision. The package imports only the standard
// library, layout and the agent-contracts-leaf runtimes vocabulary.
package layouttest
