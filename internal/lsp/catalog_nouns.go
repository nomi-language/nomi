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

// A noun's entries in the editor: completion of its keys (its `gender` and
// a form per case) and of its gender, hover, and the quick fixes that mark
// it current and add the forms the locale's messages ask for. In a
// message's text, `case=` completes the locale's cases.

// catalogNoun is one `noun` of a catalog's Nomi file.
type catalogNoun struct {
	once *ast.OnceBinding
	info *analysis.NounInfo
}

func (n catalogNoun) name() string { return n.info.Decl.Name }

// nounsOf are the nouns nodes declare.
func nounsOf(nodes []ast.Node) []catalogNoun {
	var out []catalogNoun
	for _, node := range nodes {
		if o, ok := node.(*ast.OnceBinding); ok {
			if info := analysis.NounOf(o); info != nil {
				out = append(out, catalogNoun{once: o, info: info})
			}
		}
	}
	return out
}

func (d *catalogDoc) noun(name string) (catalogNoun, bool) {
	for _, n := range d.nouns {
		if n.name() == name {
			return n, true
		}
	}
	return catalogNoun{}, false
}

// completionItem makes one completion item, as catalogCompletion's item
// does.
type completionItem func(label, detail string, kind protocol.CompletionItemKind, doc string, sort string) protocol.CompletionItem

// nounCompletion completes in a noun's table: the keys it lacks at the
// start of a line (`gender` where the language has genders, then each
// case, those the locale's messages ask for first), and a gender in the
// string of its `gender`.
func (d *catalogDoc) nounCompletion(n catalogNoun, cur catalogCursor, off int, item completionItem) []protocol.CompletionItem {
	loc := analysis.MessageRules(d.place.Locale, false)
	var items []protocol.CompletionItem
	if cur.key == "gender" {
		for i, g := range loc.Genders {
			sort := fmt.Sprintf("1%04d", i)
			what := fmt.Sprintf("a gender in %s", loc.Language)
			if containsStr(d.genders, g) {
				sort = fmt.Sprintf("0%04d", i)
				what += ", which a noun has already"
			}
			items = append(items, item(g, what, protocol.CompletionItemKindEnumMember, "", sort))
		}
		return items
	}
	if cur.key != "" {
		return d.caseCompletion(off, cur, item)
	}
	lineStart := strings.LastIndexByte(d.text[:off], '\n') + 1
	if strings.TrimSpace(strings.TrimRight(d.text[lineStart:off], "abcdefghijklmnopqrstuvwxyz_")) != "" {
		return nil
	}
	f, _ := catalog.Parse(d.text)
	e := f.Entry(n.name())
	has := func(key string) bool {
		return e != nil && (key == "source" && e.Source.Present || e.Field(key).Present)
	}
	if !has("gender") && len(loc.Genders) > 0 {
		items = append(items, item("gender", fmt.Sprintf("the noun's gender in %s", loc.Language), protocol.CompletionItemKindProperty, "", "0000"))
	}
	asked := analysis.CatalogCases(d.place, d.read)
	for i, c := range loc.CaseNames() {
		if has(c) {
			continue
		}
		sort, what := fmt.Sprintf("2%04d", i), "a case in "+loc.Language
		if containsStr(asked, c) {
			sort, what = fmt.Sprintf("1%04d", i), "a case the locale's messages ask for"
		}
		items = append(items, item(c, what, protocol.CompletionItemKindProperty, "", sort))
	}
	return items
}

// caseCompletion completes a case after `case=` in a message's or a noun
// form's text, or nil when the cursor is not there.
func (d *catalogDoc) caseCompletion(off int, cur catalogCursor, item completionItem) []protocol.CompletionItem {
	start := off
	for start > cur.body && isWordByte(d.text[start-1]) {
		start--
	}
	if !strings.HasSuffix(d.text[cur.body:start], "case=") {
		return nil
	}
	loc := analysis.MessageRules(d.place.Locale, false)
	var items []protocol.CompletionItem
	for i, c := range loc.CaseNames() {
		items = append(items, item(c, "a case in "+loc.Language, protocol.CompletionItemKindEnumMember, "", fmt.Sprintf("%04d", i)))
	}
	return items
}

// nounHover shows, over a noun's entry, its declaration and doc comment and
// whether the entry is current.
func (d *catalogDoc) nounHover(n catalogNoun, entry *catalog.Entry) *protocol.Hover {
	language := analysis.LanguageName(n.info.SourceLocale)
	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\nnoun %s: Noun\n```\n", n.name())
	if n.once.Doc != "" {
		b.WriteString("\n" + n.once.Doc + "\n")
	}
	fence := func(text string) {
		b.WriteString("\n```\n" + strings.TrimSpace(text) + "\n```\n")
	}
	loc := analysis.MessageRules(d.place.Locale, false)
	if len(loc.Genders) > 0 {
		fmt.Fprintf(&b, "\n%s nouns are %s.\n", loc.Language, strings.Join(quoteEach(loc.Genders), ", "))
	}
	switch {
	case !entry.Source.Present:
		fmt.Fprintf(&b, "\nThis entry does not record the %s text it was translated from. The text now:\n", language)
		fence(n.info.Source)
	case !mf2.Same(entry.Source.Text, n.info.Source):
		fmt.Fprintf(&b, "\nThis entry was made from an older %s text.\n\nIt was translated from:\n", language)
		fence(entry.Source.Text)
		b.WriteString("\nThe text now:\n")
		fence(n.info.Source)
	default:
		fmt.Fprintf(&b, "\nThis entry is current: it was made from the %s text as it is now.\n", language)
	}
	r := protocol.Range{Start: positionOfOffset(d.text, entry.Span.Start), End: positionOfOffset(d.text, entry.Span.End)}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: strings.TrimRight(b.String(), "\n")}, Range: &r}
}

// nounActions are the quick fixes on a noun's entry: mark it current, and
// add each form the locale's messages ask for that it lacks, a copy of its
// nominative (or of the source text) for the translator to decline.
func (d *catalogDoc) nounActions(n catalogNoun, e *catalog.Entry, parses bool, related []protocol.Diagnostic) []protocol.CodeAction {
	var actions []protocol.CodeAction
	if parses && (!e.Source.Present || !mf2.Same(e.Source.Text, n.info.Source)) {
		if text, err := catalog.SetSource(d.text, e.Name, n.info.Source); err == nil {
			actions = append(actions, quickFix(
				fmt.Sprintf("Mark the %s translation of `%s` current", d.place.Locale, e.Name),
				d.uri, textEditFor(d.text, text), related, true))
		}
	}
	copied := n.info.Source
	if nom := e.Field(mf2.Nominative); nom.Present {
		copied = nom.Text
	}
	var missing []string
	for _, c := range analysis.CatalogCases(d.place, d.read) {
		if !e.Field(c).Present {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return actions
	}
	at := e.Span.End
	for _, v := range append([]catalog.Value{e.Source, e.Text}, fieldValues(e)...) {
		if v.Present {
			at = max(at, v.Span.End)
		}
	}
	var add strings.Builder
	for _, c := range missing {
		add.WriteString("\n" + c + " = " + catalog.Block(copied))
	}
	pos := positionOfOffset(d.text, at)
	edit := protocol.TextEdit{Range: protocol.Range{Start: pos, End: pos}, NewText: add.String()}
	what := "forms"
	if len(missing) == 1 {
		what = "`" + missing[0] + "` form"
	}
	actions = append(actions, quickFix(
		fmt.Sprintf("Give the %s translation of `%s` the %s its messages ask for", d.place.Locale, e.Name, what),
		d.uri, []protocol.TextEdit{edit}, related, false))
	return actions
}

func fieldValues(e *catalog.Entry) []catalog.Value {
	out := make([]catalog.Value, len(e.Fields))
	for i, f := range e.Fields {
		out[i] = f.Value
	}
	return out
}

func quoteEach(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return out
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
