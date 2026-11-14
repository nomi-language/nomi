package rt

import (
	"math"
	"testing"
)

// The unboxed lookups answer what MapLookup over Hash and Equal answers, for
// keys present and absent, -0.0 against 0.0, NaN, and keys of another type
// that share a hash.
func TestMapLookupScalars_AgreeWithMapLookup(t *testing.T) {
	var m Map[any, any]
	for _, k := range []any{int64(0), int64(1000), int64(-1), float64(0), 2.5, math.NaN(), "", "bb", Byte(3)} {
		m = MapPut(m, Hash, Equal, k, k)
	}
	same := func(key any, got any, found bool) {
		t.Helper()
		want, wantFound := MapLookup(m, Hash, Equal, key)
		if found != wantFound || (found && !Equal(got, want)) {
			t.Errorf("lookup of %#v: got %v, %v; MapLookup answers %v, %v", key, got, found, want, wantFound)
		}
	}
	for _, n := range []int64{0, 1000, -1, 3, 2, math.MinInt64} {
		v, ok := MapLookupInt(m, n)
		same(n, v, ok)
	}
	for _, f := range []float64{0, math.Copysign(0, -1), 2.5, math.NaN(), 1000} {
		v, ok := MapLookupFloat(m, f)
		same(f, v, ok)
	}
	for _, s := range []string{"", "bb", "c"} {
		v, ok := MapLookupString(m, s)
		same(s, v, ok)
	}
	if n := testing.AllocsPerRun(100, func() {
		_, _ = MapLookupInt(m, 1000)
		_, _ = MapLookupString(m, "bb")
		_, _ = MapLookupFloat(m, 2.5)
	}); n != 0 {
		t.Errorf("the unboxed lookups allocate %.1f times, want 0", n)
	}
}
