package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// keywordShape is a keyword that opens a block, and the snippet it inserts
// at a statement start, an operand or the top level. In the body, `\t` is
// one indentation step below the keyword's line.
type keywordShape struct {
	snippet, detail string
}

var keywordShapes = map[string]keywordShape{
	"if":     {"if ${1:condition} {\n\t$0\n}", "if condition { … }"},
	"case":   {"case ${1:value} {\n\t$0\n}", "case value { … }"},
	"fn":     {"fn ${1:name}(${2}): ${3:Unit} {\n\t$0\n}", "fn name(…): Type { … }"},
	"struct": {"struct ${1:Name} {\n\t$0\n}", "struct Name { … }"},
	"enum":   {"enum ${1:Name} {\n\t$0\n}", "enum Name { … }"},
	"impl":   {"impl ${1:Type} {\n\t$0\n}", "impl Type { … }"},
	"test":   {"test \"${1:name}\" {\n\tassert $0\n}", "test \"name\" { … }"},
	"tests":  {"tests \"${1:name}\" {\n\t$0\n}", "tests \"name\" { … }"},
}

// ifElseShape is the `if` with its `else`, which an `if` whose value is not
// Unit needs. It is offered beside `if`, and only as a snippet.
var ifElseShape = keywordShape{"if ${1:condition} {\n\t${2}\n} else {\n\t$0\n}", "if condition { … } else { … }"}

// shapedKeywordCandidates offers words as keywords, those that open a block
// inserting their shape as a snippet. Without snippet support the item
// inserts the bare keyword, as it always did. The snippet replaces the
// keyword's item rather than joining it: with the bare item beside it, every
// `if` would be listed twice, and the bare one inserts no more than typing
// the word does.
func (r *completionRequest) shapedKeywordCandidates(words []string) []candidate {
	out := keywordCandidates(words)
	if !r.shapesFit() {
		return out
	}
	indent := r.lineIndent()
	for i := range out {
		if shape, ok := keywordShapes[out[i].label]; ok {
			out[i].snippet = shapeText(shape.snippet, indent)
			out[i].detail = shape.detail
			out[i].asIs = true
		}
		if out[i].label == "if" && r.s.snippetSupport {
			out = append(out, candidate{
				label:    "if else",
				kind:     protocol.CompletionItemKindKeyword,
				locality: 2,
				snippet:  shapeText(ifElseShape.snippet, indent),
				detail:   ifElseShape.detail,
				asIs:     true,
			})
		}
	}
	return out
}

// shapesFit reports whether a multi-line shape can be written at the
// cursor: not on a `//!` line, whose later lines would need the prompt too,
// and not inside a string's `${...}`.
func (r *completionRequest) shapesFit() bool {
	offs := r.lines.starts
	line := r.doc.Content[offs[lineIndexOf(offs, r.ctx.start)]:r.ctx.start]
	if strings.HasPrefix(strings.TrimLeft(line, " \t"), "//!") {
		return false
	}
	if r.ctx.hit == nil {
		return true
	}
	for _, step := range r.ctx.hit.path {
		switch step.iface().(type) {
		case *ast.StringInterp, *ast.TaggedString:
			return false
		}
	}
	return true
}

// shapeText indents a shape's later lines for a keyword written on a line
// indented by indent: render then sends them relative to that line.
func shapeText(snippet, indent string) string {
	step := strings.Repeat(" ", format.IndentWidth)
	lines := strings.Split(snippet, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + strings.ReplaceAll(lines[i], "\t", step)
	}
	return strings.Join(lines, "\n")
}
