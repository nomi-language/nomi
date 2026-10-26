package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// groupedImportPathError is the error for braces right after a `/` in an
// import path, `import std/{io, regex.Regex}`, at the `{`. Braces select
// names from one file; a path cannot be grouped. The hint spells the paths
// written as an import block when it can read them, and a fixed example when
// it cannot.
func (p *Parser) groupedImportPathError(segments []ast.Node) error {
	open := p.peek()
	prefix := make([]string, len(segments))
	for i, seg := range segments {
		prefix[i] = ast.ImportNodeName(seg)
	}
	paths := p.groupedImportPaths(strings.Join(prefix, "/") + "/")
	if len(paths) == 0 {
		paths = []string{"std/io", "std/regex.Regex"}
	}
	var hint strings.Builder
	hint.WriteString("to import several files, list each in an import block:\nimport {")
	for _, path := range paths {
		hint.WriteString("\n    " + path)
	}
	hint.WriteString("\n}")
	return ParseError{
		Line:    open.Line,
		Col:     open.Col,
		Message: "braces select names from one file, as in `std/regex.{Regex, Match}`",
		Hints:   []string{hint.String()},
	}
}

// groupedImportPaths reads the brace group at the parser's position without
// consuming it: each entry of the group, prefixed. It is nil when the group
// holds anything an import entry cannot.
func (p *Parser) groupedImportPaths(prefix string) []string {
	var paths []string
	var entry strings.Builder
	depth := 0
	for i := p.pos; i < len(p.tokens); i++ {
		tok := p.tokens[i]
		switch {
		case tok.Type == token.LBRACE:
			depth++
			if depth > 1 {
				entry.WriteString("{")
			}
		case tok.Type == token.RBRACE:
			depth--
			if depth == 0 {
				if e := strings.TrimSpace(entry.String()); e != "" {
					paths = append(paths, prefix+e)
				}
				return paths
			}
			trimmed := strings.TrimSuffix(entry.String(), ", ")
			entry.Reset()
			entry.WriteString(trimmed + "}")
		case tok.Type == token.COMMA || tok.Type == token.NEWLINE:
			// A brace list separates its entries with commas or newlines.
			if s := entry.String(); depth > 1 && (strings.HasSuffix(s, "{") || strings.HasSuffix(s, ", ")) {
				continue
			}
			if depth == 1 {
				if e := strings.TrimSpace(entry.String()); e != "" {
					paths = append(paths, prefix+e)
				}
				entry.Reset()
			} else {
				entry.WriteString(", ")
			}
		case tok.Type == token.AS:
			entry.WriteString(" as ")
		case tok.Type == token.DOT || tok.Type == token.SLASH || isImportPathSegment(tok):
			entry.WriteString(tok.Lexeme)
		default:
			return nil
		}
	}
	return nil
}

// importPathText spells path as an import entry writes it: its first
// fileSegments joined by `/`, the rest by `.`.
func importPathText(path []ast.Node, fileSegments int) string {
	var b strings.Builder
	for i, seg := range path {
		switch {
		case i == 0:
		case i < fileSegments:
			b.WriteString("/")
		default:
			b.WriteString(".")
		}
		b.WriteString(ast.ImportNodeName(seg))
	}
	return b.String()
}

// nestedImportListError is the error for a selector list inside another,
// `import nucleotide_count.{self, Nucleotide.{A, C, G, T}}`, at the inner
// `{` (the parser's position). prefix is the path before the outer list and
// open the token index of its `{`. The hint spells the outer list's entries
// as an import block, one entry per line, when it can read them, and a
// fixed example when it cannot.
func (p *Parser) nestedImportListError(prefix string, open int) error {
	inner := p.peek()
	saved := p.pos
	p.pos = open
	paths := p.groupedImportPaths(prefix + ".")
	p.pos = saved
	for i, path := range paths {
		if path == prefix+".self" {
			paths[i] = prefix
		}
	}
	if len(paths) == 0 {
		paths = []string{"nucleotide_count", "nucleotide_count.Nucleotide.{A, C, G, T}"}
	}
	var hint strings.Builder
	hint.WriteString("give each entry its own line in an import block:\nimport {")
	for _, path := range paths {
		hint.WriteString("\n    " + path)
	}
	hint.WriteString("\n}")
	return ParseError{
		Line:    inner.Line,
		Col:     inner.Col,
		Message: "a selector list cannot hold another list",
		Hints:   []string{hint.String()},
	}
}
