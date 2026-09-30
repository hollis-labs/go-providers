#!/usr/bin/env bash
# probe-harness-layout.sh <workdir>
# Which skill / config forms do Claude Code, Codex and OpenCode actually read?
# No model call, no network: Claude's API base is pointed at a closed port and we read only the first
# stream-json line (system/init); Codex uses `debug prompt-input`; OpenCode uses `debug skill|config`.
# Writes ONLY under <workdir>. Needs: claude, codex, opencode, jq, perl, git.
set +u
W=${1:?usage: probe-harness-layout.sh <workdir>}; rm -rf "$W"; mkdir -p "$W"; W=$(cd "$W" && pwd -P)
H="$W/home"; PROJ="$W/proj"; BOOT="$W/boot"; CFG="$W/cfgdir"
mkdir -p "$H/.claude" "$H/.codex" "$H/.agents" "$H/.config/opencode" "$PROJ" "$BOOT" "$CFG"
for d in "$PROJ" "$BOOT"; do git -C "$d" init -q; done
skill() { mkdir -p "$1"; printf -- '---\nname: %s\ndescription: probe %s\n---\nbody\n' "$2" "$2" > "$1/SKILL.md"; }
flat()  { mkdir -p "$(dirname "$1")"; printf -- '---\nname: %s\ndescription: probe %s\n---\nbody\n' "$2" "$2" > "$1"; }
# The ambient shell may export OPENCODE_DISABLE_EXTERNAL_SKILLS, CLAUDE_CODE_DISABLE_BUNDLED_SKILLS, CLAUDE_CODE_*,
# CODEX_*: they change results. Every probe runs under `env -i`.
E() { env -i PATH="$PATH" HOME="$H" "$@"; }
PA=(perl -e 'alarm 30; exec @ARGV')   # hard 30 s cap per probe (macOS has no `timeout`)
say() { printf 'R\t%s\t%s\t%s\n' "$1" "$2" "$3"; }
ver() { printf 'V\tclaude=%s\tcodex=%s\topencode=%s\n' "$(claude --version 2>&1|head -1)" "$(codex --version 2>&1|head -1)" "$(opencode --version 2>&1|head -1)"; }
ver

# ---------- fixtures: one probe skill per (form, location); name encodes where it lives ----------
for h in c x o; do
  skill "$PROJ/.claude/skills/$h-proj-claude-dir" "$h-proj-claude-dir"
  flat  "$PROJ/.claude/skills/$h-proj-claude-flat.md" "$h-proj-claude-flat"
  skill "$PROJ/.agents/skills/$h-proj-agents-dir" "$h-proj-agents-dir"
  flat  "$PROJ/.agents/skills/$h-proj-agents-flat.md" "$h-proj-agents-flat"
  skill "$PROJ/.opencode/skills/$h-proj-opencode-dir" "$h-proj-opencode-dir"
  flat  "$PROJ/.opencode/skills/$h-proj-opencode-flat.md" "$h-proj-opencode-flat"
  skill "$PROJ/.codex/skills/$h-proj-codex-dir" "$h-proj-codex-dir"
  skill "$PROJ/skills/$h-proj-root-dir" "$h-proj-root-dir"
  skill "$BOOT/.claude/skills/$h-boot-claude-dir" "$h-boot-claude-dir"
  skill "$BOOT/.agents/skills/$h-boot-agents-dir" "$h-boot-agents-dir"
  skill "$BOOT/.opencode/skills/$h-boot-opencode-dir" "$h-boot-opencode-dir"
  skill "$BOOT/skills/$h-boot-root-dir" "$h-boot-root-dir"
  skill "$BOOT/skill/$h-boot-root-singular" "$h-boot-root-singular"
  flat  "$BOOT/skills/$h-boot-root-flat.md" "$h-boot-root-flat"
  skill "$H/.claude/skills/$h-home-claude-dir" "$h-home-claude-dir"
  skill "$H/.agents/skills/$h-home-agents-dir" "$h-home-agents-dir"
  skill "$H/.codex/skills/$h-home-codex-dir" "$h-home-codex-dir"
  skill "$H/.config/opencode/skills/$h-home-oc-dir" "$h-home-oc-dir"
  skill "$CFG/skills/$h-cfg-skills-dir" "$h-cfg-skills-dir"
  skill "$CFG/.opencode/skills/$h-cfg-dotopencode-dir" "$h-cfg-dotopencode-dir"
  skill "$CFG/skill/$h-cfg-skill-singular-dir" "$h-cfg-skill-singular-dir"
done

# ---------- Claude Code: skills. Observation = system/init .skills[] (no API call is made) ----------
claude_skills() { # id cwd [extra claude flags BEFORE -p] ; env via CE
  local id=$1 cwd=$2; shift 2
  local out; out=$(cd "$cwd" && E CLAUDE_CONFIG_DIR="$H/.claude" ANTHROPIC_BASE_URL=http://127.0.0.1:9 ${CE:-} "${PA[@]}" claude "$@" -p x --output-format stream-json --verbose --max-turns 1 2>/dev/null | head -1 \
    | jq -r '.skills|map(select(startswith("c-")))|sort|join(",")')
  say claude "$id" "$out"
}
claude_skills C1-cwd=proj            "$PROJ"
claude_skills C2-cwd=boot            "$BOOT"
claude_skills C3-cwd=boot+add-dir=proj "$BOOT" --add-dir "$PROJ"
CE="ANTHROPIC_API_KEY=sk-dummy" claude_skills C4-cwd=boot+bare "$BOOT" --bare
CE="ANTHROPIC_API_KEY=sk-dummy" claude_skills C5-cwd=boot+bare+add-dir=proj "$BOOT" --bare --add-dir "$PROJ"
# Claude Code: config. Observation = init .permissionMode / .mcp_servers[].name
mkdir -p "$W/cc1/.claude" "$W/cc2"; git -C "$W/cc1" init -q
echo '{"permissions":{"defaultMode":"plan"}}' > "$W/cc1/.claude/settings.json"
echo '{"mcpServers":{"probe-mcp":{"type":"http","url":"http://127.0.0.1:9/mcp"}}}' > "$W/cc1/.mcp.json"
echo '{"permissions":{"defaultMode":"acceptEdits"}}' > "$W/cc2/alt-settings.json"
claude_cfg() { local id=$1 cwd=$2; shift 2; local out; out=$(cd "$cwd" && E CLAUDE_CONFIG_DIR="$H/.claude" ANTHROPIC_BASE_URL=http://127.0.0.1:9 "${PA[@]}" claude "$@" -p x --output-format stream-json --verbose --max-turns 1 2>/dev/null | head -1 | jq -r '"mode="+.permissionMode+" mcp="+([.mcp_servers[]?.name]|join(","))'); say claude "$id" "$out"; }
claude_cfg CFG1-cwd/.claude/settings.json+cwd/.mcp.json "$W/cc1"
claude_cfg CFG2---settings=file       "$W/cc2" --settings "$W/cc2/alt-settings.json"
claude_cfg CFG3-add-dir-only          "$W/cc2" --add-dir "$W/cc1"
claude_cfg CFG4---mcp-config=file     "$W/cc2" --mcp-config "$W/cc1/.mcp.json"

# ---------- Codex: skills + config + instructions. Observation = model-visible prompt (`debug prompt-input`) ----------
codex_skills() { # id cwd CODEX_HOME|- [-C dir]
  local id=$1 cwd=$2 ch=$3; shift 3; local envs=(); [ "$ch" != "-" ] && envs=(CODEX_HOME="$ch")
  local out; out=$(cd "$cwd" && E "${envs[@]}" codex "$@" debug prompt-input hi 2>&1 | jq -r '.. | strings | select(contains("<skills_instructions>"))' | /usr/bin/grep -o '^- x-[a-z0-9-]*' | sed 's/^- //' | sort | tr '\n' ',' | sed 's/,$//')
  say codex "$id" "$out"
}
codex_skills X1-cwd=proj,CODEX_HOME=default   "$PROJ" -
codex_skills X2-cwd=boot,CODEX_HOME=boot      "$BOOT" "$BOOT"
codex_skills X3-cwd=boot,CODEX_HOME=boot,-C-proj "$BOOT" "$BOOT" -C "$PROJ"
codex_skills X4-cwd=proj,CODEX_HOME=boot      "$PROJ" "$BOOT"
mkdir -p "$W/cx/home/.codex" "$W/cx/boot" "$W/cx/proj/.codex" "$W/cx/boot2/.codex"; git -C "$W/cx/proj" init -q
echo 'developer_instructions = "TOKEN-HOME-DOTCODEX"' > "$W/cx/home/.codex/config.toml"; echo 'developer_instructions = "TOKEN-CODEX-HOME-BOOT"' > "$W/cx/boot/config.toml"
echo 'developer_instructions = "TOKEN-PROJ-DOTCODEX"' > "$W/cx/proj/.codex/config.toml"; echo 'developer_instructions = "TOKEN-WRAPPER-HOME-REDIRECT"' > "$W/cx/boot2/.codex/config.toml"
echo TOKEN-AGENTS-BOOT > "$W/cx/boot/AGENTS.md"; echo TOKEN-AGENTS-PROJ > "$W/cx/proj/AGENTS.md"
codex_cfg() { local id=$1 cwd=$2 home=$3 ch=$4; local envs=(); [ "$ch" != "-" ] && envs=(CODEX_HOME="$ch"); local out; out=$(cd "$cwd" && env -i PATH="$PATH" HOME="$home" "${envs[@]}" codex debug prompt-input hi 2>&1 | /usr/bin/grep -o 'TOKEN-[A-Z-]*' | sort -u | tr '\n' ',' | sed 's/,$//'); say codex "$id" "$out"; }
codex_cfg CFG1-cwd=proj,HOME=cx/home,CODEX_HOME=default "$W/cx/proj" "$W/cx/home" -
codex_cfg CFG2-cwd=boot,CODEX_HOME=boot                 "$W/cx/boot" "$W/cx/home" "$W/cx/boot"
codex_cfg CFG3-cwd=proj,CODEX_HOME=boot                 "$W/cx/proj" "$W/cx/home" "$W/cx/boot"
codex_cfg 'CFG4-HOME=boot2(wrapper-assumption)'         "$W/cx/boot2" "$W/cx/boot2" -

# ---------- OpenCode: skills + config. Observation = `opencode debug skill|config` ----------
oc_skills() { local id=$1 cwd=$2 cfg=$3; shift 3; local envs=(); [ "$cfg" != "-" ] && envs=(OPENCODE_CONFIG_DIR="$cfg"); local out; out=$(cd "$cwd" && E "${envs[@]}" "$@" opencode debug skill 2>/dev/null | jq -r '.[]|select(.location!="<built-in>")|.name' | /usr/bin/grep '^o-' | sort | tr '\n' ',' | sed 's/,$//'); say opencode "$id" "$out"; }
oc_skills O1-cwd=proj                          "$PROJ" -
oc_skills O2-cwd=proj,OPENCODE_CONFIG_DIR=cfg  "$PROJ" "$CFG"
oc_skills O3-cwd=boot                          "$BOOT" -
oc_skills O4-cwd=proj,DISABLE_EXTERNAL_SKILLS  "$PROJ" - env OPENCODE_DISABLE_EXTERNAL_SKILLS=1
mkdir -p "$W/oc/proj/.opencode" "$W/oc/home/.config/opencode" "$W/oc/home/.opencode" "$W/oc/cfg"; git -C "$W/oc/proj" init -q
echo '{"username":"U-PROJ"}' > "$W/oc/proj/opencode.json"; echo '{"share":"disabled"}' > "$W/oc/proj/.opencode/opencode.json"
echo '{"logLevel":"WARN"}' > "$W/oc/cfg/opencode.json"; echo '{"autoupdate":false}' > "$W/oc/home/.config/opencode/opencode.json"
oc_cfg() { local id=$1 cfg=$2; local envs=(); [ "$cfg" != "-" ] && envs=(OPENCODE_CONFIG_DIR="$cfg"); local out; out=$(cd "$W/oc/proj" && env -i PATH="$PATH" HOME="$W/oc/home" "${envs[@]}" opencode debug config 2>/dev/null | jq -c 'del(."$schema",.agent,.mode,.plugin,.command)'); say opencode "$id" "$out"; }
oc_cfg CFG1-cwd=proj -
oc_cfg CFG2-cwd=proj,OPENCODE_CONFIG_DIR "$W/oc/cfg"
