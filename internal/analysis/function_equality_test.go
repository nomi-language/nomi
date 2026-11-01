package analysis_test

import (
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A function value has no equality, so no hash or order. A derive that
// would compare a function-typed component, and a `where T: Equatable`
// bound met by a function or a value holding one, are rejected; they used
// to pass the checker and stop the program with "not supported yet" (spec
// §5 *Equality*).
func TestFunctionEquality_DerivesAndBoundsAreRejected(t *testing.T) {
	bound := func(src string) []analysis.TypeError {
		_, errs := checkSourceWithStdlib(src)
		return errs
	}
	derive := func(src string) []analysis.TypeError { return buildWithStdlibForDerive(src).TypeErrors }
	cases := []struct {
		name  string
		check func(string) []analysis.TypeError
		src   string
		want  string
		hint  string
	}{
		{"derived Equatable", derive, "struct W {\n    f: (Int) -> Int\n}\n\nderive Equatable for W\n",
			"no impl of `Equatable` for `(Int) -> Int` (required via `derive Equatable for W`, field `f`)",
			"a function value has no equality; write an `impl Equatable for W` that leaves the field out"},
		{"derived Hashable through a list", derive, "struct W {\n    fs: List<(Int) -> Int>\n}\n\nderive Hashable for W\n",
			"no impl of `Hashable` for `(Int) -> Int` (required via `derive Hashable for W`, field `fs`)",
			"a function value has no equality; write an `impl Hashable for W` that leaves the field out"},
		{"derived Comparable on an enum payload", derive, "enum E {\n    Step((Int) -> Int)\n}\n\nderive Comparable for E\n",
			"no impl of `Comparable` for `(Int) -> Int` (required via `derive Comparable for E`, field `Step`)",
			"a function value has no equality; write an `impl Comparable for E` that leaves the field out"},
		{"bound met by a function", bound, "fn same<T>(a: T, b: T) -> Bool where T: Equatable {\n    a == b\n}\n\nfn main() {\n    g = fn x: Int -> x\n    _ = same(g, g)\n}\n",
			"(Int) -> Int does not implement Equatable (required by `where T: Equatable`)",
			"a function value has no equality"},
		{"bound met by a tuple holding one", bound, "fn same<T>(a: T, b: T) -> Bool where T: Equatable {\n    a == b\n}\n\nfn main() {\n    g = fn x: Int -> x\n    _ = same(#(1, g), #(1, g))\n}\n",
			"#(Int, (Int) -> Int) does not implement Equatable (required by `where T: Equatable`)",
			"`#(Int, (Int) -> Int)` holds `(Int) -> Int`, and a function value has no equality"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, e := range tc.check(tc.src) {
				got = append(got, e.Message)
				if e.Message == tc.want {
					if !slices.Contains(e.Hints, tc.hint) {
						t.Errorf("hints %q; want %q", e.Hints, tc.hint)
					}
					return
				}
			}
			t.Errorf("the checker admits this source or reports something else; want %q, got %v\n%s",
				tc.want, got, tc.src)
		})
	}
}
