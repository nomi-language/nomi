package modcache

import (
	"fmt"
	"sync"

	"golang.org/x/mod/semver"
)

var (
	releaseMu sync.Mutex
	release   string
)

// SetRelease records the release of the running nomi, a version such as
// v0.5.0, or "" for one built from source. cmd/nomi sets it once, as it
// starts, from the version a release is stamped with; no other process
// sets it, so each has none.
func SetRelease(v string) {
	releaseMu.Lock()
	defer releaseMu.Unlock()
	release = v
}

// Release is the release of the running nomi (SetRelease), or "" for one
// built from source. A release refuses a module whose [module].nomi names
// a later release (NomiNeeded) and takes the first-party libraries at its
// own version (internal/modfetch); a nomi built from source does neither.
func Release() string {
	releaseMu.Lock()
	defer releaseMu.Unlock()
	return release
}

// NomiVersionError is a module whose nomi.toml's [module].nomi names a
// later release than the running nomi.
type NomiVersionError struct {
	// Module names the module as the error does: `this project (app)`,
	// or `module github.com/owner/kit/table v0.3.0`.
	Module string
	// Needs is the release [module].nomi names, and Running the running
	// nomi's.
	Needs, Running string
}

func (e *NomiVersionError) Error() string {
	return fmt.Sprintf("%s needs nomi %s or later, and this nomi is %s; install a newer nomi", e.Module, e.Needs, e.Running)
}

// NomiNeeded is the error for a module whose [module].nomi is needs, when
// the running nomi is a release older than it, or nil. A nomi built from
// source has no release and refuses nothing, and a module that names no
// release, or a value that is not one, is never refused here; Module is
// left for the caller, which knows which module it read.
func NomiNeeded(needs string) *NomiVersionError {
	running := Release()
	if running == "" || needs == "" || !IsVersion(needs) || semver.Compare(running, needs) >= 0 {
		return nil
	}
	return &NomiVersionError{Needs: needs, Running: running}
}
