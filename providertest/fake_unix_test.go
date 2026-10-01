//go:build unix

package providertest_test

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/providertest"
)

// waitForSignal polls the call record until the fake has recorded a
// signal.
func waitForSignal(t *testing.T, f *providertest.Fake) providertest.Call {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c := f.Call(0); len(c.Signals) > 0 {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the fake recorded no signal")
	return providertest.Call{}
}

func TestIgnoringSIGTERMRecordsAndKeepsRunning(t *testing.T) {
	f := providertest.New(t, "claude", providertest.Script(providertest.Stdout("start"), providertest.Hang()).IgnoringSIGTERM())
	p := start(t, exec.Command(f.Path))
	p.readUntil(func(l string) bool { return l == "start" })
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if c := waitForSignal(t, f); c.Signals[0] != "SIGTERM" || c.Exited {
		t.Fatalf("call = %+v", c)
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("the fake exited on SIGTERM: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	_ = p.cmd.Process.Kill()
	<-done
	if c := f.Call(0); c.Exited {
		t.Errorf("a killed fake recorded an exit: %+v", c)
	}
}

func TestExitingOnSIGTERM(t *testing.T) {
	f := providertest.New(t, "codex", providertest.Script(providertest.Stdout("start"), providertest.Hang()).ExitingOnSIGTERM(0))
	p := start(t, exec.Command(f.Path))
	p.readUntil(func(l string) bool { return l == "start" })
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := p.wait(); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if c := f.Call(0); len(c.Signals) != 1 || !c.Exited || c.ExitCode != 0 {
		t.Errorf("call = %+v", c)
	}
}

func TestDefaultSIGTERMKillsTheFake(t *testing.T) {
	f := providertest.New(t, "opencode", providertest.Script(providertest.Stdout("start"), providertest.Hang()))
	p := start(t, exec.Command(f.Path))
	p.readUntil(func(l string) bool { return l == "start" })
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := p.cmd.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v", err)
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("wait status = %v, want killed by SIGTERM like a real CLI", ee)
	}
}
