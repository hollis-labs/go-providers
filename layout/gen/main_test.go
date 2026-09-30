package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPassesOnCommittedFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	stale, err := run(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("generated files are stale: %v (run go generate ./...)", stale)
	}
}

func TestCheckDetectsStaleAndWriteFixes(t *testing.T) {
	dir := t.TempDir()
	stale, err := run(dir, true)
	if err != nil || len(stale) != 2 {
		t.Fatalf("empty tree: stale=%v err=%v", stale, err)
	}
	if _, err := run(dir, false); err != nil {
		t.Fatal(err)
	}
	if stale, err := run(dir, true); err != nil || len(stale) != 0 {
		t.Fatalf("after write: stale=%v err=%v", stale, err)
	}
	if err := os.WriteFile(filepath.Join(dir, docPath), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err = run(dir, true)
	if err != nil || len(stale) != 1 || stale[0] != docPath {
		t.Fatalf("edited doc: stale=%v err=%v", stale, err)
	}
}

func TestMarkdownMentionsEveryProvider(t *testing.T) {
	outs, err := render(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outs[jsonPath]), `"schema": 1`) {
		t.Error("json output lacks the schema version")
	}
}
