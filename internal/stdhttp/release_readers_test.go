package stdhttp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/stdsystem"
	"github.com/nomi-language/nomi/rt"
)

// Every reader of the release this binary is reads buildversion.Release,
// so the default User-Agent, system.nomi_release, the first-party
// libraries' pin and the [package].nomi floor move together when a test
// stands in a release, and all go back when it ends.
func TestReleaseReaders_MoveTogether(t *testing.T) {
	type answers struct {
		userAgent string
		release   rt.Option[string]
		pinned    string
		refused   bool
	}
	read := func() answers {
		return answers{
			userAgent: userAgent(),
			release:   stdsystem.NomiRelease(),
			pinned:    pkgcache.Pinned(pkgcache.CompilerRepo),
			refused:   pkgcache.NomiNeeded("v0.0.3") != nil,
		}
	}
	fromSource := answers{userAgent: "nomi", release: rt.None[string]()}
	if got := read(); got != fromSource {
		t.Fatalf("a test binary answers %+v, want %+v", got, fromSource)
	}
	restore := buildversion.SetStampForTest("v0.0.2")
	want := answers{userAgent: "nomi/0.0.2", release: rt.Some("v0.0.2"), pinned: "v0.0.2", refused: true}
	if got := read(); got != want {
		t.Errorf("as release v0.0.2: %+v, want %+v", got, want)
	}
	restore()
	if got := read(); got != fromSource {
		t.Errorf("after the release is restored: %+v, want %+v", got, fromSource)
	}
}
