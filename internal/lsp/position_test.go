package lsp

import "testing"

func TestUTF16ToByteCol(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		line      uint32
		character uint32
		want      int // 1-based byte column
	}{
		{"ascii start", "abcd", 0, 0, 1},
		{"ascii middle", "abcd", 0, 3, 4}, // ASCII fast path == character+1
		// `—` (U+2014) is 3 bytes / 1 UTF-16 unit. Columns after it run ahead.
		{"before em-dash", "ab—cd", 0, 2, 3}, // the em-dash itself: byte col 3
		{"after em-dash", "ab—cd", 0, 3, 6},  // `c`: byte 5 (0-based) → col 6, not 4
		{"end after em-dash", "ab—cd", 0, 4, 7},
		// 😀 (U+1F600) is 4 bytes / 2 UTF-16 units (a surrogate pair).
		{"after astral", "a😀b", 0, 3, 6}, // `b`: byte 5 (0-based) → col 6
		// Second line, with a non-ASCII char earlier on it.
		{"second line after em-dash", "first\nx — y", 1, 4, 7}, // `y`: byte 6 (two spaces) → col 7
		{"out-of-range line falls back", "only", 5, 7, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := utf16ToByteCol(tc.content, tc.line, tc.character); got != tc.want {
				t.Errorf("utf16ToByteCol(%q, %d, %d) = %d, want %d", tc.content, tc.line, tc.character, got, tc.want)
			}
		})
	}
}

func TestByteToUTF16Col(t *testing.T) {
	cases := []struct {
		name    string
		content string
		line    uint32
		byteCol uint32 // 0-based
		want    uint32 // 0-based UTF-16
	}{
		{"ascii", "abcd", 0, 3, 3},
		// `—` (U+2014) is 3 bytes / 1 UTF-16 unit: `c` is byte 5 → UTF-16 col 3.
		{"after em-dash", "ab—cd", 0, 5, 3},
		// 😀 (U+1F600) is 4 bytes / 2 UTF-16 units: `b` is byte 5 → UTF-16 col 3.
		{"after astral", "a😀b", 0, 5, 3},
		{"out-of-range line falls back", "only", 5, 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := byteToUTF16Col(tc.content, tc.line, tc.byteCol); got != tc.want {
				t.Errorf("byteToUTF16Col(%q, %d, %d) = %d, want %d", tc.content, tc.line, tc.byteCol, got, tc.want)
			}
		})
	}
}
