package lsp

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// Pipe-stage hints: at the end of a stage line of a multi-line pipeline,
// the type that stage produces, when it is new.
//
//	active = users
//	|> Iter.filter(.active?)    Iter<User>
//	|> Iter.map(.name)          Iter<String>
//	|> Iter.to_list()           List<String>
//
// The rules:
//
//   - Only a pipeline with a `|>` that starts a line gets hints. A pipe
//     written on one line, or whose only line break is inside a stage
//     (`xs |> Iter.map(|x| {` ... `})`), gets none.
//   - Each stage's hint goes at the end of the stage's last line, before a
//     trailing comment. When two stages end on one line, the later one's
//     type is shown.
//   - The head gets a hint when it ends on a line before the first `|>`
//     and is not a name, a field read or a literal, whose type hover or the
//     text already shows. A call head gets one.
//   - A stage line gets one only when its type differs from the line
//     above's: the previous stage's, or for the first stage the head's,
//     shown or not. A pipeline whose stages keep the head's type shows
//     nothing.
//   - The last stage gets none when its type is already on screen where
//     the pipeline's value goes: a binding's written annotation, the
//     binding-type hint (when that setting is on), a `once` binding's
//     annotation, or the declared return type of the function whose body
//     the pipeline ends.
//   - A keyword stage shows what it passes on: `|> try` the unwrapped value,
//     `|> dbg` the same type as the line above. A stage the checker gave no
//     type (an assertion stage, an error) or a type with an unsolved
//     variable gets none, and so does, outside a generic function, a type
//     naming a type parameter. The checker records a generic call's
//     instantiated type, so that guard should not fire in code that checks;
//     it stays so a stage the checker could not solve shows no hint rather
//     than a callee's own parameter.
//   - The label is the checker's type as a binding's hint and hover spell
//     it. One longer than maxPipeHintLen is cut, and the full type is the
//     hint's tooltip.
//   - Only pipelines meeting the requested lines are located; the tokens
//     are lexed once per analyzed text (tokenCache).

// maxPipeHintLen is the longest label, in characters, shown whole.
const maxPipeHintLen = 40

// flattenPipe splits a left-nested `a |> f |> g` into its head and its pipe
// nodes, innermost first.
func flattenPipe(n *ast.Binary) (ast.Node, []*ast.Binary) {
	var stages []*ast.Binary
	cur := n
	for {
		stages = append(stages, cur)
		left, ok := cur.Left.(*ast.Binary)
		if !ok || left.Op != "|>" {
			break
		}
		cur = left
	}
	for i, j := 0, len(stages)-1; i < j; i, j = i+1, j-1 {
		stages[i], stages[j] = stages[j], stages[i]
	}
	return cur.Left, stages
}

// pipeLine is one written stage: the `|>` token's index and the outermost
// pipe node it produced. `|> try f()` parses as two pipes, the call's and
// the `try`'s, and `|> if c { … }` as the condition's and the `if`'s; the
// second of each has no `|>` token of its own and its type is the line's.
type pipeLine struct {
	pipe int
	node *ast.Binary
}

// lexedText is a text's tokens and the byte offset of each of its lines.
type lexedText struct {
	toks  []token.Token
	lines []int
}

func lex(content string) *lexedText {
	return &lexedText{toks: lexer.Lex(content), lines: lineOffsets(content)}
}

func (c *hintCollector) lexed() *lexedText {
	if c.text == nil {
		if c.req.tokens != nil {
			c.text = c.req.tokens()
		}
		if c.text == nil {
			c.text = lex(c.req.content)
		}
	}
	return c.text
}

func (c *hintCollector) hintPipeline(head ast.Node, stages []*ast.Binary) {
	if !c.req.settings.PipeTypes || c.req.content == "" || head == nil {
		return
	}
	headLine, _ := nodePosition(head)
	if c.req.endLine != 0 && headLine > c.req.endLine {
		return
	}
	toks := c.lexed().toks
	var lines []pipeLine
	for _, st := range stages {
		if i, ok := tokenAt(toks, st.Line, st.Col); ok && toks[i].Type == token.PIPE {
			lines = append(lines, pipeLine{pipe: i, node: st})
		} else if len(lines) > 0 {
			lines[len(lines)-1].node = st
		} else {
			return
		}
	}
	leading := false
	for _, l := range lines {
		if startsLine(toks, l.pipe) {
			leading = true
			break
		}
	}
	if !leading {
		return
	}
	last := lines[len(lines)-1]
	lastEnd, ok := stageEnd(toks, last.pipe)
	if !ok || !c.inRange(headLine, tokenEndLine(toks[lastEnd])) {
		return
	}
	c.pipelinesMeasured++

	// One entry per line a stage (or the head) ends on, in order; when
	// two end on one line the later one's type is the line's.
	type hintAt struct {
		line int
		tok  int
		ty   analysis.Type
		head bool
		show bool // false for a head whose type the text shows
		last bool // the pipeline's last stage
	}
	var at []hintAt
	put := func(h hintAt) {
		if n := len(at); n > 0 && at[n-1].line == h.line {
			at[n-1] = h
			return
		}
		at = append(at, h)
	}
	headType := c.fa.ExprTypes[head]
	if end, ok := lastCodeBefore(toks, lines[0].pipe); ok && tokenEndLine(toks[end]) < toks[lines[0].pipe].Line {
		put(hintAt{line: tokenEndLine(toks[end]), tok: end, ty: headType, head: true, show: !obviousHead(head)})
	}
	for i, l := range lines {
		end := lastEnd
		if i+1 < len(lines) {
			var ok bool
			if end, ok = lastCodeBefore(toks, lines[i+1].pipe); !ok {
				continue
			}
		}
		put(hintAt{line: tokenEndLine(toks[end]), tok: end, ty: c.fa.ExprTypes[l.node], show: true, last: i == len(lines)-1})
	}

	prev := pipeTypeText(headType)
	shown, hasShown := c.shownTypes[stages[len(stages)-1]]
	for _, h := range at {
		cur := pipeTypeText(h.ty)
		show := h.show && (h.head || cur != prev)
		prev = cur
		if !show || (h.last && hasShown && cur == pipeTypeText(shown)) {
			continue
		}
		label, ok := pipeTypeLabel(h.ty)
		if !ok || (!c.generic && analysis.ContainsTypeParam(analysis.ResolveTypeVar(h.ty))) {
			continue
		}
		col := c.lineCodeEnd(toks, h.tok)
		kind := InlayHintKindType
		hint := InlayHint{
			Position:    inlayHintPos(h.line, col),
			Label:       label,
			Kind:        &kind,
			PaddingLeft: true,
		}
		if full := analysis.ResolveTypeVar(h.ty).String(); full != label {
			hint.Tooltip = full
		}
		c.hints = append(c.hints, hint)
	}
}

// pipeTypeText is t as hints spell it, and "" when t is unknown.
func pipeTypeText(t analysis.Type) string {
	if t == nil {
		return ""
	}
	return analysis.ResolveTypeVar(t).String()
}

// noteShownType records, for the value a declaration gives a type that is
// already on screen, that type: a binding's annotation or binding-type
// hint, a `once` binding's annotation, and a function's declared return
// type for the expression its body ends with. A pipeline producing that
// value does not repeat it on its last stage.
func (c *hintCollector) noteShownType(n ast.Node) {
	var value ast.Node
	var ty analysis.Type
	switch n := n.(type) {
	case *ast.Binding:
		if n.TypeAnnotation == nil && !c.req.settings.BindingTypes {
			return
		}
		if sym, ok := c.fa.Definitions[analysis.Pos{Line: n.Line, Col: n.Col}]; ok {
			value, ty = n.Value, sym.Type
		}
	case *ast.OnceBinding:
		if n.TypeAnnotation == nil {
			return
		}
		if sym, ok := c.fa.Definitions[analysis.Pos{Line: n.Line, Col: n.Col}]; ok {
			value, ty = n.Value, sym.Type
		}
	case *ast.FuncDef:
		if n.ReturnTypeExpr == nil || n.Body == nil || len(n.Body.Stmts) == 0 {
			return
		}
		sym, ok := c.fa.Definitions[analysis.Pos{Line: n.Line, Col: n.Col}]
		if !ok {
			return
		}
		ft, ok := analysis.ResolveTypeVar(sym.Type).(*analysis.FuncType)
		if !ok {
			return
		}
		value, ty = n.Body.Stmts[len(n.Body.Stmts)-1], ft.Return
		if es, ok := value.(*ast.ExprStmt); ok {
			value = es.Expr
		}
	}
	if value == nil || ty == nil {
		return
	}
	if c.shownTypes == nil {
		c.shownTypes = map[ast.Node]analysis.Type{}
	}
	c.shownTypes[value] = ty
}

// unsolvedVar matches a type variable the checker left unsolved, as
// TypeVar.String renders it.
var unsolvedVar = regexp.MustCompile(`\?\d`)

// pipeTypeLabel is the label for a stage of type t, and false when t is
// unknown.
func pipeTypeLabel(t analysis.Type) (string, bool) {
	if t == nil {
		return "", false
	}
	s := analysis.ResolveTypeVar(t).String()
	if s == "" || unsolvedVar.MatchString(s) {
		return "", false
	}
	if utf8.RuneCountInString(s) > maxPipeHintLen {
		r := []rune(s)
		s = string(r[:maxPipeHintLen-1]) + "…"
	}
	return s, true
}

// obviousHead reports whether a pipeline's head shows its own type: a name
// or a field read from one (`b.items`), whose type hover gives, or a
// literal.
func obviousHead(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.FieldAccess:
		return obviousHead(v.Object)
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit,
		*ast.StringLit, *ast.StringInterp, *ast.CodepointLit, *ast.ListLit,
		*ast.VectorLit, *ast.SetLit, *ast.MapLit, *ast.TupleLit, *ast.StructLit:
		return true
	}
	return false
}

// lineCodeEnd is the 1-based byte column just past the code on the line
// where token end ends: the line without a trailing comment or blanks.
func (c *hintCollector) lineCodeEnd(toks []token.Token, end int) int {
	line := tokenEndLine(toks[end])
	text := lineText(c.req.content, c.lexed().lines, line)
	for i := end + 1; i < len(toks) && toks[i].Line == line; i++ {
		if isCommentToken(toks[i].Type) && toks[i].Col-1 <= len(text) {
			text = text[:toks[i].Col-1]
			break
		}
	}
	return len(strings.TrimRight(text, " \t\r")) + 1
}

// lineText is the text of 1-based line, without its line break.
func lineText(content string, offs []int, line int) string {
	if line < 1 || line > len(offs) {
		return ""
	}
	text := content[offs[line-1]:]
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		text = text[:nl]
	}
	return text
}

// tokenAt is the index of the token at line:col.
func tokenAt(toks []token.Token, line, col int) (int, bool) {
	i := sort.Search(len(toks), func(i int) bool {
		t := toks[i]
		return t.Line > line || (t.Line == line && t.Col >= col)
	})
	if i < len(toks) && toks[i].Line == line && toks[i].Col == col {
		return i, true
	}
	return 0, false
}

func tokenEndLine(t token.Token) int {
	if t.EndLine > t.Line {
		return t.EndLine
	}
	return t.Line
}

func isCommentToken(t token.TokenType) bool {
	return t == token.COMMENT || t == token.DOC_COMMENT || t == token.TEST_PROMPT
}

// isPipeTrivia reports whether a token is no part of an expression.
func isPipeTrivia(t token.TokenType) bool {
	switch t {
	case token.NEWLINE, token.BLANK_LINE, token.SEMICOLON, token.EOF:
		return true
	}
	return isCommentToken(t)
}

// startsLine reports whether toks[i] is the first code token on its line.
func startsLine(toks []token.Token, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if isPipeTrivia(toks[j].Type) {
			continue
		}
		return tokenEndLine(toks[j]) < toks[i].Line
	}
	return true
}

// lastCodeBefore is the index of the last code token before toks[i].
func lastCodeBefore(toks []token.Token, i int) (int, bool) {
	for j := i - 1; j >= 0; j-- {
		if !isPipeTrivia(toks[j].Type) {
			return j, true
		}
	}
	return 0, false
}

// stageEnd is the index of the last token of the stage the `|>` at
// toks[pipe] starts, when it is the pipeline's last stage: the stage runs
// until, outside any bracket, the next token is a closing bracket, a comma,
// another `|>`, or on a later line (an `else` on a later line continues an
// `if` stage).
func stageEnd(toks []token.Token, pipe int) (int, bool) {
	depth := 0
	end := -1
	for i := pipe + 1; i < len(toks); i++ {
		t := toks[i]
		if isPipeTrivia(t.Type) {
			if t.Type == token.EOF {
				break
			}
			continue
		}
		if depth == 0 && end >= 0 {
			if t.Type == token.PIPE || t.Type == token.COMMA {
				break
			}
			if t.Line > tokenEndLine(toks[end]) && t.Type != token.ELSE {
				break
			}
		}
		switch t.Type {
		case token.LPAREN, token.LBRACE, token.LBRACKET:
			depth++
		case token.RPAREN, token.RBRACE, token.RBRACKET:
			if depth == 0 {
				return end, end >= 0
			}
			depth--
		}
		end = i
	}
	return end, end >= 0
}

// tokenCache holds, per open document, the tokens of the analyzed text pipe
// hints last lexed. That text changes once per analysis, and an editor asks
// for hints after every scroll.
type tokenCache struct {
	mu   sync.Mutex
	docs map[string]tokenCacheEntry
	// lexes counts the texts lexed, for tests.
	lexes int
}

type tokenCacheEntry struct {
	content string
	text    *lexedText
}

func (tc *tokenCache) get(uri, content string) *lexedText {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if e, ok := tc.docs[uri]; ok && e.content == content {
		return e.text
	}
	if tc.docs == nil {
		tc.docs = map[string]tokenCacheEntry{}
	}
	text := lex(content)
	tc.docs[uri] = tokenCacheEntry{content: content, text: text}
	tc.lexes++
	return text
}

// forget drops a closed document's tokens.
func (tc *tokenCache) forget(uri string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	delete(tc.docs, uri)
}
