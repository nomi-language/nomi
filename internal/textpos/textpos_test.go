package textpos

import (
	"testing"
	"unicode/utf16"
)

// Each case is a line, a byte column and the UTF-16 column it is at. The
// conversion holds both ways at every character boundary.
func TestColumnsConvertBothWays(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		byteCol, u16 int
	}{
		{"ascii", "abcd", 3, 3},
		{"start", "°x", 0, 0},
		// ° (U+00B0) is 2 bytes, 1 unit.
		{"after degree", `"${x} °${unit}"`, 10, 9},
		// é precomposed (U+00E9) is 2 bytes, 1 unit.
		{"after precomposed e-acute", "café x", 6, 5},
		// e plus a combining acute (U+0301): 1 + 2 bytes, 2 units.
		{"after combining acute", "café x", 7, 6},
		// 👍🏽 is U+1F44D U+1F3FD: 4 + 4 bytes, 2 + 2 units.
		{"after emoji with skin tone", "👍🏽 x", 9, 5},
		{"between emoji and its modifier", "👍🏽 x", 4, 2},
		// 日本 (U+65E5 U+672C) are 3 bytes, 1 unit each.
		{"after CJK", "日本 x", 7, 3},
		{"inside CJK run", "日本 x", 3, 1},
		{"end of line", "a°", 3, 2},
		// An invalid byte counts one unit, as the U+FFFD an editor shows.
		{"after invalid byte", "a\xffb", 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UTF16Col(tc.line, tc.byteCol); got != tc.u16 {
				t.Errorf("UTF16Col(%q, %d) = %d, want %d", tc.line, tc.byteCol, got, tc.u16)
			}
			if got := ByteCol(tc.line, tc.u16); got != tc.byteCol {
				t.Errorf("ByteCol(%q, %d) = %d, want %d", tc.line, tc.u16, got, tc.byteCol)
			}
		})
	}
}

func TestColumnsInsideACharacterCountIt(t *testing.T) {
	// Byte 1 is inside °, so the column after it is reported.
	if got := UTF16Col("°x", 1); got != 1 {
		t.Errorf("UTF16Col inside ° = %d, want 1", got)
	}
	// Unit 1 is between the halves of 👍's surrogate pair.
	if got := ByteCol("👍x", 1); got != 4 {
		t.Errorf("ByteCol inside a surrogate pair = %d, want 4", got)
	}
}

func TestColumnsPastTheEndStopAtIt(t *testing.T) {
	if got := UTF16Col("a°", 99); got != 2 {
		t.Errorf("UTF16Col past the end = %d, want 2", got)
	}
	if got := ByteCol("a°", 99); got != 3 {
		t.Errorf("ByteCol past the end = %d, want 3", got)
	}
}

// UTF16Len agrees with Go's own UTF-16 encoding.
func TestUTF16LenAgreesWithEncoding(t *testing.T) {
	for _, s := range []string{"", "abc", "°", "café", "café", "👍🏽", "日本語", "x 👍🏽 日 é"} {
		if got, want := UTF16Len(s), len(utf16.Encode([]rune(s))); got != want {
			t.Errorf("UTF16Len(%q) = %d, want %d", s, got, want)
		}
	}
}
