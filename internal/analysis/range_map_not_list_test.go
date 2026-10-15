package analysis_test

import "testing"

// A Range and a Map are not Lists. A parameter typed List<T> refuses them as
// a List<Int> parameter does; the spec defines no conversion to a List, and
// an Iter consumer is what takes either one.
func TestRangeAndMapAreNotLists(t *testing.T) {
	const decls = "fn first<T>(xs: List<T>): Maybe<T> {\n  List.head(xs)\n}\n\n"
	for _, tc := range []struct{ name, body, want string }{
		{"a piped Range into List.equal?",
			"  _ = 0..(0) |> List.equal?([])\n",
			"`List.equal?` takes a List<T> as its first argument, but the pipe passes a Range<Int>"},
		{"a Range argument to List.equal?",
			"  _ = List.equal?(0..0, [])\n",
			"argument 1: expected List<T>, got Range<Int>"},
		{"a Range argument to a generic List parameter",
			"  _ = first(0..3)\n",
			"argument 1: expected List<T>, got Range<Int>"},
		{"a Map argument to List.equal?",
			"  _ = List.equal?({\"a\" => 1}, [])\n",
			"argument 1: expected List<T>, got Map<String, Int>"},
		{"a piped Map into a generic List parameter",
			"  _ = {\"a\" => 1} |> first()\n",
			"`first` takes a List<T> as its first argument, but the pipe passes a Map<String, Int>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main() {\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts this program, which passes a non-List where a List is expected")
			}
			findDiag(t, errs, tc.want)
		})
	}

	src := "fn main() {\n  _ = 0..3 |> Iter.to_list() |> List.equal?([0, 1, 2])\n  _ = {\"a\" => 1} |> Iter.to_list() |> List.equal?([(\"a\", 1)])\n}\n"
	if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
		t.Fatalf("materializing a Range or a Map with Iter.to_list should check: %v", errs)
	}
}
