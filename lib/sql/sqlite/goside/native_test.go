//go:build !js

package goside

import (
	"testing"
)

// The tests of what only the native build does: file databases.

// A file database waits for a lock rather than failing at once.
func TestOpen_FileDatabaseWaitsForALock(t *testing.T) {
	c, err := Open(t.TempDir() + "/x.db")
	if err != nil {
		t.Fatal(err)
	}
	defer Close(c)
	got := Run(c, 0, 0, []string{"PRAGMA busy_timeout"}, nil)
	if got.Failure.Kind != FailNone || len(got.Rows) != 1 || got.Rows[0][0].Int != 5000 {
		t.Errorf("busy_timeout: %+v", got)
	}
}
