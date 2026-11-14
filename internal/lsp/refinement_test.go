package lsp

import (
	"strings"
	"testing"
)

// Refinements in the editor (spec §4 *Refinement types*): a literal checked
// as one says so, the type shows its rule and chain, and `try_from` is a
// member to complete.

const refinementDoc = `type NonNegative Float when fn x -> x >= 0.0
type Positive NonNegative when fn x -> x > 0.0

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
	for _, want := range []string{"type Positive NonNegative when fn x -> x > 0.0", "`Positive` refines `NonNegative` refines `Float`"} {
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
