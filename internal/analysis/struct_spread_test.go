package analysis

import "testing"

// Struct update by spread: `{..base, field: value}`.
//
// THE CHECKER ADDS NO VALIDATOR. `checkStructUpdateLit` reads the head, then
// hands the remaining fields to `checkStructLitAgainstStruct` — the one place
// a struct literal's field names are validated and its values admitted. The
// tests below are arranged around the rules that change there and the ones
// that must NOT: unknown names still refuse, wrong values still refuse
// through `argMatchesParam`, and MISSING fields stop being an error because
// the spread supplies every one.
//
// The DEEP rule — a bare brace at a struct-typed field is a patch — landed
// after this file and is tested in struct_spread_deep_test.go. Every row here
// is unchanged by it, which is the point: they are the shapes a value-taking
// position still owns.

const structSpreadDecls = `struct Point {
  x: Int
  y: Int
}
struct Weight {
  kg: Float
}
interface Speaker {
  fn say(s: self): String
}
struct Loud {
  n: Int
}
impl Speaker for Loud {
  fn say(_s: Loud): String {
    "loud"
  }
}
struct Quiet {
  n: Int
}
impl Speaker for Quiet {
  fn say(_s: Quiet): String {
    "quiet"
  }
}
struct Stage {
  who: Speaker
  seats: Int
}
struct Box<T> {
  item: T
  tag: String
}
`

func TestCheck_StructSpread_Refuses(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "an unknown field name",
			body: "  p = Point{x: 1, y: 2}\n  _a = {..p, z: 3}\n",
			want: "Point has no field 'z'",
		},
		{
			name: "a field value of the wrong type",
			body: "  p = Point{x: 1, y: 2}\n  _a = {..p, x: \"s\"}\n",
			want: "field 'x' of Point: expected Int, got String",
		},
		{
			// The Int-at-a-Float row from the ordinary literal's table, in
			// the spread spelling. Same validator, so the same verdict.
			name: "an Int in a field declared Float",
			body: "  w = Weight{kg: 1.0}\n  _a = {..w, kg: 1}\n",
			want: "field 'kg' of Weight: expected Float, got Int",
		},
		{
			name: "a head that is not a struct",
			body: "  n = 5\n  _a = {..n, x: 1}\n",
			want: "struct spread `..` requires a struct, got Int",
		},
		{
			// AN INTERFACE-TYPED FIELD TAKES VALUES ONLY, and it is the
			// one struct-shaped-looking position the deep patch does not
			// reach. You cannot patch through an interface that does not
			// name its fields, so `patchTargetStruct` returns nil for one
			// and the value rule applies — exactly where
			// `deepPartialMatches` stops for `Partial<T>`.
			//
			// This row read "SHALLOW, not a deep partial" and cited rule
			// 5 of the design document until the spread became deep. The
			// verdict and the message are byte-identical across that
			// change; only the reason was wrong.
			name: "a partial at an interface-typed field",
			body: "  s = Stage{who: Loud{n: 1}, seats: 3}\n  _a = {..s, who: {n: 2}}\n",
			want: "field 'who' of Stage: expected Speaker, got {n: Int}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(structSpreadDecls + "fn main() {\n" + tc.body + "}\n")
			expectError(t, errs, tc.want)
		})
	}
}

// The accepted half. Each row is a shape that would break the feature rather
// than a test if it started refusing.
func TestCheck_StructSpread_Accepts(t *testing.T) {
	cases := map[string]string{
		// RULE 4: the spread supplies every field it does not name, so
		// naming only some is not "missing required field". This is the one
		// rule of the shared validator the spread form changes.
		"one of two fields":               "  p = Point{x: 1, y: 2}\n  _a = {..p, x: 9}\n",
		"every field":                     "  p = Point{x: 1, y: 2}\n  _a = {..p, x: 9, y: 8}\n",
		"an anonymous head":               "  r = {x: 1, y: 2}\n  _a = {..r, y: 7}\n",
		"an interface-typed field":        "  s = Stage{who: Loud{n: 1}, seats: 3}\n  _a = {..s, who: Quiet{n: 2}}\n",
		"a type-parameter field":          "  b: Box<Int> = Box{item: 3, tag: \"t\"}\n  _a = {..b, item: 4}\n",
		"a type-parameter field untouch":  "  b: Box<Int> = Box{item: 3, tag: \"t\"}\n  _a = {..b, tag: \"u\"}\n",
		"a call as the head":              "  _a = {..Point{x: 1, y: 2}, y: 6}\n",
		"a nested spread as the head":     "  p = Point{x: 1, y: 2}\n  _a = {..{..p, x: 3}, y: 4}\n",
		"a spread inside a field's value": "  p = Point{x: 1, y: 2}\n  _a = {..p, y: {..p, x: 8}.x}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(structSpreadDecls + "fn main() {\n" + body + "}\n")
			if len(errs) != 0 {
				t.Fatalf("expected no diagnostics, got %v", errs)
			}
		})
	}
}

// RULE 2: the result takes the HEAD's type. Read through a position that only
// accepts the nominal type, so an anonymous result would be caught.
func TestCheck_StructSpread_ResultTakesTheHeadsType(t *testing.T) {
	const src = structSpreadDecls + `fn takes_point(p: Point): Int {
  p.x
}

fn takes_record(r: {x: Int, y: Int}): Int {
  r.x
}

fn main() {
  p = Point{x: 1, y: 2}
  _a = takes_point({..p, x: 9})
  r = {x: 1, y: 2}
  _b = takes_record({..r, y: 7})
}
`
	if _, errs := checkSource(src); len(errs) != 0 {
		t.Fatalf("expected no diagnostics, got %v", errs)
	}
}

// An ANONYMOUS head's result is NOT the nominal type, which is the other
// direction of rule 2 and the one a "just return the declared struct" bug
// would pass.
func TestCheck_StructSpread_AnonymousHeadStaysAnonymous(t *testing.T) {
	const src = structSpreadDecls + `fn takes_point(p: Point): Int {
  p.x
}

fn main() {
  r = {x: 1, y: 2}
  _a = takes_point({..r, y: 7})
}
`
	_, errs := checkSource(src)
	if len(errs) == 0 {
		t.Fatal("an anonymous-headed spread was accepted where a Point is required")
	}
}

// A spread that names no field is its base value. Values are immutable and have
// no identity, so the copy could do nothing the value itself does not; the
// checker says so at the `..`, naming the base when it is a plain name.
func TestCheck_StructSpread_RejectsNoFields(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"a named head":       {"  p = Point{x: 1, y: 2}\n  _a = {..p}\n", "a struct spread with no fields is its base value; write `p`"},
		"an anonymous head":  {"  r = {x: 1, y: 2}\n  _a = {..r}\n", "a struct spread with no fields is its base value; write `r`"},
		"a call as the head": {"  _a = {..Point{x: 1, y: 2}}\n", "a struct spread with no fields is its base value; write the value itself"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(structSpreadDecls + "fn main() {\n" + c.body + "}\n")
			if len(errs) != 1 || errs[0].Message != c.want {
				t.Fatalf("errors = %v, want exactly %q", errs, c.want)
			}
		})
	}
}
