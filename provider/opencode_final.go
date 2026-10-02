package provider

import (
	"strings"
	"sync"
)

// opencodeStepText remembers the text of the step in progress, per opencode
// session, so the step that ends the turn can report it as the turn's final
// message (CW-20261002-0061).
//
// opencode run writes no marker on its text parts, but a turn is a run of steps
// and says which one is last: a step that ends in tool calls has reason
// "tool-calls" and the turn goes on; any other reason ends the turn. The text
// parts inside that last step are what the model wrote after its last tool
// call, which is its answer. A parser sees one line at a time, so it keeps the
// current step's text here until step_finish.
//
// A session calls both ParseLine and ParseLineEvents for every line, so
// recording a part is idempotent: a part is stored under its id, and seeing it
// twice changes nothing. A step is reset at its step_start, and reading it
// does not consume it, for the same reason.
//
// The zero value is ready to use; a nil *opencodeStepText records nothing and
// reports no text, which is how the stateless parse functions run.
type opencodeStepText struct {
	mu       sync.Mutex
	sessions map[string]*opencodeStep
}

// opencodeStepsInit guards the lazy allocation of an adapter's opencodeStepText,
// so a literal OpencodeAdapter{} works and the adapter needs no lock of its own.
var opencodeStepsInit sync.Mutex

// stepText returns the adapter's step memory, allocating it on first use.
func (a *OpencodeAdapter) stepText() *opencodeStepText {
	opencodeStepsInit.Lock()
	defer opencodeStepsInit.Unlock()
	if a.steps == nil {
		a.steps = &opencodeStepText{}
	}
	return a.steps
}

// opencodeStep is the text parts of one step in the order they arrived.
type opencodeStep struct {
	order []string
	text  map[string]string
}

// start opens a new step for the session, forgetting the previous one.
func (s *opencodeStepText) start(session string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*opencodeStep{}
	}
	s.sessions[session] = &opencodeStep{text: map[string]string{}}
}

// add records one text part of the session's step in progress.
func (s *opencodeStepText) add(session, part, text string) {
	if s == nil || text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*opencodeStep{}
	}
	step := s.sessions[session]
	if step == nil {
		step = &opencodeStep{text: map[string]string{}}
		s.sessions[session] = step
	}
	if _, seen := step.text[part]; !seen {
		step.order = append(step.order, part)
	}
	step.text[part] = text
}

// final is the text of the session's step in progress: its text parts, in
// order, one blank line apart. Empty when the step wrote none.
func (s *opencodeStepText) final(session string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.sessions[session]
	if step == nil {
		return ""
	}
	parts := make([]string, 0, len(step.order))
	for _, id := range step.order {
		if t := strings.TrimSpace(step.text[id]); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}
