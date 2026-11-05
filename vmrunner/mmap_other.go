//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package vmrunner

import (
	"errors"
	"os"
)

// mapRange is refused where the runner does not map files: each read of
// the region is a ReadAt.
func mapRange(*os.File, int64, int64) ([]byte, func() error, error) {
	return nil, nil, errors.New("this platform's runner does not map files")
}
