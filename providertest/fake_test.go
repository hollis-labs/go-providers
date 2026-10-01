package providertest_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"

	"github.com/hollis-labs/go-providers/providertest"
	"github.com/hollis-labs/go-providers/registry"
)

// proc is a running fake with line-oriented stdio.
type proc struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr *bytes.Buffer
}

func start(t *testing.T, cmd *exec.Cmd) *proc {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &proc{t: t, cmd: cmd, stdin: stdin, lines: make(chan string, 1024), stderr: &bytes.Buffer{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(p.lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return p
}

func (p *proc) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatalf("write stdin: %v", err)
	}
}

// readUntil returns the lines read up to and including the first one done
// accepts.
func (p *proc) readUntil(done func(string) bool) []string {
	p.t.Helper()
	var got []string
	timeout := time.After(10 * time.Second)
	for {
		select {
		case l, ok := <-p.lines:
			if !ok {
				p.t.Fatalf("stdout closed before the expected line; got %q", got)
			}
			got = append(got, l)
			if done(l) {
				return got
			}
		case <-timeout:
			p.t.Fatalf("timed out; got %q", got)
		}
	}
}

func (p *proc) wait() int {
	p.t.Helper()
	_ = p.stdin.Close()
	for range p.lines {
	}
	err := p.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		p.t.Fatalf("wait: %v", err)
	}
	return 0
}

func field(line, key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &m) != nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(m[key], &s)
	if s == "" {
		return string(m[key])
	}
	return s
}

func TestReplayPerTurnFixture(t *testing.T) {
	f := providertest.New(t, "claude", providertest.Replay("claude/print_turn1"))
	if !strings.HasSuffix(f.Path, "/claude") {
		t.Fatalf("Path = %s, want a binary named claude", f.Path)
	}
	out, err := exec.Command(f.Path, "-p", "say hi", "--output-format", "stream-json").Output()
	if err != nil {
		t.Fatalf("run fake: %v", err)
	}
	want := providertest.ReadFixture(t, "claude/print_turn1.jsonl")
	if !bytes.Equal(out, want) {
		t.Errorf("stdout differs from the fixture:\n got %d bytes\nwant %d bytes", len(out), len(want))
	}
	c := f.Call(0)
	if !c.Exited || c.ExitCode != 0 || c.Run != 0 {
		t.Errorf("call = %+v", c)
	}
	if v, _ := c.ArgAfter("-p"); v != "say hi" {
		t.Errorf("ArgAfter(-p) = %q", v)
	}
}

func TestReplayStderrAndExitCode(t *testing.T) {
	f := providertest.New(t, "claude", providertest.Replay("claude/print_resume_unknown_id"))
	cmd := exec.Command(f.Path, "--resume", "00000000-0000-4000-8000-0000000000ff", "-p", "x")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(stderr.String(), "No conversation found with session ID") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if field(strings.TrimSpace(string(out)), "subtype") != "error_during_execution" {
		t.Errorf("stdout = %s", out)
	}
}

func TestRunsMatchInOrderAndRunOut(t *testing.T) {
	f := providertest.New(t, "codex",
		providertest.Lines("codex-cli 0.0.0").When("--version").Always(),
		providertest.Lines("first"),
		providertest.Lines("second").Then(providertest.Exit(3)),
	)
	f.ExpectErrors()
	run := func(args ...string) (string, int) {
		out, err := exec.Command(f.Path, args...).Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}
	for _, step := range []struct {
		args []string
		out  string
		code int
	}{
		{[]string{"--version"}, "codex-cli 0.0.0\n", 0},
		{[]string{"exec", "hi"}, "first\n", 0},
		{[]string{"--version"}, "codex-cli 0.0.0\n", 0},
		{[]string{"exec", "hi"}, "second\n", 3},
		{[]string{"exec", "hi"}, "", 97},
	} {
		if out, code := run(step.args...); out != step.out || code != step.code {
			t.Errorf("%v: got (%q, %d), want (%q, %d)", step.args, out, code, step.out, step.code)
		}
	}
	calls := f.Calls()
	if len(calls) != 5 || calls[4].Run != -1 {
		t.Fatalf("calls = %+v", calls)
	}
	if errs := f.Errors(); len(errs) != 1 || !strings.Contains(errs[0], "no run left") {
		t.Errorf("Errors() = %q", errs)
	}
}

// The codex app-server capture used ids 1..4 and an initialized
// notification; this client numbers from 100, uses a string id once and
// skips the notification, as agentkit's codex driver does.
func TestTranscriptJSONRPCRemapsIDs(t *testing.T) {
	f := providertest.New(t, "codex", providertest.Replay("codex/app_server_turn"))
	p := start(t, exec.Command(f.Path, "app-server"))
	call := func(id, method string, params any) string {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
		p.send(string(b))
		lines := p.readUntil(func(l string) bool { return field(l, "method") == "" && field(l, "id") == strings.Trim(id, `"`) })
		return lines[len(lines)-1]
	}
	call("100", "initialize", map[string]any{"clientInfo": map[string]any{"name": "t", "version": "0"}})
	res := call(`"start"`, "thread/start", map[string]any{})
	if !strings.Contains(res, `"thread":{"id":"00000000-0000-4000-8000-`) {
		t.Fatalf("thread/start response = %s", res)
	}
	for _, id := range []string{"101", "102"} {
		call(id, "turn/start", map[string]any{"threadId": "x", "input": []any{}})
		p.readUntil(func(l string) bool { return field(l, "method") == "turn/completed" })
	}
	if code := p.wait(); code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, p.stderr)
	}
	c := f.Call(0)
	if len(c.Stdin) != 4 {
		t.Errorf("stdin lines = %d, want 4", len(c.Stdin))
	}
	if len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "initialized") {
		t.Errorf("notes = %q, want the skipped initialized notification", c.Notes)
	}
}

func TestTranscriptServerRequest(t *testing.T) {
	f := providertest.New(t, "codex", providertest.Replay("codex/app_server_tool_approval"))
	p := start(t, exec.Command(f.Path, "app-server"))
	p.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	p.readUntil(func(l string) bool { return field(l, "id") == "1" })
	p.send(`{"jsonrpc":"2.0","method":"initialized"}`)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"thread/start","params":{}}`)
	p.send(`{"jsonrpc":"2.0","id":3,"method":"turn/start","params":{}}`)
	lines := p.readUntil(func(l string) bool { return field(l, "method") == "item/commandExecution/requestApproval" })
	req := lines[len(lines)-1]
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"decision":"accept"}}`, field(req, "id")))
	p.readUntil(func(l string) bool { return field(l, "method") == "turn/completed" })
	if code := p.wait(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
}

func TestTranscriptUnexpectedRequestGetsError(t *testing.T) {
	f := providertest.New(t, "codex", providertest.Script(
		providertest.Recv(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
		providertest.Send(`{"jsonrpc":"2.0","id":1,"result":{}}`),
		providertest.AwaitEOF(),
	))
	f.ExpectErrors()
	p := start(t, exec.Command(f.Path))
	p.send(`{"jsonrpc":"2.0","id":7,"method":"model/list"}`)
	got := p.readUntil(func(string) bool { return true })[0]
	if field(got, "id") != "7" || !strings.Contains(got, `"error"`) {
		t.Fatalf("reply = %s, want an error for id 7", got)
	}
	p.send(`{"jsonrpc":"2.0","id":8,"method":"initialize"}`)
	got = p.readUntil(func(string) bool { return true })[0]
	if field(got, "id") != "8" || !strings.Contains(got, `"result"`) {
		t.Fatalf("reply = %s, want the scripted result renumbered to id 8", got)
	}
	_ = p.wait()
	if errs := f.Errors(); len(errs) != 1 || !strings.Contains(errs[0], "unexpected request model/list") {
		t.Errorf("Errors() = %q", errs)
	}
}

func TestStreamingStdioTranscript(t *testing.T) {
	f := providertest.New(t, "claude", providertest.Replay("claude/stream_two_turns"))
	p := start(t, exec.Command(f.Path, "-p", "--input-format", "stream-json", "--output-format", "stream-json"))
	isResult := func(l string) bool { return field(l, "type") == "result" }
	p.send(`{"type":"user","message":{"role":"user","content":"one"}}`)
	first := p.readUntil(isResult)
	if field(first[0], "subtype") != "init" {
		t.Errorf("first frame = %s, want system/init", first[0])
	}
	p.send(`{"type":"user","message":{"role":"user","content":"two"}}`)
	p.readUntil(isResult)
	if code := p.wait(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := f.Call(0).Stdin; len(got) != 2 || !strings.Contains(got[1], "two") {
		t.Errorf("stdin = %q", got)
	}
}

// The interrupt capture used request_id req_interrupt_1; a live client picks
// its own, and the replayed control_response must answer that one, as a
// JSON-RPC response answers the live id. The turn then ends and the next one
// runs on the same process (CW-20261001-0103).
func TestStreamingInterruptRemapsTheControlRequestID(t *testing.T) {
	f := providertest.New(t, "claude", providertest.Replay("claude/stream_interrupt"))
	p := start(t, exec.Command(f.Path, "-p", "--input-format", "stream-json", "--output-format", "stream-json"))
	p.send(`{"type":"user","message":{"role":"user","content":"run something slow"}}`)
	p.readUntil(func(l string) bool { return strings.Contains(l, `"type":"tool_use"`) })
	p.send(`{"type":"control_request","request_id":"live-7","request":{"subtype":"interrupt"}}`)
	lines := p.readUntil(func(l string) bool { return field(l, "type") == "result" })
	var ack string
	for _, l := range lines {
		if field(l, "type") == "control_response" {
			ack = l
		}
	}
	if !strings.Contains(ack, `"request_id":"live-7"`) || !strings.Contains(ack, `"subtype":"success"`) {
		t.Errorf("control_response = %s, want success for live-7", ack)
	}
	if res := lines[len(lines)-1]; field(res, "subtype") != "error_during_execution" || field(res, "terminal_reason") != "aborted_tools" {
		t.Errorf("interrupted turn's result = %s", res)
	}
	p.send(`{"type":"user","message":{"role":"user","content":"next"}}`)
	if next := p.readUntil(func(l string) bool { return field(l, "type") == "result" }); field(next[len(next)-1], "subtype") != "success" {
		t.Errorf("the turn after the interrupt = %s", next[len(next)-1])
	}
	if code := p.wait(); code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, p.stderr)
	}
	if errs := f.Errors(); len(errs) != 0 {
		t.Errorf("fake errors: %q", errs)
	}
}

func TestStreamingLostSessionExitsWithoutEOF(t *testing.T) {
	f := providertest.New(t, "claude", providertest.Replay("claude/stream_resume_unknown_id"))
	p := start(t, exec.Command(f.Path, "--resume", "00000000-0000-4000-8000-0000000000ff"))
	p.send(`{"type":"user","message":{"role":"user","content":"hi"}}`)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			t.Fatalf("err = %v, want exit 1", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the fake waited for stdin to close; real claude does not")
	}
}

func TestEmptyEnvironmentStillServes(t *testing.T) {
	f := providertest.New(t, "opencode", providertest.Lines("ok"))
	cmd := exec.Command(f.Path, "run")
	cmd.Env = []string{}
	out, err := cmd.Output()
	if err != nil || string(out) != "ok\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if env := f.Call(0).Env; len(env) != 0 {
		t.Errorf("recorded env = %q, want empty", env)
	}
}

func TestInstallMakesTheFakeFoundByName(t *testing.T) {
	f := providertest.New(t, "agy", providertest.Lines("from PATH"))
	f.Install()
	if f.Runtime != "antigravity" || f.Descriptor.Binary != "agy" {
		t.Fatalf("fake = %+v", f)
	}
	out, err := exec.Command("agy", "-p=x").Output()
	if err != nil || string(out) != "from PATH\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if v, ok := f.Call(0).Getenv("AGY_CLI_PATH"); !ok || v != f.Path {
		t.Errorf("AGY_CLI_PATH = %q, %v", v, ok)
	}
}

func TestRecordsDirStdinAndEcho(t *testing.T) {
	f := providertest.New(t, "pi", providertest.Script(
		providertest.RecvLine(),
		providertest.Stdout("ready"),
		providertest.Echo("got {{line}}"),
		providertest.Exit(5),
	))
	dir := t.TempDir()
	cmd := exec.Command(f.Path, "--flag")
	cmd.Dir = dir
	cmd.Env = []string{"PROBE=1"}
	cmd.Stdin = strings.NewReader("hello\na\nb\n")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 5 {
		t.Fatalf("err = %v", err)
	}
	if string(out) != "ready\ngot a\ngot b\n" {
		t.Errorf("out = %q", out)
	}
	c := f.Call(0)
	if c.Dir != dir || !c.HasArg("--flag") || strings.Join(c.Stdin, ",") != "hello,a,b" || c.ExitCode != 5 {
		t.Errorf("call = %+v", c)
	}
	if v, _ := c.Getenv("PROBE"); v != "1" {
		t.Errorf("PROBE = %q", v)
	}
}

func TestConcurrentInvocationsEachClaimOneRun(t *testing.T) {
	const n = 8
	runs := make([]providertest.Run, n)
	for i := range runs {
		runs[i] = providertest.Lines(fmt.Sprintf("run-%d", i))
	}
	f := providertest.New(t, "claude", runs...)
	var wg sync.WaitGroup
	outs := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := exec.Command(f.Path).Output()
			if err != nil {
				t.Error(err)
			}
			outs <- string(out)
		}()
	}
	wg.Wait()
	close(outs)
	seen := map[string]bool{}
	for o := range outs {
		if seen[o] {
			t.Errorf("run served twice: %q", o)
		}
		seen[o] = true
	}
	if len(seen) != n {
		t.Errorf("served %d distinct runs, want %d", len(seen), n)
	}
}

func TestFakeForATestRegisteredRuntime(t *testing.T) {
	registry.RegisterForTest(t, registry.Descriptor{
		ID:          "fakeagent",
		Binary:      "fake-agent",
		EnvOverride: "FAKEAGENT_CLI_PATH",
		Modes:       []registry.ModeSupport{{Mode: runtimes.ModeSubprocessPerTurn}},
		DefaultMode: runtimes.ModeSubprocessPerTurn,
	})
	f := providertest.New(t, "fakeagent", providertest.Lines("hi"))
	if !strings.HasSuffix(f.Path, "/fake-agent") {
		t.Errorf("Path = %s", f.Path)
	}
	if env := f.Env(); env[0] != "FAKEAGENT_CLI_PATH="+f.Path {
		t.Errorf("Env() = %q", env)
	}
	if out, err := exec.Command(f.Path).Output(); err != nil || string(out) != "hi\n" {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

// Every fixture in the corpus must load as a run, so a malformed capture
// fails here rather than in a consumer's test.
func TestEveryFixtureLoads(t *testing.T) {
	stems := map[string]bool{}
	err := fs.WalkDir(providertest.Fixtures, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Dir(p) == "." {
			return err
		}
		if !runtimes.ID(path.Dir(p)).Valid() {
			t.Errorf("%s: directory is not a runtime id", p)
		}
		switch base := path.Base(p); {
		case strings.HasSuffix(base, ".http.jsonl"):
			// Not replayed by the fake: each line is a request, a
			// response or a server-sent event.
			for i, line := range providertest.FixtureLines(t, p) {
				var step map[string]json.RawMessage
				if json.Unmarshal(line, &step) != nil || len(step) != 1 || (step["request"] == nil && step["response"] == nil && step["event"] == nil) {
					t.Errorf("%s:%d: not a request, response or event: %.80s", p, i+1, line)
				}
			}
		case strings.HasSuffix(base, ".transcript.jsonl"):
			stems[strings.TrimSuffix(p, ".transcript.jsonl")] = true
		case strings.HasSuffix(base, ".jsonl"):
			stems[strings.TrimSuffix(p, ".jsonl")] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stems) == 0 {
		t.Fatal("no fixtures found")
	}
	for stem := range stems {
		if steps := providertest.FixtureSteps(t, stem); len(steps) == 0 {
			t.Errorf("%s: no steps", stem)
		}
	}
}

func TestReplayIsByteExact(t *testing.T) {
	f := providertest.New(t, "claude",
		providertest.Replay("claude/print_error_unknown_model"),
		providertest.Script(providertest.Send(`{"model":"<synthetic>","q":"a&b"}`)),
	)
	out, _ := exec.Command(f.Path).Output()
	if want := providertest.ReadFixture(t, "claude/print_error_unknown_model.jsonl"); !bytes.Equal(out, want) {
		t.Errorf("per-turn replay is not byte-exact:\n got %s\nwant %s", out, want)
	}
	out, err := exec.Command(f.Path).Output()
	if err != nil || string(out) != `{"model":"<synthetic>","q":"a&b"}`+"\n" {
		t.Errorf("sent frame = %q, err %v", out, err)
	}
}
