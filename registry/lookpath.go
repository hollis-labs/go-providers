package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// commonLookupDirs are install directories searched after PATH for every
// runtime, because a process supervisor often starts a service with a minimal
// PATH. "~/" is the user's home directory.
var commonLookupDirs = []string{
	"~/.local/bin",
	"/opt/homebrew/bin",
	"/opt/homebrew/sbin",
	"~/bin",
	"~/go/bin",
	"/usr/local/bin",
}

// LookPath resolves d's executable: the EnvOverride variable's value when it
// is set (used as-is, not checked), else Binary on PATH, else the first
// non-directory named Binary in the common install directories and then d's
// LookupDirs.
func (d Descriptor) LookPath() (string, error) {
	if p := os.Getenv(d.EnvOverride); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath(d.Binary); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range append(append([]string(nil), commonLookupDirs...), d.LookupDirs...) {
		if rest, ok := strings.CutPrefix(dir, "~/"); ok {
			if home == "" {
				continue
			}
			dir = filepath.Join(home, rest)
		}
		p := filepath.Join(dir, d.Binary)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", &exec.Error{Name: d.Binary, Err: exec.ErrNotFound}
}
