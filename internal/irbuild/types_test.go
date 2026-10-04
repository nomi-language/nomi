package irbuild

import (
	"strings"
	"testing"
)

// --- the pinned programs ----------------------------------------------------
//
// These pin the named-type lowering byte for byte, independently
// of the corpus sweep. The sweep measures coverage; a measurement that happens
// to be 8 files must not be able to stand in for correctness.

// --- identity ---------------------------------------------------------------

// --- the enum representation, enforced --------------------------------------

// TestEnumSlotsDedupByGoTypeNotLayout pins that an enum's payload slots are
// shared by KIND and never by layout: `Int` and `Float` have identical layout,
// and a dedup keyed on layout would merge them into one field.
func TestEnumSlotsDedupByGoTypeNotLayout(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		enum  string
		slots []string
		why   string
	}{
		{
			name:  "identical layout different type gets two slots",
			src:   "enum Mixed {\n  Whole Int\n  Fraction Float\n}\n",
			enum:  "Mixed",
			slots: []string{"Int", "Float"},
			why:   "Int and Float are both 8 bytes; merging them truncates",
		},
		{
			name:  "same type across variants shares one slot",
			src:   "enum Reading {\n  Absent\n  Celsius Int\n  Fahrenheit Int\n}\n",
			enum:  "Reading",
			slots: []string{"Int"},
			why:   "only one variant is live at a time",
		},
		{
			name:  "same type within one variant needs two slots",
			src:   "enum Box {\n  Rect {w: Int, h: Int}\n}\n",
			enum:  "Box",
			slots: []string{"Int", "Int"},
			why:   "a struct-shaped variant's fields are live together",
		},
		{
			name:  "a bare enum has no storage at all",
			src:   "enum Mode {\n  Up\n  Down\n}\n",
			enum:  "Mode",
			slots: nil,
			why:   "no payload, so nothing but the tag",
		},
		{
			name:  "a zero-sized payload gets no slot",
			src:   "struct Nothing {\n}\n\nenum Maybe0 {\n  Absent\n  embeds Nothing\n}\n",
			enum:  "Maybe0",
			slots: nil,
			why:   "a zero-sized value carries no information to store",
		},
		// Function types. A tag alone cannot be the dedup key when one tag
		// covers many Go types: every function type shares
		// tagFunc, so an identity that stopped at the tag would merge these
		// two into one field and recover an `(Int) -> Int` out of storage
		// holding a `(String) -> Bool`. Interning on the rendered Go type is
		// what keeps them apart; see composite.go.
		//
		// Struct-shaped rather than positional because the front end mis-parses
		// a POSITIONAL function payload — `Num ((Int) -> Int)` reports "impl
		// function 'inspect': return type Bool does not match interface
		// 'Debug'", reading the payload's `->` as the synthesized Debug impl's
		// return. A front-end bug; the named-field spelling type-checks and
		// exercises the same slot rule.
		{
			name:  "two function types get two slots",
			src:   "enum Handler {\n  Num {f: (Int) -> Int}\n  Text {g: (String) -> Bool}\n}\n",
			enum:  "Handler",
			slots: []string{"(Int) -> Int", "(String) -> Bool"},
			why:   "one tag, two Go types; merging them recovers the wrong signature",
		},
		{
			name:  "the same function type shares one slot",
			src:   "enum Either {\n  Left {f: (Int) -> Int}\n  Right {g: (Int) -> Int}\n}\n",
			enum:  "Either",
			slots: []string{"(Int) -> Int"},
			why:   "identical Go type, and only one variant is live at a time",
		},
		// Interface types, and the opposite lesson to the two above. Every
		// interface lowers to ONE Go type (`rt.Dyn`), so an
		// identity derived from the rendered Go type would give these two
		// payloads one slot — which is SOUND for storage and lossy for
		// dispatch, the worst pair, because nothing fails and the wrong
		// table answers. An existential's identity is therefore the
		// *ifaceDef, nominal like a struct's, and the cost is a second
		// `rt.Dyn` field that is never read. An extra field, never a
		// truncation. See impl.go.
		{
			name: "two interface types get two slots",
			src: "interface Speech {\n  fn speak(v: self): String\n}\n\n" +
				"interface Greeter {\n  fn greet(v: self): String\n}\n\n" +
				"enum Either {\n  Talks {s: Speech}\n  Greets {g: Greeter}\n}\n",
			enum:  "Either",
			slots: []string{"Speech", "Greeter"},
			why:   "two Nomi types sharing one Go type must still be two identities",
		},
		{
			name: "the same interface shares one slot",
			src: "interface Speech {\n  fn speak(v: self): String\n}\n\n" +
				"enum Pair {\n  First {s: Speech}\n  Second {t: Speech}\n}\n",
			enum:  "Pair",
			slots: []string{"Speech"},
			why:   "identical Nomi type, and only one variant is live at a time",
		},
		// Collections: the same lesson as the function types above, with two
		// twists pinned separately. A list's
		// Go type is PARAMETERIZED, so the key has to recurse — otherwise
		// `List<Int>` and `List<List<Int>>` share a field and a payload
		// recovery reads an int64 out of storage holding a pointer. A tuple's
		// is STRUCTURAL with no declaration anywhere to key on, so its
		// identity is Go's own struct-type identity over the rendered text.
		{
			name:  "two list element types get two slots",
			src:   "enum Holder {\n  Nums {xs: List<Int>}\n  Words {ys: List<String>}\n}\n",
			enum:  "Holder",
			slots: []string{"List<Int>", "List<String>"},
			why:   "one tag, two Go types; merging them recovers the wrong element",
		},
		{
			name:  "the same list type shares one slot",
			src:   "enum Pairs {\n  Left {xs: List<Int>}\n  Right {ys: List<Int>}\n}\n",
			enum:  "Pairs",
			slots: []string{"List<Int>"},
			why:   "identical Go type, and only one variant is live at a time",
		},
		{
			name:  "list nesting depth is part of the identity",
			src:   "enum Depth {\n  Flat {xs: List<Int>}\n  Deep {ys: List<List<Int>>}\n}\n",
			enum:  "Depth",
			slots: []string{"List<Int>", "List<List<Int>>"},
			why:   "the key must recurse; a bare tag or a one-level key merges these",
		},
		{
			name:  "two tuple shapes get two slots",
			src:   "enum Shapes {\n  Named {p: (Int, String)}\n  Point {q: (Int, Int)}\n}\n",
			enum:  "Shapes",
			slots: []string{"(Int, String)", "(Int, Int)"},
			why:   "a structural type with no declaration still needs one identity per Go type",
		},
		{
			name:  "the same tuple shape shares one slot",
			src:   "enum Points {\n  Start {p: (Int, Int)}\n  End {q: (Int, Int)}\n}\n",
			enum:  "Points",
			slots: []string{"(Int, Int)"},
			why:   "Go's struct identity is structural, so one shape is one type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := lookupTypeDef(t, tc.src, tc.enum)
			got := make([]string, 0, len(d.slots))
			for _, s := range d.slots {
				g := s.k.nomi()
				if s.boxed {
					g = "*" + g
				}
				got = append(got, g)
			}
			if strings.Join(got, ",") != strings.Join(tc.slots, ",") {
				t.Fatalf("%s: slots = %v, want %v (%s)", tc.enum, got, tc.slots, tc.why)
			}
		})
	}
}

// TestEnumTagsStartAtOne pins the reservation in the builder's type
// declarations.
func TestEnumTagsStartAtOne(t *testing.T) {
	d := lookupTypeDef(t, "enum Step {\n  First\n  Second\n  Third\n}\n", "Step")
	for i, v := range d.variants {
		if v.tag != i+1 {
			t.Fatalf("%s.%s has tag %d, want %d — tag 0 is reserved invalid", d.nomi, v.nomi, v.tag, i+1)
		}
	}
}

// --- helpers ----------------------------------------------------------------

func lookupTypeDef(t *testing.T, src, name string) *typeDef {
	t.Helper()
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	g := &gen{nomiPath: "main.nomi", funcs: map[string]*fnSig{}, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}}
	g.pushScope()
	// Interface shells first, exactly as lowerModule orders it: a payload may
	// be interface-typed and typeOf can only answer that once the name
	// resolves to an *ifaceDef.
	g.declareIfaces(p.Modules[0].Nodes)
	g.buildTypes(p.Modules[0].Nodes)
	d := g.types[name]
	if d == nil {
		t.Fatalf("%s was not registered; table has %d entries", name, len(g.types))
	}
	if !d.lowerable {
		t.Fatalf("%s was refused: %v", name, d.refusals)
	}
	return d
}
