package analysis

import "testing"

// A type-prefixed list literal (`Name[...]`, `.Name[...]`) builds the enum
// variant or list-distinct type it names. Where a plain list is expected,
// the checker once typed it as that list and never looked at the prefix, so
// an unknown name, a variant, or a distinct type passed as a `List<T>`.

func TestTypePrefixedListLit_UnknownPrefixWhereAListIsExpected(t *testing.T) {
	cases := map[string]string{
		"annotated binding": `fn main() {
  _x: List<Int> = X[1, 2]
}`,
		"argument": `fn f(xs: List<Int>): List<Int> { xs }
fn main() {
  _y = f(X[1])
}`,
		"struct field": `struct Holder {
  items: List<Int>
}
fn main() {
  _c = Holder{items: X[1, 2]}
}`,
		"iter argument": `fn main() {
  _n = Iter.count(X[1, 2])
}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSource(src)
			expectError(t, errs, "undefined type X")
		})
	}
}

func TestTypePrefixedListLit_DotVariantWhereAListIsExpected(t *testing.T) {
	src := `fn main() {
  _z: List<Int> = .Arr[1]
}`
	_, errs := checkSource(src)
	expectError(t, errs, ".Arr resolves only to enum variants, but the expected type at this position is List<Int>")
}

func TestTypePrefixedListLit_VariantIsNotTheListItWraps(t *testing.T) {
	src := `enum JV {
  Num Int
  Arr List<JV>
}
fn main() {
  _xs: List<JV> = JV.Arr[JV.Num(1)]
}`
	_, errs := checkSource(src)
	expectError(t, errs, "expected List<JV>, got JV")
}

func TestTypePrefixedListLit_DistinctIsNotTheListItWraps(t *testing.T) {
	src := `type Ids List<Int>
fn f(xs: List<Int>): List<Int> { xs }
fn main() {
  _y = f(Ids[1, 2])
}`
	_, errs := checkSource(src)
	expectError(t, errs, "expected List<Int>, got Ids")
}

func TestTypePrefixedListLit_ValidFormsCheck(t *testing.T) {
	src := `enum JV {
  Num Int
  Arr List<JV>
}
type Ids List<Int>
fn arr(): JV { .Arr[.Num(1), .Arr[.Num(2)]] }
fn main() {
  _a: JV = JV.Arr[.Num(1), .Num(2)]
  _b: JV = .Arr[.Num(3)]
  _ids: Ids = Ids[4, 5]
  _vs: List<JV> = [.Arr[.Num(6)], JV.Arr[]]
  _c = arr()
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}
