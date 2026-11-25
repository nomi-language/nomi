package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/nomi-language/nomi/internal/buildversion"
)

// Why this reads build info and carries no `-ldflags` plumbing for the revision.
//
// Go stamps the VCS revision, the commit time and a dirtiness flag into every
// main package it builds from a repository, and it does so for all three ways
// this binary is produced, as `go version -m` shows; it
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
// section, and `-trimpath` leaves it alone too; the latter is also recorded
// at internal/irbuild/irbuild.go's goDirective.
//
// One `-X` exists, for the release tag of a binary built for a release's
// archives: scripts/release.sh sets internal/buildversion's stamp. Which
// release a binary is, from that stamp or its module version, is
// buildversion.Release, and its package doc says what each kind of build
// answers.
//
// Which version is printed. A release prints its tag first and its token
// beside it. From a checkout, Go also stamps a module version derived from
// the nearest tag (`v0.1.0+dirty`, or a pseudo-version past it). That names
// a tag, not the commit, so the revision is the token. A build from the
// module proxy (`go install ...@v0.2.0`) has no revision, and its token is
// its module version, the release itself, printed once.
//
// A build with no VCS information at all (`-buildvcs=false`, or a build from
// an unpacked source archive) reports `unknown`. That is a real state, so it
// is named rather than rendered as an empty token.

// revisionAbbrev is how many hex digits of the revision the version token
// carries (internal/buildversion).
const revisionAbbrev = buildversion.RevisionAbbrev

// runVersion is `nomi --version`.
const versionUsage = "usage: nomi version"

func runVersion() {
	fmt.Println(currentVersionLine())
	for _, line := range staleEditorLines() {
		fmt.Println(line)
	}
}

// currentVersionLine is this binary's version line: what `nomi --version`
// prints and the REPL's banner opens with.
func currentVersionLine() string {
	info, _ := debug.ReadBuildInfo()
	return versionLine(info)
}

// versionToolchainEnv names the variable that adds the Go toolchain that
// built `nomi` to its version line, for work on the compiler: any value but
// `0` does.
const versionToolchainEnv = "NOMI_DEBUG_VERSION"

// versionLine is the whole of what `nomi --version` prints.
//
// The platform is on the same line because the first thing a bug report
// needs after the revision is which target produced the binary, and it is
// free: the GOOS/GOARCH constants are linker-provided and do not depend on
// build info being present at all. So the line degrades to a revision-less
// `unknown` rather than to nothing. The Go toolchain is left out, since a
// user writes, runs and builds Nomi without one; versionToolchainEnv puts
// it in for work on the compiler.
//
// A nil info means debug.ReadBuildInfo reported no build information.
func versionLine(info *debug.BuildInfo) string {
	toolchain := ""
	if v := os.Getenv(versionToolchainEnv); v != "" && v != "0" {
		toolchain = runtime.Version()
	}
	return versionLineFor(buildversion.Release(), info, toolchain)
}

// versionLineFor is versionLine for a binary of release (empty for a build
// from source), with the Go toolchain's version before the platform when
// toolchain is not empty. A release names its tag first and keeps its token
// beside it when the token is a revision, since the tag is what its runner
// downloads are fetched by and the revision is what a prebuilt runner is
// checked against.
func versionLineFor(release string, info *debug.BuildInfo, toolchain string) string {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if toolchain != "" {
		platform = toolchain + " " + platform
	}
	token := versionToken(info)
	if release != "" && release != token {
		platform = token + ", " + platform
	}
	if release != "" {
		token = release
	}
	return fmt.Sprintf("nomi %s (%s)", token, platform)
}

// versionToken is the version itself (internal/buildversion).
func versionToken(info *debug.BuildInfo) string { return buildversion.Token(info) }
