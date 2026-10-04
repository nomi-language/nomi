package vm

// THE rt KERNELS THE MACHINE RENDERS AND COMPARES WITH, AGAINST RECORDED
// ANSWERS: rt.RowText, rt.DisplayText, rt.DebugText (bare and with an impl
// hook), rt.Equal and rt.Hash, on one value of each kind the VM
// holds.
//
// Every expected answer below is written down as a literal, so a change to any kernel's answer fails here. A Debug refusal
// is recorded by the receiver it names.

import (
	"math"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// hookedTypes are the nominal types the planted Debug impl owns.
var hookedTypes = map[string]bool{
	"shapes.Point": true, "ids.Id": true, "shapes.Shape": true, "results.Result": true,
}

func rtHook(v any) (string, bool, error) {
	r, ok := v.(*rt.Record)
	if !ok || !hookedTypes[r.Desc.Name] {
		return "", false, nil
	}
	return "<" + r.Desc.Name + " impl>", true, nil
}

func point(x, y int64) *rt.Record { return rtStruct("shapes.Point", "x", x, "y", y) }

func TestRTKernels_RecordedRenderings(t *testing.T) {
	id := rtDistinct("ids.Id", int64(5))
	for _, tc := range []struct {
		name  string
		v     any
		row   string
		disp  string // "" when it is the row
		debug string // a text, or "refuses: <receiver>"
		// hooked is Debug through rtHook; "" when it is debug.
		hooked  string
		keyable bool
	}{
		{"Int", int64(42), "42", "", "42", "", true},
		{"negative Int", int64(-7), "-7", "", "-7", "", true},
		{"Float", 1.5, "1.5", "", "1.5", "", true},
		{"integral Float", 3.0, "3.0", "", "3.0", "", true},
		{"negative zero", math.Copysign(0, -1), "-0.0", "", "-0.0", "", true},
		{"large Float", 1e21, "1.0e21", "", "1.0e21", "", true},
		{"NaN", math.NaN(), "NaN", "", "NaN", "", true},
		{"Bool", true, "True", "", "True", "", true},
		{"String", "a\"b\\c\td\n", "\"a\"b\\c\td\n\"", "a\"b\\c\td\n", "\"a\\\"b\\\\c\td\n\"", "", true},
		{"Byte", rt.Byte(7), "7", "", "7", "", true},
		{"Bytes", rt.Bytes("\x01\xff"), "<<1, 255>>", "", "<<1, 255>>", "", true},
		{"Unit", rt.Unit{}, "Unit", "", "Unit", "", true},
		{"List", listOf([]any{int64(1), "x"}), "[1, \"x\"]", "", "[1, \"x\"]", "", true},
		{"empty List", (*list)(nil), "[]", "", "[]", "", true},
		{"Vector", rt.VectorOf([]any{int64(1), int64(2)}), "#[1, 2]", "", "#[1, 2]", "", true},
		{"Map in insertion order", mapOf(nil, []rt.MapEntry[any, any]{{Key: "b", Val: int64(1)}, {Key: "a", Val: int64(2)}}),
			"{\"b\" => 1, \"a\" => 2}", "", "{\"b\" => 1, \"a\" => 2}", "", true},
		{"Set", newSet([]any{int64(3), int64(1)}), "#{3, 1}", "", "#{3, 1}", "", true},
		{"tuple", rtTuple(int64(1), "a"), "(1, \"a\")", "", "(1, \"a\")", "", true},
		{"anonymous record", rtStruct("", "y", int64(2), "x", "s"), "{x: \"s\", y: 2}", "", "{x: \"s\", y: 2}", "", true},
		{"Some", some(int64(1)), "Some(1)", "", "Some(1)", "", true},
		{"None", noneValue, "None", "", "None", "", true},
		{"Ok", rtVariant("results.Result", "Ok", "a"), "Ok(\"a\")", "", "Ok(\"a\")", "<results.Result impl>", true},
		{"Err", rtVariant("results.Result", "Err", int64(2)), "Err(2)", "", "Err(2)", "<results.Result impl>", true},
		{"Ordering", rtVariant("comparable.Ordering", "Less"), "Less", "", "refuses: Ordering.Less", "", true},
		{"Outcome", rtVariant("tasks.Outcome", "Completed", rt.Unit{}), "Completed(Unit)", "", "Completed(Unit)", "", true},
		{"distinct", id, "Id(5)", "", "Id(5)", "<ids.Id impl>", true},
		{"marker", rtDistinct("status.Ready", nil), "Ready", "", "Ready", "", true},
		{"named struct", point(1, 2), "Point{x: 1, y: 2}", "", "refuses: Point", "<shapes.Point impl>", true},
		{"user enum", rtVariant("shapes.Shape", "Circle", 1.5), "Circle(1.5)", "", "refuses: Shape.Circle", "<shapes.Shape impl>", true},
		{"Dynamic", rt.Dynamic{Inner: int64(1)}, "<dynamic: int64>", "", "refuses: rt.Dynamic", "refuses: rt.Dynamic", false},
		{"refusal inside a prelude payload", listOf([]any{some(point(1, 2))}),
			"[Some(Point{x: 1, y: 2})]", "", "refuses: Point", "[Some(<shapes.Point impl>)]", true},
		{"hooked elements", listOf([]any{point(1, 2), id}),
			"[Point{x: 1, y: 2}, Id(5)]", "", "refuses: Point", "[<shapes.Point impl>, <ids.Id impl>]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rt.RowText(tc.v); got != tc.row {
				t.Errorf("Row = %q, want %q", got, tc.row)
			}
			disp := tc.disp
			if disp == "" {
				disp = tc.row
			}
			if got := rt.DisplayText(tc.v); got != disp {
				t.Errorf("Display = %q, want %q", got, disp)
			}
			hooked := tc.hooked
			if hooked == "" {
				hooked = tc.debug
			}
			for _, d := range []struct {
				what string
				hook func(any) (string, bool, error)
				want string
			}{{"Debug", nil, tc.debug}, {"hooked Debug", rtHook, hooked}} {
				got, err := rt.DebugText(tc.v, d.hook)
				if receiver, refuses := strings.CutPrefix(d.want, "refuses: "); refuses {
					if err == nil || !strings.Contains(err.Error(), "Debug on an unrepresented receiver ("+receiver+")") {
						t.Errorf("%s = %q, %v; want a refusal naming %s", d.what, got, err, receiver)
					}
					continue
				}
				if err != nil || got != d.want {
					t.Errorf("%s = %q, %v; want %q", d.what, got, err, d.want)
				}
			}
			if tc.keyable && !rt.Equal(tc.v, tc.v) {
				t.Error("not Equal to itself")
			}
		})
	}
}

// TestRTKernels_RecordedEquality: rt.Equal in both directions, and equal
// values hashing alike, across values whose descriptors or construction
// differ.
func TestRTKernels_RecordedEquality(t *testing.T) {
	// One struct, two layouts: a descriptor is a fast path and never identity.
	yx := rt.NewStructDesc("shapes.Point", []rt.FieldSpec{{Name: "y", Type: rt.SlotInt}, {Name: "x", Type: rt.SlotInt}}).Make(int64(2), int64(1))
	for _, tc := range []struct {
		name  string
		a, b  any
		equal bool
	}{
		{"one struct, two cells", point(1, 2), point(1, 2), true},
		{"one struct, two layouts", point(1, 2), yx, true},
		{"struct fields differ", point(1, 2), point(1, 3), false},
		{"Int against Float", int64(1), 1.0, false},
		{"NaN", math.NaN(), math.NaN(), true},
		{"signed zeros", 0.0, math.Copysign(0, -1), true},
		{"Map in two insertion orders",
			mapOf(nil, []rt.MapEntry[any, any]{{Key: "b", Val: int64(1)}, {Key: "a", Val: int64(2)}}),
			mapOf(nil, []rt.MapEntry[any, any]{{Key: "a", Val: int64(2)}, {Key: "b", Val: int64(1)}}), true},
		{"Set in two insertion orders", newSet([]any{int64(1), int64(2)}), newSet([]any{int64(2), int64(1)}), true},
		{"Some over two descriptors", some(int64(1)), rtVariant("maybe.Maybe", "Some", int64(1)), true},
		{"same short name, two modules", rtStruct("a.Point", "x", int64(1)), rtStruct("b.Point", "x", int64(1)), true},
		{"anonymous record against a struct", rtStruct("", "x", int64(1)), rtStruct("a.Point", "x", int64(1)), false},
		{"empty List against empty Vector", (*list)(nil), rt.VectorOf[any](nil), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rt.Equal(tc.a, tc.b); got != tc.equal {
				t.Errorf("Equal(a, b) = %v, want %v", got, tc.equal)
			}
			if got := rt.Equal(tc.b, tc.a); got != tc.equal {
				t.Errorf("Equal(b, a) = %v, want %v", got, tc.equal)
			}
			if got := rt.Hash(tc.a) == rt.Hash(tc.b); got != tc.equal {
				t.Errorf("hashes alike = %v, want %v", got, tc.equal)
			}
		})
	}
}
