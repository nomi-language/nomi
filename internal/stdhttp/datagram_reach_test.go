package stdhttp

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/osreach"
	"github.com/nomi-language/nomi/rt"
)

// A Unix datagram socket bound or dialed on a system that has none
// (Windows) is refused with Unsupported, and its class still counts as
// reaching the operating system, as it does where the socket opens. The
// stdlib reference marks udp.bind_unix's prompt by that count
// (cmd/nomi-docgen's promptClassifier), so the page is the same whichever
// system built it. The test answers as Windows does on any system.
func TestUDPOpen_ARefusedUnixSocketReachesItsClass(t *testing.T) {
	if !hasSockets {
		t.Skip("this build has no sockets")
	}
	saved := hasUnixDatagrams
	hasUnixDatagrams = false
	t.Cleanup(func() { hasUnixDatagrams = saved })
	path := filepath.Join(t.TempDir(), "a.sock")
	for _, tc := range []struct {
		op, class string
		open      func() rt.Result[rt.NetConn, *rt.List[string]]
	}{
		{"bind", "listen", func() rt.Result[rt.NetConn, *rt.List[string]] { return UDPBind(nil, "unixgram", path, false, false) }},
		{"dial", "connect", func() rt.Result[rt.NetConn, *rt.List[string]] { return UDPDial(nil, "unixgram", path) }},
	} {
		before := osreach.Count(tc.class)
		r := tc.open()
		want := []string{"Unsupported", tc.op, "Windows has no Unix datagram sockets"}
		if r.Tag != 2 || !slices.Equal(strsOf(r.Err), want) {
			t.Errorf("%s of a Unix datagram socket answered tag %d %v, want Err %v", tc.op, r.Tag, strsOf(r.Err), want)
		}
		if got := osreach.Count(tc.class) - before; got != 1 {
			t.Errorf("%s of a Unix datagram socket counted %d %s calls, want 1", tc.op, got, tc.class)
		}
	}
}
