package providertest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Step is one action of a fake invocation. Exactly one field is set. Its
// JSON form is the line format of fixtures/*/*.transcript.jsonl.
type Step struct {
	// Stdout writes the text and a newline to stdout.
	Stdout *string `json:"stdout,omitempty"`
	// Stderr writes the text and a newline to stderr.
	Stderr *string `json:"stderr,omitempty"`
	// Send writes a JSON frame and a newline to stdout. A JSON-RPC
	// response takes the id of the live request it answers.
	Send json.RawMessage `json:"send,omitempty"`
	// Recv waits for a stdin JSON frame like this one: the same JSON-RPC
	// method, the same response id, or the same "type".
	Recv json.RawMessage `json:"recv,omitempty"`
	// RecvText waits for any stdin line.
	RecvText *string `json:"recv_text,omitempty"`
	// Echo answers every stdin line until stdin closes with this
	// template, "{{line}}" replaced by the line. "\n" in the template
	// writes several lines.
	Echo *string `json:"echo,omitempty"`
	// EOF waits for stdin to close.
	EOF bool `json:"eof,omitempty"`
	// SleepMS pauses.
	SleepMS int64 `json:"sleep_ms,omitempty"`
	// Hang blocks until the process is killed.
	Hang bool `json:"hang,omitempty"`
	// Exit ends the invocation with this code.
	Exit *int `json:"exit,omitempty"`
}

// Stdout writes line and a newline to stdout.
func Stdout(line string) Step { return Step{Stdout: &line} }

// Stderr writes line and a newline to stderr.
func Stderr(line string) Step { return Step{Stderr: &line} }

// Send writes a JSON frame to stdout. It panics if frame is not valid JSON.
func Send(frame string) Step { return Step{Send: mustJSON(frame)} }

// Recv waits for a stdin JSON frame matching frame. It panics if frame is
// not valid JSON.
func Recv(frame string) Step { return Step{Recv: mustJSON(frame)} }

// RecvLine waits for any stdin line.
func RecvLine() Step { s := ""; return Step{RecvText: &s} }

// Echo answers each stdin line until stdin closes with template, where
// "{{line}}" stands for the line read.
func Echo(template string) Step { return Step{Echo: &template} }

// AwaitEOF waits for stdin to close.
func AwaitEOF() Step { return Step{EOF: true} }

// Sleep pauses for d, rounded up to a whole millisecond.
func Sleep(d time.Duration) Step {
	ms := d.Milliseconds()
	if d%time.Millisecond != 0 || ms == 0 {
		ms++
	}
	return Step{SleepMS: ms}
}

// Hang blocks until the process is killed.
func Hang() Step { return Step{Hang: true} }

// Exit ends the invocation with code.
func Exit(code int) Step { return Step{Exit: &code} }

func mustJSON(frame string) json.RawMessage {
	if !json.Valid([]byte(frame)) {
		panic(fmt.Sprintf("providertest: not valid JSON: %s", frame))
	}
	return json.RawMessage(frame)
}

func (s Step) validate() error {
	n := 0
	for _, set := range []bool{s.Stdout != nil, s.Stderr != nil, s.Send != nil, s.Recv != nil, s.RecvText != nil, s.Echo != nil, s.EOF, s.SleepMS > 0, s.Hang, s.Exit != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		b, _ := json.Marshal(s)
		return fmt.Errorf("step %s: want exactly one action, got %d", b, n)
	}
	return nil
}

// SIGTERM handling for a run.
const (
	sigtermDefault = ""
	sigtermIgnore  = "ignore"
	sigtermExit    = "exit"
)

// Run is what the fake does for one invocation. Build one with [Replay],
// [Script] or [Lines] and refine it with the methods below; each returns a
// modified copy.
type Run struct {
	// Match lists arguments that must all appear in argv for this run to
	// serve an invocation. Empty matches any invocation.
	Match []string `json:"match,omitempty"`
	// Repeat keeps the run for later invocations instead of using it up.
	Repeat bool `json:"repeat,omitempty"`
	// Steps run in order. When they run out the fake exits 0.
	Steps []Step `json:"steps"`
	// PaceMS pauses between output steps, to make output arrive in
	// separate reads.
	PaceMS int64 `json:"pace_ms,omitempty"`
	// SIGTERM is "" (the default action: die), "ignore" or "exit".
	SIGTERM string `json:"sigterm,omitempty"`
	// SIGTERMExit is the exit code when SIGTERM is "exit".
	SIGTERMExit int `json:"sigterm_exit,omitempty"`
	// Fixture names a captured fixture, "<runtime>/<stem>", that runs
	// before Steps. [New] loads it.
	Fixture string `json:"fixture,omitempty"`
}

// Replay returns a run that replays a captured fixture, named
// "<runtime>/<stem>": <stem>.transcript.jsonl if it exists, otherwise the
// stdout of <stem>.jsonl followed by the stderr of <stem>.stderr and the
// exit code in <stem>.exit (0 when absent).
func Replay(fixture string) Run { return Run{Fixture: fixture} }

// Script returns a run of the given steps.
func Script(steps ...Step) Run { return Run{Steps: steps} }

// Lines returns a run that writes each line to stdout and exits 0.
func Lines(lines ...string) Run {
	steps := make([]Step, len(lines))
	for i, l := range lines {
		steps[i] = Stdout(l)
	}
	return Run{Steps: steps}
}

// When restricts the run to invocations whose argv contains every one of
// args.
func (r Run) When(args ...string) Run {
	r.Match = append(append([]string(nil), r.Match...), args...)
	return r
}

// Always keeps the run for every matching invocation instead of using it
// up, e.g. for a --version probe.
func (r Run) Always() Run { r.Repeat = true; return r }

// Then appends steps after the run's own.
func (r Run) Then(steps ...Step) Run {
	r.Steps = append(append([]Step(nil), r.Steps...), steps...)
	return r
}

// Paced pauses d between output steps.
func (r Run) Paced(d time.Duration) Run { r.PaceMS = d.Milliseconds(); return r }

// IgnoringSIGTERM makes the fake record SIGTERM and keep running, like a
// CLI that is slow to shut down.
func (r Run) IgnoringSIGTERM() Run { r.SIGTERM = sigtermIgnore; return r }

// ExitingOnSIGTERM makes the fake record SIGTERM and exit with code.
func (r Run) ExitingOnSIGTERM(code int) Run {
	r.SIGTERM, r.SIGTERMExit = sigtermExit, code
	return r
}

// matches reports whether argv contains every Match argument.
func (r Run) matches(argv []string) bool {
	for _, want := range r.Match {
		found := false
		for _, a := range argv {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (r Run) label(i int) string {
	if r.Fixture != "" {
		return fmt.Sprintf("run %d (%s)", i, r.Fixture)
	}
	if len(r.Match) > 0 {
		return fmt.Sprintf("run %d (when %s)", i, strings.Join(r.Match, " "))
	}
	return fmt.Sprintf("run %d", i)
}
