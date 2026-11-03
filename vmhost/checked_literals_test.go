package vmhost

import (
	"testing"

	"github.com/nomi-language/nomi/internal/irbuild"
)

// A LiteralError's static index and code-point offset land on the source
// character they name, through slots, escapes and a triple-quoted body's
// stripped indentation; an offset at a static's end lands on the next slot's
// `${`.
func TestCheckedLiteralPosMapsOffsetsToSource(t *testing.T) {
	src := "fn main() {\n" +
		"    a = Shout\"it is ${n} oh no! times\"\n" +
		"    b = Shout\"x\\ty! ${n}\"\n" +
		"    c = Shout\"\"\"\n" +
		"        first ${n}\n" +
		"        second!\n" +
		"        \"\"\"\n" +
		"}\n"
	cases := []struct {
		name             string
		site             irbuild.LiteralSite
		fragment, offset int
		line, col        int
	}{
		{"after a slot", irbuild.LiteralSite{Line: 2, Col: 9, Tag: "Shout", Statics: []string{"it is ", " oh no! times"}, Slots: []irbuild.SlotPos{{Line: 2, Col: 23}}}, 1, 6, 2, 31},
		{"at a slot", irbuild.LiteralSite{Line: 2, Col: 9, Tag: "Shout", Statics: []string{"it is ", " oh no! times"}, Slots: []irbuild.SlotPos{{Line: 2, Col: 23}}}, 0, 6, 2, 21},
		{"past an escape", irbuild.LiteralSite{Line: 3, Col: 9, Tag: "Shout", Statics: []string{"x\ty! ", ""}, Slots: []irbuild.SlotPos{{Line: 3, Col: 23}}}, 0, 3, 3, 19},
		{"a triple-quoted body's second line", irbuild.LiteralSite{Line: 4, Col: 9, Tag: "Shout", Statics: []string{"first ", "\nsecond!"}, Slots: []irbuild.SlotPos{{Line: 5, Col: 17}}}, 1, 7, 6, 15},
		{"a static that is not there", irbuild.LiteralSite{Line: 2, Col: 9, Tag: "Shout", Statics: []string{"it is ", " oh no! times"}}, 5, 0, 2, 9},
	}
	for _, c := range cases {
		line, col := checkedLiteralPos(src, c.site, c.fragment, c.offset)
		if line != c.line || col != c.col {
			t.Errorf("%s: at %d:%d, want %d:%d", c.name, line, col, c.line, c.col)
		}
	}
}
