package providertest_test

import (
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/go-providers/providertest"
)

// A call file must appear only once its start record is complete: a
// Calls() that runs while a fake is starting must never return a call
// without its Args (CW-20261001-0119). Run under -race.
func TestCallsNeverSeeAStartlessCall(t *testing.T) {
	const launches = 200
	f := providertest.New(t, "claude", providertest.Lines("ok").Always())

	var (
		done    = make(chan struct{})
		torn    atomic.Int64
		checked atomic.Int64
		wg      sync.WaitGroup
	)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				for _, c := range f.Calls() {
					checked.Add(1)
					if len(c.Args) == 0 || c.Args[0] != "--n" || c.Seq == 0 {
						torn.Add(1)
					}
				}
			}
		}()
	}

	var launchers sync.WaitGroup
	errs := make(chan error, launches)
	for i := 0; i < launches; i++ {
		launchers.Add(1)
		go func(i int) {
			defer launchers.Done()
			if out, err := exec.Command(f.Path, "--n", fmt.Sprint(i)).CombinedOutput(); err != nil {
				errs <- fmt.Errorf("launch %d: %v: %s", i, err, out)
			}
		}(i)
	}
	launchers.Wait()
	close(done)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if n := torn.Load(); n > 0 {
		t.Fatalf("Calls() returned %d call(s) without a complete start record (of %d checked)", n, checked.Load())
	}
	if got := len(f.Calls()); got != launches {
		t.Fatalf("recorded %d calls, want %d", got, launches)
	}
	seen := map[int]bool{}
	for _, c := range f.Calls() {
		if seen[c.Seq] {
			t.Fatalf("sequence %d recorded twice", c.Seq)
		}
		seen[c.Seq] = true
	}
}
