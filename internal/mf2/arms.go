package mf2

import (
	"sort"
	"strings"
)

// SelectorKeys are the keys one selector of a text takes in a locale.
type SelectorKeys struct {
	// Name is the selector's variable.
	Name string
	// Needed are the keys the selector must have an arm for: each plural
	// category the locale's rules select for the operand's type, except
	// `other`, which `*` covers, or each variant of an enum.
	Needed []string
	// Known are every key the selector may have besides `*` (and, for a
	// plural selector, numbers): the locale's categories, or the variants.
	Known []string
	// Plural reports a plural or ordinal selector, whose numeric keys
	// match exactly and are always allowed.
	Plural bool
	// Gender reports a `:nomi:gender` selector, whose Known keys are the
	// language's genders and Needed ones those the program's nouns have
	// (Locale.NounGenders).
	Gender bool
	// Weekday reports a `:nomi:weekday` selector, which needs every day
	// once a variant names one.
	Weekday bool
}

// Selectors are the keys each selector of m takes in loc, in order, as
// Validate checks them; nil for a text with no `.match`. A selector that
// selects by nothing Nomi checks (no function, `select=exact`, a `Float`'s
// plural, a `:string` over a `String`) has no keys.
func Selectors(m *Message, sig Signature, loc Locale) []SelectorKeys {
	if m == nil || !m.Matches() {
		return nil
	}
	v := &validator{sig: sig, loc: loc, vars: map[string]binding{}, used: map[string]bool{}}
	for _, d := range m.Declarations {
		v.declaration(d)
	}
	out := make([]SelectorKeys, len(m.Selectors))
	for i, s := range m.Selectors {
		out[i] = selectorKeysIn(v.read(s.Span, s.Name), loc)
		out[i].Name = s.Name
		if out[i].Weekday && !namesKey(m, i, len(m.Selectors)) {
			// A day-of-the-week selector whose variants name no day needs
			// none.
			out[i].Needed = nil
		}
	}
	return out
}

// namesKey reports whether a variant of m names a key other than `*` at
// selector position i of n.
func namesKey(m *Message, i, n int) bool {
	for _, vr := range m.Variants {
		if len(vr.Keys) == n && !vr.Keys[i].CatchAll {
			return true
		}
	}
	return false
}

// selectorKeysIn are the keys a selector holding b takes in loc.
func selectorKeysIn(b binding, loc Locale) SelectorKeys {
	f := b.function
	if f == nil || b.unknown {
		return SelectorKeys{}
	}
	switch f.Name {
	case "integer", "number", "percent", "nomi:list":
		if f.Name == "nomi:list" && b.param.Kind != KindList {
			return SelectorKeys{}
		}
		rules, needed, _, ok := pluralKeys(f, b.param, loc)
		if !ok {
			return SelectorKeys{}
		}
		return SelectorKeys{Needed: needed, Known: rules.CategoryNames(), Plural: true}
	case "string":
		if b.param.Kind != KindEnum {
			return SelectorKeys{}
		}
		return SelectorKeys{Needed: b.param.Variants, Known: b.param.Variants}
	case "nomi:gender":
		if b.param.Kind != KindNoun || len(loc.Genders) == 0 {
			return SelectorKeys{}
		}
		var needed []string
		for _, g := range loc.Genders {
			if contains(loc.NounGenders, g) {
				needed = append(needed, g)
			}
		}
		return SelectorKeys{Needed: needed, Known: loc.Genders, Gender: true}
	case "nomi:weekday":
		if b.param.Kind != KindDate {
			return SelectorKeys{}
		}
		return SelectorKeys{Needed: Weekdays, Known: Weekdays, Weekday: true}
	}
	return SelectorKeys{}
}

// pluralKeys are the rules a plural selector by f of a value p selects
// with in loc, the categories it needs an arm for, and whether they are
// cardinal (`plural`) or `ordinal`; ok is false when f selects no plural
// category: `select=exact`, or a `Float` formatted as a number.
func pluralKeys(f *Function, p Param, loc Locale) (rules Rules, needed []string, kind string, ok bool) {
	mode := "plural"
	if o, has := f.Option("select"); has && !o.Value.Variable {
		mode = o.Value.Value
	}
	if mode == "exact" || p.Kind == KindFloat && f.Name != "integer" {
		return nil, nil, "", false
	}
	rules, kind = loc.Cardinal, "plural"
	if mode == "ordinal" {
		rules, kind = loc.Ordinal, "ordinal"
	}
	if rules == nil {
		return nil, nil, "", false
	}
	cats := rules.CategoryNames()
	if f.Name == "integer" || f.Name == "nomi:list" || p.Kind == KindInt {
		cats = integerCategories(rules)
	}
	for _, c := range cats {
		if c != "other" {
			needed = append(needed, c)
		}
	}
	return rules, needed, kind, true
}

// Edit replaces Span of a text with Text; an empty Span inserts Text.
type Edit struct {
	Span Span
	Text string
}

// FitArms are the edits that give text the arms loc needs, in the order of
// the text: an arm for each key a selector needs and no arm has, and no arm
// whose key is not one of the locale's (an English `one` in Japanese,
// which has none). A new arm copies the pattern of the arm MF2 would
// select for it, usually the catch-all, and goes before the first
// catch-all, indented as it is, so the translator replaces the copied text.
// FitArms answers nil for a text that does not parse or needs nothing.
func FitArms(text string, sig Signature, loc Locale) []Edit {
	m, errs := Parse(text)
	if len(errs) > 0 || !m.Matches() {
		return nil
	}
	sels := Selectors(m, sig, loc)
	var edits []Edit
	// Arms with a key the locale does not have.
	removed := map[int]bool{}
	for i, vr := range m.Variants {
		if len(vr.Keys) != len(sels) {
			continue
		}
		for j, k := range vr.Keys {
			s := sels[j]
			if k.CatchAll || s.Known == nil || s.Plural && IsNumber(k.Value) || contains(s.Known, k.Value) {
				continue
			}
			removed[i] = true
			edits = append(edits, Edit{Span: lineSpan(text, vr.Span)})
			break
		}
	}
	// The keys each position has among the arms that stay.
	has := make([]map[string]bool, len(sels))
	for j := range sels {
		has[j] = map[string]bool{}
	}
	catchAll := -1
	for i, vr := range m.Variants {
		if removed[i] || len(vr.Keys) != len(sels) {
			continue
		}
		if catchAll < 0 && vr.CatchAll() {
			catchAll = i
		}
		for j, k := range vr.Keys {
			if !k.CatchAll {
				has[j][k.Value] = true
			}
		}
	}
	if catchAll < 0 {
		// MF2 requires the catch-all; Validate reports its absence, and
		// there is nothing to copy a new arm from.
		return sortEdits(edits)
	}
	at := m.Variants[catchAll].Span.Start
	lineStart := strings.LastIndexByte(text[:at], '\n') + 1
	indent := text[lineStart:at]
	if strings.TrimSpace(indent) != "" {
		indent = ""
	}
	var added strings.Builder
	for j, s := range sels {
		for _, key := range s.Needed {
			if has[j][key] {
				continue
			}
			keys := make([]string, len(sels))
			for x := range keys {
				keys[x] = "*"
			}
			keys[j] = key
			source := m.Variants[bestArm(m, removed, keys)]
			added.WriteString(strings.Join(keys, " ") + " {{" + text[source.Pattern.Span.Start:source.Pattern.Span.End] + "}}\n" + indent)
		}
	}
	if added.Len() > 0 {
		edits = append(edits, Edit{Span: Span{at, at}, Text: added.String()})
	}
	return sortEdits(edits)
}

// bestArm is the arm MF2 selects for keys, among the arms not removed: one
// whose every key is the wanted one or `*`, most exact first.
func bestArm(m *Message, removed map[int]bool, keys []string) int {
	best, bestExact := -1, -1
	for i, vr := range m.Variants {
		if removed[i] || len(vr.Keys) != len(keys) {
			continue
		}
		exact, ok := 0, true
		for j, k := range vr.Keys {
			switch {
			case k.CatchAll:
			case k.Value == keys[j] && keys[j] != "*":
				exact++
			default:
				ok = false
			}
		}
		if ok && exact > bestExact {
			best, bestExact = i, exact
		}
	}
	return best
}

// lineSpan widens sp to the whole lines it is on, with the line break that
// ends them, when nothing else is on them.
func lineSpan(text string, sp Span) Span {
	start := strings.LastIndexByte(text[:sp.Start], '\n') + 1
	end := sp.End
	if nl := strings.IndexByte(text[end:], '\n'); nl >= 0 {
		end += nl + 1
	} else {
		end = len(text)
	}
	if strings.TrimSpace(text[start:sp.Start]) != "" || strings.TrimSpace(text[sp.End:end]) != "" {
		return sp
	}
	return Span{start, end}
}

func sortEdits(edits []Edit) []Edit {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].Span.Start < edits[j].Span.Start })
	return edits
}

// Apply applies edits, which do not overlap, to text.
func Apply(text string, edits []Edit) string {
	edits = sortEdits(append([]Edit(nil), edits...))
	var b strings.Builder
	at := 0
	for _, e := range edits {
		b.WriteString(text[at:e.Span.Start])
		b.WriteString(e.Text)
		at = e.Span.End
	}
	b.WriteString(text[at:])
	return b.String()
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
