//go:build !unix

package providertest

import (
	"errors"
	"os"
	"time"
)

// lockFile takes an exclusive lock by creating path exclusively, for
// platforms without flock.
func lockFile(path string) (unlock func(), err error) {
	path += ".held"
	deadline := time.Now().Add(30 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
