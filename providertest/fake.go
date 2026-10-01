package providertest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// Fake is a fake CLI binary for one runtime, serving the runs given to
// [New]. Its files live in a t.TempDir, so they go away with the test.
type Fake struct {
	// Runtime is the runtime id the fake was created for.
	Runtime string
	// Descriptor holds the runtime's binary name and CLI-path variable.
	Descriptor Descriptor
	// Path is the fake executable. Pass it as given; see the package doc.
	Path string
	// Dir is the directory holding Path and nothing else on PATH lookup.
	Dir string

	t            testing.TB
	state        string
	expectErrors atomic.Bool
}

// New creates a fake binary for runtime (an id or alias such as "claude"
// or "agy") that serves runs in order, one per invocation; see [Run] for
// how an invocation picks its run. A missing fixture fails t immediately.
// Fake-side errors fail t at cleanup unless [Fake.ExpectErrors] is called.
func New(t testing.TB, runtimeID string, runs ...Run) *Fake {
	t.Helper()
	d, ok := LookupDescriptor(runtimeID)
	if !ok {
		t.Fatalf("providertest: unknown runtime %q; register it with RegisterDescriptor", runtimeID)
	}
	resolved := make([]Run, len(runs))
	for i, r := range runs {
		if r.Fixture != "" {
			steps, err := loadFixture(Fixtures, r.Fixture)
			if err != nil {
				t.Fatalf("providertest: %v", err)
			}
			r.Steps = append(steps, r.Steps...)
		}
		for _, s := range r.Steps {
			if err := s.validate(); err != nil {
				t.Fatalf("providertest: %s: %v", r.label(i), err)
			}
		}
		resolved[i] = r
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("providertest: locate the test binary: %v", err)
	}
	dir := t.TempDir()
	name := d.Binary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := linkExecutable(exe, path); err != nil {
		t.Fatalf("providertest: install fake %s: %v", name, err)
	}
	state := stateDirFor(path)
	if err := os.MkdirAll(filepath.Join(state, callsDir), 0o755); err != nil {
		t.Fatalf("providertest: %v", err)
	}
	// No HTML escaping: a replayed frame keeps the capture's bytes.
	var sc bytes.Buffer
	enc := json.NewEncoder(&sc)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(script{Runtime: d.ID, Runs: resolved}); err != nil {
		t.Fatalf("providertest: encode script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(state, scriptName), sc.Bytes(), 0o644); err != nil {
		t.Fatalf("providertest: %v", err)
	}

	f := &Fake{Runtime: d.ID, Descriptor: d, Path: path, Dir: dir, t: t, state: state}
	t.Cleanup(func() {
		if f.expectErrors.Load() {
			return
		}
		for _, e := range f.Errors() {
			t.Errorf("providertest: %s fake: %s", d.ID, e)
		}
	})
	return f
}

// linkExecutable makes path run exe. A symlink keeps the test binary
// executable as built; copying is the fallback where symlinks need
// privileges (Windows).
func linkExecutable(exe, path string) error {
	err := os.Symlink(exe, path)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// Env returns environment entries that make the runtime's adapter find the
// fake: its CLI-path variable, when it has one, and PATH with Dir first.
// Use it where the code under test takes an explicit child environment.
func (f *Fake) Env() []string {
	var env []string
	if f.Descriptor.EnvVar != "" {
		env = append(env, f.Descriptor.EnvVar+"="+f.Path)
	}
	return append(env, "PATH="+f.Dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Install points the runtime's CLI-path variable and PATH at the fake for
// the rest of the test, through t.Setenv (so not in parallel tests).
func (f *Fake) Install() {
	f.t.Helper()
	for _, kv := range f.Env() {
		k, v, _ := strings.Cut(kv, "=")
		f.t.Setenv(k, v)
	}
}

// ExpectErrors stops fake-side errors from failing the test at cleanup,
// for a test that provokes them on purpose. [Fake.Errors] still reports
// them.
func (f *Fake) ExpectErrors() { f.expectErrors.Store(true) }

// Errors returns the fake-side errors recorded so far, across calls.
func (f *Fake) Errors() []string {
	var errs []string
	for _, c := range f.Calls() {
		for _, e := range c.Errors {
			errs = append(errs, fmt.Sprintf("call %d: %s", c.Seq, e))
		}
	}
	return errs
}

// Calls returns the invocations recorded so far, in start order. A call
// still running has Exited false.
func (f *Fake) Calls() []Call {
	f.t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.state, callsDir))
	if err != nil {
		f.t.Fatalf("providertest: read calls: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	calls := make([]Call, 0, len(names))
	for _, n := range names {
		c, err := readCall(filepath.Join(f.state, callsDir, n))
		if err != nil {
			f.t.Fatalf("providertest: read call %s: %v", n, err)
		}
		calls = append(calls, c)
	}
	return calls
}

// Call returns invocation i (0-based), failing the test if there are not
// that many yet.
func (f *Fake) Call(i int) Call {
	f.t.Helper()
	calls := f.Calls()
	if i < 0 || i >= len(calls) {
		f.t.Fatalf("providertest: %s fake: want call %d, have %d", f.Runtime, i, len(calls))
	}
	return calls[i]
}

// Call is one recorded invocation of a fake.
type Call struct {
	// Seq is the 1-based invocation number.
	Seq int
	// Run is the index of the run that served the call, or -1 if none
	// matched.
	Run int
	// Args is argv without the program name.
	Args []string
	// Dir is the working directory.
	Dir string
	// Env is the environment, as os.Environ reports it.
	Env []string
	// PID is the process id.
	PID int
	// Stdin holds the lines read from stdin, without newlines.
	Stdin []string
	// Signals lists the signals the run recorded (see
	// [Run.IgnoringSIGTERM]).
	Signals []string
	// Notes are informational: notifications the transcript did not
	// expect, optional ones the client did not send.
	Notes []string
	// Errors are fake-side failures: no run left, unexpected input.
	Errors []string
	// Exited reports whether the fake exited on its own; a killed fake
	// leaves it false.
	Exited bool
	// ExitCode is the code the fake exited with.
	ExitCode int
}

// Getenv returns the value of key in the call's environment.
func (c Call) Getenv(key string) (string, bool) {
	for i := len(c.Env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(c.Env[i], "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// HasArg reports whether argv contains arg.
func (c Call) HasArg(arg string) bool {
	for _, a := range c.Args {
		if a == arg {
			return true
		}
	}
	return false
}

// ArgAfter returns the argument following flag, e.g. the id after
// "--resume".
func (c Call) ArgAfter(flag string) (string, bool) {
	for i, a := range c.Args {
		if a == flag && i+1 < len(c.Args) {
			return c.Args[i+1], true
		}
	}
	return "", false
}

func readCall(path string) (Call, error) {
	fh, err := os.Open(path)
	if err != nil {
		return Call{}, err
	}
	defer fh.Close()
	c := Call{Run: -1}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			// A record cut short by a kill; the rest of the file is
			// still a valid history.
			continue
		}
		switch {
		case r.Start != nil:
			c.Seq, c.Run, c.Args, c.Dir, c.Env, c.PID = r.Start.Seq, r.Start.Run, r.Start.Args, r.Start.Dir, r.Start.Env, r.Start.PID
		case r.Stdin != nil:
			c.Stdin = append(c.Stdin, *r.Stdin)
		case r.Signal != "":
			c.Signals = append(c.Signals, r.Signal)
		case r.Note != "":
			c.Notes = append(c.Notes, r.Note)
		case r.Error != "":
			c.Errors = append(c.Errors, r.Error)
		case r.Exit != nil:
			c.Exited, c.ExitCode = true, *r.Exit
		}
	}
	return c, sc.Err()
}
