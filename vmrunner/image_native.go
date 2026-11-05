//go:build !wasm

package vmrunner

import (
	"fmt"
	"os"
)

// ownImage opens the image appended to this executable: its code read, its
// files region mapped (OpenImage). The executable stays open for the
// process's life.
func ownImage() (*Image, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating this executable: %w", err)
	}
	return OpenImage(exe)
}
