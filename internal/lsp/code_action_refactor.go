package lsp

import (
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The refactor actions answer for the requested range, from the current
// analysis, inside the top-level declaration that holds the range's start:
//
//   - "Convert to pipe" and "Convert from pipe" (code_action_pipe.go)
//   - "Pattern match on ..." (code_action_match.go)
//   - "Generate function ..." for an undefined call (code_action_generate.go)
//   - "Extract variable" and "Inline variable" (code_action_extract.go)
//   - "Convert to case" and "Convert to if/else" (code_action_if_case.go)
//
// Each one edits the document text and hands the result to the fill fixes'
// finish, so a document that was `nomi fmt` output stays formatted and the
// edit is the run of lines that changed.

// refactorRequest is one code-action request's view of the declaration
// under the requested range.
type refactorRequest struct {
	*fillRequest
	uri string
	// start and end are the requested range as byte offsets; equal for a
	// cursor.
	start, end int
	decl       ast.Node
	parent     map[ast.Node]ast.Node
	kids       map[ast.Node][]ast.Node
	// all is every node of decl in preorder.
	all   []ast.Node
	spans map[ast.Node][2]int
}

// buildRefactorActions returns the refactor actions for rng, and the
// generate-function quick fix for each undefined call under it, naming the
// diagnostic of diags that reports the call.
func (s *Server) buildRefactorActions(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string, rng protocol.Range, diags []protocol.Diagnostic, only []protocol.CodeActionKind) []protocol.CodeAction {
	lines := newLineIndex(content)
	f := &fillRequest{s: s, content: content, nodes: nodes, fa: fa, offs: lines.starts, lines: lines}
	r := &refactorRequest{fillRequest: f, uri: uri}
	r.start, r.end = r.offsetOf(rng.Start), r.offsetOf(rng.End)
	if r.end < r.start {
		r.start, r.end = r.end, r.start
	}
	r.decl = r.declAt(r.start)
	if r.decl == nil {
		return nil
	}
	f.tokens = lexer.Lex(content)
	r.index()

	var out []protocol.CodeAction
	add := func(kind protocol.CodeActionKind, title, edited string, ds []protocol.Diagnostic) {
		if !kindAllowed(only, kind) {
			return
		}
		edit, ok := r.finish(edited)
		if !ok {
			return
		}
		a := protocol.CodeAction{
			Title:       title,
			Kind:        &kind,
			Diagnostics: ds,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(r.uri): {edit},
				},
			},
		}
		if kind == protocol.CodeActionKindQuickFix {
			preferred := true
			a.IsPreferred = &preferred
		}
		out = append(out, a)
	}
	for _, g := range r.generateFunctions(diags) {
		add(protocol.CodeActionKindQuickFix, g.title, g.edited, g.diags)
	}
	if title, edited, ok := r.toPipe(); ok {
		add(protocol.CodeActionKindRefactorRewrite, title, edited, nil)
	}
	if title, edited, ok := r.fromPipe(); ok {
		add(protocol.CodeActionKindRefactorRewrite, title, edited, nil)
	}
	for _, m := range r.patternMatches() {
		add(protocol.CodeActionKindRefactorRewrite, m.title, m.edited, nil)
	}
	for _, o := range r.ifCaseConversions() {
		add(protocol.CodeActionKindRefactorRewrite, o.title, o.edited, nil)
	}
	if title, edited, ok := r.extractVariable(); ok {
		add(protocol.CodeActionKindRefactorExtract, title, edited, nil)
	}
	if title, edited, ok := r.inlineVariable(); ok {
		add(protocol.CodeActionKindRefactorInline, title, edited, nil)
	}
	return out
}

// offer is one action a refactor offers: its title, the edited document,
// and the diagnostics it fixes.
type offer struct {
	title, edited string
	diags         []protocol.Diagnostic
}

// offsetOf is the byte offset of an LSP (UTF-16) position.
func (r *refactorRequest) offsetOf(p protocol.Position) int {
	if int(p.Line) >= len(r.offs) {
		return len(r.content)
	}
	off := posToOffset(r.offs, int(p.Line)+1, r.lines.byteCol(p.Line, p.Character))
	if off > len(r.content) {
		off = len(r.content)
	}
	return off
}

// declAt is the top-level function, impl block or test whose lines hold off.
func (r *refactorRequest) declAt(off int) ast.Node {
	line := lineIndexOf(r.offs, off) + 1
	for _, n := range r.nodes {
		first, last := 0, 0
		switch d := n.(type) {
		case *ast.FuncDef:
			if d.Body != nil {
				first, last = d.Line, d.Body.EndLine
			}
		case *ast.ImplBlock:
			first, last = d.Line, d.EndLine
		case *ast.TestDecl:
			if d.Body != nil {
				first, last = d.Line, d.Body.EndLine
			}
		}
		if first <= 0 || analysis.IsSynthesizedLine(first) {
			continue
		}
		if line >= first && line <= last {
			return n
		}
	}
	return nil
}

// index records decl's nodes, each one's parent and its children.
func (r *refactorRequest) index() {
	r.parent = map[ast.Node]ast.Node{}
	r.kids = map[ast.Node][]ast.Node{}
	r.spans = map[ast.Node][2]int{}
	seen := map[ast.Node]bool{r.decl: true}
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		r.all = append(r.all, n)
		for _, c := range nodeChildren(n) {
			if seen[c] {
				continue
			}
			seen[c] = true
			r.parent[c] = n
			r.kids[n] = append(r.kids[n], c)
			visit(c)
		}
	}
	visit(r.decl)
}

// nodeChildren is the nodes n holds directly, in field order.
func nodeChildren(n ast.Node) []ast.Node {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil
	}
	var out []ast.Node
	collectNodes(v.Elem(), &out)
	return out
}

func collectNodes(v reflect.Value, out *[]ast.Node) {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return
		}
		if n, ok := v.Interface().(ast.Node); ok {
			*out = append(*out, n)
			return
		}
		collectNodes(v.Elem(), out)
	case reflect.Interface:
		if !v.IsNil() {
			collectNodes(v.Elem(), out)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanInterface() {
				collectNodes(f, out)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectNodes(v.Index(i), out)
		}
	}
}

// span is the [start, end) byte range of n's source text. The start is the
// earliest position any node of n's subtree records. The end is found on the
// tokens: past the token at the latest recorded position, then past every
// bracket the text opened and has not closed.
func (r *refactorRequest) span(n ast.Node) (int, int, bool) {
	if s, ok := r.spans[n]; ok {
		return s[0], s[1], s[0] < s[1]
	}
	r.spans[n] = [2]int{}
	first, last := -1, -1
	var walk func(m ast.Node)
	walk = func(m ast.Node) {
		line, col := nodePos(m)
		if line > 0 && col > 0 && !analysis.IsSynthesizedLine(line) {
			off := posToOffset(r.offs, line, col)
			if first < 0 || off < first {
				first = off
			}
			if off > last {
				last = off
			}
		}
		for _, c := range r.kids[m] {
			walk(c)
		}
	}
	walk(n)
	if first < 0 {
		return 0, 0, false
	}
	i := r.tokenAt(first)
	if i >= len(r.tokens) || r.offOf(r.tokens[i]) != first {
		return 0, 0, false
	}
	depth := 0
	step := func(t token.Token) {
		switch t.Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET, token.STRING_START, token.TRIPLE_STRING_START:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET, token.STRING_END, token.TRIPLE_STRING_END:
			depth--
		}
	}
	k := i
	for ; k+1 < len(r.tokens) && r.tokens[k+1].Type != token.EOF && r.offOf(r.tokens[k+1]) <= last; k++ {
		step(r.tokens[k])
	}
	step(r.tokens[k])
	if r.tokens[k].Type == token.DOT && k+1 < len(r.tokens) && (r.tokens[k+1].Type == token.IDENT || r.tokens[k+1].Type == token.TYPE_IDENT) {
		k++
	}
	for depth > 0 {
		k++
		if k >= len(r.tokens) || r.tokens[k].Type == token.EOF {
			return 0, 0, false
		}
		step(r.tokens[k])
	}
	end := r.tokenEnd(k)
	r.spans[n] = [2]int{first, end}
	return first, end, first < end
}

// tokenEnd is the offset just past token k's text.
func (r *refactorRequest) tokenEnd(k int) int {
	start := r.offOf(r.tokens[k])
	end := len(r.content)
	hole := false
	if k+1 < len(r.tokens) && r.tokens[k+1].Type != token.EOF {
		end = r.offOf(r.tokens[k+1])
		switch r.tokens[k+1].Type {
		case token.STRING_PART, token.STRING_END, token.TRIPLE_STRING_PART, token.TRIPLE_STRING_END:
			// The string's next token starts after the `}` that closes
			// the hole token k ends.
			hole = true
		}
	}
	trim := func() {
		for end > start+1 && strings.ContainsRune(" \t\r\n", rune(r.content[end-1])) {
			end--
		}
	}
	trim()
	if hole && end > start+1 && r.content[end-1] == '}' {
		end--
		trim()
	}
	return end
}

// contains reports whether n's text holds the requested range.
func (r *refactorRequest) contains(n ast.Node) bool {
	s, e, ok := r.span(n)
	return ok && s <= r.start && r.end <= e
}

// innermost is the deepest node keep accepts whose text holds the range.
func (r *refactorRequest) innermost(keep func(ast.Node) bool) ast.Node {
	var found ast.Node
	for _, n := range r.all {
		if keep(n) && r.contains(n) {
			found = n // preorder: a later match is nested in an earlier one
		}
	}
	return found
}

// isPipe reports whether n is a `|>` node.
func isPipe(n ast.Node) bool {
	b, ok := n.(*ast.Binary)
	return ok && b.Op == "|>"
}

// isPipeStage reports whether n is the right side of a `|>`: a stage that
// takes the piped value.
func (r *refactorRequest) isPipeStage(n ast.Node) bool {
	b, ok := r.parent[n].(*ast.Binary)
	return ok && b.Op == "|>" && b.Right == n
}

// statementOf is the statement holding n and the block that lists it.
func (r *refactorRequest) statementOf(n ast.Node) (ast.Node, *ast.Block) {
	for cur := n; cur != nil; cur = r.parent[cur] {
		if b, ok := r.parent[cur].(*ast.Block); ok {
			for _, s := range b.Stmts {
				if s == cur {
					return cur, b
				}
			}
		}
	}
	return nil, nil
}

// lineIndentOf is the leading whitespace of the line holding off.
func (r *refactorRequest) lineIndentOf(off int) string {
	return r.lineIndentAt(off)
}

// render is n in `nomi fmt` layout, its later lines indented by indent so
// the text can stand where a line indented by indent holds it.
func render(n ast.Node, indent string) string {
	text := strings.TrimRight(format.RenderNode(n), "\n")
	return strings.ReplaceAll(text, "\n", "\n"+indent)
}

// replace is the document with [start, end) replaced by text.
func (r *refactorRequest) replace(start, end int, text string) string {
	return r.content[:start] + text + r.content[end:]
}
