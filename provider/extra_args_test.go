package provider

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/go-providers/registry"
)

// A caller's extras land where the prepared path puts them: for every runtime,
// mode and turn, BuildArgsWithExtras matches the projection's ResolveTurn with
// the same extras, and with none it is BuildArgs.
func TestBuildArgsWithExtrasMatchesProjectionResolveTurn(t *testing.T) {
	extra := []string{"--extra-flag", "extra-value"}
	for _, c := range argvCases(t) {
		t.Run(c.name, func(t *testing.T) {
			eb, ok := c.adapter.(ExtraArgsBuilder)
			if !ok {
				t.Fatalf("%T does not implement ExtraArgsBuilder", c.adapter)
			}
			proj := c.project(t)
			for _, turn := range argvTurns {
				b, err := proj.ResolveTurn(argvRoots, turn.in, extra)
				if err != nil {
					t.Fatalf("%s: ResolveTurn: %v", turn.name, err)
				}
				if got := eb.BuildArgsWithExtras(turn.in.Prompt, turn.in.SystemPrompt, turn.in.ResumeID, extra); !reflect.DeepEqual(got, b.Argv) {
					t.Errorf("%s:\n  BuildArgsWithExtras %q\n  ResolveTurn         %q", turn.name, got, b.Argv)
				}
				plain := c.adapter.BuildArgs(turn.in.Prompt, turn.in.SystemPrompt, turn.in.ResumeID)
				if got := eb.BuildArgsWithExtras(turn.in.Prompt, turn.in.SystemPrompt, turn.in.ResumeID, nil); !reflect.DeepEqual(got, plain) {
					t.Errorf("%s: BuildArgsWithExtras(nil) %q, BuildArgs %q", turn.name, got, plain)
				}
			}
		})
	}
}

// Every native mode's adapter takes extras at its slot, so a session runtime
// never has to splice them for a built-in adapter.
func TestNativeAdaptersImplementExtraArgsBuilder(t *testing.T) {
	for _, d := range registry.All() {
		for _, m := range d.NativeModes() {
			a, err := NewAdapter(d.ID, m)
			if err != nil {
				t.Errorf("%s/%s: %v", d.ID, m, err)
				continue
			}
			if _, ok := a.(ExtraArgsBuilder); !ok {
				t.Errorf("%s/%s: %T does not implement ExtraArgsBuilder", d.ID, m, a)
			}
		}
	}
}

// A codex exec resume turn takes the extras in front of `resume <id>`, where
// codex parses -s, --cd and --add-dir, after the adapter's own ExtraArgs.
func TestCodexBuildArgsWithExtrasBeforeResume(t *testing.T) {
	a := &CodexAdapter{ProjectDir: "/work/project", ExtraArgs: []string{"-c", `model_reasoning_effort="low"`}}
	got := a.BuildArgsWithExtras("say bye", "", "thread-1", []string{"-s", "read-only"})
	want := []string{"exec", "-c", `model_reasoning_effort="low"`, "-s", "read-only", "--json", "--skip-git-repo-check",
		"--cd", "/work/project", "resume", "thread-1", "--", "say bye"}
	if !slices.Equal(got, want) {
		t.Errorf("argv\n got %q\nwant %q", got, want)
	}
}

// Concurrent turns on one adapter, each with its own extras, do not write
// through the adapter's ExtraArgs: an append into its spare capacity would
// race here, and mix one turn's extras into another's argv.
func TestBuildArgsWithExtrasConcurrentTurns(t *testing.T) {
	own := make([]string, 2, 8)
	copy(own, []string{"-c", `model_reasoning_effort="low"`})
	a := &CodexAdapter{ExtraArgs: own}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mine := fmt.Sprintf("n=%d", i)
			got := a.BuildArgsWithExtras("hi", "", "", []string{"-c", mine})
			if at := slices.Index(got, mine); at != 4 || slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "n=") && s != mine }) {
				t.Errorf("turn %d argv = %q", i, got)
			}
		}()
	}
	wg.Wait()
}

// withOwnExtraArgs returns a copy of a built-in adapter whose own ExtraArgs
// are own.
func withOwnExtraArgs(t *testing.T, a CLIAdapter, own []string) ExtraArgsBuilder {
	t.Helper()
	switch v := a.(type) {
	case *ClaudeAdapter:
		cp := *v
		cp.ExtraArgs = own
		return &cp
	case *CodexAdapter:
		cp := *v
		cp.ExtraArgs = own
		return &cp
	case *OpencodeAdapter:
		cp := *v
		cp.ExtraArgs = own
		return &cp
	case *AntigravityAdapter:
		cp := *v
		cp.ExtraArgs = own
		return &cp
	}
	t.Fatalf("%T is not a built-in adapter with ExtraArgs", a)
	return nil
}

// BuildArgsWithExtras never writes through the adapter's own ExtraArgs, even
// when that slice has spare capacity: appending the caller's extras into it
// would overwrite whatever else shares the backing array, and mix one turn's
// extras into another's. TestBuildArgsWithExtrasConcurrentTurns catches that
// as a race under -race; this catches the write itself without it, for all
// four adapters (CW-20261001-0219).
func TestBuildArgsWithExtrasLeavesSpareCapacityAlone(t *testing.T) {
	const sentinel = "spare-capacity-untouched"
	for _, a := range []CLIAdapter{NewClaudeAdapter(), NewCodexAdapter(), NewOpencodeAdapter(), NewAntigravityAdapter()} {
		t.Run(fmt.Sprintf("%T", a), func(t *testing.T) {
			own := make([]string, 2, 8)
			copy(own, []string{"--own-flag", "own-value"})
			spare := own[len(own):cap(own)]
			for i := range spare {
				spare[i] = sentinel
			}
			eb := withOwnExtraArgs(t, a, own)
			// Fewer extras than spare slots, so an append into own would
			// land in the spare capacity.
			got := eb.BuildArgsWithExtras("hi", "", "", []string{"--extra-flag", "extra-value", "more-1", "more-2"})
			for i, s := range spare {
				if s != sentinel {
					t.Errorf("spare capacity [%d] = %q after BuildArgsWithExtras: the extras were written through ExtraArgs", i, s)
				}
			}
			// The argv does not alias own either: rewriting it leaves own alone.
			at := slices.Index(got, "--own-flag")
			if at < 0 {
				t.Fatalf("argv %q lacks the adapter's own ExtraArgs", got)
			}
			got[at] = "mutated"
			if own[0] != "--own-flag" {
				t.Errorf("rewriting the argv changed the adapter's ExtraArgs to %q", own)
			}
		})
	}
}

// The adapter's own ExtraArgs come first, then the caller's extras, for every
// runtime and mode: BuildArgsWithExtras equals the projection's ResolveTurn
// given both in that order, and with no extras it equals BuildArgs.
// TestBuildArgsWithExtrasMatchesProjectionResolveTurn leaves the adapter's own
// ExtraArgs empty, so on its own it pins this order for none of them.
func TestBuildArgsWithExtrasPutsOwnExtraArgsFirst(t *testing.T) {
	own := []string{"--own-flag", "own-value"}
	caller := []string{"--extra-flag", "extra-value"}
	both := slices.Concat(own, caller)
	seen := map[string]bool{}
	for _, c := range argvCases(t) {
		seen[fmt.Sprintf("%T", c.adapter)] = true
		t.Run(c.name, func(t *testing.T) {
			eb := withOwnExtraArgs(t, c.adapter, own)
			proj := c.project(t)
			for _, turn := range argvTurns {
				in := turn.in
				want, err := proj.ResolveTurn(argvRoots, in, both)
				if err != nil {
					t.Fatalf("%s: ResolveTurn: %v", turn.name, err)
				}
				got := eb.BuildArgsWithExtras(in.Prompt, in.SystemPrompt, in.ResumeID, caller)
				if !reflect.DeepEqual(got, want.Argv) {
					t.Errorf("%s:\n  BuildArgsWithExtras %q\n  ResolveTurn         %q", turn.name, got, want.Argv)
				}
				if o, e := slices.Index(got, "--own-flag"), slices.Index(got, "--extra-flag"); o < 0 || e < 0 || o > e {
					t.Errorf("%s: own ExtraArgs at %d, caller's extras at %d in %q; want own first", turn.name, o, e, got)
				}
				// With no extras the adapter's own ExtraArgs are all there is.
				if got, want := eb.BuildArgsWithExtras(in.Prompt, in.SystemPrompt, in.ResumeID, nil), eb.(CLIAdapter).BuildArgs(in.Prompt, in.SystemPrompt, in.ResumeID); !slices.Equal(got, want) {
					t.Errorf("%s: BuildArgsWithExtras(nil) %q, BuildArgs %q", turn.name, got, want)
				}
			}
		})
	}
	for _, typ := range []string{"*provider.ClaudeAdapter", "*provider.CodexAdapter", "*provider.OpencodeAdapter", "*provider.AntigravityAdapter"} {
		if !seen[typ] {
			t.Errorf("argvCases has no case for %s", typ)
		}
	}
}

// An extra that carries its own "--" is documented as not belonging at the
// slot (see ExtraArgsBuilder), and it is deliberately not rejected: the
// method returns only an argv, so a check could only panic or drop it. Every
// adapter puts it at the slot as given, where the prepared path puts it, and
// keeps the tail contiguous.
func TestBuildArgsWithExtrasDoesNotRejectDoubleDash(t *testing.T) {
	tail := []string{"--", "tail"}
	for _, c := range argvCases(t) {
		t.Run(c.name, func(t *testing.T) {
			eb := c.adapter.(ExtraArgsBuilder)
			proj := c.project(t)
			for _, turn := range argvTurns {
				in := turn.in
				want, err := proj.ResolveTurn(argvRoots, in, tail)
				if err != nil {
					t.Fatalf("%s: ResolveTurn: %v", turn.name, err)
				}
				got := eb.BuildArgsWithExtras(in.Prompt, in.SystemPrompt, in.ResumeID, tail)
				if !reflect.DeepEqual(got, want.Argv) {
					t.Errorf("%s:\n  BuildArgsWithExtras %q\n  ResolveTurn         %q", turn.name, got, want.Argv)
				}
				if at := slices.Index(got, "--"); at < 0 || at+1 >= len(got) || got[at+1] != "tail" {
					t.Errorf("%s: argv %q does not carry the extras' \"--\" tail intact", turn.name, got)
				}
			}
		})
	}
}
