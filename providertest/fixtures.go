package providertest

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"testing"
)

//go:embed fixtures
var embedded embed.FS

// Fixtures is the fixture corpus, one directory per runtime id:
// "claude/print_turn1.jsonl", "codex/app_server_turn.transcript.jsonl", …
// fixtures/README.md describes each file.
var Fixtures fs.FS = mustSub(embedded, "fixtures")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// ReadFixture returns the bytes of one fixture file, named
// "<runtime>/<file>", failing t if it does not exist.
func ReadFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(Fixtures, name)
	if err != nil {
		t.Fatalf("providertest: read fixture %s: %v", name, err)
	}
	return b
}

// FixtureLines returns the non-empty lines of one fixture file, named
// "<runtime>/<file>", without their newlines.
func FixtureLines(t testing.TB, name string) [][]byte {
	t.Helper()
	return splitLines(ReadFixture(t, name))
}

// FixtureSteps returns the steps [Replay] would run for a fixture named
// "<runtime>/<stem>".
func FixtureSteps(t testing.TB, fixture string) []Step {
	t.Helper()
	steps, err := loadFixture(Fixtures, fixture)
	if err != nil {
		t.Fatalf("providertest: %v", err)
	}
	return steps
}

func loadFixture(fsys fs.FS, fixture string) ([]Step, error) {
	if strings.Contains(path.Base(fixture), ".") {
		return nil, fmt.Errorf("fixture %q: name the stem, without an extension", fixture)
	}
	if b, err := fs.ReadFile(fsys, fixture+".transcript.jsonl"); err == nil {
		return parseTranscript(fixture, b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	stdout, err := fs.ReadFile(fsys, fixture+".jsonl")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no fixture %q (want %[1]s.transcript.jsonl or %[1]s.jsonl)", fixture)
		}
		return nil, err
	}
	var steps []Step
	for _, l := range splitLines(stdout) {
		steps = append(steps, Stdout(string(l)))
	}
	if b, err := fs.ReadFile(fsys, fixture+".stderr"); err == nil {
		for _, l := range splitLines(b) {
			steps = append(steps, Stderr(string(l)))
		}
	}
	code := 0
	if b, err := fs.ReadFile(fsys, fixture+".exit"); err == nil {
		if code, err = strconv.Atoi(strings.TrimSpace(string(b))); err != nil {
			return nil, fmt.Errorf("fixture %s.exit: %v", fixture, err)
		}
	}
	return append(steps, Exit(code)), nil
}

func parseTranscript(fixture string, b []byte) ([]Step, error) {
	var steps []Step
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var s Step
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("%s.transcript.jsonl:%d: %v", fixture, n, err)
		}
		if err := s.validate(); err != nil {
			return nil, fmt.Errorf("%s.transcript.jsonl:%d: %v", fixture, n, err)
		}
		steps = append(steps, s)
	}
	return steps, sc.Err()
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		l = bytes.TrimRight(l, "\r")
		if len(bytes.TrimSpace(l)) > 0 {
			out = append(out, l)
		}
	}
	return out
}
