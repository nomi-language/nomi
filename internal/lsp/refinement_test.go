package lsp

import (
	"strings"
	"testing"
)

// Refinements in the editor (spec §4 *Subtypes*): a literal checked
// as one says so, the type shows its rule and chain, and `try_from` is a
// member to complete.

const refinementDoc = `subtype NonNegative Float when fn x -> x >= 0.0
subtype Positive NonNegative when fn x -> x > 0.0

fn main()
    p: Positive = 2.5
    _ = Positive.try_from(1.0)
    _ = p
end
`

func TestRefinement_HoverOnALiteralNamesItsCheck(t *testing.T) {
	input, pos := hoverMarkerPosition(t, strings.Replace(refinementDoc, "= 2.5", "= ▮2.5", 1))
	s := NewServer()
	s.docs.Open("file:///test.nomi", input)
	doc := s.docs.Get("file:///test.nomi")
	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at the literal")
	}
	got := renderHover(sym)
	for _, want := range []string{"2.5: Positive", "checked as a Positive", "fn x -> x > 0.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("hover on the literal lacks %q:\n%s", want, got)
		}
	}
}

func TestRefinement_HoverOnTheTypeShowsItsRuleAndChain(t *testing.T) {
	input, pos := hoverMarkerPosition(t, strings.Replace(refinementDoc, "p: Positive", "p: ▮Positive", 1))
	s := NewServer()
	s.docs.Open("file:///test.nomi", input)
	doc := s.docs.Get("file:///test.nomi")
	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at the type name")
	}
	got := renderHover(sym)
	for _, want := range []string{"subtype Positive NonNegative when fn x -> x > 0.0", "`Positive` is a subtype of `NonNegative` is a subtype of `Float`"} {
		if !strings.Contains(got, want) {
			t.Errorf("hover on the type lacks %q:\n%s", want, got)
		}
	}
}

func TestRefinement_CompletionOffersTryFrom(t *testing.T) {
	src := strings.Replace(refinementDoc, "_ = Positive.try_from(1.0)", "_ = Positive.", 1)
	s := NewServer()
	uri := "file:///test.nomi"
	s.docs.Open(uri, src)
	labels := completionLabels(t, s, uri, 5, len("    _ = Positive."))
	if !labels["try_from"] {
		t.Errorf("completion after `Positive.` lacks try_from: %v", labels)
	}
	if labels["unchecked"] != true {
		// unchecked is the declaring file's, and this is it.
		t.Errorf("completion in the declaring file lacks unchecked: %v", labels)
	}
}

// A `type` gets `try_from` and `unchecked` only when its inner type
// resolves to a subtype: one over a plain type offers neither, and one over
// a subtype a `typealias` names offers both.
func TestRefinement_CompletionOffersTryFromOnlyOverASubtype(t *testing.T) {
	for _, tc := range []struct {
		name, decl string
		want       bool
	}{
		{"a prelude type that is no subtype", "type Wrap Ordering\n", false},
		{"a subtype through an alias", "subtype Small Int when fn n -> n < 10\n\ntypealias Tiny Small\n\ntype Wrap Tiny\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.decl + "\nfn main()\n    _ = Wrap.\nend\n"
			line := strings.Count(tc.decl, "\n") + 2
			s := NewServer()
			uri := "file:///test.nomi"
			s.docs.Open(uri, src)
			labels := completionLabels(t, s, uri, line, len("    _ = Wrap."))
			if labels["try_from"] != tc.want || labels["unchecked"] != tc.want {
				t.Errorf("completion after `Wrap.` offers try_from %v and unchecked %v, want %v: %v", labels["try_from"], labels["unchecked"], tc.want, labels)
			}
		})
	}
}

func TestRefinement_HoverOnARefinementOfSeveralParentsShowsThemAll(t *testing.T) {
	src := "subtype NonNeg Int when fn n -> n >= 0\nsubtype NonZ Int when fn n -> n != 0\nsubtype Pos NonNeg and NonZ when fn n -> n > 0\n\nfn main()\n    p: ▮Pos = 2\n    _ = p\nend\n"
	input, pos := hoverMarkerPosition(t, src)
	s := NewServer()
	s.docs.Open("file:///test.nomi", input)
	doc := s.docs.Get("file:///test.nomi")
	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at the type name")
	}
	got := renderHover(sym)
	for _, want := range []string{"subtype Pos NonNeg and NonZ when fn n -> n > 0", "`Pos` is a subtype of `NonNeg` and `NonZ`, and through them `Int`"} {
		if !strings.Contains(got, want) {
			t.Errorf("hover on the type lacks %q:\n%s", want, got)
		}
	}
}

// A generic refinement's hover shows its type parameters, their bounds and
// its rule, and its chain at the arguments the hovered use names.
func TestRefinement_HoverOnAGenericRefinement(t *testing.T) {
	const src = `subtype Ascending<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)

fn main()
    xs: Ascending<Int> = [1, 2]
    _ = xs
end
`
	input, pos := hoverMarkerPosition(t, strings.Replace(src, "xs: Ascending", "xs: ▮Ascending", 1))
	s := NewServer()
	s.docs.Open("file:///test.nomi", input)
	doc := s.docs.Get("file:///test.nomi")
	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		t.Fatal("no symbol at the type name")
	}
	got := renderHover(sym)
	for _, want := range []string{"subtype Ascending<T> List<T> where T: Comparable when fn xs ->", "*subtype*", "`Ascending.try_from(...)`"} {
		if !strings.Contains(got, want) {
			t.Errorf("hover on the type lacks %q:\n%s", want, got)
		}
	}
}
