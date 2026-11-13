package irbuild

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestExistentialDebugImplsAreInSourceOrder lowers one program several times
// in one process and requires the same bytes each time. Its derived Debug of
// a struct holding an interface value names the Debug body of every type
// the checker saw enter the interface (checkedErasures), and the order of
// those names is encoded: read in the checker record's map order, it changed
// from one lowering to the next, and so did the std bundle's listed section
// (std/mail/testing's `Opened`).
func TestExistentialDebugImplsAreInSourceOrder(t *testing.T) {
	const shapes = 8
	var b strings.Builder
	b.WriteString("import std::io\n\ninterface Shape\n    fn area(s: self) -> Int\nend\n")
	for i := range shapes {
		fmt.Fprintf(&b, "\nstruct S%d\n    x: Int\nend\n\nimpl Shape for S%d\n    fn area(s: S%d) -> Int\n        s.x\n    end\nend\n", i, i, i)
	}
	b.WriteString("\nstruct Holder\n    shape: Shape\nend\n\nfn main()\n")
	for i := range shapes {
		fmt.Fprintf(&b, "    io.print(Debug.inspect(Holder{shape: S%d{x: %d}}))\n", i, i)
	}
	b.WriteString("end\n")
	src := b.String()

	encode := func() []byte {
		res := lowerIROnly(t, src)
		var out []byte
		for _, m := range res.IR {
			data, err := ir.EncodeObject(m)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, data...)
		}
		return out
	}
	first := encode()
	if !bytes.Contains(first, []byte("S7.inspect")) {
		t.Fatalf("the lowering names no implementer's Debug body, so the order compared nothing")
	}
	for range 4 {
		if !bytes.Equal(encode(), first) {
			t.Fatal("lowering the same program twice in one process gave different bytes")
		}
	}
}
