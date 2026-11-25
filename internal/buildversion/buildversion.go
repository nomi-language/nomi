package buildversion

import (
	"regexp"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// RevisionAbbrev is how many hex digits of the revision the version token
// carries. git's own abbreviation (`core.abbrev`) starts at 7 and widens
// with repository size; 12 is what git widens to well before a repository
// this size needs more, so a token names one commit and is a prefix of
// what `git log` prints for it.
const RevisionAbbrev = 12

// stamp is the release scripts/release.sh cuts `nomi` as, set by
// `-ldflags -X`; "" in every other build.
var stamp string

// testStamp, when set, stands in for stamp (SetStampForTest).
var testStamp atomic.Pointer[string]

// SetStampForTest makes this process read v as its stamp, as a binary
// scripts/release.sh cut as v would, until the returned function restores
// the stamp before it. A test stands in a release with it: Release and
// BuiltByReleaseScript, and every reader of them, move together.
func SetStampForTest(v string) (restore func()) {
	old := testStamp.Swap(&v)
	return func() { testStamp.Store(old) }
}

func currentStamp() string {
	if p := testStamp.Load(); p != nil {
		return *p
	}
	return stamp
}

var buildInfo = sync.OnceValue(func() *debug.BuildInfo {
	info, _ := debug.ReadBuildInfo()
	return info
})

// Release is the release this binary is, or "" for a build from source
// (ReleaseOf of its stamp and build info).
func Release() string { return ReleaseOf(currentStamp(), buildInfo()) }

// BuiltByReleaseScript reports whether scripts/release.sh built this
// binary, which it stamps.
func BuiltByReleaseScript() bool { return currentStamp() != "" }

// Name is how a message names this binary: its release followed by its
// Token in parentheses, or the token alone when it is no release or the
// token is the release itself.
func Name() string { return NameOf(Release(), Token(buildInfo())) }

// NameOf is Name for a binary of release and token.
func NameOf(release, token string) string {
	if release == "" || release == token {
		return token
	}
	return release + " (" + token + ")"
}

var (
	tagVersion    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)
	pseudoVersion = regexp.MustCompile(`[0-9]{14}-[0-9a-f]{12}$`)
	// describedCommit is the suffix `git describe --tags --dirty` gives a
	// commit past a tag, or a tree with changes.
	describedCommit = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+(-dirty)?$|-dirty$`)
)

// IsRelease reports whether v is a release version: a tag vX.Y.Z or a
// prerelease vX.Y.Z-..., as scripts/release-tags.sh accepts, and not a Go
// pseudo-version or `git describe`'s name for a commit that is not a tag.
func IsRelease(v string) bool {
	return tagVersion.MatchString(v) && !pseudoVersion.MatchString(v) && !describedCommit.MatchString(v)
}

// ReleaseOf is the release of a binary stamped with stamp whose build info
// is info: the stamp when it is a release version, else the main module's
// version when that is one, else "".
func ReleaseOf(stamp string, info *debug.BuildInfo) string {
	if IsRelease(stamp) {
		return stamp
	}
	if info != nil && IsRelease(info.Main.Version) {
		return info.Main.Version
	}
	return ""
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
