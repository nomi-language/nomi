// Package testside is what sqlkit's own tests need from Go and std does not
// offer: a scratch directory for a database file, and its removal.
package testside

import "os"

// TempDir makes a new, empty directory under the system's temporary
// directory and answers its path.
func TempDir() (string, error) {
	return os.MkdirTemp("", "sqlkit-test-")
}

// RemoveAll removes path and everything under it.
func RemoveAll(path string) error {
	return os.RemoveAll(path)
}
