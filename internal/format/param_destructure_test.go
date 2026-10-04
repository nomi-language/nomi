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
	fmtContains(t, "fn f(Dur(x): Dur): Int { x }\n", "fn f(Dur(x)): Int")
}

func TestFmt_Distinct_AlreadyConcise(t *testing.T) {
	fmtEq(t, "fn f(Dur(x)): Int { x }\n", "fn f(Dur(x)): Int {\n    x\n}\n")
}

func TestFmt_TypedStruct_StripRedundantAnnotation(t *testing.T) {
	fmtContains(t, "fn f(Point{x, y}: Point): Int { x + y }\n", "fn f(Point{x, y}): Int")
}

func TestFmt_QualifiedEnum_StripRedundantAnnotation(t *testing.T) {
	fmtContains(t, "fn f(E.V(x): E): Int { x }\n", "fn f(E.V(x)): Int")
}

// --- fmt keeps non-redundant / load-bearing annotations ---

func TestFmt_Tuple_KeepsAnnotation(t *testing.T) {
	fmtContains(t, "fn f((a, b): (Int, Int)): Int { a + b }\n", "(a, b): (Int, Int)")
}

func TestFmt_AnonStruct_KeepsAnnotation(t *testing.T) {
	fmtContains(t, "fn f({x, y}: Point): Int { x + y }\n", "{x, y}: Point")
}

func TestFmt_Distinct_KeepsMismatchedAnnotation(t *testing.T) {
	// Annotation names a different type than the head — load-bearing, keep it.
	fmtContains(t, "fn f(Dur(x): other): Int { x }\n", "Dur(x): other")
}

// --- fmt for lambda params ---

func TestFmt_Lambda_Distinct_StripRedundantAnnotation(t *testing.T) {
	fmtContains(t, "f = |Dur(x): Dur| x\n", "|Dur(x)|")
}

func TestFmt_Lambda_Tuple_KeepsAnnotation(t *testing.T) {
	fmtContains(t, "f = |(a, b): (Int, Int)| a\n", "|(a, b): (Int, Int)|")
}

// --- round-trip stability ---

func TestFmt_RoundTrip_Stable(t *testing.T) {
	cases := []string{
		"fn f(Dur(x)): Int {\n    x\n}\n",
		"fn f((a, b): (Int, Int)): Int {\n    a + b\n}\n",
		"fn f(Point{x, y}): Int {\n    x + y\n}\n",
		"fn f({x, y}: Point): Int {\n    x + y\n}\n",
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
