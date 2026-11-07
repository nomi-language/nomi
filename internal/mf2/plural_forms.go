package mf2

import "strings"

// PluralCandidates are the parameters of sig that AddPluralForms can
// select on in text: each `Int` one, in order, when text parses and has no
// `.match`.
func PluralCandidates(text string, sig Signature) []string {
	m, errs := Parse(text)
	if len(errs) > 0 || m.Matches() {
		return nil
	}
	var out []string
	for _, p := range sig.Params {
		if p.Kind == KindInt {
			out = append(out, p.Name)
		}
	}
	return out
}

// AddPluralForms rewrites text, a message with no `.match`, to select on
// the parameter name by its plural category in loc: its declarations, an
// `.input {$name :integer}` unless one declares `$name` already, `.match
// $name`, and an arm for each category loc's rules select for a whole
// number besides `other`, then `*`, each holding the text's pattern for
// the author to edit. It writes one line per declaration and arm, with no
// indentation, and answers false when text does not parse, already
// matches, or declares `$name` with a function that selects no plural
// category.
func AddPluralForms(text, name string, sig Signature, loc Locale) (string, bool) {
	m, errs := Parse(text)
	if len(errs) > 0 || m.Matches() {
		return "", false
	}
	var lines []string
	declared := false
	for _, d := range m.Declarations {
		declared = declared || d.Name == name
		lines = append(lines, text[d.Span.Start:d.Span.End])
	}
	if !declared {
		lines = append([]string{".input {$" + name + " :integer}"}, lines...)
	}
	lines = append(lines, ".match $"+name)
	head := strings.Join(lines, "\n")
	probe, errs := Parse(head + "\n* {{}}")
	if len(errs) > 0 {
		return "", false
	}
	sels := Selectors(probe, sig, loc)
	if len(sels) != 1 || !sels[0].Plural {
		return "", false
	}
	pattern := text[m.Pattern.Span.Start:m.Pattern.Span.End]
	for _, key := range sels[0].Needed {
		lines = append(lines, key+" {{"+pattern+"}}")
	}
	lines = append(lines, "* {{"+pattern+"}}")
	return strings.Join(lines, "\n"), true
}
