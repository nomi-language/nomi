package analysis_test

import "testing"

const anonExpectedDecls = `enum Color
    Red
    Green
end

struct Point
    x: Int
    y: Int = 0
end

fn call(r: {f: (Int) -> Int}) -> Int
    r.f(2)
end

`

// An anonymous struct literal where an anonymous struct type is expected
// checks each field's value against that field's type, as a tuple literal
// checks its items (spec §4 *Target-typed construction*, §4 *Variant Resolution*):
// a dot-leading variant, a Byte, a vector, a named struct and a lambda
// each take the field's type.
func TestAnonStructLit_AFieldValueTakesItsFieldsType(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a dot-leading variant", "    a: {c: Color} = {c: .Green}\n    _ = a"},
		{"a byte", "    a: {b: Byte} = {b: 66}\n    _ = a"},
		{"a vector", "    a: {v: Vector<Int>} = {v: [3]}\n    _ = a"},
		{"a named struct", "    a: {p: Point} = {p: {x: 2}}\n    _ = a"},
		{"a lambda", "    _ = call({f: fn x -> x + 1})"},
		{"nested", "    a: {inner: {c: Color}, t: (Color, Byte)} = {inner: {c: .Red}, t: (.Green, 7)}\n    _ = a"},
		{"fields in another order", "    a: {c: Color, b: Byte} = {b: 1, c: .Red}\n    _ = a"},
		{"a later list element", "    xs = [{c: Color.Red}, {c: .Green}]\n    _ = xs"},
		{"a later branch", "    r = if True then {c: Color.Red} else {c: .Green}\n    _ = r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(anonExpectedDecls + "fn main()\n" + tc.body + "\nend\n")
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A field whose value does not fit its field's type, and a literal with a
// field missing, are still mismatches that name the field.
func TestAnonStructLit_AFieldThatDoesNotFitIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, body, want, at string }{
		{"a value of another type", "    a: {c: Color} = {c: 1}\n    _ = a",
			"type mismatch: expected {c: Color}, got {c: Int}", "{c: 1}"},
		{"a missing field", "    a: {c: Color, n: Int} = {c: .Red}\n    _ = a",
			"type mismatch: expected {c: Color, n: Int}, got {c: Color}", "{c: .Red}"},
		{"an element of another type", "    a: {v: Vector<Int>} = {v: [\"a\"]}\n    _ = a",
			"vector element type mismatch: expected Int, got String", "\"a\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := anonExpectedDecls + "fn main()\n" + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}
