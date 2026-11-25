package buildversion

import (
	"runtime/debug"
	"testing"
)

const testRevision = "e8635380091fdebf6ee103ebbd8a47a70eeb0fea"

// info is the build info of a binary whose main module's version is
// version, with a VCS revision when fromCheckout.
func info(version string, fromCheckout, modified bool) *debug.BuildInfo {
	b := &debug.BuildInfo{Main: debug.Module{Path: "github.com/nomi-language/nomi", Version: version}}
	if fromCheckout {
		m := "false"
		if modified {
			m = "true"
		}
		b.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: testRevision}, {Key: "vcs.modified", Value: m}}
	}
	return b
}

// What each kind of build is: a release when its stamp or its module
// version is a release version, the stamp winning, and a build from source
// otherwise. The rows are the build kinds the package doc lists, each with
// the stamp and build info Go and scripts/release.sh give it.
func TestReleaseOf_EachKindOfBuild(t *testing.T) {
	for _, c := range []struct {
		name  string
		stamp string
		info  *debug.BuildInfo
		want  string
		// name is what Name says for the build: its release and token.
		wantName string
	}{
		{"scripts/release.sh at its tag", "v0.0.2", info("v0.0.2", true, false), "v0.0.2", "v0.0.2 (e8635380091f)"},
		{"scripts/release.sh's dry run, past the tag", "v0.0.0-dryrun", info("v0.0.3-0.20261123101112-e8635380091f", true, false), "v0.0.0-dryrun", "v0.0.0-dryrun (e8635380091f)"},
		{"a stamp that names a commit, not a release", "v0.0.2-3-ge863538", info("v0.0.3-0.20261123101112-e8635380091f", true, false), "", "e8635380091f"},
		{"a stamp of a modified tree", "v0.0.2-dirty", info("v0.0.2+dirty", true, true), "", "e8635380091f-dirty"},
		{"a stamp that names no release, at a tag", "dev", info("v0.0.2", true, false), "v0.0.2", "v0.0.2 (e8635380091f)"},
		{"the stamp wins over another tag", "v0.0.3", info("v0.0.2", true, false), "v0.0.3", "v0.0.3 (e8635380091f)"},
		{"go install @version", "", info("v0.0.2", false, false), "v0.0.2", "v0.0.2"},
		{"go install @version of a prerelease", "", info("v0.1.0-rc.1", false, false), "v0.1.0-rc.1", "v0.1.0-rc.1"},
		{"go install @commit", "", info("v0.0.3-0.20261123101112-e8635380091f", false, false), "", "v0.0.3-0.20261123101112-e8635380091f"},
		{"go build in a clean checkout at a tag", "", info("v0.0.2", true, false), "v0.0.2", "v0.0.2 (e8635380091f)"},
		{"go build in a modified checkout at a tag", "", info("v0.0.2+dirty", true, true), "", "e8635380091f-dirty"},
		{"go build between tags", "", info("v0.0.3-0.20261123101112-e8635380091f", true, false), "", "e8635380091f"},
		{"go build between tags, modified", "", info("v0.0.3-0.20261123101112-e8635380091f+dirty", true, true), "", "e8635380091f-dirty"},
		{"go build of a prerelease's untagged child", "", info("v1.2.3-rc.1.0.20261123101112-e8635380091f", true, false), "", "e8635380091f"},
		{"go build before any tag", "", info("v0.0.0-20261123101112-e8635380091f", true, false), "", "e8635380091f"},
		{"go build with no VCS information", "", info("(devel)", false, false), "", "unknown"},
		{"a test binary", "", info("", false, false), "", "unknown"},
		{"no build info", "", nil, "", "unknown"},
	} {
		got := ReleaseOf(c.stamp, c.info)
		if got != c.want {
			t.Errorf("%s: ReleaseOf(%q, %v) = %q, want %q", c.name, c.stamp, c.info, got, c.want)
		}
		if name := NameOf(got, Token(c.info)); name != c.wantName {
			t.Errorf("%s: named %q, want %q", c.name, name, c.wantName)
		}
	}
}

// A release version is the form scripts/release-tags.sh tags: vX.Y.Z or a
// prerelease of it, with no leading zeros, and never a Go pseudo-version or
// git describe's name for a commit past a tag.
func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.0.1":                               true,
		"v10.20.30":                            true,
		"v1.2.3-rc.1":                          true,
		"v0.0.0-reltest":                       true,
		"0.0.1":                                false,
		"v0.1":                                 false,
		"v01.2.3":                              false,
		"v0.0.1+dirty":                         false,
		"v0.0.1-dirty":                         false,
		"v0.0.1-3-gabc1234":                    false,
		"v0.0.1-3-gabc1234-dirty":              false,
		"v0.0.2-0.20261123101112-0123456789ab": false,
		"v0.0.0-20261123101112-0123456789ab":   false,
		"v1.2.3-rc.1.0.20261123101112-0123456789ab": false,
		"(devel)": false,
		"":        false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %t, want %t", v, got, want)
		}
	}
}

// A test stands in a release by its stamp, and Release, BuiltByReleaseScript
// and Name all follow it until it is restored.
func TestSetStampForTest_MovesEveryAnswer(t *testing.T) {
	if Release() != "" || BuiltByReleaseScript() {
		t.Fatalf("a test binary is release %q, built by the release script %t; want neither", Release(), BuiltByReleaseScript())
	}
	restore := SetStampForTest("v0.0.2")
	if Release() != "v0.0.2" || !BuiltByReleaseScript() || Name() != "v0.0.2 ("+Token(buildInfo())+")" {
		t.Errorf("stamped v0.0.2: release %q, built by the release script %t, named %q", Release(), BuiltByReleaseScript(), Name())
	}
	inner := SetStampForTest("")
	if Release() != "" || BuiltByReleaseScript() {
		t.Errorf("stamped none: release %q, built by the release script %t", Release(), BuiltByReleaseScript())
	}
	inner()
	if Release() != "v0.0.2" {
		t.Errorf("restored to v0.0.2: release %q", Release())
	}
	restore()
	if Release() != "" || BuiltByReleaseScript() {
		t.Errorf("restored: release %q, built by the release script %t", Release(), BuiltByReleaseScript())
	}
}
