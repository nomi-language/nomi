package analysis

import (
	"strings"
	"testing"
)

// THE STRUCT LITERAL-ATTACH PATH VALIDATES FIELD NAMES AND FIELD VALUES.
//
// Without the checks below, these five programs would all pass `nomi check`
// clean:
//
//	Point{x: "s", y: 1}   where `x: Int`
//	Holder{p: 3}          where `p: Point`
//	Box{bogus: 1}         unknown field, and a missing one
//	Point{bogus: 1}       unknown field, and a missing one
//	Box{}                 missing field
//
// The unknown NAME and the two wrong VALUES would be reported by nothing.
//
// This path goes through `checkStructLitAgainstStruct`, which the
// constructor-call record form `Point({x: "s"})` already used and which
// already refused all five in that spelling.
func TestCheck_StructLit_ValidatesFields(t *testing.T) {
	const decls = `struct Point {
  x: Int
  y: Int
}
struct Box {
  v: Int
}
struct Holder {
  p: Point
}
struct Weight {
  kg: Float
}
`
	cases := []struct {
		name string
		expr string
		want []string
	}{
		{
			name: "a String in a field declared Int",
			expr: `Point{x: "s", y: 1}`,
			want: []string{"field 'x' of Point: expected Int, got String"},
		},
		{
			name: "an Int in a field declared as another struct",
			expr: `Holder{p: 3}`,
			want: []string{"field 'p' of Holder: expected Point, got Int"},
		},
		{
			name: "an unknown field name, reported as well as the field it displaced",
			expr: `Box{bogus: 1}`,
			want: []string{
				"Box has no field 'bogus'",
				"missing field 'v' of Box",
			},
		},
		{
			name: "an unknown field name on a two-field struct",
			expr: `Point{bogus: 1}`,
			want: []string{
				"Point has no field 'bogus'",
				"missing field 'x' of Point",
				"missing field 'y' of Point",
			},
		},
		{
			name: "no fields at all",
			expr: `Box{}`,
			want: []string{"missing field 'v' of Box"},
		},
		{
			// An Int where a Float is declared. It looks like a coercion
			// question and it is not: at the twin `p: Float = 1`, `take(1)`
			// into `fn take(x: Float)` and the field default `f: Float = 1`
			// were each refused `expected Float, got Int`. The struct
			// literal was the only position in the language that accepted it.
			name: "an Int in a field declared Float, which every other position refuses",
			expr: `Weight{kg: 1}`,
			want: []string{"field 'kg' of Weight: expected Float, got Int"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(decls + "fn main() {\n  _a = " + tc.expr + "\n}\n")
			for _, w := range tc.want {
				expectError(t, errs, w)
			}
		})
	}
}

// THE SAME FIELD SHAPES ARE STILL ACCEPTED, and this is the half that would
// break a feature rather than a test. `checkStructLitAgainstStruct` checks
// values with `argMatchesParam` — the rule the constructor-call record forms
// and the field-default check already used, reaching `ifaceParamAdmits` for an
// interface-typed field. A bare `TypesEqual` would reject most of the rows
// below, and every one of them is a shape the language accepts elsewhere.
//
// Each row was measured in BOTH spellings at the twin — `Cfg{f: v}` and
// `Cfg({f: v})` — and the two agreed on every one, which is what established
// that adopting the call form's rule costs no leniency.
func TestCheck_StructLit_StaysLenientWhereTheLanguageIs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "an untyped empty list at List<Int>",
			src:  "struct Cfg {\n  v: List<Int>\n}\nfn main() {\n  _a = Cfg{v: []}\n}\n",
		},
		{
			name: "a lambda at a function-typed field, params inferred from the field",
			src:  "struct Cfg {\n  f: (Int) -> Int\n}\nfn main() {\n  _a = Cfg{f: |x| x + 1}\n}\n",
		},
		{
			name: "an anonymous-struct field",
			src:  "struct Cfg {\n  v: { a: Int }\n}\nfn main() {\n  _a = Cfg{v: {a: 1}}\n}\n",
		},
		{
			name: "a bare type-parameter field, which is what inference binds",
			src:  "struct Box<T> {\n  v: T\n}\nfn main() {\n  _a = Box{v: 1}\n}\n",
		},
		{
			name: "a defaulted field omitted",
			src:  "struct Cfg {\n  port: Int = 80\n}\nfn main() {\n  _a = Cfg{}\n}\n",
		},
		{
			name: "a concrete implementer at an interface-typed field",
			src: `interface Clock {
  fn at(c: self): Int
}
struct RealClock {
  t: Int
}
impl Clock for RealClock {
  fn at(c: RealClock): Int { c.t }
}
struct Cfg {
  c: Clock
}
fn main() {
  _a = Cfg{c: RealClock{t: 1}}
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			expectNoErrors(t, errs)
		})
	}
}

// A NON-IMPLEMENTER AT AN INTERFACE-TYPED FIELD IS STILL REFUSED, in the call
// form's words rather than the literal form's. The literal form's phrasing was
// interface-only and could say nothing about `x: Int` given a String, so
// struct literals and field defaults both use the call form's phrasing:
//
//	field 'c' of Cfg: expected Clock, got NoClock
func TestCheck_StructLit_InterfaceFieldRefusalUsesTheSharedWording(t *testing.T) {
	src := `interface Clock {
  fn at(c: self): Int
}
struct NoClock {
  t: Int
}
struct Cfg {
  c: Clock
}
fn main() {
  _a = Cfg{c: NoClock{t: 1}}
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "field 'c' of Cfg: expected Clock, got NoClock")
	for _, e := range errs {
		if strings.Contains(e.Message, "does not implement Clock") {
			t.Fatalf("the interface-only phrasing should be gone, got %q", e.Message)
		}
	}
}

// ANONYMOUS STRUCTS ARE NOT TOUCHED, and that had to be established rather
// than assumed: a field-name check against a STRUCTURAL type is a different
// question than against a nominal one, and getting it wrong removes a feature.
//
// They are not touched because they never reach the changed code.
// `checkStructLit` returns an `*AnonStructType` built from the literal's own
// fields at its `n.TypeName == nil` branch, BEFORE the type name is resolved
// and before any nominal validation runs. There is no declared shape to check
// names or omissions against — the literal IS the declaration — so "unknown
// field" and "missing required field" have no meaning there.
//
// The one place an anonymous struct meets a nominal shape is the
// constructor-call record form `Cfg({...})`, and that position was already
// checked by the function this change made shared. It is the SOURCE of the
// rule, not a new target of it.
func TestCheck_AnonStruct_IsStructuralAndUnvalidated(t *testing.T) {
	t.Run("an anon struct literal has no field-name rule to break", func(t *testing.T) {
		_, errs := checkSource("fn main() {\n  r = {a: 1, b: \"two\"}\n  _x = r.a\n  _y = r.b\n}\n")
		expectNoErrors(t, errs)
	})
	t.Run("an anon struct type annotation admits the matching literal", func(t *testing.T) {
		_, errs := checkSource("fn main() {\n  _r: { a: Int } = {a: 1}\n}\n")
		expectNoErrors(t, errs)
	})
	t.Run("two anon structs with different fields stay distinct types", func(t *testing.T) {
		_, errs := checkSource("fn take(_r: { a: Int }) {}\nfn main() {\n  take({b: 1})\n}\n")
		if len(withoutUnusedBindingErrors(errs)) == 0 {
			t.Fatalf("expected the structural mismatch to be reported, got none")
		}
	})
	t.Run("the constructor-call record form still reports, unchanged", func(t *testing.T) {
		_, errs := checkSource("struct Cfg {\n  port: Int\n}\nfn main() {\n  _a = Cfg({bogus: 1})\n}\n")
		expectError(t, errs, "Cfg has no field 'bogus'")
		expectError(t, errs, "missing field 'port' of Cfg")
	})
}

// THE MISSING-FIELD RULE IS ONE RULE, applied in the struct-variant path too.
//
// `checkStructVariantLit`'s header declined it on the ground that adding it
// there alone would put a third convention beside two existing ones. The count
// was wrong. The two constructor-call record forms already reported missing
// fields with the phrasing `missing field 'v' of Box`.
// So there was one convention
// with two holes in it, the struct literal-attach path and this one, and both
// are closed in the same words.
func TestCheck_StructVariantLit_RequiresNonDefaultedFields(t *testing.T) {
	t.Run("a required variant field omitted is reported", func(t *testing.T) {
		_, errs := checkSource("enum Shape {\n  Wrap { inner: Int }\n}\nfn main() {\n  _s = Shape.Wrap{}\n}\n")
		expectError(t, errs, "missing field 'inner' of Shape.Wrap")
	})
	t.Run("an unknown name does not excuse the field it displaced", func(t *testing.T) {
		_, errs := checkSource("enum Shape {\n  Wrap { inner: Int }\n}\nfn main() {\n  _s = Shape.Wrap{bogus: 1}\n}\n")
		expectError(t, errs, "no field 'bogus' on variant Wrap of enum Shape")
		expectError(t, errs, "missing field 'inner' of Shape.Wrap")
	})

	// A DEFAULTED VARIANT FIELD IS NOT REQUIRED, and this row is the reason
	// `type_builder.go` changed. `buildStructType` set `HasDefault` on a
	// struct field (:907); the variant loop beside it did not (:972). Nothing
	// read a variant field's `HasDefault` until this rule existed, so every
	// variant field read as REQUIRED. The first run reported 14 defaulted
	// fields missing across 5 files, among them every field of
	// `tests/07-structs-and-enums/variant_field_defaults/`, the
	// corpus file whose subject is exactly this.
	t.Run("a defaulted variant field may be omitted", func(t *testing.T) {
		_, errs := checkSource("enum Backoff {\n  Exponential { max_restarts: Int = 10, max_elapsed: Int = 900 }\n}\nfn main() {\n  _b = Backoff.Exponential{}\n}\n")
		expectNoErrors(t, errs)
	})
	t.Run("a defaulted variant field may still be supplied", func(t *testing.T) {
		_, errs := checkSource("enum Backoff {\n  Exponential { max_restarts: Int = 10, max_elapsed: Int = 900 }\n}\nfn main() {\n  _b = Backoff.Exponential{max_restarts: 3}\n}\n")
		expectNoErrors(t, errs)
	})
}

// A STRUCT-LITERAL HEAD THAT IS NOT A STRUCT KEEPS THE OLD SHAPE, and these
// three rows exist because the guard that gives it that shape was otherwise
// untested — a mutant deleting it survived the rest of this file.
//
// `checkStructLit` resolved the head before this change too, and when the
// result was not a `*StructType` it left `declaredFields` empty, walked the
// values with a bare `checkNode`, and returned whatever it resolved to. The
// rewrite made that arm explicit instead of implicit. It has to stay: the
// shared validator reads `st.Fields` and `st.TypeParamDefs`, so delegating a
// nil `*StructType` panics.
//
// All three are ACCEPTED, with no diagnostic, at the twin and here alike.
// That is not an endorsement — `E{v: 1}` naming an enum and `Meters{v: 1}`
// naming a distinct type both look like holes of the same family as the one
// the nominal-struct check closes. They are UNCHANGED, which is what this test
// asserts, and
// they are a separate question from the nominal-struct path.
func TestCheck_StructLit_NonStructHeadIsUnchanged(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "an interface name",
			src:  "interface I {\n  fn f(x: self): Int\n}\nfn main() {\n  _a = I{v: 1}\n}\n",
		},
		{
			name: "an enum name",
			src:  "enum E {\n  A\n}\nfn main() {\n  _a = E{v: 1}\n}\n",
		},
		{
			name: "a distinct type name",
			src:  "type Meters Int\nfn main() {\n  _a = Meters{v: 1}\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource(tc.src)
			expectNoErrors(t, errs)
		})
	}
}
