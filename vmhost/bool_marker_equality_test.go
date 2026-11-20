package vmhost_test

import "testing"

// `Bool.False(x)` binds x to std/bool's host type False, a marker of one
// value, whose derived Equatable the machine computes by shape: `x == x`
// and `x != x` over it run, and so do True's.
func TestRun_BoolMarkerPayloadsCompareByShape(t *testing.T) {
	got := runOutput(t, `fn same(b: Bool) -> Bool
    case b
        Bool.False(x) -> x == x
        Bool.True(y) -> y != y
    end
end

fn main()
    dbg same(False)
    dbg same(True)
end
`)
	want := "dbg line 9: same(False) = True\ndbg line 10: same(True) = False\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
