package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
)

// A message's catalogs in the editor: completion, hover and quick fixes in
// a catalog file, and the quick fix on a `message` that writes a missing
// translation. A catalog is a TOML file at a Nomi file's path under the
// module's `[i18n].catalogs` directory, in a locale's directory
// (analysis.CatalogAt); any other TOML file gets nothing from here.

// catalogDoc is a catalog an editor asked about: its text as the analysis
// reads it, where it sits, and the messages of the Nomi file whose
// translations it holds.
type catalogDoc struct {
	uri, path, text string
	place           analysis.CatalogPlace
	messages        []catalogMessage
	nouns           []catalogNoun
	// genders are the genders the nouns of the module's catalogs in this
	// locale give, which a selector on a noun's gender needs arms for
	// (analysis.CatalogGenders).
	genders []string
	// read reads a catalog as the analysis does: an editor's buffer, else
	// the disk.
	read func(string) (string, bool)
}

// catalogMessage is one `message` of a catalog's Nomi file.
type catalogMessage struct {
	fn   *ast.FuncDef
	info *analysis.MessageInfo
	sig  mf2.Signature
}

func (m catalogMessage) name() string { return m.info.Decl.Name }

// messagesOf are the messages nodes declare, with their signatures from fa.
func messagesOf(nodes []ast.Node, fa *analysis.FileAnalysis) []catalogMessage {
	var out []catalogMessage
	for _, n := range nodes {
		fn, ok := n.(*ast.FuncDef)
		if !ok {
			continue
		}
		if info := analysis.MessageOf(fn); info != nil {
			out = append(out, catalogMessage{fn: fn, info: info, sig: analysis.MessageSignature(fa, fn)})
		}
	}
	return out
}

// catalogAt is the catalog at uri, or nil when uri is no catalog.
func (s *Server) catalogAt(uri string) *catalogDoc {
	path := uriToPath(uri)
	place, ok := analysis.CatalogAt(path)
	if !ok {
		return nil
	}
	text, _ := s.docs.InputText(path)
	d := &catalogDoc{uri: uri, path: path, text: text, place: place}
	if snap := s.docs.Analyzed(pathToURI(place.NomiPath)); snap != nil {
		d.messages = messagesOf(snap.Nodes, snap.Analysis)
		d.nouns = nounsOf(snap.Nodes)
	}
	d.read = s.docs.InputText
	d.genders = analysis.CatalogGenders(place, d.read)
	return d
}

func (d *catalogDoc) message(name string) (catalogMessage, bool) {
	for _, m := range d.messages {
		if m.name() == name {
			return m, true
		}
	}
	return catalogMessage{}, false
}

// rules is the locale a text of key ("text" or "source") is checked in.
func (d *catalogDoc) rules(m catalogMessage, key string) mf2.Locale {
	if key == "source" {
		return analysis.MessageRules(m.info.SourceLocale, false)
	}
	loc := analysis.MessageRules(d.place.Locale, false)
	loc.NounGenders = d.genders
	return loc
}

// catalogCursor is where an offset of a catalog's text is: in a table's
// header, in a key's string, or elsewhere.
type catalogCursor struct {
	// header reports the cursor in `[name]`, after its `[`.
	header bool
	// table is the table the cursor is in or names, and key the key whose
	// string holds it ("" outside any string).
	table, key string
	// body is where the string's text starts and end where it ends: at
	// its closing quotes, or at the end of the text when it has none.
	body, end int
}

// scanCatalog finds what holds offset in a catalog's text: a lexical scan
// that, unlike catalog.Parse, reads text that is still being typed.
func scanCatalog(text string, offset int) catalogCursor {
	table := ""
	i := 0
	for i < len(text) && i <= offset {
		// At a line's start.
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		lineEnd := strings.IndexByte(text[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += i
		}
		switch {
		case i < len(text) && text[i] == '[':
			end := strings.IndexByte(text[i:lineEnd], ']')
			name := text[i+1 : lineEnd]
			if end >= 0 {
				name = text[i+1 : i+end]
			}
			name = strings.Trim(strings.TrimSpace(name), `"'`)
			if offset > i && (end < 0 || offset <= i+end) && offset <= lineEnd {
				return catalogCursor{header: true, table: name}
			}
			table = name
			i = lineEnd + 1
		case i < len(text) && text[i] == '#':
			i = lineEnd + 1
		default:
			eq := strings.IndexByte(text[i:lineEnd], '=')
			if eq < 0 {
				i = lineEnd + 1
				continue
			}
			key := strings.Trim(strings.TrimSpace(text[i:i+eq]), `"'`)
			j := i + eq + 1
			for j < lineEnd && (text[j] == ' ' || text[j] == '\t') {
				j++
			}
			for _, q := range []string{"'''", `"""`, "'", `"`} {
				if !strings.HasPrefix(text[j:], q) {
					continue
				}
				body := j + len(q)
				if len(q) == 3 && strings.HasPrefix(text[body:], "\n") {
					body++
				}
				var end int
				if len(q) == 3 {
					end = strings.Index(text[body:], q)
				} else {
					end = strings.IndexAny(text[body:min(lineEnd, len(text))], q)
				}
				stop := len(text)
				if end >= 0 {
					stop = body + end
				} else if len(q) == 1 {
					stop = lineEnd
				}
				if offset >= body && offset <= stop {
					return catalogCursor{table: table, key: key, body: body, end: stop}
				}
				lineEnd = strings.IndexByte(text[min(stop, len(text)):], '\n')
				if lineEnd < 0 {
					lineEnd = len(text)
				} else {
					lineEnd += stop
				}
				break
			}
			i = lineEnd + 1
		}
	}
	return catalogCursor{table: table}
}

// catalogCompletion completes in a catalog: a message's name in a table's
// header, a parameter after `$`, and a variant's keys at the start of a
// line of a `.match`: the plural categories the locale's rules select for
// the selector, or the variants of its enum.
func (s *Server) catalogCompletion(d *catalogDoc, pos protocol.Position) []protocol.CompletionItem {
	off := offsetOfPosition(d.text, pos)
	cur := scanCatalog(d.text, off)
	wordStart := off
	for wordStart > 0 && isWordByte(d.text[wordStart-1]) {
		wordStart--
	}
	replace := protocol.Range{Start: positionOfOffset(d.text, wordStart), End: pos}
	item := func(label, detail string, kind protocol.CompletionItemKind, doc string, sort string) protocol.CompletionItem {
		it := protocol.CompletionItem{Label: label, Kind: &kind, Detail: &detail, SortText: &sort,
			TextEdit: protocol.TextEdit{Range: replace, NewText: label}}
		if doc != "" {
			it.Documentation = protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: doc}
		}
		return it
	}
	var items []protocol.CompletionItem
	if cur.header {
		f, _ := catalog.Parse(d.text)
		for i, m := range d.messages {
			if e := f.Entry(m.name()); e != nil && m.name() != cur.table {
				continue
			}
			items = append(items, item(m.name(), signatureOf(m), protocol.CompletionItemKindFunction, m.fn.Doc, fmt.Sprintf("%04d", i)))
		}
		for i, n := range d.nouns {
			if e := f.Entry(n.name()); e != nil && n.name() != cur.table {
				continue
			}
			items = append(items, item(n.name(), "noun "+n.name(), protocol.CompletionItemKindConstant, n.once.Doc, fmt.Sprintf("1%04d", i)))
		}
		return items
	}
	if n, ok := d.noun(cur.table); ok {
		return d.nounCompletion(n, cur, off, item)
	}
	if cur.key == "" {
		return nil
	}
	m, ok := d.message(cur.table)
	if !ok {
		return nil
	}
	if items := d.caseCompletion(off, cur, item); items != nil {
		return items
	}
	if wordStart > 0 && d.text[wordStart-1] == '$' {
		for i, p := range m.sig.Params {
			items = append(items, item(p.Name, p.Type, protocol.CompletionItemKindVariable, "", fmt.Sprintf("%04d", i)))
		}
		return items
	}
	// A variant's keys: the line so far holds only keys, in a `.match`.
	lineStart := strings.LastIndexByte(d.text[:off], '\n') + 1
	lineStart = max(lineStart, cur.body)
	if strings.Contains(d.text[lineStart:off], "{") || !strings.Contains(d.text[cur.body:off], ".match") {
		return nil
	}
	body := d.text[cur.body:cur.end]
	rules := d.rules(m, cur.key)
	sels := selectorsOf(body, off-cur.body, m, rules)
	if sels == nil {
		return nil
	}
	// The keys the text's other arms have, read line by line, since the
	// text may not parse while it is being written.
	have := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if keys, _, ok := strings.Cut(line, "{{"); ok && !strings.HasPrefix(strings.TrimSpace(line), ".") {
			for _, k := range strings.Fields(keys) {
				have[k] = true
			}
		}
	}
	seen := map[string]bool{}
	n := 0
	for _, sel := range sels {
		for _, k := range sel.Needed {
			if seen[k] {
				continue
			}
			seen[k] = true
			what := fmt.Sprintf("a variant of `$%s`", sel.Name)
			if sel.Plural {
				what = fmt.Sprintf("a plural category in %s: %s", rules.Language, pluralExamples(rules, k))
			}
			if sel.Gender {
				what = fmt.Sprintf("a gender a noun has in %s", rules.Language)
			}
			sortKey := fmt.Sprintf("1%04d", n)
			if !have[k] {
				sortKey = fmt.Sprintf("0%04d", n)
			}
			n++
			items = append(items, item(k, what, protocol.CompletionItemKindEnumMember, "", sortKey))
		}
		if sel.Gender {
			// The language's other genders, which no noun has yet.
			for _, k := range sel.Known {
				if seen[k] {
					continue
				}
				seen[k] = true
				items = append(items, item(k, fmt.Sprintf("a gender in %s, which no noun has yet", rules.Language), protocol.CompletionItemKindEnumMember, "", fmt.Sprintf("1%04d", n)))
				n++
			}
		}
	}
	items = append(items, item("*", "the catch-all, which MessageFormat 2 requires", protocol.CompletionItemKindEnumMember, "", "2"))
	return items
}

// selectorsOf are the keys each selector of a catalog string takes: from
// the string itself, or, while the line at cursor is half typed, from the
// string without that line, or from the message's source text.
func selectorsOf(body string, cursor int, m catalogMessage, rules mf2.Locale) []mf2.SelectorKeys {
	lineStart := strings.LastIndexByte(body[:cursor], '\n') + 1
	lineEnd := strings.IndexByte(body[cursor:], '\n')
	if lineEnd < 0 {
		lineEnd = len(body)
	} else {
		lineEnd += cursor
	}
	for _, text := range []string{body, body[:lineStart] + body[min(lineEnd+1, len(body)):], m.info.Source} {
		if parsed, errs := mf2.Parse(text); len(errs) == 0 && parsed.Matches() {
			return mf2.Selectors(parsed, m.sig, rules)
		}
	}
	// A `.match` with no variant yet: its declarations and selectors alone.
	head := body[:lineStart]
	if parsed, errs := mf2.Parse(head + "* {{}}"); len(errs) == 0 && parsed.Matches() {
		return mf2.Selectors(parsed, m.sig, rules)
	}
	return nil
}

// pluralExamples names a few numbers a category holds, as `2, 3, 4`.
func pluralExamples(rules mf2.Locale, category string) string {
	var out []string
	for n := 0; n <= 1000 && len(out) < 3; n++ {
		if rules.Cardinal != nil && rules.Cardinal.CategoryOf(fmt.Sprint(n)) == category {
			out = append(out, fmt.Sprint(n))
		}
	}
	if len(out) == 0 {
		return "fractions"
	}
	return strings.Join(out, ", ")
}

// signatureOf is how a message reads in a completion's detail.
func signatureOf(m catalogMessage) string {
	var ps []string
	for i, p := range m.fn.Params {
		t := ""
		if i < len(m.sig.Params) {
			t = m.sig.Params[i].Type
		}
		ps = append(ps, p.Name+": "+t)
	}
	return fmt.Sprintf("message %s(%s)", m.name(), strings.Join(ps, ", "))
}

// catalogHover shows, over a catalog's entry, the message it translates:
// its signature and doc comment, whether the translation is current, and
// for a stale one the text it was translated from beside the text now.
func (s *Server) catalogHover(d *catalogDoc, pos protocol.Position) *protocol.Hover {
	off := offsetOfPosition(d.text, pos)
	f, _ := catalog.Parse(d.text)
	var entry *catalog.Entry
	for _, e := range f.Entries {
		if e.Span.Start <= off {
			entry = e
		}
	}
	if entry == nil {
		return nil
	}
	if n, ok := d.noun(entry.Name); ok {
		return d.nounHover(n, entry)
	}
	m, ok := d.message(entry.Name)
	if !ok {
		return nil
	}
	language := analysis.LanguageName(m.info.SourceLocale)
	var b strings.Builder
	fmt.Fprintf(&b, "```nomi\n%s\n```\n", signatureOf(m))
	if m.fn.Doc != "" {
		b.WriteString("\n" + m.fn.Doc + "\n")
	}
	fence := func(text string) {
		b.WriteString("\n```\n" + strings.TrimSpace(text) + "\n```\n")
	}
	switch {
	case !entry.Source.Present:
		fmt.Fprintf(&b, "\nThis translation does not record the %s text it was translated from. The text now:\n", language)
		fence(m.info.Source)
	case !mf2.Same(entry.Source.Text, m.info.Source):
		fmt.Fprintf(&b, "\nThis translation was made from an older %s text.\n\nIt was translated from:\n", language)
		fence(entry.Source.Text)
		b.WriteString("\nThe text now:\n")
		fence(m.info.Source)
	default:
		fmt.Fprintf(&b, "\nThis translation is current: it was made from the %s text as it is now.\n", language)
	}
	r := protocol.Range{Start: positionOfOffset(d.text, entry.Span.Start), End: positionOfOffset(d.text, entry.Span.End)}
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: strings.TrimRight(b.String(), "\n")}, Range: &r}
}

// catalogActions are the quick fixes in a catalog for the entries the range
// touches: mark a stale translation current, and give a translation the
// arms its locale needs.
func (s *Server) catalogActions(d *catalogDoc, rng protocol.Range, diags []protocol.Diagnostic) []protocol.CodeAction {
	start, end := offsetOfPosition(d.text, rng.Start), offsetOfPosition(d.text, rng.End)
	f, perrs := catalog.Parse(d.text)
	var actions []protocol.CodeAction
	for i, e := range f.Entries {
		entryEnd := len(d.text)
		if i+1 < len(f.Entries) {
			entryEnd = f.Entries[i+1].Span.Start
		}
		if end < e.Span.Start || start > entryEnd {
			continue
		}
		related := diagnosticsIn(d.text, diags, e.Span.Start, entryEnd)
		if n, ok := d.noun(e.Name); ok {
			actions = append(actions, d.nounActions(n, e, len(perrs) == 0, related)...)
			continue
		}
		m, ok := d.message(e.Name)
		if !ok {
			continue
		}
		if len(perrs) == 0 && e.Text.Present && (!e.Source.Present || !mf2.Same(e.Source.Text, m.info.Source)) {
			if text, err := catalog.SetSource(d.text, e.Name, m.info.Source); err == nil {
				actions = append(actions, quickFix(
					fmt.Sprintf("Mark the %s translation of `%s` current", d.place.Locale, e.Name),
					d.uri, textEditFor(d.text, text), related, true))
			}
		}
		if e.Text.Present && e.Text.Exact {
			if edits := mf2.FitArms(e.Text.Text, m.sig, d.rules(m, "text")); len(edits) > 0 {
				var tes []protocol.TextEdit
				for _, ed := range edits {
					tes = append(tes, protocol.TextEdit{
						Range:   protocol.Range{Start: positionOfOffset(d.text, e.Text.Offset(ed.Span.Start)), End: positionOfOffset(d.text, e.Text.Offset(ed.Span.End))},
						NewText: ed.Text,
					})
				}
				actions = append(actions, quickFix(
					fmt.Sprintf("Give the %s translation of `%s` the arms %s needs", d.place.Locale, e.Name, analysis.LanguageName(d.place.Locale)),
					d.uri, tes, related, false))
			}
		}
	}
	return actions
}

// missingTranslationActions are the quick fixes on a `message` for each
// locale that lacks its translation: write the entry, with the source text
// as `source` and as `text`, given every arm the locale needs, for the
// translator to translate.
func (s *Server) missingTranslationActions(nodes []ast.Node, fa *analysis.FileAnalysis, rng protocol.Range, diags []protocol.Diagnostic) []protocol.CodeAction {
	var actions []protocol.CodeAction
	for _, m := range messagesOf(nodes, fa) {
		kd := m.info.Decl
		first, last := uint32(kd.Line-1), uint32(kd.NameLine-1)
		if kd.Span.EndLine > 0 {
			last = uint32(kd.Span.EndLine - 1)
		}
		if rng.End.Line < first || rng.Start.Line > last {
			continue
		}
		for _, l := range m.info.Locales {
			if !l.Required {
				continue
			}
			text := strings.Trim(m.info.Source, "\n")
			fitted := mf2.Apply(text, mf2.FitArms(text, m.sig, analysis.MessageRules(l.Locale, false)))
			entry := fmt.Sprintf("[%s]\nsource = %s\ntext = %s\n", kd.Name, catalog.Block(m.info.Source), catalog.Block(fitted))
			var related []protocol.Diagnostic
			for _, dg := range diags {
				if strings.HasPrefix(dg.Message, fmt.Sprintf("%s: `%s` has no translation", l.Locale, kd.Name)) {
					related = append(related, dg)
				}
			}
			title := fmt.Sprintf("Add the %s translation of `%s` to %s", l.Locale, kd.Name, relativeTo(m.info.Root, l.Path))
			uri := pathToURI(l.Path)
			kind := protocol.CodeActionKindQuickFix
			action := protocol.CodeAction{Title: title, Kind: &kind, Diagnostics: related}
			if l.Found {
				existing, _ := s.docs.InputText(l.Path)
				at := positionOfOffset(existing, len(existing))
				prefix := ""
				switch {
				case existing == "":
				case strings.HasSuffix(existing, "\n\n"):
				case strings.HasSuffix(existing, "\n"):
					prefix = "\n"
				default:
					prefix = "\n\n"
				}
				action.Edit = &protocol.WorkspaceEdit{Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {{Range: protocol.Range{Start: at, End: at}, NewText: prefix + entry}},
				}}
			} else {
				if !s.createFiles {
					continue
				}
				action.Edit = &protocol.WorkspaceEdit{DocumentChanges: []any{
					protocol.CreateFile{Kind: "create", URI: protocol.DocumentUri(uri)},
					protocol.TextDocumentEdit{
						TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}},
						Edits:        []any{protocol.TextEdit{Range: protocol.Range{}, NewText: entry}},
					},
				}}
			}
			actions = append(actions, action)
		}
	}
	return actions
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

func quickFix(title, uri string, edits []protocol.TextEdit, diags []protocol.Diagnostic, preferred bool) protocol.CodeAction {
	kind := protocol.CodeActionKindQuickFix
	a := protocol.CodeAction{Title: title, Kind: &kind, Diagnostics: diags,
		Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentUri][]protocol.TextEdit{protocol.DocumentUri(uri): edits}}}
	if preferred {
		a.IsPreferred = &preferred
	}
	return a
}

// diagnosticsIn are the diagnostics among diags that start between the
// offsets start and end of text.
func diagnosticsIn(text string, diags []protocol.Diagnostic, start, end int) []protocol.Diagnostic {
	var out []protocol.Diagnostic
	for _, dg := range diags {
		if off := offsetOfPosition(text, dg.Range.Start); off >= start && off <= end {
			out = append(out, dg)
		}
	}
	return out
}

// textEditFor is the one edit that turns old into new: the span between
// their common prefix and suffix.
func textEditFor(old, new string) []protocol.TextEdit {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	for p > 0 && !utf8.RuneStart(old[p]) {
		p--
	}
	q := 0
	for q < len(old)-p && q < len(new)-p && old[len(old)-1-q] == new[len(new)-1-q] {
		q++
	}
	for q > 0 && !utf8.RuneStart(old[len(old)-q]) {
		q--
	}
	return []protocol.TextEdit{{
		Range:   protocol.Range{Start: positionOfOffset(old, p), End: positionOfOffset(old, len(old)-q)},
		NewText: new[p : len(new)-q],
	}}
}

// positionOfOffset is the protocol position (UTF-16 columns) of a byte
// offset of text.
func positionOfOffset(text string, off int) protocol.Position {
	off = max(0, min(off, len(text)))
	lineStart := strings.LastIndexByte(text[:off], '\n') + 1
	line := strings.Count(text[:lineStart], "\n")
	return protocol.Position{Line: uint32(line), Character: utf16ColOnLine(text[lineStart:], uint32(off-lineStart))}
}

// offsetOfPosition is the byte offset of a protocol position of text.
func offsetOfPosition(text string, pos protocol.Position) int {
	off := 0
	for l := uint32(0); l < pos.Line; l++ {
		nl := strings.IndexByte(text[off:], '\n')
		if nl < 0 {
			return len(text)
		}
		off += nl + 1
	}
	lineEnd := strings.IndexByte(text[off:], '\n')
	line := text[off:]
	if lineEnd >= 0 {
		line = text[off : off+lineEnd]
	}
	return off + byteColOnLine(line, pos.Character) - 1
}
