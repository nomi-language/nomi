package lsp

import (
	"fmt"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
)

// Hover inside MessageFormat 2 text: a `phrase` or `noun` body, and a
// catalog's `source`, `text` and form strings. A `$name` shows the
// parameter or `.local` it names, a function and an option what they do
// (mf2.FunctionDocOf, the one table of them), a variant key under a
// plural selector its category in the text's locale with example numbers,
// and a keyword one line.

// mf2Text is one MF2 text hovered in, with what its hover needs to know
// of the declaration it belongs to.
type mf2Text struct {
	text string
	// decl is the phrase's or noun's name, and noun reports a noun.
	decl string
	noun bool
	sig  mf2.Signature
	// params are the declaration's parameters, for their comments.
	params []ast.Param
	// loc is the locale the text is in: the source locale for the
	// declaration's own text, the catalog's for a translation.
	loc mf2.Locale
}

// hover is the hover over the token at byte offset off of the text, and
// that token's span.
func (t mf2Text) hover(off int) (string, mf2.Span, bool) {
	toks := mf2.Tokens(t.text)
	at := -1
	for i, tok := range toks {
		if tok.Span.Start <= off && off < tok.Span.End {
			at = i
			break
		}
		if tok.Span.End == off {
			at = i
		}
	}
	if at < 0 {
		return "", mf2.Span{}, false
	}
	tok := toks[at]
	word := t.text[tok.Span.Start:tok.Span.End]
	m, _ := mf2.Parse(t.text)
	var md string
	switch tok.Kind {
	case mf2.TokenKeyword:
		md = mf2.KeywordDocs[word]
	case mf2.TokenVariable:
		md = t.variableHover(m, tok.Name)
	case mf2.TokenFunction:
		md = functionHover(strings.TrimPrefix(word, ":"))
	case mf2.TokenOption:
		// An option follows its function; a markup's options have none.
		for i := at - 1; i >= 0; i-- {
			if toks[i].Kind == mf2.TokenMarkup {
				break
			}
			if toks[i].Kind == mf2.TokenFunction {
				fn := t.text[toks[i].Span.Start+1 : toks[i].Span.End]
				md = t.optionHover(fn, word)
				break
			}
		}
	case mf2.TokenKey, mf2.TokenCatchAll:
		md = t.keyHover(m, tok.Span)
	}
	if md == "" {
		return "", mf2.Span{}, false
	}
	return md, tok.Span, true
}

// variableHover shows the parameter or `.local` `$name` names.
func (t mf2Text) variableHover(m *mf2.Message, name string) string {
	var input string
	if m != nil {
		for _, d := range m.Declarations {
			if d.Name != name {
				continue
			}
			decl := t.text[d.Span.Start:d.Span.End]
			if d.Local {
				return "```\n" + decl + "\n```\n\nA variable this text declares."
			}
			input = decl
		}
	}
	for i, p := range t.sig.Params {
		if p.Name != name {
			continue
		}
		var b strings.Builder
		b.WriteString("```nomi\n" + p.Name)
		if p.Type != "" {
			b.WriteString(": " + p.Type)
		}
		b.WriteString("\n```\n\n")
		if t.noun {
			b.WriteString("How many: a phrase that writes `" + t.decl + "` passes it with `count=` on `:nomi:noun`, as in `{$coin :nomi:noun count=$n}`, and 1 when it names none. The noun's text selects its plural form by it.")
		} else {
			b.WriteString("A parameter of `" + t.decl + "`.")
			if i < len(t.params) {
				if doc := paramComment(t.params[i]); doc != "" {
					b.WriteString("\n\n" + doc)
				}
			}
		}
		if input != "" {
			b.WriteString("\n\nAnnotated by `" + input + "`.")
		}
		return b.String()
	}
	return fmt.Sprintf("`$%s` is not a parameter of `%s`.", name, t.decl)
}

// paramComment is the text of the comments around a parameter in its
// list, its doc.
func paramComment(p ast.Param) string {
	var lines []string
	for _, tr := range append(append([]ast.Trivia{}, p.Comments.Before...), p.Comments.Trailing...) {
		text := strings.TrimSpace(tr.Text)
		if !strings.HasPrefix(text, "//") {
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.TrimLeft(text, "/")))
	}
	return strings.Join(lines, "\n")
}

// functionHover shows what the function named name does, its options and
// how it selects.
func functionHover(name string) string {
	doc, ok := mf2.FunctionDocOf(name)
	if !ok {
		return fmt.Sprintf("```\n:%s\n```\n\n`:%s` is not a function Nomi formats.", name, name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "```\n:%s\n```\n\n%s", name, doc.Summary)
	if doc.Nomi() {
		fmt.Fprintf(&b, "\n\n`:%s` is one of Nomi's own functions, in the `nomi` namespace: MessageFormat 2 reserves names without a namespace for its standard functions.", name)
	}
	if doc.Later {
		return b.String()
	}
	b.WriteString("\n\nTakes " + doc.Takes + ".")
	if doc.Selects != "" {
		b.WriteString("\n\nUnder `.match` it selects " + doc.Selects)
	} else {
		b.WriteString("\n\nIt selects nothing under `.match`.")
	}
	if len(doc.Options) > 0 {
		b.WriteString("\n\n**Options**\n")
		for _, o := range doc.Options {
			fmt.Fprintf(&b, "\n- `%s`: %s", o.Name, o.Doc)
		}
	}
	return b.String()
}

// optionHover shows what the option name does for the function fn; for
// `:nomi:noun`'s `case`, the cases of the text's locale.
func (t mf2Text) optionHover(fn, name string) string {
	doc, ok := mf2.FunctionDocOf(fn)
	if !ok {
		return ""
	}
	o, ok := doc.Option(name)
	if !ok {
		return fmt.Sprintf("`:%s` has no option `%s`.", fn, name)
	}
	text := fmt.Sprintf("`%s`, an option of `:%s`\n\n%s", name, fn, o.Doc)
	if fn == "nomi:noun" && name == "case" {
		if len(t.loc.Cases) == 0 {
			text += fmt.Sprintf("\n\n%s nouns have one form, the `nominative`.", t.loc.Language)
		} else {
			text += fmt.Sprintf("\n\nThe cases of %s: %s.", t.loc.Language, strings.Join(quoteEach(t.loc.Cases), ", "))
		}
	}
	return text
}

// keyHover shows what the variant key at span selects: a plural category
// with example numbers, a gender, a day, an enum's variant, or for `*`
// the keys it covers that no arm names.
func (t mf2Text) keyHover(m *mf2.Message, span mf2.Span) string {
	if m == nil {
		return ""
	}
	col := -1
	var key mf2.Key
	for _, v := range m.Variants {
		for i, k := range v.Keys {
			if k.Span == span {
				col, key = i, k
			}
		}
	}
	sels := mf2.Selectors(m, t.sig, t.loc)
	if col < 0 || col >= len(sels) {
		return ""
	}
	s := sels[col]
	sel := "`$" + s.Name + "`"
	in := fmt.Sprintf("%s (`%s`)", t.loc.Language, t.loc.Tag)
	kind := "plural category"
	rules := t.loc.Cardinal
	if s.Ordinal {
		kind, rules = "ordinal category", t.loc.Ordinal
	}
	examples := func(cat string) string {
		if rules == nil {
			return ""
		}
		return mf2.PluralExamples(rules, cat, s.Integers)
	}
	if key.CatchAll {
		var b strings.Builder
		fmt.Fprintf(&b, "`*` is the catch-all key: the arm %s takes when no other key matches it. MessageFormat 2 requires a variant whose keys are all `*`.", sel)
		var keys []string
		switch {
		case s.Plural:
			keys = append(append(keys, s.Needed...), "other")
		case len(s.Known) > 0:
			keys = s.Known
		default:
			return b.String()
		}
		named := map[string]bool{}
		for _, v := range m.Variants {
			if col < len(v.Keys) && !v.Keys[col].CatchAll {
				named[v.Keys[col].Value] = true
			}
		}
		var covered []string
		for _, k := range keys {
			if named[k] {
				continue
			}
			if ex := examples(k); s.Plural && ex != "" {
				covered = append(covered, fmt.Sprintf("`%s` (%s)", k, ex))
			} else if s.Plural && s.Integers {
				// A category no whole number is in: the selector never
				// holds one of it.
				continue
			} else {
				covered = append(covered, "`"+k+"`")
			}
		}
		switch {
		case len(covered) == 0 && s.Plural && s.Integers:
			fmt.Fprintf(&b, "\n\nEvery %s of %s that a whole number is in has an arm here, so `*` covers none.", kind, in)
		case len(covered) == 0 && s.Plural:
			fmt.Fprintf(&b, "\n\nEvery %s of %s has an arm here, so `*` covers none.", kind, in)
		case len(covered) == 0:
			b.WriteString("\n\nEvery key has an arm here, so `*` covers none.")
		case s.Plural:
			fmt.Fprintf(&b, "\n\nIn %s it covers %s.", in, strings.Join(covered, ", "))
		default:
			fmt.Fprintf(&b, "\n\nIt covers %s.", strings.Join(covered, ", "))
		}
		return b.String()
	}
	k := key.Value
	switch {
	case s.Plural && mf2.IsNumber(k):
		return fmt.Sprintf("`%s` matches %s exactly when it is %s, ahead of its %s.", k, sel, k, kind)
	case s.Plural && containsStr(s.Known, k):
		ex := examples(k)
		if ex == "" {
			return fmt.Sprintf("`%s` is a %s in %s that no whole number is in, so %s never takes this arm.", k, kind, in, sel)
		}
		if !strings.HasSuffix(ex, "…") {
			ex += "."
		}
		return fmt.Sprintf("`%s` is a %s in %s: %s takes this arm for %s", k, kind, in, sel, ex)
	case s.Plural:
		return fmt.Sprintf("`%s` is not a %s in %s, whose categories are %s.", k, kind, in, strings.Join(quoteEach(s.Known), ", "))
	case s.Gender && containsStr(s.Known, k):
		return fmt.Sprintf("`%s` is a gender of %s nouns: the arm %s takes when its noun is %s in this locale.", k, t.loc.Language, sel, k)
	case s.Gender:
		return fmt.Sprintf("`%s` is not a gender of %s nouns, which are %s.", k, t.loc.Language, strings.Join(quoteEach(s.Known), ", "))
	case s.Weekday:
		return fmt.Sprintf("`%s` is a day of the week: the arm %s takes on a %s.", k, sel, strings.ToUpper(k[:1])+k[1:])
	case len(s.Known) > 0:
		typ := "the enum"
		for _, p := range t.sig.Params {
			if p.Name == s.Name && p.Type != "" {
				typ = "`" + p.Type + "`"
			}
		}
		return fmt.Sprintf("`%s` is a variant of %s: the arm %s takes when it is `%s`.", k, typ, sel, k)
	}
	return fmt.Sprintf("`%s` matches %s exactly.", k, sel)
}

// phraseBodyHover is the hover inside the MF2 body of a `phrase` or `noun`
// in nodes at pos, or nil when pos is in none.
func phraseBodyHover(fa *analysis.FileAnalysis, nodes []ast.Node, content string, pos analysis.Pos) *protocol.Hover {
	for _, n := range nodes {
		t, kd, ok := sourceText(fa, n)
		if !ok || kd.Body.Line > pos.Line {
			continue
		}
		off := -1
		for i := 0; i <= len(t.text); i++ {
			if line, col := kd.BodyPosition(i); line == pos.Line && col == pos.Col {
				off = i
				break
			}
		}
		if off < 0 {
			continue
		}
		md, span, ok := t.hover(off)
		if !ok {
			return nil
		}
		line, col := kd.BodyPosition(span.Start)
		endLine, endCol := kd.BodyPosition(span.End)
		if endLine != line {
			endCol = col
		}
		return tokenHover(content, md, analysis.Pos{Line: line, Col: col}, endCol-col)
	}
	return nil
}

// sourceText is the text of the `phrase` or `noun` n declares or was
// expanded from.
func sourceText(fa *analysis.FileAnalysis, n ast.Node) (mf2Text, *ast.KeywordDecl, bool) {
	switch d := n.(type) {
	case *ast.FuncDef:
		if info := analysis.PhraseOf(d); info != nil && info.Decl != nil && info.Decl.Body != nil {
			kd := info.Decl
			return mf2Text{text: kd.Body.Value, decl: kd.Name, sig: analysis.PhraseSignature(fa, d), params: kd.Params,
				loc: analysis.PhraseRules(info.SourceLocale, false)}, kd, true
		}
	case *ast.OnceBinding:
		if info := analysis.NounOf(d); info != nil && info.Decl != nil && info.Decl.Body != nil {
			kd := info.Decl
			return mf2Text{text: kd.Body.Value, decl: kd.Name, noun: true, sig: mf2.NounSignature(kd.Name),
				loc: analysis.PhraseRules(info.SourceLocale, false)}, kd, true
		}
	}
	return mf2Text{}, nil, false
}

// catalogTextHover is the hover inside the MF2 of a catalog entry's
// strings at off, or nil when off is in none.
func (d *catalogDoc) catalogTextHover(entry *catalog.Entry, off int) *protocol.Hover {
	var texts []mf2Text
	var values []catalog.Value
	if n, ok := d.noun(entry.Name); ok {
		kd := n.info.Decl
		base := mf2Text{decl: kd.Name, noun: true, sig: mf2.NounSignature(kd.Name)}
		src := base
		src.loc = analysis.PhraseRules(n.info.SourceLocale, false)
		texts, values = append(texts, src), append(values, entry.Source)
		for _, f := range entry.Fields {
			if f.Name == "gender" {
				continue
			}
			form := base
			form.loc = analysis.PhraseRules(d.place.Locale, false)
			texts, values = append(texts, form), append(values, f.Value)
		}
	} else if m, ok := d.phrase(entry.Name); ok {
		base := mf2Text{decl: m.name(), sig: m.sig, params: m.info.Decl.Params}
		src, text := base, base
		src.loc, text.loc = d.rules(m, "source"), d.rules(m, "text")
		texts, values = append(texts, src, text), append(values, entry.Source, entry.Text)
	}
	for i, v := range values {
		if !v.Present || !v.Exact || off < v.Body || off > v.Body+len(v.Text) {
			continue
		}
		t := texts[i]
		t.text = v.Text
		md, span, ok := t.hover(off - v.Body)
		if !ok {
			return nil
		}
		r := protocol.Range{Start: positionOfOffset(d.text, v.Offset(span.Start)), End: positionOfOffset(d.text, v.Offset(span.End))}
		return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: md}, Range: &r}
	}
	return nil
}
