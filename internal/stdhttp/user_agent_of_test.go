package stdhttp

import "testing"

// TestUserAgentOf_ARelease holds the default User-Agent's form: the
// release's version without the tag's `v`, and the bare name for a build
// that is not a release.
func TestUserAgentOf_ARelease(t *testing.T) {
	for tag, want := range map[string]string{
		"":            "nomi",
		"v0.5.0":      "nomi/0.5.0",
		"v1.2.3-rc.1": "nomi/1.2.3-rc.1",
	} {
		if got := userAgentOf(tag); got != want {
			t.Errorf("userAgentOf(%q) = %q, want %q", tag, got, want)
		}
	}
}
