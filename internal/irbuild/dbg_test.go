package irbuild

import (
	"strings"
	"testing"
)

// `dbg`, and why its output is pinned as absolute text.
//
// Two of `dbg`'s three inputs are shared code:
//
//   - the operand's SOURCE TEXT is `format.RenderNode`, called at build time
//     with the result baked in, so a change to the formatter changes every
//     `dbg` line in the language.
//   - the LAYOUT is `rt.Dbg`, with three shapes.
//
// So the transcripts are pinned, and the pin is what makes a shape regression
// visible. The fixtures live in testdata rather than the corpus because the
// corpus barely uses `dbg`, and two of the three layout shapes have no reacher
// under `tests/`.

// TestDbg_MultiLineShapeIsExercised is the anti-vacuity guard on the shape that
// has no other reacher.
//
// The multi-line layout is selected by the formatter wrapping the operand,
// which depends on the rendered width, so shortening the two wide literals in
// the fixture would silently move both of them to the single-line arm and
// leave the arm untested. This fails with a reason instead.
func TestDbg_MultiLineShapeIsExercised(t *testing.T) {
	got := vmReference(fixture("dbg_shapes.nomi"))
	if !strings.Contains(got.stdout, "dbg line 61:\n  [\n") {
		t.Fatalf("the multi-line LIST operand does not wrap, so that arm of "+
			"rt.Dbg is untested — lengthen the literals in dbg_shapes.nomi\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "dbg line 74:\n  Rev{\n") {
		t.Fatalf("the multi-line STRUCT operand does not wrap, so that arm of "+
			"rt.Dbg rests on one operand type\n%s", got.stdout)
	}
	// And the single-line arm has to stay reached, for the same reason in the
	// other direction: a formatter change that stopped wrapping ANYTHING would
	// satisfy neither check above, and one that started wrapping everything
	// would satisfy both and leave the single-line arm dark.
	if !strings.Contains(got.stdout, "dbg line 48: 41 + 1 = 42\n") {
		t.Fatalf("the single-line shape is not reached\n%s", got.stdout)
	}
}

// TestDbg_DeclarationOrderIsNotSortedOrder is the anti-vacuity guard on
// dbg_surfaces.nomi's structs, the same one debugdisplay_test.go carries for its
// own fixture and for the same reason: alphabetical fields make declaration
// order and sorted order one string, and every pair assertion above would then
// pass with `dbg` wired to the wrong renderer.
func TestDbg_DeclarationOrderIsNotSortedOrder(t *testing.T) {
	src := mustReadFixture(t, "dbg_surfaces.nomi")
	for _, name := range []string{"Rev", "Custom"} {
		fields := structFieldOrder(src, name)
		if len(fields) < 2 {
			t.Fatalf("struct %s in dbg_surfaces.nomi does not have two fields to order", name)
		}
		if sortedStrings(fields) {
			t.Fatalf("struct %s declares its fields alphabetically (%v), so "+
				"declaration order and sorted order are the same string and every "+
				"dbg/values: pair over it is vacuous", name, fields)
		}
	}
}
