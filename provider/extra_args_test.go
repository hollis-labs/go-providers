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
