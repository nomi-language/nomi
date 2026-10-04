package lsp

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
	"github.com/nomi-language/nomi/std"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// COMPLETION INSIDE A TYPED LITERAL.
//
// In the body of `<Type>"…"` completion offers the bodies the type's own
// documentation writes: every `<Type>"…"` (or `"""…"""`, or backtick) on a
// `///` or `//!` line of the type's doc comment, of its `impl Literal` block
// and of its inherent `impl` blocks, in the order the file has them. Each is
// inserted as a snippet whose tab stops are the body's runs of letters and
// digits, so `2026-05-04` becomes `${1:2026}-${2:05}-${3:04}`. A type whose
// docs write no example gets nothing.

// literalExampleLimit is how many examples completion offers.
const literalExampleLimit = 5

// literalBody is a cursor inside a typed literal's body.
type literalBody struct {
	tag string
	// start is the body's first byte; end is its closing delimiter, or the
	// cursor when the literal is not closed on its line.
	start, end int
}

// literalBodyAt reports whether off is in the body of a typed literal opened
// on off's line: the line is scanned from its start (after a `//!` prompt
// marker) for strings, so a quote inside a string or a comment opens nothing.
func literalBodyAt(content string, off int) (literalBody, bool) {
	if off > len(content) {
		return literalBody{}, false
	}
	lineStart := strings.LastIndexByte(content[:off], '\n') + 1
	lineEnd := len(content)
	if i := strings.IndexByte(content[off:], '\n'); i >= 0 {
		lineEnd = off + i
	}
	line := content[lineStart:lineEnd]
	i := 0
	if t := strings.TrimLeft(line, " \t"); strings.HasPrefix(t, "//!") {
		i = len(line) - len(t) + 3
	}
	cursor := off - lineStart
	for i < cursor {
		c := line[i]
		switch {
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return literalBody{}, false
		case c == '"' || c == '`':
			delim := string(c)
			if strings.HasPrefix(line[i:], `"""`) {
				delim = `"""`
			}
			tagStart := i
			for tagStart > 0 && isWordByte(line[tagStart-1]) {
				tagStart--
			}
			tag := line[tagStart:i]
			bodyStart := i + len(delim)
			close, closed := closingDelim(line, bodyStart, delim)
			if !closed || close >= cursor {
				if bodyStart > cursor || tag == "" || tag[0] < 'A' || tag[0] > 'Z' ||
					(tagStart > 0 && line[tagStart-1] == '.') || interpolating(line[bodyStart:cursor], delim) {
					return literalBody{}, false
				}
				end := cursor
				if closed {
					end = close
				}
				return literalBody{tag: tag, start: lineStart + bodyStart, end: lineStart + end}, true
			}
			i = close + len(delim)
		default:
			i++
		}
	}
	return literalBody{}, false
}

// closingDelim is the index of the delimiter closing a string whose body
// starts at from on line.
func closingDelim(line string, from int, delim string) (int, bool) {
	for j := from; j < len(line); j++ {
		if delim != "`" && line[j] == '\\' {
			j++
			continue
		}
		if strings.HasPrefix(line[j:], delim) {
			return j, true
		}
	}
	return 0, false
}

// interpolating reports whether the body text before the cursor leaves it
// inside an open `${...}`, where the position is code.
func interpolating(body, delim string) bool {
	if delim == "`" {
		return false
	}
	open := strings.LastIndex(body, "${")
	return open >= 0 && !strings.Contains(body[open:], "}")
}

// literalCompletions offers the examples of the literal's type's docs.
func (s *Server) literalCompletions(doc *analysis.DocSnapshot, lb literalBody) []protocol.CompletionItem {
	examples := s.literalExamples(doc, lb.tag)
	if len(examples) == 0 {
		return nil
	}
	rng := protocol.Range{Start: offsetToPosition(doc.Content, lb.start), End: offsetToPosition(doc.Content, lb.end)}
	rng = utf16RangeFromContent(doc.Content, rng)
	kind := protocol.CompletionItemKindValue
	detail := fmt.Sprintf("example from %s docs", lb.tag)
	items := make([]protocol.CompletionItem, 0, len(examples))
	for i, body := range examples {
		item := protocol.CompletionItem{
			Label:      body,
			Kind:       &kind,
			Detail:     &detail,
			FilterText: &examples[i],
		}
		sortText := fmt.Sprintf("%04d", i)
		item.SortText = &sortText
		text := body
		if s.snippetSupport {
			text = exampleSnippet(body)
			format := protocol.InsertTextFormatSnippet
			item.InsertTextFormat = &format
		}
		item.TextEdit = protocol.TextEdit{Range: rng, NewText: text}
		items = append(items, item)
	}
	return items
}

// exampleSnippet makes each run of letters and digits in body a tab stop.
func exampleSnippet(body string) string {
	var b strings.Builder
	stop := 0
	for i := 0; i < len(body); {
		if !isAlnum(body[i]) {
			b.WriteString(snippetEscape(body[i : i+1]))
			i++
			continue
		}
		j := i
		for j < len(body) && isAlnum(body[j]) {
			j++
		}
		stop++
		fmt.Fprintf(&b, "${%d:%s}", stop, body[i:j])
		i = j
	}
	return b.String()
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// literalExamples are the distinct literal bodies tag's docs write, in file
// order, at most literalExampleLimit. The type is the one whose `Literal`
// impl the analysis knows under that name, read from its declaring file.
func (s *Server) literalExamples(doc *analysis.DocSnapshot, tag string) []string {
	fa := doc.Analysis
	if fa == nil || fa.ModuleScope == nil || fa.ModuleScope.Lookup(tag) == nil {
		return nil
	}
	return s.literalExamplesOf(doc, tag)
}

// stdLiteralExamples caches the examples of the standard library's literal
// types, whose embedded sources never change: reading them parses the
// declaring file.
var stdLiteralExamples sync.Map // "std/calendar\x00Date" → []string

// literalExamplesOf is literalExamples for a type the document may not name
// yet: one an accepted completion item imports. A standard-library type's
// examples are read once per process.
func (s *Server) literalExamplesOf(doc *analysis.DocSnapshot, tag string) []string {
	fa := doc.Analysis
	if fa == nil || fa.ProjectImpls == nil {
		return nil
	}
	home, found := "", false
	for _, fn := range fa.ProjectImpls.IfaceMethodImpls["Literal"]["from_fragments"] {
		if fn == nil || fa.ProjectImpls.ReceiverOf(fn) != tag {
			continue
		}
		h, ok := fa.ProjectImpls.ImplFiles[fn]
		if !ok || (found && h != home) {
			return nil
		}
		home, found = h, true
	}
	if !found {
		return nil
	}
	key := home + "\x00" + tag
	if cached, ok := stdLiteralExamples.Load(key); ok {
		return cached.([]string)
	}
	src, ok := s.moduleSource(home, doc)
	if !ok {
		return nil
	}
	examples := docExamples(src, tag)
	if strings.HasPrefix(home, "std/") {
		stdLiteralExamples.Store(key, examples)
	}
	return examples
}

// moduleSource is the text of the file a module key names, as
// moduleKeyToURI resolves it: the document itself, a stdlib module, or a
// project file (open buffer first).
func (s *Server) moduleSource(home string, doc *analysis.DocSnapshot) (string, bool) {
	switch {
	case home == "":
		return doc.Content, true
	case strings.HasPrefix(home, "std/"):
		data, ok := std.ReadFile(strings.TrimPrefix(home, "std/"))
		return string(data), ok
	}
	return s.contentForURI(s.moduleKeyToURI(home, doc.URI))
}

// docExamples scans src's doc lines about tag: the comment block above its
// declaration, and its `impl Literal` and inherent `impl` blocks.
func docExamples(src, tag string) []string {
	lines := strings.Split(src, "\n")
	var ranges [][2]int // 0-based inclusive line ranges
	decl := regexp.MustCompile(`^\s*(pub\s+)?(opaque\s+)?(host\s+)?(struct|enum|type)\s+` + regexp.QuoteMeta(tag) + `\b`)
	for i, l := range lines {
		if !decl.MatchString(l) {
			continue
		}
		first := i
		for first > 0 && strings.HasPrefix(strings.TrimSpace(lines[first-1]), "//") {
			first--
		}
		ranges = append(ranges, [2]int{first, i})
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	for _, n := range nodes {
		b, ok := n.(*ast.ImplBlock)
		if !ok || b.Receiver == nil || b.EndLine < b.Line {
			continue
		}
		if name, _, _ := strings.Cut(b.Receiver.TypeString(), "<"); name != tag {
			continue
		}
		if b.Interface != nil {
			if name, _, _ := strings.Cut(b.Interface.TypeString(), "<"); name != "Literal" {
				continue
			}
		}
		ranges = append(ranges, [2]int{b.Line - 1, b.EndLine - 1})
	}
	inRange := func(i int) bool {
		for _, r := range ranges {
			if i >= r[0] && i <= r[1] {
				return true
			}
		}
		return false
	}
	var out []string
	seen := map[string]bool{}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !inRange(i) {
			continue
		}
		var bodies []string
		switch {
		case strings.HasPrefix(t, "//!"):
			bodies = taggedLiteralBodies(t[len("//!"):], tag)
		case strings.HasPrefix(t, "///"):
			for _, span := range markdownCodeSpans(t[len("///"):]) {
				bodies = append(bodies, taggedLiteralBodies(span, tag)...)
			}
		}
		for _, body := range bodies {
			if body == "" || body == "..." || body == "…" || seen[body] {
				continue
			}
			seen[body] = true
			out = append(out, body)
			if len(out) == literalExampleLimit {
				return out
			}
		}
	}
	return out
}

// taggedLiteralBodies lexes code, a `//!` example line or a Markdown code span
// from a `///` line, and returns the body of each typed literal tagged tag
// that has no interpolation. Prose that only mentions the type, such as the
// "`Date`s" of "two `Date`s", holds no literal token and yields nothing.
func taggedLiteralBodies(code, tag string) []string {
	var out []string
	for _, tok := range lexer.Lex(code) {
		if isStaticTaggedLiteral(tok.Type) && tok.Tag == tag {
			out = append(out, tok.Lexeme)
		}
	}
	return out
}

func isStaticTaggedLiteral(t token.TokenType) bool {
	return t == token.TAGGED_STRING_LITERAL || t == token.RAW_TAGGED_STRING_LITERAL ||
		t == token.TAGGED_TRIPLE_STRING_LITERAL
}

// markdownCodeSpans returns the contents of the code spans in a line of
// Markdown: text between a run of N backticks and the next run of exactly N,
// so “ Regex`\d+` “ yields the raw literal with its own backticks.
func markdownCodeSpans(line string) []string {
	var out []string
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		start := i + n
		end := -1
		for j := start; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(line) && line[j+m] == '`' {
				m++
			}
			if m == n {
				end = j
				break
			}
			j += m
		}
		if end < 0 {
			break
		}
		out = append(out, line[start:end])
		i = end + n
	}
	return out
}
