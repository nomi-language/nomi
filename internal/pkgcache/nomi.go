package pkgcache

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
// v0.0.2, or "" for one built from source. cmd/nomi sets it once, as it
// starts, from the version a release is stamped with; no other process
// sets it, so each has none.
func SetRelease(v string) {
	releaseMu.Lock()
	defer releaseMu.Unlock()
	release = v
}

// Release is the release of the running nomi (SetRelease), or "" for one
// built from source. A release refuses a package whose [package].nomi names
// a later release (NomiNeeded) and takes the compiler's repository at its
// own version (Pinned); a nomi built from source does neither.
func Release() string {
	releaseMu.Lock()
	defer releaseMu.Unlock()
	return release
}

// CompilerRepo is the compiler's own repository. Its lib/ holds the
// first-party libraries, which share the compiler's release tags.
const CompilerRepo = "github.com/nomi-language/nomi"

// Pinned is the version a build takes repo at whatever version a
// requirement on it names: the running release for CompilerRepo, since a
// first-party library at a release is written against that release's std,
// and "" for every other repository and for a nomi built from source.
func Pinned(repo string) string {
	if repo != CompilerRepo {
		return ""
	}
	return Release()
}

// Takes is the version a build takes repo at for a requirement or a lock
// line at v: the Pinned version when there is one, else v. A
// commit is an explicit request for that commit and is taken as named.
func Takes(repo, v string) string {
	if pin := Pinned(repo); pin != "" && !IsCommit(v) {
		return pin
	}
	return v
}

// NomiVersionError is a package whose nomi.toml's [package].nomi names a
// later release than the running nomi.
type NomiVersionError struct {
	// Package names the package as the error does: `this project (app)`,
	// or `package github.com/owner/kit/table v0.3.0`.
	Package string
	// Needs is the release [package].nomi names, and Running the running
	// nomi's.
	Needs, Running string
}

func (e *NomiVersionError) Error() string {
	return fmt.Sprintf("%s needs nomi %s or later, and this nomi is %s; install a newer nomi", e.Package, e.Needs, e.Running)
}

// NomiNeeded is the error for a package whose [package].nomi is needs, when
// the running nomi is a release older than it, or nil. A nomi built from
// source has no release and refuses nothing, and a package that names no
// release, or a value that is not one, is never refused here; Package is
// left for the caller, which knows which package it read.
func NomiNeeded(needs string) *NomiVersionError {
	running := Release()
	if running == "" || needs == "" || !IsVersion(needs) || semver.Compare(running, needs) >= 0 {
		return nil
	}
	return &NomiVersionError{Needs: needs, Running: running}
}
