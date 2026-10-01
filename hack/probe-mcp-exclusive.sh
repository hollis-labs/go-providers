#!/usr/bin/env bash
# probe-mcp-exclusive.sh <workdir>
# Which MCP servers do Claude Code, Codex and OpenCode load next to the ones a
# launch plants? A user-level server sits in a scratch HOME; a "planted" set is
# passed the way this package's launch conventions pass it. Every probe runs
# under `env -i` with HOME in <workdir>, so no real config or credential is
# read or written. (The CLIs may still leave their own temp files in /tmp.)
#
# No model call: Claude's API base is a closed port and only the first
# stream-json line (system/init, which lists the MCP servers) is read; Codex
# lists its config (`codex mcp list`) and, for exec, is pointed at a closed
# port with a dummy key; OpenCode prints its resolved config (`debug config`).
# The stdio marker servers record that they were spawned.
# Needs: claude, codex, opencode, jq, perl; python3 (stdlib pty) for the Claude PTY probes only.
#
# Output: tab-separated lines like the layout probe's golden.
#   V  <versions>
#   R  <runtime>  <probe id>  <result>
# A result names the servers a runtime reported (mcp=) and, where the probe can
# tell, the servers it actually spawned (spawned=).
set +u
W=${1:?usage: probe-mcp-exclusive.sh <workdir>}; rm -rf "$W"; mkdir -p "$W"; W=$(cd "$W" && pwd -P)
H="$W/home"; BOOT="$W/boot"; PROJ="$W/proj"; MARKS="$W/marks"
mkdir -p "$H/.codex" "$H/.config/opencode" "$BOOT" "$PROJ" "$MARKS" "$W/empty-xdg"
E() { env -i PATH="$PATH" HOME="$H" "$@"; }
PA=(perl -e 'alarm 45; exec @ARGV')   # hard cap per probe (macOS has no `timeout`)
say() { printf 'R\t%s\t%s\t%s\n' "$1" "$2" "$3"; }
printf 'V\tclaude=%s\tcodex=%s\topencode=%s\n' "$(claude --version 2>&1|head -1)" "$(codex --version 2>&1|head -1)" "$(opencode --version 2>&1|head -1)"

# A stdio MCP server that records it was spawned, then answers just enough MCP
# (initialize, tools/list) for a client to call it connected.
cat > "$W/marker-mcp.sh" <<'EOS'
#!/usr/bin/env bash
mkdir -p "$MARKER_DIR"; : > "$MARKER_DIR/$1.started"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | jq -c '.id // empty'); [ -z "$id" ] && continue
  case "$(printf '%s' "$line" | jq -r '.method // empty')" in
    initialize) pv=$(printf '%s' "$line" | jq -r '.params.protocolVersion // "2025-06-18"')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"%s","capabilities":{"tools":{}},"serverInfo":{"name":"%s","version":"0"}}}\n' "$id" "$pv" "$1";;
    tools/list) printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}\n' "$id";;
    *) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id";;
  esac
done
EOS
chmod +x "$W/marker-mcp.sh"
spawned() { local f out=""; for f in "$MARKS"/*.started; do [ -e "$f" ] && out="$out$(basename "$f" .started),"; done; printf '%s' "${out%,}"; }
clear_marks() { rm -f "$MARKS"/*.started; }

# ---------- Claude Code. Observation = system/init .mcp_servers[].name + the markers ----------
# User-level server in the scratch ~/.claude.json; planted set in <boot>/.mcp.json (cwd = boot, as the
# convention launches it) passed with --mcp-config.
cat > "$H/.claude.json" <<EOF
{"mcpServers":{"user-probe":{"type":"stdio","command":"$W/marker-mcp.sh","args":["user-probe"],"env":{"MARKER_DIR":"$MARKS"}}}}
EOF
cat > "$BOOT/.mcp.json" <<EOF
{"mcpServers":{"planted-probe":{"type":"stdio","command":"$W/marker-mcp.sh","args":["planted-probe"],"env":{"MARKER_DIR":"$MARKS"}}}}
EOF
git -C "$BOOT" init -q
claude_mcp() { # id [env assignments via CE] ; claude flags after -- ; mode flags before -p via MODE
  local id=$1; shift; clear_marks
  local names; names=$(cd "$BOOT" && E ANTHROPIC_BASE_URL=http://127.0.0.1:9 ${CE:-} "${PA[@]}" claude "$@" -p x --output-format stream-json --verbose --max-turns 1 2>/dev/null | head -1 | jq -r '[.mcp_servers[]?.name]|sort|join(",")')
  sleep 1; say claude "$id" "mcp=$names spawned=$(spawned)"
}
claude_mcp_stream() { # streaming-stdio: the init line follows the first stdin frame
  local id=$1; shift; clear_marks
  local names; names=$(cd "$BOOT" && printf '{"type":"user","message":{"role":"user","content":"x"}}\n' | E ANTHROPIC_BASE_URL=http://127.0.0.1:9 "${PA[@]}" claude "$@" -p --input-format stream-json --output-format stream-json --verbose --max-turns 1 2>/dev/null | head -1 | jq -r '[.mcp_servers[]?.name]|sort|join(",")')
  sleep 1; say claude "$id" "mcp=$names spawned=$(spawned)"
}
claude_mcp MCP1-print+mcp-config                       --mcp-config "$BOOT/.mcp.json"
claude_mcp MCP2-print+mcp-config+strict                --mcp-config "$BOOT/.mcp.json" --strict-mcp-config
claude_mcp MCP3-print+strict,nothing-passed            --strict-mcp-config
claude_mcp MCP4-print,neither-flag
CE="ANTHROPIC_API_KEY=sk-dummy" claude_mcp MCP5-bare+mcp-config        --bare --mcp-config "$BOOT/.mcp.json"
CE="ANTHROPIC_API_KEY=sk-dummy" claude_mcp MCP6-bare+mcp-config+strict --bare --mcp-config "$BOOT/.mcp.json" --strict-mcp-config
claude_mcp_stream MCP7-streaming+mcp-config            --mcp-config "$BOOT/.mcp.json"
claude_mcp_stream MCP8-streaming+mcp-config+strict     --mcp-config "$BOOT/.mcp.json" --strict-mcp-config

# Claude's PTY (the TUI) starts MCP servers only after its first-run dialogs (onboarding, the API key, the
# project's MCP servers), so the scratch config pre-approves all three; a python3 pty drives it for 20 s and the
# markers show what spawned. The API key is a dummy and the API base a closed port.
PH="$W/ptyhome"; PB="$W/ptyboot"; mkdir -p "$PH" "$PB"; cp "$BOOT/.mcp.json" "$PB/.mcp.json"; git -C "$PB" init -q
PKEY=sk-dummy-for-the-probe-only-0123456789
cat > "$PH/.claude.json" <<EOF
{"hasCompletedOnboarding":true,"theme":"dark","customApiKeyResponses":{"approved":["${PKEY: -20}"],"rejected":[]},
 "mcpServers":{"user-probe":{"type":"stdio","command":"$W/marker-mcp.sh","args":["user-probe"],"env":{"MARKER_DIR":"$MARKS"}}},
 "projects":{"$PB":{"hasTrustDialogAccepted":true,"hasCompletedProjectOnboarding":true,"enableAllProjectMcpServers":true}}}
EOF
cat > "$W/pty-drive.py" <<'EOS'
import os, pty, select, signal, sys, time
cwd, secs, args = sys.argv[1], float(sys.argv[2]), sys.argv[3:]
pid, fd = pty.fork()
if pid == 0:
    os.chdir(cwd); os.execvpe("claude", ["claude"] + args, os.environ)
end = time.time() + secs
while time.time() < end:
    if select.select([fd], [], [], 0.5)[0]:
        try: os.read(fd, 65536)
        except OSError: break
os.kill(pid, signal.SIGKILL)
EOS
claude_pty() { local id=$1; shift; clear_marks
  if command -v python3 >/dev/null; then
    env -i PATH="$PATH" HOME="$PH" TERM=xterm-256color ANTHROPIC_API_KEY="$PKEY" ANTHROPIC_BASE_URL=http://127.0.0.1:9 python3 "$W/pty-drive.py" "$PB" 20 "$@" >/dev/null 2>&1
    sleep 1; say claude "$id" "spawned=$(spawned)"
  else say claude "$id" "skipped (no python3)"; fi
}
claude_pty MCP9-pty+mcp-config                --mcp-config "$PB/.mcp.json"
claude_pty MCP10-pty+mcp-config+strict        --mcp-config "$PB/.mcp.json" --strict-mcp-config

# ---------- Codex. Observation = `codex mcp list` (the config), and for exec the markers ----------
# User-level server in the scratch ~/.codex/config.toml; planted set in <cxboot>/config.toml (CODEX_HOME = boot,
# as the convention launches it); a project .codex/config.toml with a third server.
CXB="$W/cxboot"; CXP="$W/cxproj"; mkdir -p "$CXB" "$CXP/.codex"; git -C "$CXB" init -q; git -C "$CXP" init -q
toml() { printf '[mcp_servers.%s]\ncommand = "%s/marker-mcp.sh"\nargs = ["%s"]\nenv = { MARKER_DIR = "%s" }\n' "$1" "$W" "$1" "$MARKS"; }
toml user-probe > "$H/.codex/config.toml"; toml planted-probe > "$CXB/config.toml"; toml project-probe > "$CXP/.codex/config.toml"
codex_list() { local id=$1 cwd=$2; shift 2; local out; out=$(cd "$cwd" && E "$@" "${PA[@]}" codex mcp list --json 2>/dev/null | jq -r '[.[]?.name]|sort|join(",")'); say codex "$id" "mcp=$out"; }
codex_exec() { local id=$1 cwd=$2; shift 2; clear_marks; (cd "$cwd" && E OPENAI_API_KEY=sk-dummy OPENAI_BASE_URL=http://127.0.0.1:9 "$@" "${PA[@]}" codex exec --skip-git-repo-check x >/dev/null 2>&1); sleep 1; say codex "$id" "spawned=$(spawned)"; }
codex_list MCP1-CODEX_HOME=default,cwd=boot     "$CXB"
codex_list MCP2-CODEX_HOME=boot                  "$CXB" CODEX_HOME="$CXB"
codex_list MCP3-CODEX_HOME=boot,cwd=project      "$CXP" CODEX_HOME="$CXB"
codex_list MCP4-CODEX_HOME=default,cwd=project   "$CXP"
codex_exec MCP5-exec,CODEX_HOME=default          "$CXB"
codex_exec MCP6-exec,CODEX_HOME=boot             "$CXB" CODEX_HOME="$CXB"

# ---------- OpenCode. Observation = the resolved config's mcp servers (`opencode debug config`) ----------
# User-level server in the scratch ~/.config/opencode/opencode.json; planted set in <ocboot>/opencode.json
# (OPENCODE_CONFIG_DIR = boot, cwd = project, as the convention launches it); a project opencode.json.
OCB="$W/ocboot"; OCP="$W/ocproj"; mkdir -p "$OCB" "$OCP"; git -C "$OCP" init -q
ocjson() { printf '{"mcp":{"%s":{"type":"local","command":["%s/marker-mcp.sh","%s"],"environment":{"MARKER_DIR":"%s"},"enabled":true}}}\n' "$1" "$W" "$1" "$MARKS"; }
ocjson user-probe > "$H/.config/opencode/opencode.json"; ocjson planted-probe > "$OCB/opencode.json"; ocjson project-probe > "$OCP/opencode.json"
oc_cfg() { local id=$1; shift; local out; out=$(cd "$OCP" && E OPENCODE_CONFIG_DIR="$OCB" "$@" "${PA[@]}" opencode debug config 2>/dev/null | jq -r '[.mcp // {} | keys[]]|sort|join(",")'); say opencode "$id" "mcp=$out"; }
oc_cfg MCP1-OPENCODE_CONFIG_DIR=boot
oc_cfg MCP2-XDG_CONFIG_HOME=empty                XDG_CONFIG_HOME="$W/empty-xdg"
oc_cfg MCP3-DISABLE_PROJECT_CONFIG               OPENCODE_DISABLE_PROJECT_CONFIG=1
oc_cfg MCP4-XDG_CONFIG_HOME=empty+DISABLE_PROJECT_CONFIG XDG_CONFIG_HOME="$W/empty-xdg" OPENCODE_DISABLE_PROJECT_CONFIG=1
