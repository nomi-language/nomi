package analysis_test

import "testing"

const wideningInvariantDecls = `fn two<T>(a: ClosedRange<T>, b: ClosedRange<T>) -> Int
    _ = (a, b)
    1
end

fn hold<T>(x: T, r: ClosedRange<T>) -> T
    _ = r
    x
end

fn through<T>(f: (T) -> Int, r: ClosedRange<T>) -> Int
    f(Range.start(r))
end

fn positive(p: PositiveInt) -> Int
    p
end

`

func wideningInvariantSource(body string) string {
	return wideningInvariantDecls + "fn main()\n    p: PositiveInt = 3\n    i: Int = -1\n    rp: ClosedRange<PositiveInt> = 1..=5\n    ri: ClosedRange<Int> = -5..=5\n    _ = (p, i, rp, ri)\n" + body + "\nend\n"
}

// A type parameter an argument solved to a refinement widens to the ancestor
// a later argument names at an invariant type argument (spec §4 *Chains
// and conversion*): the refinement's value converts to it, and the invariant
// argument has it exactly.
func TestWiden_ToTheAncestorAnInvariantArgumentNames(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"clamp to a range literal of the base", "    n: Int = Int.clamp(p, -5..=5)\n    _ = n"},
		{"clamp to a range of the base", "    _ = Int.clamp(p, ri)"},
		{"max beside a value of the base", "    _ = Int.max(p, -5)"},
		{"a generic function", "    n: Int = hold(p, ri)\n    _ = n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(wideningInvariantSource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
}

// The mirror: nothing narrows to the refinement an invariant argument names,
// two invariant arguments of one parameter must agree, and a parameter a
// function argument takes is not widened past it.
func TestWiden_AnInvariantArgumentConvertsNothing(t *testing.T) {
	const toBase = "argument 2: expected ClosedRange<Int>, got ClosedRange<PositiveInt>\nhelp: ClosedRange<PositiveInt> does not convert to ClosedRange<Int>: ClosedRange's type parameter T is invariant, since ClosedRange's inner type both holds a T and takes one; build a new ClosedRange<Int> from the value's parts"
	const toRefinement = "argument 2: expected ClosedRange<PositiveInt>, got ClosedRange<Int>\nhelp: ClosedRange<Int> does not convert to ClosedRange<PositiveInt>: ClosedRange's type parameter T is invariant, since ClosedRange's inner type both holds a T and takes one; build a new ClosedRange<PositiveInt> from the value's parts"
	for _, tc := range []struct{ name, body, want, at string }{
		{"a base value beside a range of the refinement", "    _ = Int.clamp(i, rp)", toBase, "rp"},
		{"a generic function, the same", "    _ = hold(i, rp)", toBase, "rp"},
		{"two ranges, the refinement first", "    _ = two(rp, ri)", toRefinement, "ri"},
		{"two ranges, the base first", "    _ = two(ri, rp)", toBase, "rp"},
		{"past a function of the refinement", "    _ = through(positive, ri)", toRefinement, "ri"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := wideningInvariantSource(tc.body)
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}
