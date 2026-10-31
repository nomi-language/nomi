package analysis_test

import "testing"

// A list, vector, set or map literal takes spreads anywhere, any number of
// them. A map spread has the literal's type; a list, vector or set spread is
// any Iter of the literal's elements. The elements have its element type.

func TestCheck_CollectionSpreads_Accepts(t *testing.T) {
	cases := map[string]string{
		"list, spread first":           "  xs = [1]\n  _a: List<Int> = [..xs, 2]\n",
		"list, spread between":         "  xs = [1]\n  _a: List<Int> = [0, ..xs, 2]\n",
		"list, two spreads":            "  xs = [1]\n  _a: List<Int> = [..xs, ..xs]\n",
		"list, empty spread":           "  _a: List<Int> = [..[], 1, ..[2]]\n",
		"vector, between":              "  v = #[1]\n  _a: Vector<Int> = #[0, ..v, 2]\n",
		"vector, two spreads":          "  v = #[1]\n  _a: Vector<Int> = #[..v, 2, ..v]\n",
		"set, spread last":             "  s = %[1]\n  _a: Set<Int> = %[0, ..s]\n",
		"set, two spreads":             "  s = %[1]\n  _a: Set<Int> = %[..s, ..%[2]]\n",
		"list, spread of a Maybe":      "  m = Some(1)\n  _a: List<Int> = [0, ..m, ..None]\n",
		"list, spread of an Iter":      "  _a: List<Int> = [0, ..[1, 2] |> Iter.map(fn x -> x * 2)]\n",
		"list, spread of a set":        "  _a = [0, ..%[1]]\n",
		"vector, spread of a list":     "  _a: Vector<Int> = #[1, ..[2], 3]\n",
		"vector, only an Iter spread":  "  _a = #[..Some(1)]\n",
		"set, spread of a list":        "  _a: Set<Int> = %[1, ..[2]]\n",
		"set, an Iter spread then set": "  _a = %[..Some(1), ..%[2]]\n",
		"map, spread last":             "  m = %{\"a\" => 1}\n  _a: Map<String, Int> = %{\"a\" => 0, ..m}\n",
		"map, two spreads":             "  m = %{\"a\" => 1}\n  _a: Map<String, Int> = %{..m, ..%{\"b\" => 2}}\n",
		"map, open first":              "  m = %{\"a\" => 1}\n  _a: Map<String, Int> = %{..%{}, ..m}\n",
		"map, update after spreads": "  m = %{\"a\" => %{\"x\" => 1}}\n" +
			"  _a: Map<String, Map<String, Int>> = %{\"b\" => %{}, ..m, \"a\" => %{.., \"y\" => 2}}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main() {\n" + body + "}\n")
			if len(errs) != 0 {
				t.Fatalf("errors = %v, want none", errs)
			}
		})
	}
}

// Each refusal is one error, with its exact message and position.
func TestCheck_CollectionSpreads_Refuses(t *testing.T) {
	cases := map[string]struct {
		body      string
		want      string
		line, col int
	}{
		"a list spread of another element": {"  xs = [1]\n  _a = [..xs, ..[\"b\"]]\n",
			"list spread element type mismatch: Int vs String", 3, 17},
		"a list spread that is no Iter": {"  n = 1\n  _a = [0, ..n]\n",
			"list spread `..` requires an Iter of the list's elements, got Int", 3, 14},
		"a list spread of an Iter of another element": {"  _a = [0, ..Some(\"b\")]\n",
			"list spread element type mismatch: Int vs String", 2, 14},
		"a vector spread that is no Iter": {"  v = #[1]\n  _a = #[1, ..v, ..2]\n",
			"vector spread `..` requires an Iter of the vector's elements, got Int", 3, 18},
		"a vector spread of a list of another element": {"  v = #[1]\n  _a = #[1, ..v, ..[\"b\"]]\n",
			"vector spread element type mismatch: expected Int, got String", 3, 18},
		"a set spread after an element, that is no Iter": {"  n = 1\n  _a = %[1, ..n]\n",
			"set spread `..` requires an Iter of the set's elements, got Int", 3, 13},
		"two map spreads of different types": {"  m = %{\"a\" => 1}\n  o = %{1 => 2}\n  _a = %{..m, ..o}\n",
			"map spread type mismatch: expected Map<String, Int>, got Map<Int, Int>", 4, 15},
		"a map entry before every spread has no current value": {"  m = %{\"a\" => %{\"x\" => 1}}\n  _a = %{\"a\" => %{.., \"y\" => 2}, ..m}\n",
			"a bare `..` has no value to update here", 3, 19},
		"one set spread and no element": {"  s = %[1]\n  _a = %[..s]\n",
			"a set spread with no elements is its base value; write `s`", 3, 10},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main() {\n" + c.body + "}\n")
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly %q", errs, c.want)
			}
			e := errs[0]
			if e.Message != c.want || e.Line != c.line || e.Col != c.col {
				t.Errorf("error = %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, c.line, c.col, c.want)
			}
		})
	}
}
