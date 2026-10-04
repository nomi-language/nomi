package rt

import "testing"

func TestCodepointCellsDecodeScalarsAndInvalidUTF8(t *testing.T) {
	type boxed struct{ scalar int64 }
	type node ListCell[boxed, node]
	for _, tc := range []struct {
		s    string
		want []int64
	}{
		{"", nil},
		{"\x00é\U0010ffff", []int64{0, 233, 1114111}},
		{"e\u0301👩‍💻", []int64{101, 769, 128105, 8205, 128187}},
		{"a\xff\xc0\xaf\xe2\x82", []int64{97, 65533, 65533, 65533, 65533, 65533}},
	} {
		xs := StringToCodepointCells[boxed, node](tc.s, func(cp Codepoint) boxed { return boxed{int64(cp)} })
		native := StringToCodepoints(tc.s)
		if ListCellCount[boxed](xs) != int64(len(tc.want)) {
			t.Fatalf("%q: length", tc.s)
		}
		for _, want := range tc.want {
			if xs == nil || native == nil || xs.Head.scalar != want || int64(native.Head) != want {
				t.Fatalf("%q: expected scalar %d", tc.s, want)
			}
			xs, native = xs.Tail, native.Tail
		}
		if xs != nil || native != nil {
			t.Fatalf("%q: extra scalars", tc.s)
		}
	}
}
