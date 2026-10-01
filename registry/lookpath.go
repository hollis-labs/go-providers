package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// userLookupDirs and systemLookupDirs are install directories searched after
// PATH for every runtime, because a process supervisor often starts a service
// with a minimal PATH. A runtime's own LookupDirs go between them, so its
// installer's directory wins over /usr/local/bin. "~/" is the user's home
// directory.
var (
	userLookupDirs = []string{
		"~/.local/bin",
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"~/bin",
		"~/go/bin",
	}
	systemLookupDirs = []string{"/usr/local/bin"}
)

// LookPath resolves d's executable: the EnvOverride variable's value when it
// is set (used as-is, not checked), else Binary on PATH, else the first
// non-directory named Binary in the user install directories, then d's
// LookupDirs, then /usr/local/bin.
func (d Descriptor) LookPath() (string, error) {
	if p := os.Getenv(d.EnvOverride); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath(d.Binary); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	dirs := append(append(append([]string(nil), userLookupDirs...), d.LookupDirs...), systemLookupDirs...)
	for _, dir := range dirs {
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
