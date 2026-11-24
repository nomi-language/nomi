package rt

import "testing"

// TestBytesIndexOfAny_EveryByteIsItsOwnCandidate holds BytesIndexOfAny,
// BytesIndexOfNone and their last forms to a search that tests each byte
// against each byte of the set, over every byte value in the data, sets
// that reach each of the bit set's four words, high bytes and byte values
// that are not UTF-8 included, and windows whose bounds fall before, inside
// and past the data, a stop before its start among them.
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
			for _, stop := range []int64{-1, 0, 64, 65, 300, 512, 1000} {
				for _, in := range []bool{true, false} {
					want := None[int64]()
					for i := max(0, int(from)); i < min(len(data), int(stop)); i++ {
						if naive(set, i) == in {
							want = Some(int64(i))
							break
						}
					}
					search, name := BytesIndexOfAny, "index_of_any"
					if !in {
						search, name = BytesIndexOfNone, "index_of_none"
					}
					if got := search(data, set, SmallInt(from), SmallInt(stop)); got != want {
						t.Errorf("%s(set %q, from %d, stop %d) = %v, want %v", name, set, from, stop, got, want)
					}
					cases++
				}
			}
		}
		for _, in := range []bool{true, false} {
			want := None[int64]()
			for i := len(data) - 1; i >= 0; i-- {
				if naive(set, i) == in {
					want = Some(int64(i))
					break
				}
			}
			search, name := BytesLastIndexOfAny, "last_index_of_any"
			if !in {
				search, name = BytesLastIndexOfNone, "last_index_of_none"
			}
			if got := search(data, set); got != want {
				t.Errorf("%s(set %q) = %v, want %v", name, set, got, want)
			}
			cases++
		}
	}
	t.Logf("%d searches", cases)
}
