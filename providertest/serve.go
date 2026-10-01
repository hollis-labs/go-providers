package providertest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// This file is the fake binary itself: the code that runs when the test
// binary starts under a fake's name.

const (
	scriptName = "script.json"
	stateName  = "state.json"
	lockName   = "lock"
	callsDir   = "calls"

	// exitFakeError is the exit code of an invocation the fake could not
	// serve: no run left, or stdin closed while a transcript waited.
	exitFakeError = 97
)

type script struct {
	Runtime string `json:"runtime"`
	Runs    []Run  `json:"runs"`
}

type fakeState struct {
	Used  []bool `json:"used"`
	Calls int    `json:"calls"`
}

type record struct {
	Start  *startRecord `json:"start,omitempty"`
	Stdin  *string      `json:"stdin,omitempty"`
	Signal string       `json:"signal,omitempty"`
	Note   string       `json:"note,omitempty"`
	Error  string       `json:"error,omitempty"`
	Exit   *int         `json:"exit,omitempty"`
}

type startRecord struct {
	Seq  int      `json:"seq"`
	Run  int      `json:"run"`
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
	Env  []string `json:"env"`
	PID  int      `json:"pid"`
}

func init() {
	if dir := fakeStateDir(os.Args); dir != "" {
		exit(serve(dir))
	}
}

// exit ends the fake without os.Exit's hooks: a -race build sleeps a
// second in the race detector's finalizer on every clean os.Exit, and the
// fake has nothing buffered to flush.
func exit(code int) { syscall.Exit(code) }

// stateDirFor is where the fake installed at exe keeps its script and
// call records: a hidden directory beside it.
func stateDirFor(exe string) string {
	return filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".providertest")
}

// fakeStateDir reports the fake state directory when this process was
// started as a fake, and "" for an ordinary run of the test binary.
func fakeStateDir(args []string) string {
	if len(args) == 0 || args[0] == "" {
		return ""
	}
	p := args[0]
	if !strings.ContainsAny(p, `/\`) {
		lp, err := exec.LookPath(p)
		if err != nil {
			return ""
		}
		p = lp
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	dir := stateDirFor(abs)
	if _, err := os.Stat(filepath.Join(dir, scriptName)); err != nil {
		return ""
	}
	return dir
}

func serve(dir string) int {
	fail := func(err error) int {
		fmt.Fprintf(os.Stderr, "providertest: fake %s: %v\n", os.Args[0], err)
		return exitFakeError
	}
	b, err := os.ReadFile(filepath.Join(dir, scriptName))
	if err != nil {
		return fail(err)
	}
	var sc script
	if err := json.Unmarshal(b, &sc); err != nil {
		return fail(err)
	}
	args := os.Args[1:]
	seq, idx, err := claim(dir, sc, args)
	if err != nil {
		return fail(err)
	}
	rec, err := openRecorder(filepath.Join(dir, callsDir, fmt.Sprintf("%06d.jsonl", seq)))
	if err != nil {
		return fail(err)
	}
	wd, _ := os.Getwd()
	rec.write(record{Start: &startRecord{Seq: seq, Run: idx, Args: args, Dir: wd, Env: os.Environ(), PID: os.Getpid()}})
	if idx < 0 {
		msg := fmt.Sprintf("no run left for invocation %d %q", seq, args)
		rec.write(record{Error: msg})
		fmt.Fprintf(os.Stderr, "providertest: %s fake: %s\n", sc.Runtime, msg)
		return rec.exit(exitFakeError)
	}

	run := sc.Runs[idx]
	handleSIGTERM(run, rec)
	e := &engine{run: run, rec: rec, lines: readLines(os.Stdin, rec), ids: map[string]json.RawMessage{}, stdout: os.Stdout, stderr: os.Stderr}
	return rec.exit(e.exec())
}

// claim picks the run for this invocation under the state lock: the first
// unused (or repeating) run whose When arguments argv contains.
func claim(dir string, sc script, args []string) (seq, idx int, err error) {
	unlock, err := lockFile(filepath.Join(dir, lockName))
	if err != nil {
		return 0, 0, err
	}
	defer unlock()
	st := fakeState{Used: make([]bool, len(sc.Runs))}
	if b, err := os.ReadFile(filepath.Join(dir, stateName)); err == nil {
		if err := json.Unmarshal(b, &st); err != nil {
			return 0, 0, err
		}
	}
	st.Calls++
	idx = -1
	for i, r := range sc.Runs {
		if (r.Repeat || !st.Used[i]) && r.matches(args) {
			idx = i
			st.Used[i] = st.Used[i] || !r.Repeat
			break
		}
	}
	b, err := json.Marshal(st)
	if err != nil {
		return 0, 0, err
	}
	return st.Calls, idx, os.WriteFile(filepath.Join(dir, stateName), b, 0o644)
}

func handleSIGTERM(run Run, rec *recorder) {
	if run.SIGTERM == sigtermDefault {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	go func() {
		for range ch {
			rec.write(record{Signal: "SIGTERM"})
			if run.SIGTERM == sigtermExit {
				exit(rec.exit(run.SIGTERMExit))
			}
		}
	}()
}

type recorder struct {
	mu sync.Mutex
	f  *os.File
}

func openRecorder(path string) (*recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &recorder{f: f}, nil
}

func (r *recorder) write(rec record) {
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.f.Write(append(b, '\n'))
}

func (r *recorder) exit(code int) int {
	r.write(record{Exit: &code})
	return code
}

// readLines feeds stdin lines to the engine, recording each as it arrives
// so a fake that is killed mid-run still leaves its input behind.
func readLines(in io.Reader, rec *recorder) <-chan []byte {
	ch := make(chan []byte, 256)
	go func() {
		defer close(ch)
		r := bufio.NewReaderSize(in, 64*1024)
		for {
			line, err := r.ReadBytes('\n')
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 {
				s := string(line)
				rec.write(record{Stdin: &s})
				ch <- line
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

type engine struct {
	run     Run
	rec     *recorder
	lines   <-chan []byte
	pending []byte
	// ids maps a request id in the transcript to the id the live client
	// used, so responses answer the live request.
	ids    map[string]json.RawMessage
	stdout io.Writer
	stderr io.Writer
}

func (e *engine) exec() int {
	for _, s := range e.run.Steps {
		switch {
		case s.Stdout != nil:
			e.writeLine(e.stdout, *s.Stdout)
		case s.Stderr != nil:
			e.writeLine(e.stderr, *s.Stderr)
		case s.Send != nil:
			e.writeLine(e.stdout, string(e.remap(s.Send)))
		case s.Recv != nil:
			if !e.recv(s.Recv) {
				return exitFakeError
			}
		case s.RecvText != nil:
			if _, ok := e.next(); !ok {
				e.fail("stdin closed while waiting for a line")
				return exitFakeError
			}
		case s.Echo != nil:
			for line, ok := e.next(); ok; line, ok = e.next() {
				e.writeLine(e.stdout, strings.ReplaceAll(*s.Echo, "{{line}}", string(line)))
			}
		case s.EOF:
			e.drain()
		case s.SleepMS > 0:
			time.Sleep(time.Duration(s.SleepMS) * time.Millisecond)
		case s.Hang:
			for {
				time.Sleep(time.Hour)
			}
		case s.Exit != nil:
			return *s.Exit
		}
	}
	return 0
}

func (e *engine) writeLine(w io.Writer, s string) {
	if e.run.PaceMS > 0 {
		time.Sleep(time.Duration(e.run.PaceMS) * time.Millisecond)
	}
	_, _ = io.WriteString(w, s+"\n")
}

func (e *engine) next() ([]byte, bool) {
	if e.pending != nil {
		l := e.pending
		e.pending = nil
		return l, true
	}
	l, ok := <-e.lines
	return l, ok
}

func (e *engine) fail(format string, args ...any) {
	e.rec.write(record{Error: fmt.Sprintf(format, args...)})
}

func (e *engine) note(format string, args ...any) {
	e.rec.write(record{Note: fmt.Sprintf(format, args...)})
}

// recv waits for a stdin frame matching want. It skips an expected
// notification the client never sends, ignores a notification the
// transcript does not expect, and answers an unexpected request with a
// JSON-RPC error so the client does not hang on it.
func (e *engine) recv(raw json.RawMessage) bool {
	want := parseFrame(raw)
	for {
		line, ok := e.next()
		if !ok {
			e.fail("stdin closed while waiting for %s", want.describe())
			return false
		}
		got := parseFrame(line)
		switch {
		case want.matches(got):
			if want.isRequest() {
				e.ids[canonicalID(want.id())] = got.id()
			}
			return true
		case want.isNotification():
			e.pending = line
			e.note("client did not send %s", want.describe())
			return true
		case got.isNotification():
			e.note("ignored %s", got.describe())
		case got.isRequest():
			e.fail("unexpected %s while waiting for %s", got.describe(), want.describe())
			e.replyError(got)
		default:
			e.fail("unexpected stdin %q while waiting for %s", truncate(line), want.describe())
		}
	}
}

// drain reads stdin until it closes. Requests nobody scripted get a
// JSON-RPC error; other lines are only recorded.
func (e *engine) drain() {
	for line, ok := e.next(); ok; line, ok = e.next() {
		if got := parseFrame(line); got.isRequest() {
			e.fail("unexpected %s after the scripted exchange", got.describe())
			e.replyError(got)
		}
	}
}

func (e *engine) replyError(req frame) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.id(),
		"error":   map[string]any{"code": -32601, "message": "providertest: no scripted answer for " + req.method()},
	})
	e.writeLine(e.stdout, string(b))
}

// remap gives a response the id of the live request it answers.
func (e *engine) remap(raw json.RawMessage) json.RawMessage {
	f := parseFrame(raw)
	if !f.isResponse() {
		return raw
	}
	live, ok := e.ids[canonicalID(f.id())]
	if !ok || canonicalID(live) == canonicalID(f.id()) {
		return raw
	}
	f.obj["id"] = live
	b, err := json.Marshal(f.obj)
	if err != nil {
		return raw
	}
	return b
}

// frame is a JSON line seen through JSON-RPC eyes; non-objects have a nil
// obj.
type frame struct {
	obj map[string]json.RawMessage
}

func parseFrame(b []byte) frame {
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil {
		return frame{}
	}
	return frame{obj: obj}
}

func (f frame) str(key string) string {
	var s string
	if raw, ok := f.obj[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func (f frame) method() string       { return f.str("method") }
func (f frame) id() json.RawMessage  { return f.obj["id"] }
func (f frame) hasID() bool          { _, ok := f.obj["id"]; return ok }
func (f frame) isRequest() bool      { return f.method() != "" && f.hasID() }
func (f frame) isNotification() bool { return f.method() != "" && !f.hasID() }
func (f frame) isResponse() bool {
	if f.method() != "" || !f.hasID() {
		return false
	}
	_, res := f.obj["result"]
	_, errField := f.obj["error"]
	return res || errField
}

func (f frame) matches(got frame) bool {
	switch {
	case f.obj == nil:
		return true
	case f.method() != "":
		return got.method() == f.method() && got.hasID() == f.hasID()
	case f.isResponse():
		return got.isResponse() && canonicalID(got.id()) == canonicalID(f.id())
	case f.str("type") != "":
		return got.str("type") == f.str("type")
	default:
		return got.obj != nil
	}
}

func (f frame) describe() string {
	switch {
	case f.isRequest():
		return "request " + f.method()
	case f.isNotification():
		return "notification " + f.method()
	case f.isResponse():
		return "response to id " + string(f.id())
	case f.str("type") != "":
		return fmt.Sprintf("a %q frame", f.str("type"))
	default:
		return "a line"
	}
}

func canonicalID(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

func truncate(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
