package analysis_test

import "testing"

// Map, Set and Vector have no empty constructor; their empty values are the
// literals. A call to one is the "type has no member" error, and its hint
// names the literal.
func TestCheck_EmptyConstructorNamesTheLiteral(t *testing.T) {
	cases := []struct {
		body, want, hint string
		col              int
	}{
		{"  _m: Map<String, Int> = Map.empty()\n", "type 'Map' has no member 'empty'", "write the empty map as %{}", 30},
		{"  _s: Set<Int> = Set.new()\n", "type 'Set' has no member 'new'", "write the empty set as %[]", 22},
		{"  _v: Vector<Int> = Vector.empty()\n", "type 'Vector' has no member 'empty'", "write the empty vector as #[]", 28},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main() {\n" + c.body + "}\n")
			if len(errs) == 0 {
				t.Fatalf("the checker accepts %q; the empty constructor must be an error", c.body)
			}
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly %q", errs, c.want)
			}
			e := errs[0]
			if e.Message != c.want || e.Line != 2 || e.Col != c.col {
				t.Errorf("error = %d:%d %q, want 2:%d %q", e.Line, e.Col, e.Message, c.col, c.want)
			}
			if got := hintsOf(e); got != c.hint {
				t.Errorf("hint = %q, want %q", got, c.hint)
			}
		})
	}
}

// A user type of the same name is not std's collection, so its missing
// member gets no literal hint.
func TestCheck_EmptyConstructorHintIsOnlyForStd(t *testing.T) {
	_, errs := checkSourceWithStdlib("struct Bag {\n  n: Int\n}\nfn main() {\n  _b = Bag.empty()\n}\n")
	if len(errs) != 1 || errs[0].Message != "type 'Bag' has no member 'empty'" {
		t.Fatalf("errors = %v, want the no-member error", errs)
	}
	if got := hintsOf(errs[0]); got != "" {
		t.Errorf("hint = %q, want none", got)
	}
}
