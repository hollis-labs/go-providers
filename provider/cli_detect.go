package provider

import (
	"os"
	"os/exec"
	"path/filepath"
)

// lookPathExpanded tries exec.LookPath first, then checks common install
// directories that may not be in the service PATH (e.g., when launched by
// a process supervisor with a minimal PATH).
func lookPathExpanded(name string) (string, error) {
	// Standard LookPath using current PATH.
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	home, _ := os.UserHomeDir()

	// Common locations not always in service PATH.
	candidates := []string{
		filepath.Join(home, ".local", "bin", name),
		"/opt/homebrew/bin/" + name,
		"/opt/homebrew/sbin/" + name,
		filepath.Join(home, "bin", name),
		filepath.Join(home, "go", "bin", name),
		filepath.Join(home, ".opencode", "bin", name),
		"/usr/local/bin/" + name,
	}

	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}

	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
