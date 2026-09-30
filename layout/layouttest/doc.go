// Package layouttest lets other modules pin their own provider path tables
// against package layout from their own tests.
//
//	layouttest.AssertSkillPlacement(t, layout.Codex, layout.ModeCodexExec, layout.RootBoot, "skills")
//	layouttest.AssertProjectDirFlag(t, layout.Claude, layout.ModeClaudeBare, argv)
//	layouttest.AssertEnv(t, layout.Codex, layout.ModeCodexExec, map[string]string{"CODEX_HOME": "boot"})
//
// The helpers only compare against layout.Table(); they run no harness. Adopting
// them is each consumer's decision. The package imports only the standard
// library and layout.
package layouttest
