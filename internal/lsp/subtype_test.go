package lsp

import (
	"strings"
	"testing"
)

// Subtypes in the editor (spec §4 *Subtypes*): a literal checked
// as one says so, the type shows its rule and chain, and `try_from` is a
// member to complete.

const subtypeDoc = `subtype NonNegative Float when fn x -> x >= 0.0
subtype Positive NonNegative when fn x -> x > 0.0

fn main()
    p: Positive = 2.5
    _ = Positive.try_from(1.0)
    _ = p
end
`

func TestSubtype_HoverOnALiteralNamesItsCheck(t *testing.T) {
	input, pos := hoverMarkerPosition(t, strings.Replace(subtypeDoc, "= 2.5", "= ▮2.5", 1))
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

func TestSubtype_HoverOnTheTypeShowsItsRuleAndChain(t *testing.T) {
	input, pos := hoverMarkerPosition(t, strings.Replace(subtypeDoc, "p: Positive", "p: ▮Positive", 1))
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

func TestSubtype_CompletionOffersTryFrom(t *testing.T) {
	src := strings.Replace(subtypeDoc, "_ = Positive.try_from(1.0)", "_ = Positive.", 1)
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
func TestSubtype_CompletionOffersTryFromOnlyOverASubtype(t *testing.T) {
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

func TestSubtype_HoverOnASubtypeOfSeveralParentsShowsThemAll(t *testing.T) {
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

// A generic subtype's hover shows its type parameters, their bounds and
// its rule, and its chain at the arguments the hovered use names.
func TestSubtype_HoverOnAGenericSubtype(t *testing.T) {
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

// subtypeStdDoc is a std file, which names what it imports by hand, that
// declares a subtype and calls the functions written for it.
const subtypeStdDoc = `import
    std::int.Int
    std::option.Option
end

pub subtype Positive Int when fn n -> n > 0

pub fn one() -> Positive
    Positive.unchecked(1)
end

pub fn positive(n: Int) -> Option<Positive>
    Positive.try_from(n)
end
`

const subtypeStdURI = "file:///path/to/std/positive.nomi"

// In a std document, as in any other, hover on a call of a subtype's
// `unchecked` or `try_from` shows the function's signature.
func TestSubtype_HoverOnAWrittenFunctionInAStdDocument(t *testing.T) {
	for name, want := range map[string]string{
		"unchecked": "fn unchecked(value: Int) -> Positive",
		"try_from":  "fn try_from(value: Int) -> Option<Positive>",
	} {
		input, pos := hoverMarkerPosition(t, strings.Replace(subtypeStdDoc, "Positive."+name+"(", "Positive.▮"+name+"(", 1))
		s := NewServer()
		s.docs.Open(subtypeStdURI, input)
		doc := s.docs.Get(subtypeStdURI)
		if len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
			t.Fatalf("the std document reports %v %v", doc.Errors, doc.Analysis.TypeErrors)
		}
		sym := doc.Analysis.SymbolAt(pos)
		if sym == nil {
			t.Fatalf("no symbol at the %s call", name)
		}
		if got := renderHover(sym); !strings.Contains(got, want) {
			t.Errorf("hover on %s lacks %q:\n%s", name, want, got)
		}
	}
}

// Completion after the subtype's name in the std file that declares it
// offers both functions.
func TestSubtype_CompletionOffersTheWrittenFunctionsInAStdDocument(t *testing.T) {
	src := strings.Replace(subtypeStdDoc, "    Positive.unchecked(1)", "    Positive.", 1)
	s := NewServer()
	s.docs.Open(subtypeStdURI, src)
	line := strings.Count(src[:strings.Index(src, "    Positive.\n")], "\n")
	labels := completionLabels(t, s, subtypeStdURI, line, len("    Positive."))
	if !labels["try_from"] || !labels["unchecked"] {
		t.Errorf("completion after `Positive.` in its std file offers try_from %v and unchecked %v: %v", labels["try_from"], labels["unchecked"], labels)
	}
}
