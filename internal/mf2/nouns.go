package mf2

import (
	"encoding/json"
	"fmt"
	"sync"
)

// A noun is a value of std's `Noun`: a `noun` declaration, whose text in
// each locale is a form per grammatical case, each a message over one
// implicit `$count`, and a gender in a language whose nouns have one. A
// message writes a noun with `:nomi:noun` (options `case`, the nominative
// when left out, and `count`, 1 when left out) and selects on its gender
// with `:nomi:gender`.

// NounParam is the one parameter a noun's form reads: how many of the
// thing the text names.
const NounParam = "count"

// NounSignature is the signature a noun's form is checked with.
func NounSignature(name string) Signature {
	return Signature{Name: name, Params: []Param{{Name: NounParam, Kind: KindInt, Type: "Int", Localize: true}}}
}

// NounTable is a noun as a program carries it: what the compiler checked,
// for the formatter to read. The value a `noun` declaration expands into
// holds it as text (EncodeNoun).
type NounTable struct {
	// Name is the noun's qualified name (`shop.book`).
	Name string `json:"name"`
	// Source is the source locale; Forms[Source] holds the declaration's
	// text as its nominative.
	Source string `json:"source"`
	// Genders are each locale's gender of the noun, for a locale whose
	// language has genders.
	Genders map[string]string `json:"genders,omitempty"`
	// Forms are each locale's canonical form texts by case: the source's
	// and every current translation's. A locale with none falls back along
	// its parents to the source.
	Forms map[string]map[string]string `json:"forms"`
}

// EncodeNoun writes t as text.
func EncodeNoun(t NounTable) string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// nouns caches decoded nouns and their parsed forms by their text.
var nouns sync.Map // string -> *decodedNoun

type decodedNoun struct {
	NounTable
	mu     sync.Mutex
	parsed map[[2]string]*Message
}

func decodeNoun(text string) (*decodedNoun, error) {
	if d, ok := nouns.Load(text); ok {
		return d.(*decodedNoun), nil
	}
	var t NounTable
	if err := json.Unmarshal([]byte(text), &t); err != nil {
		return nil, fmt.Errorf("a noun table that does not decode: %w", err)
	}
	d := &decodedNoun{NounTable: t, parsed: map[[2]string]*Message{}}
	actual, _ := nouns.LoadOrStore(text, d)
	return actual.(*decodedNoun), nil
}

// DecodeNoun reads text EncodeNoun wrote, once per text.
func DecodeNoun(text string) (*NounTable, error) {
	d, err := decodeNoun(text)
	if err != nil {
		return nil, err
	}
	return &d.NounTable, nil
}

// NounForm is the parsed form of case in locale of the noun text, once
// per noun, locale and case; false when the locale has no such form.
func NounForm(text, locale, kase string) (*Message, bool) {
	d, err := decodeNoun(text)
	if err != nil {
		return nil, false
	}
	src, ok := d.Forms[locale][kase]
	if !ok {
		return nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := [2]string{locale, kase}
	if m, ok := d.parsed[key]; ok {
		return m, true
	}
	m, errs := Parse(src)
	if len(errs) > 0 {
		return nil, false
	}
	d.parsed[key] = m
	return m, true
}

// CaseUse is a `:nomi:noun` in a text: the case it asks a noun for, and
// where.
type CaseUse struct {
	Case string
	Span Span
}

// GenderSelector is a selector of a `.match` on a noun's gender: its
// variable, where it is, and the keys its variants name.
type GenderSelector struct {
	Name string
	Span Span
	Keys []Key
}

// NounUses are the cases m asks nouns for with `:nomi:noun`, the
// nominative where it names none, and its selectors on a noun's gender,
// for the checks that hold every noun of a program to every message: a
// noun has every form some text asks for, and a selector on gender an arm
// for every gender some noun has.
func NounUses(m *Message, sig Signature) (cases []CaseUse, genders []GenderSelector) {
	if m == nil {
		return nil, nil
	}
	use := func(e *Expression) {
		f := e.Function
		if f == nil || f.Name != "nomi:noun" {
			return
		}
		u := CaseUse{Case: Nominative, Span: f.Span}
		if o, ok := f.Option("case"); ok && !o.Value.Variable {
			u = CaseUse{Case: o.Value.Value, Span: o.Value.Span}
		}
		cases = append(cases, u)
	}
	var pattern func(p Pattern)
	pattern = func(p Pattern) {
		for _, part := range p.Parts {
			if part.Expr != nil {
				use(part.Expr)
			}
		}
	}
	for i := range m.Declarations {
		use(&m.Declarations[i].Value)
	}
	pattern(m.Pattern)
	for _, vr := range m.Variants {
		pattern(vr.Pattern)
	}
	if !m.Matches() {
		return cases, nil
	}
	v := &validator{sig: sig, vars: map[string]binding{}, used: map[string]bool{}}
	for _, d := range m.Declarations {
		v.declaration(d)
	}
	for i, s := range m.Selectors {
		b := v.read(s.Span, s.Name)
		if b.function == nil || b.function.Name != "nomi:gender" || b.param.Kind != KindNoun {
			continue
		}
		g := GenderSelector{Name: s.Name, Span: s.Span}
		for _, vr := range m.Variants {
			if len(vr.Keys) == len(m.Selectors) && !vr.Keys[i].CatchAll {
				g.Keys = append(g.Keys, vr.Keys[i])
			}
		}
		genders = append(genders, g)
	}
	return cases, genders
}
