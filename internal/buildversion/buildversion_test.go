package buildversion

import (
	"runtime/debug"
	"testing"
)

// A release is a tag's version: not a pseudo-version, which names the
// nearest tag of an untagged commit, nor a modified checkout's, nor a
// build that names no version.
func TestReleaseOf_IsATagsVersionOnly(t *testing.T) {
	for _, c := range []struct{ version, want string }{
		{"v0.5.0", "v0.5.0"},
		{"v1.2.3-rc.1", "v1.2.3-rc.1"},
		{"v0.5.0+dirty", ""},
		{"v0.5.1-0.20261123101112-0123456789ab", ""},
		{"v0.5.1-0.20261123101112-0123456789ab+dirty", ""},
		{"v0.0.0-20261123101112-0123456789ab", ""},
		{"v1.2.3-rc.1.0.20261123101112-0123456789ab", ""},
		{"(devel)", ""},
		{"", ""},
	} {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/nomi-language/nomi", Version: c.version}}
		if got := ReleaseOf(info); got != c.want {
			t.Errorf("ReleaseOf(%q) = %q, want %q", c.version, got, c.want)
		}
	}
	if got := ReleaseOf(nil); got != "" {
		t.Errorf("ReleaseOf(nil) = %q, want none", got)
	}
}
