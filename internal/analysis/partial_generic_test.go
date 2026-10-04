package analysis_test

import "testing"

// A `_` in a generic call, which includes every interface-qualified call
// (generic over `self`), makes a function of the open slots, as it does in a
// non-generic call. checkGenericCall used to return the callee's result, so
// `Iter.each(xs, Console.write_line(c, _))` was "argument 2: expected
// (String) -> Unit, got Unit".

const partialGenericDecls = `
interface Console {
  fn write_line(c: self, line: String): Unit
}

struct Stdout {
  prefix: String
}

impl Console for Stdout {
  fn write_line(c: Stdout, line: String): Unit {
    _ = c
    _ = line
    Unit
  }
}

fn pair<T>(a: T, b: T): (T, T) {
  (a, b)
}

fn spread<T>(x: T, xs: List<T> = [], twice: Bool = False): List<T> {
  _ = twice
  [x, ..xs]
}
`

func TestPartialGeneric_TypesAsAFunctionOfTheOpenSlots(t *testing.T) {
	_, errs := checkSourceWithStdlib(partialGenericDecls + `
fn main() {
  c = Stdout{prefix: "> "}
  Iter.each(["a", "b"], Console.write_line(c, _))
  w: (String) -> Unit = Console.write_line(c, _)
  w("c")
  p: (Int) -> (Int, Int) = pair(1, _)
  q: (Int) -> (Int, Int) = pair(_, 2)
  _ = (p(2), q(1))
  s: (List<Int>) -> List<Int> = spread(1, xs: _)
  _ = s([2])
}
`)
	expectNoStdlibErrors(t, errs)
}

func TestPartialGeneric_RejectsAMismatchedPartial(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The partial is a function: binding it where its result is
		// expected is the mismatch, named with the function type.
		{`  x: Unit = Console.write_line(Stdout{prefix: ""}, _)`, "expected Unit, got (String) -> Unit"},
		// The open slot's type is the one the written arguments solved.
		{`  p: (String) -> (Int, Int) = pair(1, _)`, "expected (String) -> (Int, Int), got (Int) -> (Int, Int)"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(partialGenericDecls + "\nfn main() {\n" + tc.src + "\n  _ = 0\n}\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}
