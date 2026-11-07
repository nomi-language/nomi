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

// "Add plural forms for `$count`": on a `phrase` whose text has no
// `.match`, for each `Int` parameter, and on a `noun` for its `$count`, a
// rewrite of the text to select on the parameter by plural category, with
// an arm for each category the source locale's CLDR rules select for a
// whole number and the catch-all, each holding the old text
// (mf2.AddPluralForms). The body is written the way `nomi fmt` writes a
// multi-line one: the opening backtick, a line per declaration and arm
// indented four spaces past the declaration, and the closing backtick on a
// line of its own at that indentation. In a catalog the same rewrite is
// offered on a translation's `text` and a noun's form under the cursor,
// with the catalog locale's categories, written as a multi-line literal
// string.

// pluralFormActions are the rewrites for the `phrase` or `noun` under rng.
func pluralFormActions(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string, rng protocol.Range) []protocol.CodeAction {
	line := int(rng.Start.Line) + 1
	var out []protocol.CodeAction
	for _, n := range nodes {
		kd, _ := keywordDeclOf(n)
		if kd == nil {
			continue
		}
		endLine := kd.Body.Line
		if kd.Body.Triple {
			endLine = kd.Body.EndLine
		}
		if line < kd.Line || line > endLine {
			continue
		}
		var sig mf2.Signature
		var source string
		switch d := n.(type) {
		case *ast.FuncDef:
			info := analysis.PhraseOf(d)
			if info == nil {
				continue
			}
			sig, source = analysis.PhraseSignature(fa, d), info.SourceLocale
		case *ast.OnceBinding:
			info := analysis.NounOf(d)
			if info == nil {
				continue
			}
			sig, source = mf2.NounSignature(kd.Name), info.SourceLocale
		default:
			continue
		}
		loc := analysis.PhraseRules(source, false)
		for _, name := range mf2.PluralCandidates(kd.Body.Value, sig) {
			text, ok := mf2.AddPluralForms(kd.Body.Value, name, sig, loc)
			if !ok {
				continue
			}
			edit := protocol.TextEdit{Range: bodyRange(content, kd), NewText: bodyText(content, kd.Line, text)}
			out = append(out, rewriteAction(fmt.Sprintf("Add plural forms for `$%s`", name), uri, edit))
		}
	}
	return out
}

// bodyRange is the range of kd's body, its backticks included.
func bodyRange(content string, kd *ast.KeywordDecl) protocol.Range {
	b := kd.Body
	startLine, startCol := b.Line, b.Col-1
	endLine, endCol := b.Line, b.Col-1+len(b.Value)+2
	if b.Triple {
		endLine, endCol = b.EndLine, b.EndCol
	}
	return protocol.Range{
		Start: protocol.Position{Line: uint32(startLine - 1), Character: byteToUTF16Col(content, uint32(startLine-1), uint32(startCol))},
		End:   protocol.Position{Line: uint32(endLine - 1), Character: byteToUTF16Col(content, uint32(endLine-1), uint32(endCol))},
	}
}

// bodyText is text as a multi-line body of the declaration on line
// declLine: each line four spaces past the declaration's indentation, and
// the closing backtick on a line of its own there.
func bodyText(content string, declLine int, text string) string {
	lineText, _ := nthLine(content, uint32(declLine-1))
	indent := lineText[:len(lineText)-len(strings.TrimLeft(lineText, " \t"))] + "    "
	var b strings.Builder
	b.WriteString("`\n")
	for _, l := range strings.Split(text, "\n") {
		if l != "" {
			b.WriteString(indent + l)
		}
		b.WriteString("\n")
	}
	b.WriteString(indent + "`")
	return b.String()
}

func rewriteAction(title, uri string, edits ...protocol.TextEdit) protocol.CodeAction {
	kind := protocol.CodeActionKindRefactorRewrite
	return protocol.CodeAction{Title: title, Kind: &kind,
		Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentUri][]protocol.TextEdit{protocol.DocumentUri(uri): edits}}}
}

// catalogPluralActions are the rewrites in a catalog for the string under
// rng: a phrase's `text`, or one of a noun's forms.
func (d *catalogDoc) catalogPluralActions(rng protocol.Range) []protocol.CodeAction {
	at := offsetOfPosition(d.text, rng.Start)
	f, _ := catalog.Parse(d.text)
	loc := analysis.PhraseRules(d.place.Locale, false)
	var out []protocol.CodeAction
	offer := func(v catalog.Value, sig mf2.Signature) {
		if !v.Present || at < v.KeySpan.Start || at > v.Span.End {
			return
		}
		for _, name := range mf2.PluralCandidates(v.Text, sig) {
			text, ok := mf2.AddPluralForms(v.Text, name, sig, loc)
			if !ok {
				continue
			}
			edit := protocol.TextEdit{
				Range:   protocol.Range{Start: positionOfOffset(d.text, v.Span.Start), End: positionOfOffset(d.text, v.Span.End)},
				NewText: catalog.Literal(text + "\n"),
			}
			out = append(out, rewriteAction(fmt.Sprintf("Add %s plural forms for `$%s`", analysis.LanguageName(d.place.Locale), name), d.uri, edit))
		}
	}
	for _, e := range f.Entries {
		if _, ok := d.noun(e.Name); ok {
			for _, field := range e.Fields {
				if field.Name != "gender" {
					offer(field.Value, mf2.NounSignature(e.Name))
				}
			}
			continue
		}
		if m, ok := d.phrase(e.Name); ok {
			offer(e.Text, m.sig)
		}
	}
	return out
}
