package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"testing"
)

// The stdlib half of TestCheck_StructLit_StaysLenientWhereTheLanguageIs.
//
// These five rows are the ones whose field TYPE is a stdlib name — `Debug`,
// `Display`, `Iter<Int>`, the universal `Struct`, and `Maybe<Int>` fed `None`.
// They are the rows that carry the leniency argument, because each is a value
// that is NOT equal to its field's declared type and is admitted anyway:
// `argMatchesParam` reaches `ifaceParamAdmits` for the four interface-typed
// ones and unifies through a fresh type variable for `None`. A bare
// `TypesEqual` would reject all five.
//
// They are here rather than beside the others because `checkSource` builds a
// file with no stdlib, so `Debug` does not resolve in it at all. Each was
// measured in both spellings at the twin — `Cfg{f: v}` and `Cfg({f: v})` —
// and both accepted all five.
func TestCheck_StructLit_StdlibTypedFieldsStayLenient(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "a Debug field holding an Int — Debug is universal",
			src:  "struct Cfg {\n  d: Debug\n}\nfn main() {\n  _a = Cfg{d: 5}\n}\n",
		},
		{
			name: "a Display field holding an Int",
			src:  "struct Cfg {\n  d: Display\n}\nfn main() {\n  _a = Cfg{d: 5}\n}\n",
		},
		{
			name: "an Iter<Int> field holding a list — interface widening",
			src:  "struct Cfg {\n  i: Iter<Int>\n}\nfn main() {\n  _a = Cfg{i: [1, 2]}\n}\n",
		},
		{
			name: "a universal Struct field holding a named struct",
			src:  "struct P {\n  x: Int\n}\nstruct Cfg {\n  s: Struct\n}\nfn main() {\n  _a = Cfg{s: P{x: 1}}\n}\n",
		},
		{
			name: "None at Maybe<Int>",
			src:  "struct Cfg {\n  m: Maybe<Int>\n}\nfn main() {\n  _a = Cfg{m: None}\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// THE FIELD LABEL'S HOVER IS THE SAME IN BOTH SPELLINGS, which it was not
// before. `recordLitFieldLabelTypes` re-records the label with the field's
// INSTANTIATED type, and it can only do that after the field loop, because
// the loop is what solves the substitution. The literal-attach form already
// called it there. The constructor-call record form registered its own symbol
// INSIDE the loop carrying the RAW declared type, so for a generic struct it
// wrote down the unbound parameter:
//
//	Box{v: 1}     hovered  v: Int
//	Box({v: 1})   hovered  v: T
//
// Sharing the validator put both on one call site after the loop. This test
// is what kills that mutant: removing the mint branch from
// `recordLitFieldLabelTypes` leaves the call form with NO symbol at all, and
// leaving the registration in the loop brings back `v: T`.
func TestCheck_StructLit_FieldLabelHoverAgreesAcrossSpellings(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line int
		col  int
	}{
		// `  _a = Box{v: 1}` — the label `v` starts at column 12.
		{name: "literal-attach", src: "struct Box<T> {\n  v: T\n}\nfn main() {\n  _a = Box{v: 1}\n}\n", line: 5, col: 12},
		// `  _a = Box({v: 1})` — one column further, past the paren.
		{name: "constructor-call record form", src: "struct Box<T> {\n  v: T\n}\nfn main() {\n  _a = Box({v: 1})\n}\n", line: 5, col: 13},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa, _ := checkSourceWithStdlib(tc.src)
			sym, ok := fa.References[analysis.Pos{Line: tc.line, Col: tc.col}]
			if !ok || sym == nil {
				t.Fatalf("no reference at the field label (%d,%d); hover shows nothing", tc.line, tc.col)
			}
			if sym.Name != "v" {
				t.Fatalf("reference at (%d,%d) is %q, not the field label", tc.line, tc.col, sym.Name)
			}
			if sym.Type == nil {
				t.Fatalf("the field label carries no type; hover shows just the name")
			}
			if got := sym.Type.String(); got != "Int" {
				t.Errorf("field label hover: got %q, want %q — the substitution did not reach it", got, "Int")
			}
		})
	}
}
