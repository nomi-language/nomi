package analysis_test

import "testing"

// A tuple, an anonymous struct or a function has no impl of its own, so it
// meets no program-declared interface bound, and std's Equatable, Hashable,
// Comparable and Display only through its parts (spec: Bounds and dispatch
// in generic code). Each rejection is at the call and says why.
func TestStructuralTypesAtABoundRejectWhatTheyDoNotImplement(t *testing.T) {
	const decls = `interface Shape {
  fn area(value: self) -> Int
}

struct Q {
  a: Int
}

struct F {
  f: (Int) -> Int
}

fn total<T>(x: T) -> Int where T: Shape {
  Shape.area(x)
}

fn h<T>(_x: T) -> Int where T: Hashable {
  1
}

fn e<T>(_x: T) -> Int where T: Equatable {
  1
}

fn inc(x: Int) -> Int {
  x + 1
}
`
	cases := []struct {
		name, body, want string
	}{
		{"a tuple at a program interface", `total((1, 2))`,
			"(Int, Int) does not implement Shape (required by `where T: Shape`)\n" +
				"help: no impl can be written for a tuple, which implements only Equatable, Hashable, Comparable and Display, each when every element does; declare a struct of your own that implements `Shape`"},
		{"an anonymous struct at a program interface", `total(.{a: 1})`,
			".{a: Int} does not implement Shape (required by `where T: Shape`)\n" +
				"help: no impl can be written for an anonymous struct"},
		{"a function at a program interface", `total(inc)`,
			"(Int) -> Int does not implement Shape (required by `where T: Shape`)\n" +
				"help: a function value implements no interface but Debug"},
		// A struct with no impl of its own meets Equatable and Hashable by
		// its equality, so only one that holds a function is refused.
		{"a tuple with a struct without Hashable", `h((1, F{f: inc}))`,
			"(Int, F) does not implement Hashable (required by `where T: Hashable`)\n" +
				"help: `(Int, F)` holds `(Int) -> Int`, and a function value has no equality"},
		{"a tuple with a struct without Equatable", `e((F{f: inc}, 2))`,
			"(F, Int) does not implement Equatable (required by `where T: Equatable`)\n" +
				"help: `(F, Int)` holds `(Int) -> Int`, and a function value has no equality"},
		{"a nested tuple with a struct without Hashable", `h((1, ("a", F{f: inc})))`,
			"(Int, (String, F)) does not implement Hashable (required by `where T: Hashable`)"},
		{"an anonymous struct with a struct without Hashable", `h(.{q: F{f: inc}})`,
			".{q: F} does not implement Hashable (required by `where T: Hashable`)\n" +
				"help: `.{q: F}` holds `(Int) -> Int`, and a function value has no equality"},
		{"a tuple with a function", `h((1, inc))`,
			"(Int, (Int) -> Int) does not implement Hashable (required by `where T: Hashable`)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "\nfn run() -> Int {\n  " + tc.body + "\n}\n"
			fa := buildWithStdlibForDerive(src)
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the checker admits %s, or rejects it with another message; want %q, got:\n%s",
					tc.body, tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
}

// A tuple whose elements all meet one of std's structural interfaces meets
// it, at a bound and nested in a std type whose impl bounds its argument,
// and so does an anonymous struct for Equatable, Hashable and Display.
func TestStructuralTypesAtABoundAdmitWhatTheirPartsImplement(t *testing.T) {
	const decls = `struct P {
  a: Int
}

derive Equatable for P
derive Hashable for P

fn h<T>(_x: T) -> Int where T: Hashable {
  1
}

fn e<T>(_x: T) -> Int where T: Equatable {
  1
}

fn c<T>(_x: T) -> Int where T: Comparable {
  1
}

fn d<T>(_x: T) -> Int where T: Display {
  1
}
`
	for _, body := range []string{
		`h((1, "a"))`,
		`h((1, ("a", 2.0)))`,
		`h((P{a: 1}, 2))`,
		`h(Some((1, 2)))`,
		`h([(1, 2)])`,
		`h(.{a: 1, b: "x"})`,
		`e((1, [P{a: 1}]))`,
		`e([(1, 2)])`,
		`c((1, "a"))`,
		`c([(1, "a")])`,
		`d((1, "a"))`,
		`d(.{a: 1})`,
	} {
		t.Run(body, func(t *testing.T) {
			src := decls + "\nfn run() -> Int {\n  " + body + "\n}\n"
			fa := buildWithStdlibForDerive(src)
			if len(fa.TypeErrors) != 0 {
				t.Fatalf("the checker rejects %s:\n%s", body, errorsText(fa.TypeErrors))
			}
		})
	}
}

// An interface-typed parameter or list element takes a tuple, an anonymous
// struct or a function by the rule a bound does, with the help a bound
// gives when it refuses one.
func TestStructuralTypesAsInterfaceValuesRejectWhatTheyDoNotImplement(t *testing.T) {
	const decls = `interface Shape {
  fn area(value: self) -> Int
}

struct Q {
  a: Int
}

fn hh(h: Hashable) -> Int {
  Hashable.hash(h)
}

fn sh(s: Shape) -> Int {
  Shape.area(s)
}

fn show(d: Display) -> String {
  "${d}"
}

fn inc(x: Int) -> Int {
  x + 1
}
`
	cases := []struct {
		name, stmt, want string
	}{
		{"a tuple with a struct without Hashable", `_ = hh((1, Q{a: 1}))`,
			"argument 1: expected Hashable, got (Int, Q)\n" +
				"help: `(Int, Q)` is Hashable when each element is, and `Q` is not: add `derive Hashable` to `Q`"},
		{"a tuple at a program interface", `_ = sh((1, 2))`,
			"argument 1: expected Shape, got (Int, Int)\n" +
				"help: no impl can be written for a tuple, which implements only Equatable, Hashable, Comparable and Display, each when every element does; declare a struct of your own that implements `Shape`"},
		{"an anonymous struct at a program interface", `_ = sh(.{a: 1})`,
			"argument 1: expected Shape, got .{a: Int}\n" +
				"help: no impl can be written for an anonymous struct"},
		{"a tuple with a function at Display", `_ = show((1, inc))`,
			"help: `(Int, (Int) -> Int)` is Display when each part is, and `(Int) -> Int` is not: a function value has no `Display`"},
		{"a list element", `xs: List<Hashable> = [(1, Q{a: 1})]
  _ = xs`,
			"list element type mismatch: expected Hashable, got (Int, Q)\n" +
				"help: `(Int, Q)` is Hashable when each element is, and `Q` is not"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "\nfn run() -> Unit {\n  " + tc.stmt + "\n}\n"
			fa := buildWithStdlibForDerive(src)
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the checker admits %s, or rejects it with another message; want %q, got:\n%s",
					tc.stmt, tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
}

// A tuple or anonymous struct whose parts all meet one of std's structural
// interfaces is a value of that interface: an argument, a list element, a
// map key, a binding.
func TestStructuralTypesAsInterfaceValuesAdmitWhatTheirPartsImplement(t *testing.T) {
	const decls = `struct P {
  a: Int
}

derive Equatable for P
derive Hashable for P

fn hh(h: Hashable) -> Int {
  Hashable.hash(h)
}

fn eq(a: Equatable, b: Equatable) -> Bool {
  a == b
}

fn show(d: Display) -> String {
  "${d}"
}
`
	for _, stmt := range []string{
		`_ = hh((1, 2))`,
		`_ = hh((P{a: 1}, "x"))`,
		`_ = hh(.{a: 1, b: (2, 3)})`,
		`_ = eq((1, 2), (1, 2))`,
		`_ = eq(.{a: 1}, 3)`,
		`_ = show((1, "a"))`,
		`_ = show(.{a: 1})`,
		"xs: List<Hashable> = [(1, 2), 3]\n  _ = xs",
		"ds: List<Display> = [(1, \"x\"), .{b: 2.5}]\n  _ = ds",
		"m: Map<Hashable, Int> = %{(1, 2) => 1, 3 => 4}\n  _ = m",
		"e: Equatable = (P{a: 1}, [1])\n  _ = e",
	} {
		t.Run(stmt, func(t *testing.T) {
			src := decls + "\nfn run() -> Unit {\n  " + stmt + "\n}\n"
			fa := buildWithStdlibForDerive(src)
			if len(fa.TypeErrors) != 0 {
				t.Fatalf("the checker rejects %s:\n%s", stmt, errorsText(fa.TypeErrors))
			}
		})
	}
}
