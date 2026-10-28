package analysis_test

import "testing"

// A `_` in a generic call, which includes every interface-qualified call
// (generic over `self`), makes a function of the open slots, as it does in a
// non-generic call. checkGenericCall used to return the callee's result, so
// `Iter.each(xs, Console.write_line(c, _))` was "argument 2: expected
// (String) -> Unit, got Unit".

const partialGenericDecls = `
interface Console {
  fn write_line(c: self, line: String) -> Unit
}

struct Stdout {
  prefix: String
}

impl Console for Stdout {
  fn write_line(c: Stdout, line: String) -> Unit {
    _ = c
    _ = line
    Unit
  }
}

fn pair<T>(a: T, b: T) -> #(T, T) {
  #(a, b)
}

fn spread<T>(x: T, xs: List<T> = [], twice: Bool = False) -> List<T> {
  _ = twice
  [x, ..xs]
}
`

func TestPartialGeneric_TypesAsAFunctionOfTheOpenSlots(t *testing.T) {
	_, errs := checkSourceWithStdlib(partialGenericDecls + `
fn main() {
  c = #Stdout{prefix: "> "}
  Iter.each(["a", "b"], &Console.write_line(c, &1))
  w: (String) -> Unit = &Console.write_line(c, &1)
  w("c")
  p: (Int) -> #(Int, Int) = &pair(1, &1)
  q: (Int) -> #(Int, Int) = &pair(&1, 2)
  _ = #(p(2), q(1))
  s: (List<Int>) -> List<Int> = &spread(1, xs: &1)
  _ = s([2])
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestPartialGeneric_RejectsAMismatchedPartial(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The partial is a function: binding it where its result is
		// expected is the mismatch, named with the function type.
		{`  x: Unit = &Console.write_line(#Stdout{prefix: ""}, &1)`, "expected Unit, got (String) -> Unit"},
		// The open slot's type is the one the written arguments solved.
		{`  p: (String) -> #(Int, Int) = &pair(1, &1)`, "expected (String) -> #(Int, Int), got (Int) -> #(Int, Int)"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(partialGenericDecls + "\nfn main() {\n" + tc.src + "\n  _ = 0\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}

// A partial capture whose position expects a function of known parameter
// types takes them, as the lambda it stands for would: `Iter.flatten`'s slot
// is an `Iter<Iter<U>>`, and a `List<List<String>>` enters it as it enters
// a direct call. `Iter.count` over a list of lists too.
func TestPartialGeneric_TakesTheExpectedParameterTypes(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn main() {
  found: Result<List<List<String>>, String> = Ok([["a"], ["b"]])
  _ = Result.map(found, &Iter.flatten(&1))
  f: (List<List<Int>>) -> List<Int> = &Iter.flatten(&1)
  g: (List<List<Int>>) -> Int = &Iter.count(&1)
  _ = #(f([[1]]), g([[1]]))
}
`)
	expectNoStdlibErrors(t, errs)
}

// The mirror: a result the callee does not give is still the mismatch.
func TestPartialGeneric_TakesTheExpectedParameterTypesButNotAnyResult(t *testing.T) {
	_, errs := checkSourceWithStdlib("\nfn main() {\n  f: (List<List<Int>>) -> List<String> = &Iter.flatten(&1)\n  _ = f\n}\n")
	expectStdlibError(t, errs, "expected (List<List<Int>>) -> List<String>, got (List<List<Int>>) -> List<Int>")
}

// A generic function named as a value whose parameter would take the
// expected one only by a nested conversion is refused, and the help names
// the capture that fits: bare `Iter.flatten` is no `(List<List<Int>>) ->
// List<Int>`, and `&Iter.flatten(&1)` is one.
func TestPartialGeneric_AFunctionValueThatDoesNotFitNamesTheCapture(t *testing.T) {
	const hint = "\nhelp: a function value's parameter takes a List where an Iter is declared only at the top, not inside another type; write the capture `&Iter.flatten(&1)`, whose parameters are the ones expected here"
	for _, tc := range []struct{ name, body, want, at string }{
		{"an argument", "  _ = Maybe.map(Some([[1], [2]]), Iter.flatten)",
			"argument 2: expected (List<List<Int>>) -> U, got (Iter<Iter<U>>) -> List<U>" + hint, "Iter.flatten"},
		{"an annotated binding", "  f: (List<List<Int>>) -> List<Int> = Iter.flatten\n  _ = f",
			"type mismatch: expected (List<List<Int>>) -> List<Int>, got (Iter<Iter<U>>) -> List<U>" + hint, "Iter.flatten"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main() {\n" + tc.body + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}

// The mirror: where the capture would not fit either, as for a result the
// callee does not give, the mismatch has no such help.
func TestPartialGeneric_NoCaptureHintWhereTheCaptureWouldNotFit(t *testing.T) {
	src := "fn main() {\n  f: (List<List<Int>>) -> List<String> = Iter.flatten\n  _ = f\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectDiagAt(t, src, errs, "type mismatch: expected (List<List<Int>>) -> List<String>, got (Iter<Iter<U>>) -> List<U>", "Iter.flatten")
}
