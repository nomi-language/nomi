package irbuild

import (
	"testing"
)

// `Debug.inspect` on a receiver whose concrete type is chosen at RUN TIME.
//
// # Why this needs a fixture that READS THE OUTPUT
//
// Getting Debug wrong is a wrong ANSWER, not a build error. Every plausible
// mis-wiring of an erased Debug call — the table pointing at inspect.go's
// structural inspector, the table ignoring a hand-written impl, an opaque type
// leaking its payload — produces a program that compiles and runs and prints a
// different string. `0 COMPILE-FAIL` says nothing about any of them.
//
// Four renderings are pinned APART here and none may be collapsed into another:
//
//	Debug.inspect        declaration order, honours a hand-written impl
//	assert values: row   rt.RowText / rt.InspectStruct — NAME sort,
//	                     non-overridable
//	Display / ${x}       a third renderer entirely
//	opaque under Debug   shows LESS than the structural walk
//
// `debug_display_surfaces.nomi` pins those four at a CONCRETE receiver.
// This fixture asks the same four through the ERASED route, which is the only
// place a dispatch table can answer differently from the direct call.
//
// # The negative control, and what it is for
//
// Every value is inspected TWICE — once through the erased route and once
// directly — and the two columns must agree string for string. That is not
// redundancy: `stdiface.go` declined `Debug` a dispatch table precisely because
// "its call surface already lowers, so a table would change a working path", and
// the direct column is the only thing in this fixture that can see that
// regression. A fixture holding only the erased case cannot see the working case
// break.
//
// It is NOT sufficient on its own, for the reason debugdisplay_test.go states:
// the two columns agreeing proves the table and the direct call answer the same
// thing, not that either answers the RIGHT thing. So every string is also
// spelled out absolutely below.

// TestDebugErased_IsPinnedAbsolutely spells out the transcript.
//
// The load-bearing lines, and what each one would catch:
//
//	1 / 8    Rev{zeta: 1, alpha: 2}  DECLARATION order. Sorted order is
//	         `Rev{alpha: 2, zeta: 1}` and is what the values: row answers, so a
//	         table wired to inspect.go's inspector fails here and only here.
//	2 / 7 / 9  CUSTOM-DEBUG          a hand-written impl WINS through the table.
//	         A structural renderer answers `Custom{zeta: 3, alpha: 4}`.
//	3 / 10   <opaque Meters>         Debug shows LESS. The structural walk
//	         answers `Meters(7)`.
//	5 / 12   "hi"                    QUOTED. `Display.to_string("hi")` is `hi`,
//	         so this separates Debug's leaf from Display's at a scalar.
//	values:  Rev{alpha: 2, zeta: 1}  SORTED, and blind to both impls above.
func TestDebugErased_IsPinnedAbsolutely(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("debug_erased.nomi")
	const f = "testdata/debug_erased.nomi"

	want := // The BARE TYPE PARAMETER column.
		"1 param  Rev    = Rev{zeta: 1, alpha: 2}\n" +
			"2 param  Custom = CUSTOM-DEBUG\n" +
			"3 param  Meters = <opaque Meters>\n" +
			"4 param  Int    = 42\n" +
			"5 param  String = \"hi\"\n" +
			// The EXISTENTIAL column.
			"6 iface  Rev    = Rev{zeta: 1, alpha: 2}\n" +
			"7 iface  Custom = CUSTOM-DEBUG\n" +
			// THE NEGATIVE CONTROL: the direct route, unchanged.
			"8 direct Rev    = Rev{zeta: 1, alpha: 2}\n" +
			"9 direct Custom = CUSTOM-DEBUG\n" +
			"10 direct Meters = <opaque Meters>\n" +
			"11 direct Int    = 42\n" +
			"12 direct String = \"hi\"\n" +
			"ok " + f + " :: the four renderings of one value survive erasure\n" +
			// The assertion `values:` row: a different renderer, SORTED.
			"FAIL " + f + " :: the values row is a different renderer from Debug\n" +
			"  line 116: assertion failed\n" +
			"    assert no_rev(r)\n" +
			"    values:\n" +
			"      r\n" +
			"        = Rev{alpha: 2, zeta: 1}\n" +
			"test result: FAILED. 1 passed, 1 failed\n"

	if got := vmReference(path); got.stdout != want || got.exit != 1 {
		t.Errorf("VM transcript drifted (exit %d):\n--- got ---\n%s\n--- want ---\n%s",
			got.exit, got.stdout, want)
	}
}
