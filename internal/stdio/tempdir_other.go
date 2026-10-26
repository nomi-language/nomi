//go:build !wasip1

package stdio

import "os"

// osTempDir is where temporary directories are made, and whether the
// program can reach it: the operating system's (os.TempDir).
func osTempDir() (string, bool) { return os.TempDir(), true }
