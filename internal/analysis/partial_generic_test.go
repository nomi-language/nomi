package analysis_test

import "testing"

// A `_` in a generic call, which includes every interface-qualified call
// (generic over `self`), makes a function of the open slots, as it does in a
// non-generic call. checkGenericCall used to return the callee's result, so
// `Iter.each(xs, Console.write_line(c, _))` was "argument 2: expected
// (String) -> Unit, got Unit".

const partialGenericDecls = `
interface Console
  fn write_line(c: self, line: String) -> Unit
end

struct Stdout
  prefix: String
end

impl Console for Stdout
  fn write_line(c: Stdout, line: String) -> Unit
    _ = c
    _ = line
    Unit
  end
end

fn pair<T>(a: T, b: T) -> (T, T)
  (a, b)
end

fn spread<T>(x: T, xs: List<T> = [], twice: Bool = False) -> List<T>
  _ = twice
  [x, ..xs]
end
`

func TestPartialGeneric_TypesAsAFunctionOfTheOpenSlots(t *testing.T) {
	_, errs := checkSourceWithStdlib(partialGenericDecls + `
fn main()
  c = Stdout{prefix: "> "}
  Iter.each(["a", "b"], &Console.write_line(c, &1))
  w: (String) -> Unit = &Console.write_line(c, &1)
  w("c")
  p: (Int) -> (Int, Int) = &pair(1, &1)
  q: (Int) -> (Int, Int) = &pair(&1, 2)
  _ = (p(2), q(1))
  s: (List<Int>) -> List<Int> = &spread(1, xs: &1)
  _ = s([2])
end
`)
	expectNoStdlibErrors(t, errs)
}

func TestPartialGeneric_RejectsAMismatchedPartial(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The partial is a function: binding it where its result is
		// expected is the mismatch, named with the function type.
		{`  x: Unit = &Console.write_line(Stdout{prefix: ""}, &1)`, "expected Unit, got (String) -> Unit"},
		// The open slot's type is the one the written arguments solved.
		{`  p: (String) -> (Int, Int) = &pair(1, &1)`, "expected (String) -> (Int, Int), got (Int) -> (Int, Int)"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(partialGenericDecls + "\nfn main()\n" + tc.src + "\n  _ = 0\nend\n")
			expectStdlibError(t, errs, tc.want)
		})
	}
}

// A partial capture whose position expects a function of known parameter
// types takes them, as the lambda it stands for would: `Iter.flatten`'s slot
// is an `Iter<Iter<T>>`, and a `List<List<String>>` enters it as it enters
// a direct call. `Iter.count` over a list of lists too.
func TestPartialGeneric_TakesTheExpectedParameterTypes(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn main()
  found: Result<List<List<String>>, String> = Ok([["a"], ["b"]])
  _ = Result.map(found, &Iter.flatten(&1))
  f: (List<List<Int>>) -> Iter<Int> = &Iter.flatten(&1)
  g: (List<List<Int>>) -> Int = &Iter.count(&1)
  _ = (f([[1]]), g([[1]]))
end
`)
	expectNoStdlibErrors(t, errs)
}

// The mirror: a result the callee does not give is still the mismatch.
func TestPartialGeneric_TakesTheExpectedParameterTypesButNotAnyResult(t *testing.T) {
	_, errs := checkSourceWithStdlib("\nfn main()\n  f: (List<List<Int>>) -> Iter<String> = &Iter.flatten(&1)\n  _ = f\nend\n")
	expectStdlibError(t, errs, "expected (List<List<Int>>) -> Iter<String>, got (List<List<Int>>) -> Iter<Int>")
}

// A generic function named as a value whose parameter takes the expected
// one by a conversion inside a container fits, as its capture does: a
// function's parameter is contravariant at any depth (spec §5 *Covariant
// conversion*), so bare `total` is a `(List<List<Int>>) -> Int`, whose
// wrapper views each inner List as an Iter.
func TestPartialGeneric_AFunctionValueConvertsInsideAContainer(t *testing.T) {
	const total = "fn total<T>(rows: List<Iter<T>>) -> Int\n  rows |> Iter.map(Iter.count) |> Iter.sum()\nend\n\n"
	for _, body := range []string{
		"  _ = Maybe.map(Some([[1], [2]]), total)",
		"  f: (List<List<Int>>) -> Int = total\n  _ = f",
	} {
		_, errs := checkSourceWithStdlib(total + "fn main()\n" + body + "\nend\n")
		expectNoStdlibErrors(t, errs)
	}
}

// An Iter's element takes a List as the top does: `Iter.flatten` is an
// `(Iter<Iter<T>>) -> Iter<T>`, which stands where a function of a List of
// Lists is expected, as `Iter.flatten(lists)` takes the lists.
func TestPartialGeneric_AnIterElementTakesAList(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn main()
  found: Result<List<List<String>>, String> = Ok([["a"], ["b"]])
  _ = Result.map(found, Iter.flatten)
  f: (List<List<Int>>) -> Iter<Int> = Iter.flatten
  g: (List<Vector<Int>>) -> Iter<Int> = Iter.flatten
  _ = (f([[1]]), g([Vector[1]]))
end
`)
	expectNoStdlibErrors(t, errs)
}
