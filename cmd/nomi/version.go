package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/nomi-language/nomi/internal/ffirun"
)

// WHY THIS READS BUILD INFO AND CARRIES NO `-ldflags` PLUMBING FOR THE REVISION.
//
// Go stamps the VCS revision, the commit time and a dirtiness flag into every
// main package it builds from a repository, and it does so for all three ways
// this binary is produced. Measured at e8635380 with `go version -m`, which
// reads the artifact's own build-info section:
//
//	go build -o nomi ./cmd/nomi              vcs.revision + vcs.modified present
//	go install ./cmd/nomi                    identical (Makefile's build-cli runs both)
//	go build -trimpath \
//	    -ldflags "-s -w" -o ... .                identical (scripts/release.sh's shape)
//
// So `-ldflags -X` version injection would add a build-time contract that has
// to be threaded through the Makefile, release.sh and every ad-hoc `go build`
// a contributor types, in exchange for information the toolchain already
// provides. `-s -w` strips the symbol table and DWARF, not the build-info
// section, and `-trimpath` leaves it alone too — the latter already recorded
// at internal/irbuild/irbuild.go's goDirective.
//
// ONE `-X` EXISTS, for the one thing build info cannot hold: the release tag.
// scripts/release.sh sets internal/ffirun.ReleaseVersion, because
// `nomi build --target` downloads the other platform's runner from the release
// by its tag, and a commit does not name a tag. A binary built any other way
// leaves it empty and is unaffected.
//
// WHICH VERSION IS PRINTED. From a checkout, Go also stamps a module version
// derived from the nearest tag (`v0.1.0+dirty`, or a pseudo-version past it).
// That names a tag, not the commit, so the revision is printed instead. A
// build from the module proxy (`go install ...@v0.2.0`) has no revision and
// prints its module version.
//
// A build with no VCS information at all — `-buildvcs=false`, or a build from
// an unpacked source archive — reports `unknown`. That is a real state, so it
// is named rather than rendered as an empty token.

// revisionAbbrev is how many hex digits of the revision the version token
// carries. scripts/release.sh names its archives from `git describe --tags
// --always --dirty`, whose abbreviation is git's `core.abbrev` (7 by default
// and auto-widening with repository size); 12 is what git itself widens to
// well before a repository this size needs more, so a version token and an
// archive name refer to the same commit by a prefix of the same length or
// longer.
const revisionAbbrev = 12

// runVersion is `nomi --version`.
func runVersion() { fmt.Println(currentVersionLine()) }

// currentVersionLine is this binary's version line: what `nomi --version`
// prints and the REPL's banner opens with.
func currentVersionLine() string {
	info, _ := debug.ReadBuildInfo()
	return versionLine(info)
}

// versionLine is the whole of what `nomi --version` prints.
//
// The Go toolchain and the platform are on the same line because the first
// thing a bug report needs after the revision is which toolchain and which
// target produced the binary, and both are free: runtime.Version and the
// GOOS/GOARCH constants are linker-provided and do not depend on build info
// being present at all. So the line degrades to a revision-less `unknown`
// rather than to nothing.
//
// A nil info means debug.ReadBuildInfo reported no build information.
func versionLine(info *debug.BuildInfo) string {
	return versionLineFor(ffirun.ReleaseVersion, info)
}

// versionLineFor is versionLine for a binary cut as release (empty when it
// was not). A release names its tag first and keeps the revision beside it,
// since the tag is what its runner downloads are fetched by and the revision
// is what a prebuilt runner is checked against.
func versionLineFor(release string, info *debug.BuildInfo) string {
	if release != "" {
		return fmt.Sprintf("nomi %s (%s, %s %s/%s)", release, versionToken(info), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("nomi %s (%s %s/%s)", versionToken(info), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// versionToken is the version itself: the revision this binary was built from,
// with git's own `-dirty` suffix when the tree had uncommitted changes, or
// the module version when the build carries no revision. A build from a
// checkout also has a module version, which Go derives from the nearest tag
// (`v0.1.0+dirty`, or a pseudo-version); the revision names the commit
// exactly, so it wins.
func versionToken(info *debug.BuildInfo) string {
	if info == nil {
		return "unknown"
	}
	revision := ""
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		// A build resolved from the module proxy (`go install ...@v0.2.0`)
		// has a module version and no VCS information. `(devel)` is a build
		// from a directory with no VCS information, which names nothing.
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		return "unknown"
	}
	if len(revision) > revisionAbbrev {
		revision = revision[:revisionAbbrev]
	}
	if modified {
		return revision + "-dirty"
	}
	return revision
}

// versionRequested reports whether args name the version query.
//
// Both spellings are accepted because this CLI already uses both shapes: every
// command is a bare word (`run`, `check`, `build`, `gen`, `dap`, `test`,
// `fmt`), while flags are `-o`, `-w`, `-debug` and also `--line`. `nomi` has no
// `--help` to be consistent with — an unrecognized first argument starts the
// REPL — so there is no existing convention that picks one.
func versionRequested(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "--version" || args[1] == "version"
}
