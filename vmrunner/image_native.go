//go:build !wasm

package vmrunner

import (
	"fmt"
	"os"
)

// ownImage reads the image appended to this executable.
func ownImage() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating this executable: %w", err)
	}
	return ReadImage(exe)
}
