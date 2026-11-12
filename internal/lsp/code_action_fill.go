package lsp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Three quick fixes write what a checker error says is missing:
//
//   - "Implement missing functions" on an `impl Iface for T` block that lacks
//     required functions: each one, at the end of the block, with the
//     signature the completion stub writes (completion_impl_stubs.go).
//   - "Add missing fields" on a struct literal: each required field the
//     literal does not write, in declaration order, in the literal's layout.
//   - "Add missing arms" on a non-exhaustive `case` over an enum: an arm per
//     variant the checker names, with the pattern the completion item writes
//     (completion_structural.go), at the arms' indentation.
//
// Each written value or body is the `todo` expression (placeholderAt),
// which fits whatever type its position expects. The result checks cleanly:
// a stub's parameters are not reported as unread because its body holds a
// `todo`, and the language server lists each hole as a `todo` warning.
//
// When the document is already `nomi fmt` output, the edited document is
// formatted again and the action returns the lines that differ, so applying
// it leaves a formatted file.

// fillPlaceholder is the `todo` a fill writes, and fillReasonedPlaceholder
// the one it writes where a binding named `todo` is in scope, which a bare
// `todo` would read (spec §2 *Keywords*): with a reason, `todo` is the
// placeholder everywhere.
const (
	fillPlaceholder         = "todo"
	fillReasonedPlaceholder = `todo "fill in"`
)

// todoWord matches a `todo` a written signature or pattern binds.
var todoWord = regexp.MustCompile(`\btodo\b`)

// placeholderAt is the placeholder a fill writes at line and col of the
// file, inside the signature or pattern binds is the text of (a stub's
// signature, an arm's pattern; "" for none): fillPlaceholder, or
// fillReasonedPlaceholder when a binding named `todo` is in scope there or
// binds names one.
func (f *fillRequest) placeholderAt(line, col int, binds string) string {
	if todoWord.MatchString(binds) {
		return fillReasonedPlaceholder
	}
	if f.fa != nil {
		if scope := f.fa.ScopeAt(analysis.Pos{Line: line, Col: col}); scope != nil && scope.Lookup("todo") != nil {
			return fillReasonedPlaceholder
		}
	}
	return fillPlaceholder
}

var (
	missingFunctionMessage = regexp.MustCompile(`^impl '[^']*' for '[^']*': missing function '[^']+' required by interface '[^']*'$`)
	missingFieldMessage    = regexp.MustCompile(`^missing field '([^']+)' of `)
	missingArmsMessage     = regexp.MustCompile("^non-exhaustive case on [^:]+: missing (.+)$")
)

type fillKind int

const (
	fillFunctions fillKind = iota
	fillFields
	fillArms
)

// fillRequest holds one code-action request's document for the fill fixes.
type fillRequest struct {
	s       *Server
	content string
	nodes   []ast.Node
	fa      *analysis.FileAnalysis
	offs    []int
	lines   *lineIndex
	tokens  []token.Token
	// formatted is 1 when content is `nomi fmt` output, -1 when it is not,
	// 0 before it is asked.
	formatted int
}

// fillTarget is one node a fix rewrites, and the diagnostics about it.
type fillTarget struct {
	kind fillKind
	// line:col starts the diagnostics' range and endLine:endCol ends it
	// (1-based, byte columns).
	line, col       int
	endLine, endCol int
	diags           []protocol.Diagnostic
	// names are what the messages say is missing, in message order.
	names []string
}

// buildFillActions returns a fill fix for each node one of diags reports
// something missing from. The diagnostics about one node (one per missing
// function or field) share one action, which fixes them all.
func (s *Server) buildFillActions(content string, nodes []ast.Node, fa *analysis.FileAnalysis, uri string, diags []protocol.Diagnostic) []protocol.CodeAction {
	var targets []*fillTarget
	lines := newLineIndex(content)
	for _, d := range diags {
		kind, name, ok := classifyFill(diagnosticHeadline(d.Message))
		if !ok {
			continue
		}
		line := int(d.Range.Start.Line) + 1
		col := lines.byteCol(d.Range.Start.Line, d.Range.Start.Character)
		var t *fillTarget
		for _, have := range targets {
			if have.kind == kind && have.line == line && have.col == col {
				t = have
			}
		}
		if t == nil {
			t = &fillTarget{kind: kind, line: line, col: col,
				endLine: int(d.Range.End.Line) + 1, endCol: lines.byteCol(d.Range.End.Line, d.Range.End.Character)}
			targets = append(targets, t)
		}
		t.diags = append(t.diags, d)
		if kind == fillArms {
			t.names = append(t.names, strings.Split(name, ", ")...)
		} else if name != "" {
			t.names = append(t.names, name)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	f := &fillRequest{s: s, content: content, nodes: nodes, fa: fa, offs: lines.starts, lines: lines, tokens: lexer.Lex(content)}
	var actions []protocol.CodeAction
	for _, t := range targets {
		var title, edited string
		var ok bool
		switch t.kind {
		case fillFunctions:
			title, edited, ok = f.fillFunctions(t)
		case fillFields:
			title, edited, ok = f.fillFields(t)
		case fillArms:
			title, edited, ok = f.fillArms(t)
		}
		if !ok {
			continue
		}
		edit, ok := f.finish(edited)
		if !ok {
			continue
		}
		kind := protocol.CodeActionKindQuickFix
		preferred := true
		actions = append(actions, protocol.CodeAction{
			Title:       title,
			Kind:        &kind,
			Diagnostics: t.diags,
			IsPreferred: &preferred,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{
					protocol.DocumentUri(uri): {edit},
				},
			},
		})
	}
	return actions
}

// classifyFill reads which fix a diagnostic message asks for, and the name
// it says is missing: a field, or the comma-separated variants.
func classifyFill(msg string) (fillKind, string, bool) {
	if missingFunctionMessage.MatchString(msg) {
		return fillFunctions, "", true
	}
	if m := missingFieldMessage.FindStringSubmatch(msg); m != nil {
		return fillFields, m[1], true
	}
	if m := missingArmsMessage.FindStringSubmatch(msg); m != nil {
		return fillArms, m[1], true
	}
	return 0, "", false
}

// find returns the node keep accepts whose position keep answers is the
// start of t's diagnostics, or else the first one whose position lies
// inside their range: a diagnostic about a node spans the node, and need
// not start where the node's position is.
func (f *fillRequest) find(t *fillTarget, keep func(ast.Node) (int, int, bool)) ast.Node {
	var exact, inside ast.Node
	for _, n := range f.nodes {
		if exact != nil {
			break
		}
		analysis.WalkNodes(n, func(c ast.Node) {
			if exact != nil {
				return
			}
			l, cl, ok := keep(c)
			switch {
			case !ok:
			case l == t.line && cl == t.col:
				exact = c
			case inside == nil && inRange(l, cl, t.line, t.col, t.endLine, t.endCol):
				inside = c
			}
		})
	}
	if exact != nil {
		return exact
	}
	return inside
}

// inRange reports whether line:col lies in the range from startLine:startCol
// up to, not including, endLine:endCol.
func inRange(line, col, startLine, startCol, endLine, endCol int) bool {
	if line < startLine || (line == startLine && col < startCol) {
		return false
	}
	return line < endLine || (line == endLine && col < endCol)
}

// --- implement missing functions --------------------------------------------

func (f *fillRequest) fillFunctions(t *fillTarget) (string, string, bool) {
	n := f.find(t, func(n ast.Node) (int, int, bool) {
		b, ok := n.(*ast.ImplBlock)
		if !ok || b.Interface == nil {
			return 0, 0, false
		}
		return b.Line, b.Col, true
	})
	impl, _ := n.(*ast.ImplBlock)
	if impl == nil {
		return "", "", false
	}
	r := &completionRequest{s: f.s, fa: f.fa, scope: f.fa.ModuleScope}
	idef := r.interfaceDecl(impl.Interface)
	if idef == nil {
		return "", "", false
	}
	site := &implStubSite{header: impl, items: impl.Items, written: map[string]bool{}}
	for _, item := range impl.Items {
		switch it := item.(type) {
		case *ast.FuncDef:
			site.written[it.Name] = true
		case *ast.ExternFunc:
			site.written[it.Name] = true
		}
	}
	var required []*ast.InterfaceMethod
	for i := range idef.Methods {
		m := &idef.Methods[i]
		if m.Body == nil && !m.Extern && !site.written[m.Name] {
			required = append(required, m)
		}
	}
	if len(required) == 0 {
		return "", "", false
	}
	g := newStubGen(idef, site)
	indent := strings.Repeat(" ", format.IndentWidth)
	stubs := make([]string, len(required))
	for i, m := range required {
		stubs[i] = indent + g.stub(m, indent, f.placeholderAt(impl.Line, impl.Col, g.signature(m, false)), false)
	}

	title := "Implement missing functions"
	if len(required) == 1 {
		title = fmt.Sprintf("Implement missing function '%s'", required[0].Name)
	}
	endOff := posToOffset(f.offs, impl.EndLine, impl.EndCol)
	if impl.EndLine == 0 || !f.endAt(endOff) {
		return "", "", false
	}
	// The functions go on the lines above the impl's `end`, a blank line
	// before each one that follows another item. `impl X for T end` opens
	// onto its own lines.
	var b strings.Builder
	for i, s := range stubs {
		if i > 0 || len(impl.Items) > 0 || len(impl.EndTrivia) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s + "\n")
	}
	lines := b.String()
	if f.endOnHeaderLine(endOff) {
		lines = strings.Join(stubs, "\n\n") + "\n"
	}
	return title, f.insertBeforeEnd(endOff, posToOffset(f.offs, impl.Line, impl.Col), lines), true
}

// endAt reports whether the `end` keyword starts at off.
func (f *fillRequest) endAt(off int) bool {
	if off < 0 || off+3 > len(f.content) || f.content[off:off+3] != "end" {
		return false
	}
	return off+3 == len(f.content) || !isIdentByte(f.content[off+3])
}

// endOnHeaderLine reports whether something precedes the `end` at endOff on
// its line: an empty body written on its header's line.
func (f *fillRequest) endOnHeaderLine(endOff int) bool {
	return strings.TrimSpace(f.content[f.lineStart(endOff):endOff]) != ""
}

// insertBeforeEnd inserts lines (each ending in a newline) before the `end`
// at endOff, which closes the body whose header starts at headerOff. An
// `end` on its header's line moves to a line of its own.
func (f *fillRequest) insertBeforeEnd(endOff, headerOff int, lines string) string {
	at := f.lineStart(endOff)
	if f.endOnHeaderLine(endOff) {
		lines = "\n" + lines + f.lineIndentAt(headerOff)
		at = endOff
		for at > 0 && (f.content[at-1] == ' ' || f.content[at-1] == '\t') {
			at--
		}
	}
	return f.content[:at] + lines + f.content[at:]
}

// --- add missing fields -----------------------------------------------------

func (f *fillRequest) fillFields(t *fillTarget) (string, string, bool) {
	n := f.find(t, func(n ast.Node) (int, int, bool) {
		lit, ok := n.(*ast.StructLit)
		if !ok {
			return 0, 0, false
		}
		return lit.Line, lit.Col, true
	})
	if n == nil {
		// A named literal's diagnostic may stand at its name, after the `#`
		// (`.Rect{`, `geo.Point{`).
		n = f.find(t, func(n ast.Node) (int, int, bool) {
			lit, ok := n.(*ast.StructLit)
			if !ok {
				return 0, 0, false
			}
			switch tn := lit.TypeName.(type) {
			case *ast.DotVariantType:
				return tn.Line, tn.Col, true
			case *ast.QualifiedType:
				return tn.ModuleLine, tn.ModuleCol, true
			}
			return 0, 0, false
		})
	}
	lit, _ := n.(*ast.StructLit)
	if lit == nil || lit.Spread != nil {

		return "", "", false
	}
	missing := f.missingFields(lit)
	if len(missing) == 0 {
		missing = t.names
	}
	if len(missing) == 0 {
		return "", "", false
	}
	open, close, ok := f.braceAfter(posToOffset(f.offs, lit.Line, lit.Col))
	if !ok {
		return "", "", false
	}
	openOff, closeOff := f.offOf(f.tokens[open]), f.offOf(f.tokens[close])
	entries := make([]string, len(missing))
	placeholder := f.placeholderAt(lit.Line, lit.Col, "")
	for i, name := range missing {
		entries[i] = name + ": " + placeholder
	}
	var edited string
	if f.tokens[open].Line == f.tokens[close].Line {
		inner := strings.TrimSpace(f.content[openOff+1 : closeOff])
		inner = strings.TrimSpace(strings.TrimSuffix(inner, ","))
		if inner != "" {
			inner += ", "
		}
		edited = f.content[:openOff+1] + inner + strings.Join(entries, ", ") + f.content[closeOff:]
	} else {
		indent := f.lineIndentAt(openOff) + strings.Repeat(" ", format.IndentWidth)
		if len(lit.Fields) > 0 && lit.Fields[0].Line > f.tokens[open].Line {
			indent = strings.Repeat(" ", lit.Fields[0].Col-1)
		}
		var b strings.Builder
		for _, e := range entries {
			b.WriteString(indent + e + ",\n")
		}
		edited = f.insertBeforeClose(open, close, b.String(), true)
	}
	title := "Add missing fields"
	if len(missing) == 1 {
		title = fmt.Sprintf("Add missing field '%s'", missing[0])
	}
	return title, edited, true
}

// missingFields lists, in declaration order, the fields without a default
// that lit does not write, read from the type the checker gave lit: a
// struct's, or a struct-shaped variant's.
func (f *fillRequest) missingFields(lit *ast.StructLit) []string {
	var fields []analysis.FieldDef
	switch t := analysis.ResolveTypeVar(f.fa.ExprTypes[lit]).(type) {
	case *analysis.StructType:
		fields = t.Fields
	case *analysis.EnumType:
		name := variantNameOf(lit.TypeName)
		for _, v := range t.Variants {
			if v.Name != name {
				continue
			}
			fields = v.Fields
			if st, ok := analysis.ResolveTypeVar(v.Embedded).(*analysis.StructType); ok && len(fields) == 0 {
				fields = st.Fields
			}
		}
	}
	written := map[string]bool{}
	for _, fv := range lit.Fields {
		written[fv.Name] = true
	}
	var out []string
	for _, fd := range fields {
		if !written[fd.Name] && !fd.HasDefault {
			out = append(out, fd.Name)
		}
	}
	return out
}

// --- add missing arms -------------------------------------------------------

func (f *fillRequest) fillArms(t *fillTarget) (string, string, bool) {
	n := f.find(t, func(n ast.Node) (int, int, bool) {
		c, ok := n.(*ast.Case)
		if !ok || c.Value == nil {
			return 0, 0, false
		}
		return c.Line, c.Col, true
	})
	c, _ := n.(*ast.Case)
	if c == nil {
		return "", "", false
	}
	et, _ := analysis.ResolveTypeVar(f.fa.ExprTypes[c.Value]).(*analysis.EnumType)
	if et == nil {
		return "", "", false
	}
	var arms []string
	for _, name := range t.names {
		found := false
		for _, v := range et.Variants {
			if v.Name == name {
				pat, _ := variantPattern(v, 0, false)
				arms = append(arms, pat+" -> "+f.placeholderAt(c.Line, c.Col, pat))
				found = true
			}
		}
		if !found {
			return "", "", false
		}
	}
	if len(arms) == 0 {
		return "", "", false
	}
	if c.Span.IsZero() {
		return "", "", false
	}
	endOff := posToOffset(f.offs, c.Span.EndLine, c.Span.EndCol) - len("end")
	if !f.endAt(endOff) {
		return "", "", false
	}
	headerOff := posToOffset(f.offs, c.Line, c.Col)
	indent := f.lineIndentAt(headerOff) + strings.Repeat(" ", format.IndentWidth)
	if len(c.Branches) > 0 {
		indent = strings.Repeat(" ", c.Branches[0].Col-1)
	}
	var b strings.Builder
	for _, a := range arms {
		b.WriteString(indent + a + "\n")
	}
	edited := f.insertBeforeEnd(endOff, headerOff, b.String())
	title := "Add missing arms"
	if len(arms) == 1 {
		title = "Add missing arm " + strings.Fields(arms[0])[0]
	}
	return title, edited, true
}

// --- text helpers -----------------------------------------------------------

// insertBeforeClose inserts lines (each ending in a newline) before the `}`
// at token close, which closes the multi-line block opened at token open.
// With comma, the last item before it gets the trailing comma a multi-line
// literal's items carry, if it lacks one.
func (f *fillRequest) insertBeforeClose(open, close int, lines string, comma bool) string {
	closeOff := f.offOf(f.tokens[close])
	at := f.lineStart(closeOff)
	if strings.TrimSpace(f.content[at:closeOff]) != "" {
		// Something precedes the `}` on its line: break before it.
		lines = "\n" + lines + f.lineIndentAt(f.offOf(f.tokens[open]))
		at = closeOff
		for at > 0 && (f.content[at-1] == ' ' || f.content[at-1] == '\t') {
			at--
		}
	}
	prefix := f.content[:at]
	if prev := f.prevSignificant(close); comma && prev > open && f.tokens[prev].Type != token.COMMA {
		end := f.offOf(f.tokens[prev+1])
		for end > 0 && (f.content[end-1] == ' ' || f.content[end-1] == '\t') {
			end--
		}
		if end <= at {
			prefix = f.content[:end] + "," + f.content[end:at]
		}
	}
	return prefix + lines + f.content[at:]
}

// braceAfter finds the first `{` at or after off and the `}` closing it.
func (f *fillRequest) braceAfter(off int) (int, int, bool) {
	for i := f.tokenAt(off); i < len(f.tokens); i++ {
		if f.tokens[i].Type == token.LBRACE {
			return f.closeOf(i)
		}
	}
	return 0, 0, false
}

// closeOf returns open and the index of the bracket closing it.
func (f *fillRequest) closeOf(open int) (int, int, bool) {
	depth := 0
	for i := open; i < len(f.tokens); i++ {
		switch f.tokens[i].Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			depth++
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			depth--
			if depth == 0 {
				return open, i, f.tokens[i].Type == token.RBRACE
			}
		case token.EOF:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

func isTrivia(t token.Token) bool {
	switch t.Type {
	case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT:
		return true
	}
	return false
}

func (f *fillRequest) nextSignificant(i int) int {
	for i++; i < len(f.tokens) && isTrivia(f.tokens[i]); i++ {
	}
	return i
}

func (f *fillRequest) prevSignificant(i int) int {
	for i--; i >= 0 && isTrivia(f.tokens[i]); i-- {
	}
	return i
}

// tokenAt is the index of the first token at or after off.
func (f *fillRequest) tokenAt(off int) int {
	return sort.Search(len(f.tokens), func(i int) bool {
		t := f.tokens[i]
		return t.Type == token.EOF || f.offOf(t) >= off
	})
}

func (f *fillRequest) offOf(t token.Token) int {
	return posToOffset(f.offs, t.Line, t.Col)
}

func (f *fillRequest) lineStart(off int) int {
	return f.offs[lineIndexOf(f.offs, off)]
}

// lineIndentAt is the leading whitespace of the line holding off.
func (f *fillRequest) lineIndentAt(off int) string {
	start := f.lineStart(off)
	end := start
	for end < len(f.content) && (f.content[end] == ' ' || f.content[end] == '\t') {
		end++
	}
	return f.content[start:end]
}

// finish turns the edited document into one edit over the lines that
// changed. A document that was `nomi fmt` output is formatted again first,
// so the fix leaves it formatted.
func (f *fillRequest) finish(edited string) (protocol.TextEdit, bool) {
	if f.formatted == 0 {
		f.formatted = -1
		if out, err := format.Format(f.content); err == nil && out == f.content {
			f.formatted = 1
		}
	}
	if f.formatted == 1 {
		if out, err := format.Format(edited); err == nil {
			edited = out
		}
	}
	if edited == f.content {
		return protocol.TextEdit{}, false
	}
	return lineDiffEdit(f.content, edited), true
}

// lineDiffEdit is one edit that turns before into after, replacing the
// whole lines between their common leading and trailing lines.
func lineDiffEdit(before, after string) protocol.TextEdit {
	a := strings.SplitAfter(before, "\n")
	b := strings.SplitAfter(after, "\n")
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	start := len(strings.Join(a[:p], ""))
	end := len(before) - len(strings.Join(a[len(a)-s:], ""))
	return rangeEdit(before, start, end, strings.Join(b[p:len(b)-s], ""))
}

// diagCovers reports whether the position line:col (1-based, byte column)
// lies in diagnostic d's range in the indexed text: at its start, or
// inside it.
func diagCovers(lines *lineIndex, d protocol.Diagnostic, line, col int) bool {
	startLine, endLine := int(d.Range.Start.Line)+1, int(d.Range.End.Line)+1
	startCol := lines.byteCol(d.Range.Start.Line, d.Range.Start.Character)
	endCol := lines.byteCol(d.Range.End.Line, d.Range.End.Character)
	return (line == startLine && col == startCol) || inRange(line, col, startLine, startCol, endLine, endCol)
}
