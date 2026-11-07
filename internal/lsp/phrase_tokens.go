package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/catalog"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/semtokens"
)

// Semantic tokens inside MessageFormat 2 text: the body of a `phrase` or
// `noun`, and the `source`, `text` and form strings of a catalog's
// entries. No editor Nomi supports ships an MF2 grammar, so without these
// the text reads as one string. Text a pattern writes gets no token, so
// it keeps the editor's string color; the syntax around it does
// (mf2.Tokens). A text with a syntax error has tokens up to the error.

// mf2TokenType is the legend's type for an MF2 token. A variable naming a
// parameter of the declaration (a noun's `$count`) is a parameter, and
// one a `.local` declares a variable.
func mf2TokenType(t mf2.Token, params map[string]bool) string {
	switch t.Kind {
	case mf2.TokenKeyword, mf2.TokenCatchAll:
		return "keyword"
	case mf2.TokenVariable:
		if params[t.Name] {
			return "parameter"
		}
		return "variable"
	case mf2.TokenNamespace:
		return "namespace"
	case mf2.TokenFunction:
		return "function"
	case mf2.TokenOption, mf2.TokenAttribute:
		return "property"
	case mf2.TokenMarkup:
		return "type"
	case mf2.TokenName, mf2.TokenKey:
		return "enumMember"
	case mf2.TokenNumber:
		return "number"
	case mf2.TokenQuoted:
		return "string"
	default: // quotes, braces and operators
		return "operator"
	}
}

// keywordDeclOf is the `phrase` or `noun` declaration n is, or was
// expanded from, and the names of the parameters its text reads.
func keywordDeclOf(n ast.Node) (*ast.KeywordDecl, map[string]bool) {
	kd, ok := n.(*ast.KeywordDecl)
	if !ok {
		info := analysis.ExpansionOfNode(n)
		if info == nil {
			return nil, nil
		}
		kd = info.Decl
	}
	if kd == nil || kd.Body == nil || kd.Keyword != "phrase" && kd.Keyword != "noun" {
		return nil, nil
	}
	params := map[string]bool{}
	if kd.Keyword == "noun" {
		params[mf2.NounParam] = true
	}
	for _, p := range kd.Params {
		params[p.Name] = true
	}
	return kd, params
}

// phraseBodyTokens are the tokens of the MF2 in nodes' `phrase` and `noun`
// bodies, with byte columns. A token that would span lines (a quoted
// literal with a line break) is left out.
func phraseBodyTokens(nodes []ast.Node) []semtokens.Token {
	var out []semtokens.Token
	for _, n := range nodes {
		kd, params := keywordDeclOf(n)
		if kd == nil {
			continue
		}
		for _, t := range mf2.Tokens(kd.Body.Value) {
			line, col := kd.BodyPosition(t.Span.Start)
			endLine, endCol := kd.BodyPosition(t.Span.End)
			if endLine != line || endCol <= col {
				continue
			}
			out = append(out, semtokens.Token{Line: line, Col: col, Length: endCol - col, Type: mf2TokenType(t, params)})
		}
	}
	return out
}

// catalogTokens are the tokens of the MF2 in a catalog's strings, with
// byte columns: each entry's `source` and `text`, and a noun's forms. A
// basic string with an escape cannot be placed byte for byte and gets
// none.
func catalogTokens(d *catalogDoc) []semtokens.Token {
	f, _ := catalog.Parse(d.text)
	var out []semtokens.Token
	for _, e := range f.Entries {
		params := map[string]bool{}
		values := []catalog.Value{e.Source, e.Text}
		if _, ok := d.noun(e.Name); ok {
			params[mf2.NounParam] = true
			values = []catalog.Value{e.Source}
			for _, field := range e.Fields {
				if field.Name != "gender" {
					values = append(values, field.Value)
				}
			}
		} else if m, ok := d.phrase(e.Name); ok {
			for _, p := range m.sig.Params {
				params[p.Name] = true
			}
		}
		for _, v := range values {
			if !v.Present || !v.Exact {
				continue
			}
			for _, t := range mf2.Tokens(v.Text) {
				line, col := catalog.LineCol(d.text, v.Offset(t.Span.Start))
				endLine, endCol := catalog.LineCol(d.text, v.Offset(t.Span.End))
				if endLine != line || endCol <= col {
					continue
				}
				out = append(out, semtokens.Token{Line: line, Col: col, Length: endCol - col, Type: mf2TokenType(t, params)})
			}
		}
	}
	return out
}
