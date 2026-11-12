package format

import (
	"strings"
	"testing"
)

// fmtEq formats src and checks the result equals want.
func fmtEq(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	if got != want {
		t.Errorf("Format(%q) =\n%q\nwant\n%q", src, got, want)
	}
}

// fmtContains formats src and checks the result contains want.
func fmtContains(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format(%q) error: %v", src, err)
	}
	if !strings.Contains(got, want) {
		t.Errorf("Format(%q) =\n%q\nshould contain %q", src, got, want)
	}
}

// --- fmt canonicalization: strip a redundant self-typing annotation ---

func TestFmt_Distinct_StripRedundantAnnotation(t *testing.T) {
	// `Dur(x): Dur` → `Dur(x)` (the head already names Dur).
	fmtContains(t, "fn f(Dur(x): Dur) -> Int\n    x\nend\n", "fn f(Dur(x)) -> Int")
}

func TestFmt_Distinct_AlreadyConcise(t *testing.T) {
	fmtEq(t, "fn f(Dur(x)) -> Int\n    x\nend\n", "fn f(Dur(x)) -> Int\n    x\nend\n")
}

func TestFmt_TypedStruct_StripRedundantAnnotation(t *testing.T) {
	fmtContains(t, "fn f(Point{x:, y:}: Point) -> Int\n    x + y\nend\n", "fn f(Point{x:, y:}) -> Int")
}

func TestFmt_QualifiedEnum_StripRedundantAnnotation(t *testing.T) {
	fmtContains(t, "fn f(E.V(x): E) -> Int\n    x\nend\n", "fn f(E.V(x)) -> Int")
}

// --- fmt keeps non-redundant / load-bearing annotations ---

func TestFmt_Tuple_KeepsAnnotation(t *testing.T) {
	fmtContains(t, "fn f((a, b): (Int, Int)) -> Int\n    a + b\nend\n", "(a, b): (Int, Int)")
}

func TestFmt_AnonStruct_KeepsAnnotation(t *testing.T) {
	fmtContains(t, "fn f({x:, y:}: Point) -> Int\n    x + y\nend\n", "{x:, y:}: Point")
}

func TestFmt_Distinct_KeepsMismatchedAnnotation(t *testing.T) {
	// Annotation names a different type than the head — load-bearing, keep it.
	fmtContains(t, "fn f(Dur(x): other) -> Int\n    x\nend\n", "Dur(x): other")
}

// A parameter written as its type alone, `fn stats(Request)`, stays the type
// alone, in every kind of parameter list, with a default too.
func TestFmt_TypeOnlyParam(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn stats(Request, n: Int) -> Int\n    n\nend\n", "fn stats(Request, n: Int) -> Int\n    n\nend\n"},
		{"f = fn Request -> 1\n", "f = fn Request -> 1\n"},
		{"interface I\n    fn g(v: self, Request) -> Int\nend\n", "interface I\n    fn g(v: self, Request) -> Int\nend\n"},
		{"fn f(Label = Label{}) -> Int\n    1\nend\n", "fn f(Label = Label{}) -> Int\n    1\nend\n"},
	}
	for _, c := range cases {
		fmtEq(t, c.src, c.want)
		if err := SameMeaning(c.src, c.want); err != nil {
			t.Errorf("SameMeaning(%q): %v", c.src, err)
		}
	}
}

// A head with nothing after it keeps its annotation: `fn f(Label)` is a
// parameter written as its type alone, which binds nothing, while
// `fn f(Label: Label)` is checked as a pattern against Label.
func TestFmt_BareHead_KeepsAnnotation(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn render(Label: Label) -> String\n    \"x\"\nend\n", "fn render(Label: Label) -> String\n    \"x\"\nend\n"},
		{"f = fn Label: Label -> 1\n", "f = fn Label: Label -> 1\n"},
		{"fn f(Label: Label = Label{}) -> Int\n    1\nend\n", "fn f(Label: Label = Label{}) -> Int\n    1\nend\n"},
		{"fn f(T: Int) -> Int\n    1\nend\n", "fn f(T: Int) -> Int\n    1\nend\n"},
		{"fn f(None: Maybe<Int>) -> Int\n    1\nend\n", "fn f(None: Maybe<Int>) -> Int\n    1\nend\n"},
		{"fn f(Some(x): Maybe<Int>) -> Int\n    x\nend\n", "fn f(Some(x): Maybe<Int>) -> Int\n    x\nend\n"},
		{"fn f(.V: E) -> Int\n    1\nend\n", "fn f(.V: E) -> Int\n    1\nend\n"},
		{"fn f(Dur as d: Dur) -> Int\n    1\nend\n", "fn f(Dur as d: Dur) -> Int\n    1\nend\n"},
	}
	for _, c := range cases {
		fmtEq(t, c.src, c.want)
		if err := SameMeaning(c.src, c.want); err != nil {
			t.Errorf("SameMeaning(%q): %v", c.src, err)
		}
	}
}

// Each strip is one SameMeaning accepts, on every kind of parameter list.
func TestFmt_StripRedundantAnnotation_SameMeaning(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn f(Dur(x): Dur) -> Int\n    x\nend\n", "fn f(Dur(x)) -> Int\n    x\nend\n"},
		{"fn f(Point{x:, y:}: Point) -> Int\n    x\nend\n", "fn f(Point{x:, y:}) -> Int\n    x\nend\n"},
		{"fn f(Point{}: Point) -> Int\n    1\nend\n", "fn f(Point{}) -> Int\n    1\nend\n"},
		{"fn f(E.V: E) -> Int\n    1\nend\n", "fn f(E.V) -> Int\n    1\nend\n"},
		{"fn f(Dur(x): Dur = Dur(1)) -> Int\n    x\nend\n", "fn f(Dur(x) = Dur(1)) -> Int\n    x\nend\n"},
		{"f = fn Dur(x): Dur -> x\n", "f = fn Dur(x) -> x\n"},
		{"interface I\n    fn g(Dur(x): Dur) -> Int\nend\n", "interface I\n    fn g(Dur(x)) -> Int\nend\n"},
	}
	for _, c := range cases {
		fmtEq(t, c.src, c.want)
		if err := SameMeaning(c.src, c.want); err != nil {
			t.Errorf("SameMeaning(%q): %v", c.src, err)
		}
	}
}

// A comment inside the annotation's parentheses keeps the annotation; one
// after it moves to the parameter.
func TestFmt_StripRedundantAnnotation_Comments(t *testing.T) {
	cases := []struct{ src, want string }{
		{"fn f(Dur(x): ( // why\nDur)) -> Int\n    x\nend\n", "fn f(Dur(x): (\n    // why\n    Dur\n)) -> Int\n    x\nend\n"},
		{"fn f(Dur(x): Dur // why\n) -> Int\n    x\nend\n", "fn f(\n    Dur(x), // why\n) -> Int\n    x\nend\n"},
	}
	for _, c := range cases {
		fmtEq(t, c.src, c.want)
		if err := SameMeaning(c.src, c.want); err != nil {
			t.Errorf("SameMeaning(%q): %v", c.src, err)
		}
	}
}

// --- fmt for lambda params ---

func TestFmt_Lambda_Distinct_StripRedundantAnnotation(t *testing.T) {
	fmtContains(t, "f = fn Dur(x): Dur -> x\n", "fn Dur(x) ->")
}

func TestFmt_Lambda_Tuple_KeepsAnnotation(t *testing.T) {
	fmtContains(t, "f = fn (a, b): (Int, Int) -> a\n", "fn (a, b): (Int, Int) ->")
}

// --- round-trip stability ---

func TestFmt_RoundTrip_Stable(t *testing.T) {
	cases := []string{
		"fn f(Dur(x)) -> Int\n    x\nend\n",
		"fn f((a, b): (Int, Int)) -> Int\n    a + b\nend\n",
		"fn f(Point{x:, y:}) -> Int\n    x + y\nend\n",
		"fn f({x:, y:}: Point) -> Int\n    x + y\nend\n",
	}
	for _, src := range cases {
		got, err := Format(src)
		if err != nil {
			t.Fatalf("Format(%q) error: %v", src, err)
		}
		if got != src {
			t.Errorf("not idempotent:\nin:  %q\nout: %q", src, got)
		}
		// Second pass must equal the first.
		got2, _ := Format(got)
		if got2 != got {
			t.Errorf("second pass differs:\n1: %q\n2: %q", got, got2)
		}
	}
}
