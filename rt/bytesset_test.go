package rt

import "testing"

// TestBytesIndexOfAny_EveryByteIsItsOwnCandidate holds BytesIndexOfAny and
// BytesLastIndexOfAny to a search that tests each byte against each byte of
// the set, over every byte value in the data and sets that reach each of the
// bit set's four words, high bytes and byte values that are not UTF-8
// included.
func TestBytesIndexOfAny_EveryByteIsItsOwnCandidate(t *testing.T) {
	var all []byte
	for b := range 256 {
		all = append(all, byte(b))
	}
	data := Bytes(append(append([]byte{}, all...), all...))
	sets := []Bytes{"", "\x00", "\xff", "\x80\xc3", "a\x3f\x40\x7f", "\xbf\xc0", "\x00\x40\x80\xc0", "zz", Bytes(all)}
	naive := func(set Bytes, i int) bool {
		for k := 0; k < len(set); k++ {
			if set[k] == data[i] {
				return true
			}
		}
		return false
	}
	cases := 0
	for _, set := range sets {
		for _, from := range []int64{-1, 0, 1, 63, 64, 200, 256, 511, 512, 600} {
			want := None[int64]()
			for i := max(0, int(from)); i < len(data); i++ {
				if naive(set, i) {
					want = Some(int64(i))
					break
				}
			}
			if got := BytesIndexOfAny(data, set, SmallInt(from)); got != want {
				t.Errorf("index_of_any(set %q, from %d) = %v, want %v", set, from, got, want)
			}
			cases++
		}
		want := None[int64]()
		for i := len(data) - 1; i >= 0; i-- {
			if naive(set, i) {
				want = Some(int64(i))
				break
			}
		}
		if got := BytesLastIndexOfAny(data, set); got != want {
			t.Errorf("last_index_of_any(set %q) = %v, want %v", set, got, want)
		}
		cases++
	}
	t.Logf("%d searches", cases)
}
