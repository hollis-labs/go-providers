//go:build harnessprobe

package layout_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHarnessProbe re-runs hack/probe-harness-layout.sh against the installed
// harnesses and diffs the measured rows with the newest golden. A harness
// upgrade that changes where skills or config are read turns this red on a
// maintainer's machine; it never runs in CI (build tag harnessprobe).
//
//	go test -tags harnessprobe ./layout -run TestHarnessProbe
func TestHarnessProbe(t *testing.T) {
	for _, bin := range []string{"claude", "codex", "opencode", "jq", "perl", "git", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "hack", "probe-harness-layout.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", script, filepath.Join(t.TempDir(), "p")).Output()
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	golden, err := os.ReadFile(newestGolden(t))
	if err != nil {
		t.Fatal(err)
	}
	gotV, gotR := split(string(out))
	wantV, wantR := split(string(golden))
	if gotV != wantV {
		t.Logf("harness versions changed: golden %q, installed %q", wantV, gotV)
	}
	if gotR != wantR {
		t.Errorf("harness behaviour changed against %s (installed: %s).\nRe-run the probe, review docs/HARNESS-DISCOVERY.md and layout/table.go, then commit a new golden.\n--- golden\n%s\n--- measured\n%s",
			filepath.Base(newestGolden(t)), gotV, wantR, gotR)
	}
}

// split separates the version line (V) from the measured rows (R).
func split(tsv string) (versions, rows string) {
	var r []string
	for _, line := range strings.Split(strings.TrimSpace(tsv), "\n") {
		if strings.HasPrefix(line, "V\t") {
			versions = line
		} else {
			r = append(r, line)
		}
	}
	return versions, strings.Join(r, "\n")
}
