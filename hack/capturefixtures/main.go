//go:build ignore

// Command capturefixtures re-records the live claude and codex fixtures
// under providertest/fixtures and scrubs them for a public repository.
//
//	go run hack/capturefixtures/main.go -runtimes claude,codex
//	go run hack/capturefixtures/main.go -runtimes claude -only stream_interrupt
//
// -only re-records just the named fixtures (and runs, without writing, the
// captures they depend on), merging into the runtime's captured.json. It
// refuses when the CLI's version differs from the manifest's: a manifest
// names one version for every fixture it lists.
//
// It makes real model calls (the cheapest model each CLI offers, trivial
// prompts) and the CLIs write their usual session files under their own
// state directories. Every fixture is scrubbed before it is written:
// session/thread/message ids become placeholders, the capture directory
// becomes /work/project, the home directory becomes /home/user, and opaque
// blobs (thinking signatures, encrypted reasoning) become REDACTED. Run the
// scrub grep in providertest/fixtures/README.md before committing.
//
// opencode's run fixtures and the antigravity fixtures were captured by hand
// earlier and are not re-recorded here (-runtimes opencode records only
// serve_abort); copilot and pi fixtures are synthetic.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	outDir      = flag.String("out", "providertest/fixtures", "fixture root")
	runtimesArg = flag.String("runtimes", "claude,codex", "comma-separated runtimes to capture")
	claudeModel = flag.String("claude-model", "haiku", "claude --model")
	codexModel  = flag.String("codex-model", "gpt-6-luna", "codex -m")
	timeout     = flag.Duration("timeout", 3*time.Minute, "per-capture timeout")
	onlyArg     = flag.String("only", "", "comma-separated fixture stems to re-record; empty records all")
)

const (
	trivialPrompt = "say hi"
	secondPrompt  = "say bye"
	toolPrompt    = "Run the shell command `echo providertest` and reply with its output only."
	slowPrompt    = "Run the shell command `ping -c 30 127.0.0.1` and reply with its last line only."
	// writePrompt needs a permission the default posture does not grant.
	writePrompt = "Run the shell command `touch providertest.txt`, then say done."
	// lostID is already a placeholder, so the scrubber leaves it alone and
	// the session-lost fixtures carry the id the fake will be asked for.
	lostID = "00000000-0000-4000-8000-0000000000ff"
)

func main() {
	flag.Parse()
	work, err := os.MkdirTemp("", "capturefixtures-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(work)
	work, _ = filepath.EvalSymlinks(work)
	proj := filepath.Join(work, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, rt := range strings.Split(*runtimesArg, ",") {
		c := newCapturer(strings.TrimSpace(rt), work, proj)
		switch c.runtime {
		case "claude":
			c.claude()
		case "codex":
			c.codex()
		case "opencode":
			c.opencode()
		default:
			log.Fatalf("no capture plan for runtime %q", c.runtime)
		}
		c.writeManifest()
	}
}

type capturer struct {
	runtime string
	dir     string
	proj    string
	scrub   *scrubber
	version string
	argv    map[string][]string
}

func newCapturer(runtime, work, proj string) *capturer {
	dir := filepath.Join(*outDir, runtime)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	return &capturer{runtime: runtime, dir: dir, proj: proj, scrub: newScrubber(work, proj), argv: map[string][]string{}}
}

// want reports whether any of stems is to be recorded: always, unless -only
// names others.
func (c *capturer) want(stems ...string) bool {
	if *onlyArg == "" {
		return true
	}
	for _, only := range strings.Split(*onlyArg, ",") {
		for _, stem := range stems {
			if strings.TrimSpace(only) == stem {
				return true
			}
		}
	}
	return false
}

// --- claude ---------------------------------------------------------------

// claudeIsolation keeps the operator's own settings, hooks, MCP servers and
// skills out of the capture: only the CLI's built-in surface shows up.
var claudeIsolation = []string{"--setting-sources", "local", "--strict-mcp-config", "--disable-slash-commands"}

func (c *capturer) claudePrint(stem string, pre []string, prompt string, extra ...string) result {
	args := append([]string{}, pre...)
	args = append(args, "-p", prompt, "--output-format", "stream-json", "--verbose", "--model", *claudeModel)
	args = append(args, claudeIsolation...)
	args = append(args, extra...)
	return c.perTurn(stem, "claude", args)
}

func (c *capturer) claude() {
	c.version = cliVersion("claude")
	if c.want("print_turn1", "print_turn2_resume") {
		t1 := c.claudePrint("print_turn1", nil, trivialPrompt)
		if c.want("print_turn2_resume") {
			c.claudePrint("print_turn2_resume", []string{"--resume", t1.sessionID()}, secondPrompt)
		}
	}
	if c.want("print_resume_unknown_id") {
		c.claudePrint("print_resume_unknown_id", []string{"--resume", lostID}, trivialPrompt)
	}
	if c.want("print_tool_use") {
		c.claudePrint("print_tool_use", nil, toolPrompt, "--allowedTools", "Bash(echo:*)")
	}
	if c.want("print_tool_denied") {
		c.claudePrint("print_tool_denied", nil, writePrompt)
	}
	if c.want("print_error_unknown_model") {
		c.perTurn("print_error_unknown_model", "claude", append([]string{"-p", trivialPrompt, "--output-format", "stream-json", "--verbose", "--model", "claude-nonexistent-0"}, claudeIsolation...))
	}

	streamArgs := func(pre ...string) []string {
		args := append([]string{}, pre...)
		args = append(args, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--model", *claudeModel)
		return append(args, claudeIsolation...)
	}
	user := func(text string) []byte {
		b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
		return b
	}
	untilResult := func(line []byte) bool { return jsonField(line, "type") == "result" }

	if c.want("stream_two_turns", "stream_resume") {
		var streamSID string
		c.duplex("stream_two_turns", "claude", streamArgs(), func(d *duplexSession) {
			d.send(user(trivialPrompt))
			res := d.readUntil(untilResult)
			streamSID = jsonField(res, "session_id")
			d.send(user(secondPrompt))
			d.readUntil(untilResult)
		})
		if c.want("stream_resume") {
			c.duplex("stream_resume", "claude", streamArgs("--resume", streamSID), func(d *duplexSession) {
				d.send(user(secondPrompt))
				d.readUntil(untilResult)
			})
		}
	}
	if c.want("stream_resume_unknown_id") {
		c.duplex("stream_resume_unknown_id", "claude", streamArgs("--resume", lostID), func(d *duplexSession) {
			d.send(user(trivialPrompt))
			d.readUntil(untilResult)
		})
	}
	// A turn interrupted mid-tool by a control_request, then a turn on the
	// same process: the interrupt ends the turn (a control_response, the
	// tool rejected, an error_during_execution result) and keeps the
	// process (CW-20261001-0103). The 30-second ping keeps the tool running
	// while the interrupt goes in; it is allowed and nothing else is. Claude
	// Code refuses a sleep outright ("Blocked: standalone sleep"), so a
	// sleep would end the tool before the interrupt arrived.
	if c.want("stream_interrupt") {
		c.duplex("stream_interrupt", "claude", streamArgs("--allowedTools", "Bash(ping:*)"), func(d *duplexSession) {
			d.send(user(slowPrompt))
			d.readUntil(func(line []byte) bool { return bytes.Contains(line, []byte(`"type":"tool_use"`)) })
			time.Sleep(time.Second)
			d.send([]byte(`{"type":"control_request","request_id":"req_interrupt_1","request":{"subtype":"interrupt"}}`))
			d.readUntil(untilResult)
			d.send(user(secondPrompt))
			d.readUntil(untilResult)
		})
	}
}

// --- codex ----------------------------------------------------------------

func (c *capturer) codex() {
	c.version = cliVersion("codex")
	model := []string{"-m", *codexModel, "-c", `model_reasoning_effort="low"`}
	codexExec := func(stem string, args ...string) result {
		return c.perTurn(stem, "codex", append(append([]string{"exec"}, args...), model...))
	}
	// codexResume is a resume turn as CodexAdapter builds it: every exec
	// option in front of the resume subcommand, where codex applies it to the
	// resumed turn, and the prompt last, after "--" (CW-20261001-0109).
	codexResume := func(stem, thread, prompt string) result {
		args := append(append([]string{"exec"}, model...), "--json", "--skip-git-repo-check", "--cd", c.proj, "resume", thread, "--", prompt)
		return c.perTurn(stem, "codex", args)
	}
	if c.want("exec_turn1", "exec_turn2_resume") {
		t1 := codexExec("exec_turn1", trivialPrompt, "--json", "--skip-git-repo-check")
		if c.want("exec_turn2_resume") {
			codexResume("exec_turn2_resume", t1.field("thread_id"), secondPrompt)
		}
	}
	if c.want("exec_resume_unknown_id") {
		codexResume("exec_resume_unknown_id", lostID, trivialPrompt)
	}
	if c.want("exec_tool_use") {
		codexExec("exec_tool_use", toolPrompt, "--json", "--skip-git-repo-check", "-s", "read-only")
	}
	if c.want("exec_error_unknown_model") {
		c.perTurn("exec_error_unknown_model", "codex", []string{"exec", trivialPrompt, "--json", "--skip-git-repo-check", "-m", "gpt-nonexistent-0"})
	}

	rpc := func(d *duplexSession) *rpcClient { return &rpcClient{d: d} }
	initialize := func(r *rpcClient) {
		r.call("initialize", map[string]any{"clientInfo": map[string]any{"name": "providertest-capture", "version": "0"}})
		r.notify("initialized", nil)
	}
	turn := func(r *rpcClient, threadID, text string) {
		r.call("turn/start", map[string]any{
			"threadId": threadID,
			"input":    []map[string]any{{"type": "text", "text": text}},
			"effort":   "low",
		})
		r.d.readUntil(func(line []byte) bool { return jsonField(line, "method") == "turn/completed" })
	}
	if c.want("app_server_turn", "app_server_resume", "app_server_resume_unknown_id", "app_server_tool_approval") {
		var appThread string
		c.duplex("app_server_turn", "codex", []string{"app-server"}, func(d *duplexSession) {
			r := rpc(d)
			initialize(r)
			res := r.call("thread/start", map[string]any{"cwd": c.proj, "model": *codexModel})
			appThread = threadID(res)
			turn(r, appThread, trivialPrompt)
			turn(r, appThread, secondPrompt)
		})
		c.duplex("app_server_resume", "codex", []string{"app-server"}, func(d *duplexSession) {
			r := rpc(d)
			initialize(r)
			r.call("thread/resume", map[string]any{"threadId": appThread, "model": *codexModel})
			turn(r, appThread, trivialPrompt)
		})
		c.duplex("app_server_resume_unknown_id", "codex", []string{"app-server"}, func(d *duplexSession) {
			r := rpc(d)
			initialize(r)
			r.call("thread/resume", map[string]any{"threadId": lostID})
		})
		// thread/start's per-thread approvalPolicy still took "untrusted" on
		// codex-cli 0.159.2; the config key does not (CW-20261001-0127).
		c.duplex("app_server_tool_approval", "codex", []string{"app-server"}, func(d *duplexSession) {
			r := rpc(d)
			initialize(r)
			res := r.call("thread/start", map[string]any{
				"cwd": c.proj, "model": *codexModel,
				"approvalPolicy": "untrusted", "sandbox": "read-only",
			})
			turn(r, threadID(res), "Create an empty file named providertest.txt in the current directory with the shell command `touch providertest.txt`, then say done.")
		})
	}
	// A turn interrupted while its command runs, then a turn on the same
	// process and thread: turn/interrupt answers {}, the turn completes with
	// status "interrupted" (CW-20261001-0160). The sandbox lets the sleep run
	// unasked; nothing else is asked for.
	if c.want("app_server_interrupt") {
		c.duplex("app_server_interrupt", "codex", []string{"app-server", "-c", `sandbox_mode="workspace-write"`, "-c", `approval_policy="never"`}, func(d *duplexSession) {
			r := rpc(d)
			initialize(r)
			thread := threadID(r.call("thread/start", map[string]any{"cwd": c.proj, "model": *codexModel}))
			started := r.call("turn/start", map[string]any{
				"threadId": thread,
				"input":    []map[string]any{{"type": "text", "text": "Run the shell command `sleep 30` and reply done."}},
				"effort":   "low",
			})
			var res struct {
				Result struct {
					Turn struct {
						ID string `json:"id"`
					} `json:"turn"`
				} `json:"result"`
			}
			_ = json.Unmarshal(started, &res)
			d.readUntil(func(line []byte) bool {
				return jsonField(line, "method") == "item/started" && bytes.Contains(line, []byte(`"commandExecution"`))
			})
			time.Sleep(time.Second)
			r.call("turn/interrupt", map[string]any{"threadId": thread, "turnId": res.Result.Turn.ID})
			d.readUntil(func(line []byte) bool { return jsonField(line, "method") == "turn/completed" })
			turn(r, thread, secondPrompt)
		})
	}
}

// --- opencode serve -------------------------------------------------------

// opencodeServeNoise are event types `opencode serve` streams about the
// server itself (its plugins, model catalog, integrations), not the session.
// A capture drops them; every other event is kept.
var opencodeServeNoise = map[string]bool{
	"plugin.added": true, "catalog.updated": true, "integration.updated": true, "reference.updated": true,
}

func (c *capturer) opencode() {
	c.version = cliVersion("opencode")
	// A turn aborted while its bash command runs, then a turn on the same
	// server and session: POST /session/{id}/abort answers true, the turn
	// ends with session.error MessageAbortedError then session.idle, and the
	// aborted tool's cleanup follows with a second session.idle
	// (CW-20261001-0160).
	if c.want("serve_abort") {
		c.httpCapture("serve_abort", "opencode", []string{"serve", "--port", "0", "--hostname", "127.0.0.1"}, func(h *httpSession) {
			sid := jsonField(h.post("/session", map[string]any{}), "id")
			h.post("/session/"+sid+"/prompt_async", map[string]any{"parts": []map[string]any{{"type": "text", "text": "Run the shell command `sleep 30` with the bash tool, then reply done."}}})
			h.until(func(ev []byte) bool {
				return bytes.Contains(ev, []byte(`"type":"tool"`)) && bytes.Contains(ev, []byte(`"status":"running"`))
			})
			time.Sleep(time.Second)
			h.post("/session/"+sid+"/abort", nil)
			h.until(func(ev []byte) bool { return jsonField(ev, "type") == "session.idle" })
			// The aborted tool's cleanup arrives after the first idle,
			// with a second session.idle of its own; let it land before
			// the next turn so the fixture keeps the two apart.
			time.Sleep(2 * time.Second)
			h.post("/session/"+sid+"/prompt_async", map[string]any{"parts": []map[string]any{{"type": "text", "text": secondPrompt}}})
			h.until(func(ev []byte) bool { return bytes.Contains(ev, []byte(`"type":"busy"`)) })
			h.until(func(ev []byte) bool { return jsonField(ev, "type") == "session.idle" })
		})
	}
}

// httpSession records an HTTP server's exchange with one client: each
// request and its response, and the server-sent events in the order they
// arrived. The file is <stem>.http.jsonl, one step per line:
// {"request":{"method","path","body"}}, {"response":{"status","body"}},
// {"event":{...}}.
type httpSession struct {
	c      *capturer
	base   string
	dir    string
	mu     sync.Mutex
	steps  []string
	events chan []byte
}

func (c *capturer) httpCapture(stem, bin string, args []string, drive func(*httpSession)) {
	log.Printf("%s/%s: %s %s (http)", c.runtime, stem, bin, strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.proj
	out, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	listen := regexp.MustCompile(`listening on (http://\S+)`)
	sc := bufio.NewScanner(out)
	var base string
	for sc.Scan() {
		if m := listen.FindStringSubmatch(sc.Text()); m != nil {
			base = m[1]
			break
		}
	}
	if base == "" {
		log.Fatalf("%s: no listening line", bin)
	}
	go func() {
		for sc.Scan() {
		}
	}()
	h := &httpSession{c: c, base: base, dir: c.proj, events: make(chan []byte, 4096)}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/event?directory="+url.QueryEscape(c.proj), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		defer func() { _ = resp.Body.Close() }()
		r := bufio.NewReaderSize(resp.Body, 1<<20)
		for {
			line, err := r.ReadBytes('\n')
			if data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:")); ok {
				data = bytes.TrimSpace(data)
				if !opencodeServeNoise[jsonField(data, "type")] && json.Valid(data) {
					h.record("event", data)
				}
				h.events <- data
			}
			if err != nil {
				close(h.events)
				return
			}
		}
	}()
	drive(h)
	time.Sleep(500 * time.Millisecond) // let the last events land
	h.mu.Lock()
	steps := append([]string(nil), h.steps...)
	h.mu.Unlock()
	if !c.want(stem) {
		return
	}
	c.write(stem+".http.jsonl", []byte(strings.Join(steps, "\n")+"\n"))
	c.argv[stem] = c.scrub.args(append([]string{bin}, args...))
}

func (h *httpSession) record(kind string, raw []byte) {
	scrubbed := h.c.scrub.line(raw)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps = append(h.steps, fmt.Sprintf(`{%q:%s}`, kind, scrubbed))
}

// post sends a POST under the session's directory and records the
// exchange; it returns the response body.
func (h *httpSession) post(path string, body any) []byte {
	var rd io.Reader
	reqBody := []byte("null")
	if body != nil {
		reqBody, _ = json.Marshal(body)
		rd = bytes.NewReader(reqBody)
	}
	req, _ := http.NewRequest(http.MethodPost, h.base+path+"?directory="+url.QueryEscape(h.dir), rd)
	req.Header.Set("Content-Type", "application/json")
	h.record("request", []byte(fmt.Sprintf(`{"method":"POST","path":%q,"body":%s}`, path, reqBody)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	respBody := bytes.TrimSpace(b)
	if len(respBody) == 0 || !json.Valid(respBody) {
		respBody, _ = json.Marshal(string(b))
	}
	h.record("response", []byte(fmt.Sprintf(`{"status":%d,"body":%s}`, resp.StatusCode, respBody)))
	return b
}

// until waits for an event done accepts.
func (h *httpSession) until(done func([]byte) bool) {
	deadline := time.After(*timeout)
	for {
		select {
		case ev, ok := <-h.events:
			if !ok {
				log.Fatalf("%s: event stream ended", h.c.runtime)
			}
			if done(ev) {
				return
			}
		case <-deadline:
			log.Fatalf("%s: timed out waiting for an event", h.c.runtime)
		}
	}
}

func threadID(res []byte) string {
	var v struct {
		Result struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		} `json:"result"`
	}
	_ = json.Unmarshal(res, &v)
	if v.Result.Thread.ID == "" {
		log.Fatalf("thread/start: no thread id in %s", res)
	}
	return v.Result.Thread.ID
}

// --- per-turn capture -----------------------------------------------------

type result struct {
	stdout [][]byte // raw, unscrubbed
}

func (r result) field(key string) string {
	for _, l := range r.stdout {
		if v := jsonField(l, key); v != "" {
			return v
		}
	}
	log.Fatalf("no %q in capture", key)
	return ""
}

func (r result) sessionID() string { return r.field("session_id") }

func (c *capturer) perTurn(stem, bin string, args []string) result {
	log.Printf("%s/%s: %s %s", c.runtime, stem, bin, strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.proj
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := exitCode(cmd.Run())

	lines := splitLines(stdout.Bytes())
	if !c.want(stem) {
		return result{stdout: lines}
	}
	var out bytes.Buffer
	for _, l := range lines {
		out.Write(c.scrub.line(l))
		out.WriteByte('\n')
	}
	c.write(stem+".jsonl", out.Bytes())
	if s := bytes.TrimSpace(stderr.Bytes()); len(s) > 0 {
		c.write(stem+".stderr", append(c.scrub.line(s), '\n'))
	}
	if code != 0 {
		c.write(stem+".exit", []byte(strconv.Itoa(code)+"\n"))
	}
	c.argv[stem] = c.scrub.args(append([]string{bin}, args...))
	return result{stdout: lines}
}

// --- duplex capture -------------------------------------------------------

// duplexSession records a transcript from the fake's point of view: what
// the client writes is "recv", what the CLI writes is "send".
type duplexSession struct {
	c      *capturer
	stdin  io.WriteCloser
	lines  chan []byte
	steps  []string
	mu     sync.Mutex
	stderr bytes.Buffer
}

func (c *capturer) duplex(stem, bin string, args []string, drive func(*duplexSession)) {
	log.Printf("%s/%s: %s %s (duplex)", c.runtime, stem, bin, strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.proj
	d := &duplexSession{c: c, lines: make(chan []byte, 1024)}
	cmd.Stderr = &lockedWriter{mu: &d.mu, w: &d.stderr}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	d.stdin = stdin
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	go func() {
		defer close(d.lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			d.lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()

	drive(d)

	// A CLI that ends the session itself (a lost resume id) exits without
	// waiting for stdin to close; only record an eof the CLI waited for.
	if !d.drainUntilClosed(3 * time.Second) {
		_ = stdin.Close()
		d.steps = append(d.steps, `{"eof":true}`)
		for line := range d.lines {
			d.record("send", line)
		}
	}
	code := exitCode(cmd.Wait())
	d.mu.Lock()
	for _, l := range splitLines(d.stderr.Bytes()) {
		s, _ := json.Marshal(map[string]string{"stderr": string(c.scrub.line(l))})
		d.steps = append(d.steps, string(s))
	}
	d.mu.Unlock()
	d.steps = append(d.steps, fmt.Sprintf(`{"exit":%d}`, code))
	if !c.want(stem) {
		return
	}
	c.write(stem+".transcript.jsonl", []byte(strings.Join(d.steps, "\n")+"\n"))
	c.argv[stem] = c.scrub.args(append([]string{bin}, args...))
}

// drainUntilClosed records output until stdout closes or grace passes
// without it closing, and reports whether it closed.
func (d *duplexSession) drainUntilClosed(grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-d.lines:
			if !ok {
				return true
			}
			d.record("send", line)
		case <-timer.C:
			return false
		}
	}
}

func (d *duplexSession) record(kind string, line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	scrubbed := d.c.scrub.line(line)
	if json.Valid(scrubbed) {
		d.steps = append(d.steps, fmt.Sprintf(`{%q:%s}`, kind, scrubbed))
		return
	}
	key := "stdout"
	if kind == "recv" {
		key = "recv_text"
	}
	s, _ := json.Marshal(map[string]string{key: string(scrubbed)})
	d.steps = append(d.steps, string(s))
}

func (d *duplexSession) send(frame []byte) {
	d.record("recv", frame)
	if _, err := d.stdin.Write(append(frame, '\n')); err != nil {
		log.Fatalf("write stdin: %v", err)
	}
}

// readUntil records CLI output until done reports true for a line, and
// returns that line. Server-to-client JSON-RPC requests are approved.
func (d *duplexSession) readUntil(done func([]byte) bool) []byte {
	deadline := time.After(*timeout)
	for {
		select {
		case line, ok := <-d.lines:
			if !ok {
				return nil // the CLI exited; the transcript shows how
			}
			d.record("send", line)
			if jsonField(line, "method") != "" && jsonRaw(line, "id") != nil {
				d.approve(line)
			}
			if done(line) {
				return line
			}
		case <-deadline:
			log.Fatalf("%s: timed out waiting for output", d.c.runtime)
		}
	}
}

func (d *duplexSession) approve(req []byte) {
	reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(jsonRaw(req, "id")), "result": map[string]any{"decision": "accept"}})
	d.send(reply)
}

type rpcClient struct {
	d    *duplexSession
	next int
}

func (r *rpcClient) call(method string, params any) []byte {
	r.next++
	id := r.next
	frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	r.d.send(frame)
	return r.d.readUntil(func(line []byte) bool {
		return jsonField(line, "method") == "" && string(jsonRaw(line, "id")) == strconv.Itoa(id)
	})
}

func (r *rpcClient) notify(method string, params any) {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		m["params"] = params
	}
	frame, _ := json.Marshal(m)
	r.d.send(frame)
}

// --- scrubbing ------------------------------------------------------------

type scrubber struct {
	literal [][2]string
	uuids   map[string]string
	ids     map[string]string
	counts  map[string]int
}

var (
	uuidRE   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	apiIDRE  = regexp.MustCompile(`\b(msg|req|resp|rs|call|toolu|srvtoolu|fc|ws|ses|prt|evt)_[0-9A-Za-z]{8,}\b`)
	emailRE  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	blobRE   = regexp.MustCompile(`"(signature|encrypted_content)":"[^"]*"`)
	utilRE   = regexp.MustCompile(`"utilization":[0-9.eE+-]+`)
	socketRE = regexp.MustCompile(`"messaging_socket_path":"[^"]*"`)
	// Account credit balance (codex account/rateLimits/updated).
	balanceRE = regexp.MustCompile(`"balance":"[^"]*"`)
)

func newScrubber(work, proj string) *scrubber {
	home, _ := os.UserHomeDir()
	user := filepath.Base(home)
	tmp := os.TempDir()
	s := &scrubber{uuids: map[string]string{}, ids: map[string]string{}, counts: map[string]int{}}
	// Longest first, so the project path wins over its parents.
	s.literal = [][2]string{
		{proj, "/work/project"},
		{work, "/work"},
		{tmp + "/", "/tmp/"},
		{home, "/home/user"},
	}
	// A CLI can report a path without its leading slash (opencode's
	// session "path"); scrub that form too.
	for _, l := range append([][2]string(nil), s.literal...) {
		if bare := strings.TrimPrefix(l[0], "/"); bare != l[0] && len(bare) > 1 {
			s.literal = append(s.literal, [2]string{bare, strings.TrimPrefix(l[1], "/")})
		}
	}
	sort.SliceStable(s.literal, func(i, j int) bool { return len(s.literal[i][0]) > len(s.literal[j][0]) })
	if user != "" && user != "user" && user != "root" {
		s.literal = append(s.literal, [2]string{user, "user"})
	}
	if host, err := os.Hostname(); err == nil && len(host) > 2 {
		s.literal = append(s.literal, [2]string{host, "fixture-host"})
	}
	return s
}

func (s *scrubber) line(b []byte) []byte {
	out := string(b)
	for _, r := range s.literal {
		out = strings.ReplaceAll(out, r[0], r[1])
	}
	out = blobRE.ReplaceAllString(out, `"$1":"REDACTED"`)
	out = utilRE.ReplaceAllString(out, `"utilization":0`)
	out = socketRE.ReplaceAllString(out, `"messaging_socket_path":"/tmp/providertest.sock"`)
	out = balanceRE.ReplaceAllString(out, `"balance":"0"`)
	out = emailRE.ReplaceAllString(out, "user@example.com")
	out = uuidRE.ReplaceAllStringFunc(out, s.uuid)
	out = apiIDRE.ReplaceAllStringFunc(out, s.apiID)
	return []byte(out)
}

func (s *scrubber) args(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = string(s.line([]byte(a)))
	}
	return out
}

func (s *scrubber) uuid(u string) string {
	l := strings.ToLower(u)
	if strings.HasPrefix(l, "00000000-0000-4000-8000-") {
		return l
	}
	if p, ok := s.uuids[l]; ok {
		return p
	}
	p := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(s.uuids)+1)
	s.uuids[l] = p
	return p
}

func (s *scrubber) apiID(id string) string {
	if p, ok := s.ids[id]; ok {
		return p
	}
	prefix := id[:strings.IndexByte(id, '_')]
	s.counts[prefix]++
	p := fmt.Sprintf("%s_fixture%04d", prefix, s.counts[prefix])
	s.ids[id] = p
	return p
}

// --- output ---------------------------------------------------------------

func (c *capturer) write(name string, data []byte) {
	if err := os.WriteFile(filepath.Join(c.dir, name), data, 0o644); err != nil {
		log.Fatal(err)
	}
}

func (c *capturer) writeManifest() {
	argv := c.argv
	if *onlyArg != "" {
		var prev struct {
			Version string              `json:"version"`
			Argv    map[string][]string `json:"argv"`
		}
		b, err := os.ReadFile(filepath.Join(c.dir, "captured.json"))
		switch {
		case errors.Is(err, os.ErrNotExist):
			prev.Version, prev.Argv = c.version, map[string][]string{}
		case err != nil || json.Unmarshal(b, &prev) != nil:
			log.Fatalf("-only: cannot read %s/captured.json to merge into", c.dir)
		}
		if prev.Version != c.version {
			log.Fatalf("-only: %s is %s, the manifest records %s; re-record every fixture", c.runtime, c.version, prev.Version)
		}
		for stem, a := range c.argv {
			if c.want(stem) {
				prev.Argv[stem] = a
			}
		}
		argv = prev.Argv
	}
	m := map[string]any{
		"runtime":  c.runtime,
		"version":  c.version,
		"captured": time.Now().UTC().Format("2006-01-02"),
		"argv":     argv,
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	c.write("captured.json", append(b, '\n'))
}

func cliVersion(bin string) string {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		log.Fatalf("%s --version: %v", bin, err)
	}
	return strings.TrimSpace(string(out))
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	log.Fatalf("run: %v", err)
	return -1
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		if l = bytes.TrimRight(l, "\r"); len(bytes.TrimSpace(l)) > 0 {
			out = append(out, l)
		}
	}
	return out
}

func jsonRaw(line []byte, key string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	return m[key]
}

func jsonField(line []byte, key string) string {
	var s string
	if raw := jsonRaw(line, key); raw != nil && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
