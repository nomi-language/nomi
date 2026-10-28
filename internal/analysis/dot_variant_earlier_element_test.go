package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// A bare variant in a collection literal with no expected type resolves
// against the enum an earlier element of the same position fixes, as
// Swift's `[Color.red, .green]` does (spec, "Variant resolution").

const earlierElemDecls = "enum N {\n  A\n  C\n  G\n}\n\nenum M {\n  A\n  C\n  Z\n}\n\nderive Equatable for N\nderive Hashable for N\n\n"

func checkEarlierElem(t *testing.T, body string) []string {
	t.Helper()
	_, errs := checkSourceWithStdlib(earlierElemDecls + "fn main() {\n" + body + "\n}\n")
	var out []string
	for _, e := range errs {
		out = append(out, diagText(e))
	}
	return out
}

func TestDotVariant_EarlierElementSetsTheEnum(t *testing.T) {
	cases := map[string]string{
		"list":           "  xs = [N.A, .C, .G]\n  _ = xs",
		"vector":         "  xs = #[N.A, .C]\n  _ = xs",
		"set":            "  xs = %[N.A, .C]\n  _ = xs",
		"map keys":       "  m = %{N.A => 0, .C => 1}\n  _ = m",
		"map values":     "  m = %{\"a\" => N.A, \"c\" => .C}\n  _ = m",
		"list of maps":   "  ms = [%{N.A => 0}, %{.C => 1, .G => 2}]\n  _ = ms",
		"list of tuples": "  ts = [#(N.A, 1), #(.C, 2)]\n  _ = ts",
		"payload":        "  ms = [Some(N.A), Some(.C)]\n  _ = ms",
		"spread heads":   "  xs = [N.A, .C]\n  ys = [N.G, .A, ..xs]\n  _ = ys",
		"nested list":    "  xs = [[N.A], [.C, .G]]\n  _ = xs",
		// `[]` leaves its element type open until `[N.A]` solves it to N.
		"empty first list":      "  xs = [[], [N.A], [.C]]\n  _ = xs",
		"empty first map value": "  m = %{\"a\" => [], \"b\" => [N.A], \"c\" => [.C]}\n  _ = m",
		"None first":            "  ms = [None, Some(N.A), Some(.C)]\n  _ = ms",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if errs := checkEarlierElem(t, body); len(errs) != 0 {
				t.Fatalf("the checker rejects a bare variant after a qualified element:\n%s", strings.Join(errs, "\n"))
			}
		})
	}
}

// A `once` with an empty first row, as protein-translation's table was
// written: the rows after `[]` take N from `[N.A]`.
func TestDotVariant_EmptyFirstRowInAOnce(t *testing.T) {
	_, errs := checkSourceWithStdlib(earlierElemDecls + "once rows = [[], [N.A], [.C]]\n\nfn main() {\n  _ = rows\n}\n")
	if len(errs) != 0 {
		var out []string
		for _, e := range errs {
			out = append(out, diagText(e))
		}
		t.Fatalf("the checker rejects the table:\n%s", strings.Join(out, "\n"))
	}
}

// A subtests table is a list of tuples with no expected type, so a map in a
// row's Ok takes its keys' enum from its first key.
func TestDotVariant_EarlierElementSetsTheEnumInASubtestsTable(t *testing.T) {
	src := earlierElemDecls +
		"fn counts(s: String) -> Result<Map<N, Int>, String> {\n  _ = s\n  Ok(%{N.A => 0, .C => 0})\n}\n\n" +
		"test \"counts\" {\n" +
		"  cases = [\n" +
		"    #(\"empty\", \"\", Ok(%{N.A => 0, .C => 0})),\n" +
		"    #(\"bad\", \"X\", Err(\"bad\")),\n" +
		"  ]\n" +
		"  subtests cases, #(name, strand, expected), name {\n" +
		"    assert counts(strand) == expected\n" +
		"  }\n" +
		"}\n"
	_, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("the checker rejects the table: %v", errs)
	}
}

// Resolution runs left to right: a bare variant before any qualified element
// of its position resolves against nothing and keeps its error, with a hint
// to qualify the first element.
func TestDotVariant_BareFirstElementKeepsItsError(t *testing.T) {
	errs := checkEarlierElem(t, "  xs = [.A, N.C]\n  _ = xs")
	want := ".A requires a determinable enum type at this position; annotate the binding or qualify the variant\n" +
		"help: qualify the first element of the literal, and the bare variants after it take its enum"
	for _, e := range errs {
		if e == want {
			return
		}
	}
	t.Fatalf("want %q, got:\n%s", want, strings.Join(errs, "\n"))
}

// An element with no enum in its type sets nothing, and the bare variant
// after it keeps its error without the literal hint.
func TestDotVariant_EarlierElementWithoutAnEnumSetsNothing(t *testing.T) {
	errs := checkEarlierElem(t, "  xs = [1, .C]\n  _ = xs")
	want := ".C requires a determinable enum type at this position; annotate the binding or qualify the variant"
	for _, e := range errs {
		if e == want {
			return
		}
	}
	t.Fatalf("want %q, got:\n%s", want, strings.Join(errs, "\n"))
}

// The earlier element's enum is the one a later bare variant must name.
func TestDotVariant_EarlierElementsEnumLacksTheVariant(t *testing.T) {
	errs := checkEarlierElem(t, "  xs = [N.A, .Z]\n  _ = xs")
	want := "no variant 'Z' on enum N"
	for _, e := range errs {
		if strings.HasPrefix(e, want) {
			return
		}
	}
	t.Fatalf("want %q, got:\n%s", want, strings.Join(errs, "\n"))
}

// An expected type wins over the first element: the qualified element of
// another enum is a mismatch, and the bare one resolves against the
// expected enum.
func TestDotVariant_ExpectedTypeWinsOverTheFirstElement(t *testing.T) {
	errs := checkEarlierElem(t, "  xs: List<M> = [N.A, .Z]\n  _ = xs")
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "list element type mismatch: expected M, got N") {
		t.Fatalf("want only the mismatch at N.A, got:\n%s", strings.Join(errs, "\n"))
	}
}

const tableDecls = "enum Size {\n  Small\n  Large\n}\n\nenum Color {\n  Red\n  Blue\n}\n\n"

func checkTable(t *testing.T, body string) []string {
	t.Helper()
	_, errs := checkSourceWithStdlib(tableDecls + "fn main() {\n" + body + "\n}\n")
	var out []string
	for _, e := range errs {
		out = append(out, diagText(e))
	}
	return out
}

// A table whose first row has a bare variant no enum resolves reports that
// variant once, with the qualified name to write, and nothing for the bare
// variants of later rows: once the first row's is qualified, they resolve.
// Each later row used to report its own, and its `[.Blue]` the hint to
// qualify the first element of a list it is already after.
func TestDotVariant_AnUnresolvedFirstRowIsReportedOnce(t *testing.T) {
	want := ".Small requires a determinable enum type at this position; annotate the binding or qualify the variant\n" +
		"help: qualify `Size.Small` in the literal's first element, and the bare variants at its place in the later elements take its enum"
	cases := map[string]string{
		"two rows":   "  cases = [#(.Small, [Color.Red]), #(.Large, [.Blue])]\n  _ = cases",
		"three rows": "  cases = [\n    #(.Small, [Color.Red]),\n    #(.Large, [.Blue]),\n    #(.Small, [.Red, .Blue]),\n  ]\n  _ = cases",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := checkTable(t, body)
			if len(got) == 0 {
				t.Fatal("the checker accepts a bare variant that no enum resolves")
			}
			if len(got) != 1 || got[0] != want {
				t.Fatalf("want the one error\n%s\ngot %d:\n%s", want, len(got), strings.Join(got, "\n"))
			}
		})
	}
}

// A `subtests` table's rows are not reported as undetermined on top of it.
func TestDotVariant_AnUnresolvedFirstRowOfASubtestsTableIsReportedOnce(t *testing.T) {
	src := tableDecls + "test \"sizes\" {\n" +
		"  cases = [\n    #(\"a\", .Small, [Color.Red]),\n    #(\"b\", .Large, [.Blue]),\n    #(\"c\", .Small, [.Red, .Blue]),\n  ]\n" +
		"  subtests cases, #(name, size, colors), name {\n    assert size == Size.Small or colors == [Color.Red]\n  }\n}\n"
	got := frontEndErrors(t, src, frontend.Mode{Tests: true})
	want := ":13:12: .Small requires a determinable enum type at this position; annotate the binding or qualify the variant\n" +
		":13:12: help: qualify `Size.Small` in the literal's first element, and the bare variants at its place in the later elements take its enum"
	if len(got) != 2 || strings.Join(got, "\n") != want {
		t.Fatalf("want the one error\n%s\ngot %d:\n%s", want, len(got), strings.Join(got, "\n"))
	}
}

// Qualifying the first row's variant resolves every row.
func TestDotVariant_AQualifiedFirstRowResolvesTheTable(t *testing.T) {
	if got := checkTable(t, "  cases = [#(Size.Small, [Color.Red]), #(.Large, [.Blue])]\n  _ = cases"); len(got) != 0 {
		t.Fatalf("the checker rejects the table:\n%s", strings.Join(got, "\n"))
	}
}

// A refusal against a type that is not an enum does not follow from an
// earlier element's, and a later row still reports it.
func TestDotVariant_AVariantAgainstANonEnumIsStillReported(t *testing.T) {
	got := checkTable(t, "  cases = [#(.Small, 1), #(.Large, Int.abs(.Red))]\n  _ = cases")
	if len(got) != 2 || !strings.HasPrefix(got[0], ".Small requires a determinable enum type") ||
		got[1] != ".Red resolves only to enum variants, but the expected type at this position is Int" {
		t.Fatalf("want .Small's refusal and .Red's, got %d:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// An anonymous struct literal after an element of a named struct builds that
// struct, as it would under an annotation (spec, "Variant resolution").
func TestStructLit_EarlierElementSetsTheStruct(t *testing.T) {
	decls := "struct Key {\n  a: Int\n  b: Int\n}\n\n"
	cases := map[string]string{
		"list":           "  xs = [#Key{a: 1, b: 2}, #{a: 3, b: 4}]\n  _ = xs",
		"vector":         "  xs = #[#Key{a: 1, b: 2}, #{a: 3, b: 4}]\n  _ = xs",
		"map values":     "  m = %{\"k\" => #Key{a: 1, b: 2}, \"j\" => #{a: 3, b: 4}}\n  _ = m",
		"list of tuples": "  ts = [#(#Key{a: 1, b: 2}, \"x\"), #(#{a: 3, b: 4}, \"y\")]\n  _ = ts",
		"payload":        "  ms = [Some(#Key{a: 1, b: 2}), Some(#{a: 3, b: 4})]\n  _ = ms",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "fn main() {\n" + body + "\n}\n")
			for _, e := range errs {
				t.Errorf("the checker rejects an anonymous struct literal after a named one: %s", diagText(e))
			}
		})
	}
}

// Left to right only, and the literal must still fit the struct: a field the
// struct lacks is an error at the anonymous literal.
func TestStructLit_EarlierElementStillChecksTheFields(t *testing.T) {
	decls := "struct Key {\n  a: Int\n  b: Int\n}\n\n"
	for name, c := range map[string]struct{ body, want string }{
		"anonymous first": {"  xs = [#{a: 3, b: 4}, #Key{a: 1, b: 2}]\n  _ = xs", "list element type mismatch"},
		"unknown field":   {"  xs = [#Key{a: 1, b: 2}, #{a: 3, c: 4}]\n  _ = xs", "c"},
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "fn main() {\n" + c.body + "\n}\n")
			if len(errs) == 0 {
				t.Fatalf("the checker admits %q", c.body)
			}
			if got := diagText(errs[0]); !strings.Contains(got, c.want) {
				t.Errorf("error %q does not mention %q", got, c.want)
			}
		})
	}
}
