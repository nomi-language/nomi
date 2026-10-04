package irbuild

import (
	"os"
	"strings"
	"testing"
)

// The Display/Debug surfaces: `Display.to_string`, `${x}`, `Debug.inspect`
// (including the universal `impl Debug for T` that
// `analysis.SynthesizeUniversalDebug` appends for every declared type) and the
// assertion `values:` row. This file pins the strings each one answers for a
// named type.
//
// # The two inputs that make the four renderings distinguishable
//
// A non-alphabetical struct and a hand-written `impl Debug`. With
// `Point {x, y}`, declaration order and sorted order are the same string, so
// wiring `Debug.inspect` to the structural inspector, or the `values:` row to
// the Display renderer, would pass. With `Rev {zeta, alpha}` they are
// `Rev{zeta: 1, alpha: 2}` and `Rev{alpha: 2, zeta: 1}` and only one wiring
// passes.
//
// # Every string is spelled out absolutely
//
// Both the report layout and `rt.InspectStruct`'s sort are shared code, and a
// change that repoints a surface at the wrong renderer changes only the
// string that surface prints. The pinned transcript below catches either.

// TestDebugDisplay_FourSurfacesArePinnedAbsolutely spells out the transcript.
//
// Every line is load-bearing and the field ORDER lines are the point:
//
//	line 5  Debug.inspect(r) = Rev{zeta: 1, alpha: 2}     DECLARATION order
//	line 7  Debug.inspect(p) = Plain{zeta: 1, alpha: 2}   DECLARATION order
//	          the `values:` rows = Rev{alpha: 2, zeta: 1}   SORTED
//
// and the precedence lines are the other point:
//
//	line 6  Debug.inspect(c) = CUSTOM-DEBUG               hand impl WINS
//	          c's `values:` row = CUSTOM-DEBUG               hand impl WINS there too
//
// The opaque pair is the third: Debug shows `<opaque Meters>` and the structural
// walk shows `Meters(7)`, so Debug renders LESS there — the one row where the
// two disagree in that direction.
//
// The `Bool` rows are the fourth point. `Bool`'s two variants are zero-width
// `pub host type`s, so line 9 renders a value the builder stores nowhere and
// line 12 renders one it materialized from a tag. Line 11 is the container
// composing through the same leaf. Their `values:` rows are `True` and
// `[True, False]`, the one place in this file where a Debug rendering and a
// row rendering agree; it is pinned so that agreement is checked rather than
// assumed.
func TestDebugDisplay_FourSurfacesArePinnedAbsolutely(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("debug_display_surfaces.nomi")
	const f = "testdata/debug_display_surfaces.nomi"

	// Surface 1 and 2: the user's `impl Display`, reached by name and by
	// interpolation. Both spellings answer the same string, which is what makes
	// them ONE renderer rather than two.
	want := "1 Display.to_string(r) = REV-DISPLAY\n" +
		"2 interpolated r       = REV-DISPLAY\n" +
		"3 Display.to_string(c) = CUSTOM-DISPLAY\n" +
		"4 interpolated c       = CUSTOM-DISPLAY\n" +
		// Surface 3: Debug dispatch. DECLARATION order for a synthesized body,
		// the hand-written impl for the type that has one, and the degenerate
		// name-only body for the opaque type.
		"5 Debug.inspect(r)     = Rev{zeta: 1, alpha: 2}\n" +
		"6 Debug.inspect(c)     = CUSTOM-DEBUG\n" +
		"7 Debug.inspect(p)     = Plain{zeta: 1, alpha: 2}\n" +
		"8 Debug.inspect(m)     = <opaque Meters>\n" +
		// The `host type` rows: a Bool, a Bool through a binding, a container of
		// them, and the singleton materialized out of the tag by a `case` arm.
		"9 Debug.inspect(True)  = True\n" +
		"10 Debug.inspect(b)    = False\n" +
		"11 Debug.inspect(list) = [True, False]\n" +
		"12 materialized payload = False\n" +
		"ok " + f + " :: the four renderings of one value\n" +
		// Surface 4: the assertion `values:` row. SORTED, blind to `impl
		// Display`, and rendered by a hand-written `impl Debug`.
		"FAIL " + f + " :: values row for a struct with an impl Display\n" +
		"  line 193: assertion failed\n" +
		"    assert no_rev(r)\n" +
		"    values:\n" +
		"      r\n" +
		"        = Rev{alpha: 2, zeta: 1}\n" +
		"FAIL " + f + " :: values row honours a hand-written impl Debug\n" +
		"  line 198: assertion failed\n" +
		"    assert no_custom(c)\n" +
		"    values:\n" +
		"      c\n" +
		"        = CUSTOM-DEBUG\n" +
		"FAIL " + f + " :: values row for a struct with no impls at all\n" +
		"  line 203: assertion failed\n" +
		"    assert no_plain(p)\n" +
		"    values:\n" +
		"      p\n" +
		"        = Plain{alpha: 2, zeta: 1}\n" +
		"FAIL " + f + " :: values row for a Bool\n" +
		"  line 208: assertion failed\n" +
		"    assert no_bool(b)\n" +
		"    values:\n" +
		"      b\n" +
		"        = True\n" +
		"FAIL " + f + " :: values row for a list of Bool\n" +
		"  line 213: assertion failed\n" +
		"    assert no_bools(bs)\n" +
		"    values:\n" +
		"      bs\n" +
		"        = [True, False]\n" +
		"FAIL " + f + " :: values row shows an opaque payload Debug hides\n" +
		"  line 218: assertion failed\n" +
		"    assert no_meters(m)\n" +
		"    values:\n" +
		"      m\n" +
		"        = Meters(7)\n" +
		"test result: FAILED. 1 passed, 6 failed\n"

	if got := vmReference(path); got.stdout != want || got.exit != 1 {
		t.Fatalf("VM transcript is not what this fixture pins:\n--- got ---\n%s\n--- want ---\n%s\nexit=%d",
			got.stdout, want, got.exit)
	}
}

// TestDebugDisplay_DeclarationOrderIsNotSortedOrder is the property the two
// pinned strings encode, asserted as a property so a future edit that
// alphabetizes the fixture's fields fails HERE with a reason rather than
// silently making both pins vacuous.
func TestDebugDisplay_DeclarationOrderIsNotSortedOrder(t *testing.T) {
	// Read the fixture's own field order rather than restating it, so the two
	// cannot drift apart.
	src := mustReadFixture(t, "debug_display_surfaces.nomi")
	for _, name := range []string{"Rev", "Custom", "Plain"} {
		fields := structFieldOrder(src, name)
		if len(fields) < 2 {
			t.Fatalf("struct %s must declare at least two fields for the order "+
				"assertion to mean anything; got %v", name, fields)
		}
		if sortedStrings(fields) {
			t.Fatalf("struct %s declares its fields in ALPHABETICAL order (%v), so "+
				"this fixture cannot distinguish `Debug.inspect`'s declaration "+
				"order from the `values:` row's sorted order — which is the only thing "+
				"it exists to distinguish", name, fields)
		}
	}
}

// --- helpers ------------------------------------------------------------------

// mustReadFixture reads a fixture's own source, so a property asserted about it
// cannot drift from the file it describes.
func mustReadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(fixture(name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(b)
}

// structFieldOrder is the field names of `struct <name>` in DECLARATION order.
//
// A line scanner rather than a parse, deliberately: this reads the source the
// way a human checking the fixture would, so it cannot inherit a bug from the
// same front end the assertion is about.
func structFieldOrder(src, name string) []string {
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) != "struct "+name+" {" {
			continue
		}
		var out []string
		for _, f := range lines[i+1:] {
			f = strings.TrimSpace(f)
			if f == "}" {
				return out
			}
			if fld, _, ok := strings.Cut(f, ":"); ok && fld != "" {
				out = append(out, strings.TrimSpace(fld))
			}
		}
		return out
	}
	return nil
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
