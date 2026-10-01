# Consumers: pinning your table to `layout`

`layout.Table()` is the one answer to "which file, skill, flag or environment
variable does agent CLI X need, relative to which root". agentkit
(`agentlaunch`) and go-agent-wrapper (`plant`) read it through `layout.Find`
and `layout.SkillRoot`; adopting the `layouttest` pins is each consumer's
decision. This page lists every place the workspace answered that question
when it was surveyed and the `layouttest` call that would pin it in that
repo's own tests. Locations were measured 2026-09-25 (file:line, plus or
minus a few) and predate those reads; re-check before relying on them.

Values passed to the calls below are the ones the table holds today. Where a
consumer's current value differs, the call fails, which is the point: the
difference is a finding, and each one is recorded in `CHANGELOG.md` (v0.27.0,
"What consumers can delete after adopting `layout`") and `docs/HARNESS-DISCOVERY.md`. Import paths:
`github.com/hollis-labs/go-providers/layout` and
`github.com/hollis-labs/go-providers/layout/layouttest`. Consumers need
`go >= 1.26.6` and a go-providers release that contains this package.

Non-Go readers (Cairn YAML layouts, agent-launcher) read
`layout/layout.json` instead: `{"schema":1,"entries":[{provider, mode, variant, concern, root, rel, form, file_mode, flag, env, cwd, probe, unprobed, note}]}`.
A conformance test there loads the JSON and applies the same three comparisons
as the calls below.

## The nine path tables

| # | Table (location) | Pin with | Expected today |
|---|---|---|---|
| 1 | go-providers legacy `BootDirSpec` (`provider/bootdir_{claude,codex,opencode,antigravity}.go`) | already pinned in-repo: `TestBootDirSpecEqualsLayout` | derived from the table |
| 2 | go-providers `ProviderProjection` + `LaunchConvention` (`provider/projection.go`) | already pinned in-repo: `TestLayoutRegression_NonSkillProjectionUnchanged`, `TestProjectedSkillPlacementIsReadByHarness` | derived from the table |
| 3 | agentkit legacy skill path, two copies (`agentlaunch/providerplant/plant.go:386`, `agentlaunch/materialize.go:407`) | `layouttest.AssertSkillPlacement(t, runtimes.Claude, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, ".claude/skills")`, `...(t, runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, "skills")`, `...(t, runtimes.OpenCode, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, "skills")` with the prefix the mapping actually returns; also assert the form is directory (`<prefix>/<id>/SKILL.md`) | fails: flat `<id>.md`, opencode `.opencode/skills`, codex `skills/<id>.md`. Status: the opencode path was fixed in agentkit v0.7.0 and the claude path in v0.9.0 |
| 4 | agentkit provider by runtime legality + boot renderer (`agentlaunch/matrix/matrix.go:125`) | not a layout concern (support matrix); out of scope | n/a |
| 5 | go-agent-wrapper `providerSettingsPath` + `hookPath` (`plant/plant.go:203-227`) | `layouttest.AssertEnv(t, runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, map[string]string{"CODEX_HOME": "boot"})` for the mechanism it assumes; native config path: `layout.Find(runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.NativeConfig)` `.Rel == "config.toml"` | codex `.codex/config.toml` is right only with HOME redirected (probe CFG4); opencode `.config/opencode/opencode.json` differs from `opencode.json` under `OPENCODE_CONFIG_DIR` |
| 5b | go-agent-wrapper adapter `Resolve` argv (`adapters/{claude,codex,opencode}/*.go`) | `layouttest.AssertProjectDirFlag(t, runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, argv)` and the claude/opencode equivalents on the resolved argv | passes when the flag is `--cd` / `--add-dir` / `--dir` |
| 6 | Cairn layouts (`cairn/bootdir/layouts/{claude,codex}.yaml`) | read `layout.json`; for each skills row assert `root`, `rel` and `form` equal the YAML's skills destination; Go-side equivalent: `layouttest.AssertSkillPlacement(t, runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, "skills")` | fails for codex (`.agents/skills`, lost when `--cd` is passed, probes X2 vs X3) |
| 7 | Nanite per-provider planters (`internal/runtime/agent/bootdir_*.go`, `skill_plant.go:325-362`) | `layouttest.AssertSkillPlacement(t, runtimes.OpenCode, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, "skills")`; `...(t, runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, "skills")`; `layouttest.AssertEnv(t, runtimes.OpenCode, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, map[string]string{"OPENCODE_CONFIG_DIR": "boot"})` | codex "no native skill mechanism" is wrong; the `.opencode/skills` copy is dead when cwd is the project (probe O2) |
| 8 | agent-launcher `HarnessArgv` (`internal/boot/invoke.go:234-247`) | read `layout.json`; assert the codex `project-dir` row's `flag` is `--cd` (working root) versus the `--add-dir` it passes (extra directory, different meaning) | fails for codex `--add-dir` |
| 9 | go-providers argv builders (`provider/argv.go`; `BuildArgs` in `pty_{claude,codex,opencode,antigravity}.go`, `bootdir_claude.go BareInjectionPaths`) | already pinned in-repo: `TestBuildArgsMatchesProjectionResolveTurn`, `TestNoPositionalAfterAddDir` | derived from the table: `BuildArgs` resolves the same per-runtime convention as the projection, with path flags from the adapter's fields |

## The five skill authors

Every author must place skills at `layout.SkillRoot(runtime, shape)` in the
directory form. Content pinning uses `provider.SkillPackage.Hash` /
`TreeHash`, which is go-agentdef's `sha256:<hex>` over the sorted whole skill
tree.

| # | Author (location) | Pin with | Expected today |
|---|---|---|---|
| 1 | Tether `internal/skills` (`skills.go:164-249`, converted at `app/agent_ops.go:264-296`) | `layouttest.AssertSkillPlacement(t, runtimes.Claude, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn}, layout.RootBoot, prefixTetherUsesForClaude)`; codex writes skills as sections inside `AGENTS.md`, which is not a skills row (`layout.SkillRoot(runtimes.Codex, layout.Shape{Mode: runtimes.ModeSubprocessPerTurn})` is `skills/`) | fails: flat `.claude/skills/<id>.md`; codex sections are not a skill and clobber `AGENTS.md` |
| 2 | agentkit `NativeFileSkill` mapping (two copies, table 3) | as table 3 | fails as table 3 |
| 3 | Nanite `skill_plant.go` (`:218-460`) | as table 7 | claude passes; opencode dual planting half dead; codex missing |
| 4 | go-providers `projectSkillPackages` (`provider/projection.go`) | in-repo: `TestProjectedSkillPlacementIsReadByHarness` | table-derived; the only author with a probe-checked test |
| 5 | Cairn `bootdir/skills.go` | as table 6 | fails for codex |

## Adding a probe row

New or changed rows need a probe id that exists in the newest golden under
`provider/testdata/harness-discovery/` (or an `Unprobed` reason). Extend
`hack/probe-harness-layout.sh`, re-run it, commit the new TSV, and update
`docs/HARNESS-DISCOVERY.md`; `layout` tests refuse rows whose ids are missing.
