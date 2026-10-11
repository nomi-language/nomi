package semtokens_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/semtokens"
)

// UTF16 recounts each token's column and length in UTF-16 code units of its
// own line, for text before the token and inside its span.
func TestUTF16_RecountsColumnsAndLengths(t *testing.T) {
	code := "ascii\n" +
		"°x\n" + // ° is 2 bytes, 1 unit
		"é x\n" + // precomposed: 2 bytes, 1 unit
		"é x\n" + // combining: 3 bytes, 2 units
		"👍🏽 x\n" + // two surrogate pairs: 8 bytes, 4 units
		"日本 x\n" + // 3 bytes, 1 unit each
		"a日本b\n" // a span holding CJK
	cases := []struct {
		name string
		in   semtokens.Token
		col  int
		len  int
	}{
		{"ascii unchanged", semtokens.Token{Line: 1, Col: 2, Length: 3}, 2, 3},
		{"after degree", semtokens.Token{Line: 2, Col: 3, Length: 1}, 2, 1},
		{"after precomposed e-acute", semtokens.Token{Line: 3, Col: 4, Length: 1}, 3, 1},
		{"after combining acute", semtokens.Token{Line: 4, Col: 5, Length: 1}, 4, 1},
		{"after emoji with skin tone", semtokens.Token{Line: 5, Col: 10, Length: 1}, 6, 1},
		{"after CJK", semtokens.Token{Line: 6, Col: 8, Length: 1}, 4, 1},
		{"CJK inside the span", semtokens.Token{Line: 7, Col: 1, Length: 8}, 1, 4},
		{"line out of range unchanged", semtokens.Token{Line: 99, Col: 5, Length: 2}, 5, 2},
	}
	in := make([]semtokens.Token, len(cases))
	for i, c := range cases {
		in[i] = c.in
	}
	out := semtokens.UTF16(code, in)
	for i, c := range cases {
		if out[i].Col != c.col || out[i].Length != c.len || out[i].Line != c.in.Line {
			t.Errorf("%s: %+v became %+v, want col %d length %d", c.name, c.in, out[i], c.col, c.len)
		}
	}
}
