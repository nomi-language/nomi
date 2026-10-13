// Package buildversion names the commit a binary was built from, read from the
// build-info section Go stamps into it. `nomi --version` prints it, `nomi
// build --no-vm` writes it into a .nomic file's header, and nomi-vm
// names it when it refuses a .nomic file another version wrote. It imports only
// the standard library, so a runner may link it.
package buildversion

import "runtime/debug"

// RevisionAbbrev is how many hex digits of the revision the version token
// carries. scripts/release.sh names its archives from `git describe --tags
// --always --dirty`, whose abbreviation is git's `core.abbrev` (7 by default
// and auto-widening with repository size); 12 is what git itself widens to
// well before a repository this size needs more, so a version token and an
// archive name refer to the same commit by a prefix of the same length or
// longer.
const RevisionAbbrev = 12

// Current is Token for this binary's own build info.
func Current() string {
	info, _ := debug.ReadBuildInfo()
	return Token(info)
}

// Token is the version itself: the revision this binary was built from, with
// git's own `-dirty` suffix when the tree had uncommitted changes, or the
// module version when the build carries no revision. A build from a checkout
// also has a module version, which Go derives from the nearest tag
// (`v0.1.0+dirty`, or a pseudo-version); the revision names the commit
// exactly, so it wins. A nil info, or one naming nothing, is `unknown`.
func Token(info *debug.BuildInfo) string {
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
	if len(revision) > RevisionAbbrev {
		revision = revision[:RevisionAbbrev]
	}
	if modified {
		return revision + "-dirty"
	}
	return revision
}
