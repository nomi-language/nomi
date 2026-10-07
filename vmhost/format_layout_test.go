package vmhost

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// Layout variants for the rule that formatting keeps meaning
// (format_meaning_test.go). The seeds are almost all formatted already, and a
// formatter breaks on layouts it has not written itself: odd spacing, line
// breaks inside expressions, comments between tokens, parentheses it would
// not write or missing ones it would, lines too long to fit. layoutVariant
// rewrites a program's text into such a layout. It works on tokens, not on
// the syntax tree, so a variant may not parse, and may mean something other
// than its seed; it is checked as an input of its own either way.

// layoutVariant is src with one to three layout rewrites applied to each of
// its .nomi files, chosen by seed.
func layoutVariant(src string, seed int64) string {
	r := rand.New(rand.NewSource(seed))
	names, files, ok := splitInput(src)
	if !ok {
		return src
	}
	l := &layout{r: r}
	passes := []func(string, float64) string{l.respace, l.breakLines, l.comment, l.joinLines, l.wrap, l.unwrap, l.lengthen}
	for _, name := range names {
		if !strings.HasSuffix(name, ".nomi") {
			continue
		}
		text := files[name]
		for n := 1 + r.Intn(3); n > 0; n-- {
			text = passes[r.Intn(len(passes))](text, 0.05+0.45*r.Float64())
		}
		files[name] = text
	}
	if len(names) == 1 && names[0] == "main.nomi" && !strings.Contains(src, "// FILE:") {
		return files["main.nomi"]
	}
	var b strings.Builder
	for _, name := range names {
		b.WriteString("// FILE: " + name + "\n" + files[name])
		if !strings.HasSuffix(files[name], "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

type layout struct {
	r *rand.Rand
	n int // comments inserted so far, to number them
}

// vtok is a token of the source with its byte offset.
type vtok struct {
	typ   token.TokenType
	text  string
	start int
	line  int
	// interp is set inside a string's `${...}`.
	interp bool
	// prompt is set on a line holding a `//!` prompt.
	prompt bool
}

// sourceTokens are src's tokens in order, without the lexer's synthetic
// NEWLINE, BLANK_LINE and EOF and without the parts of an interpolated
// string after its first, whose positions are not their text's.
func sourceTokens(src string) []vtok {
	lineStart := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lineStart = append(lineStart, i+1)
		}
	}
	prompts := map[int]bool{}
	var out []vtok
	depth := 0
	for _, tk := range lexer.Lex(src) {
		switch tk.Type {
		case token.TEST_PROMPT:
			prompts[tk.Line] = true
		case token.STRING_START, token.TRIPLE_STRING_START:
			depth++
		case token.STRING_END, token.TRIPLE_STRING_END:
			depth--
			continue
		case token.STRING_PART, token.TRIPLE_STRING_PART, token.NEWLINE, token.BLANK_LINE, token.EOF:
			continue
		}
		if tk.Line < 1 || tk.Line > len(lineStart) {
			continue
		}
		start := lineStart[tk.Line-1] + tk.Col - 1
		if start < 0 || start > len(src) {
			continue
		}
		inside := depth > 0
		if tk.Type == token.STRING_START || tk.Type == token.TRIPLE_STRING_START {
			inside = depth > 1
		}
		out = append(out, vtok{typ: tk.Type, text: tk.Lexeme, start: start, line: tk.Line, interp: inside})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	for i := range out {
		out[i].prompt = prompts[out[i].line]
	}
	return out
}

// canEnd mirrors the lexer's rule for a token after which a line break
// ends a statement. It only steers where variants break lines; a wrong
// entry makes fewer variants parse.
func canEnd(t token.TokenType) bool {
	switch t {
	case token.IDENT, token.TYPE_IDENT, token.INT, token.FLOAT, token.DECIMAL, token.CODEPOINT_LITERAL,
		token.STRING_LITERAL, token.STRING_START, token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_START,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL,
		token.TAGGED_STRING_LITERAL, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL,
		token.RPAREN, token.RBRACKET, token.RBRACE, token.GT,
		token.RETURN, token.BREAK, token.CONTINUE, token.ASSERT, token.REFUTE, token.TRY, token.DBG,
		token.TODO, token.EXPORT, token.UNDERSCORE, token.COMMENT, token.DOC_COMMENT, token.TEST_PROMPT:
		return true
	}
	return false
}

// gap is the whitespace before toks[i], toks[i-1] being the token before it.
type gap struct {
	start, end int
	prev, next vtok
}

func (g gap) text(src string) string { return src[g.start:g.end] }

// gaps are the whitespace runs before each token but the first, outside
// strings.
func gaps(src string, toks []vtok) []gap {
	var out []gap
	for i := 1; i < len(toks); i++ {
		end := toks[i].start
		start := end
		for start > toks[i-1].start+1 && strings.IndexByte(" \t\r\n", src[start-1]) >= 0 {
			start--
		}
		if toks[i].prompt || toks[i-1].prompt {
			continue
		}
		out = append(out, gap{start: start, end: end, prev: toks[i-1], next: toks[i]})
	}
	return out
}

// breakable reports whether a line break in g continues the statement.
func (g gap) breakable() bool {
	if g.next.interp || g.prev.interp && g.prev.typ != token.STRING_START {
		return false
	}
	switch g.next.typ {
	case token.PIPE, token.RPAREN, token.RBRACKET:
		return g.prev.typ != token.COMMENT && g.prev.typ != token.DOC_COMMENT
	}
	return !canEnd(g.prev.typ)
}

type edit struct {
	start, end int
	text       string
}

// apply makes non-overlapping edits to src.
func apply(src string, edits []edit) string {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	last := len(src) + 1
	for _, e := range edits {
		if e.end > last {
			continue
		}
		src = src[:e.start] + e.text + src[e.end:]
		last = e.start
	}
	return src
}

func (l *layout) indent() string {
	if l.r.Intn(4) == 0 {
		return strings.Repeat("\t", l.r.Intn(3))
	}
	return strings.Repeat(" ", l.r.Intn(14))
}

func (l *layout) note(prefix string) string {
	l.n++
	return fmt.Sprintf("// %s%d", prefix, l.n)
}

// respace changes the spacing between tokens on a line, and puts spaces
// between some tokens written together.
func (l *layout) respace(src string, p float64) string {
	var edits []edit
	for _, g := range gaps(src, sourceTokens(src)) {
		if strings.Contains(g.text(src), "\n") || l.r.Float64() >= p {
			continue
		}
		if g.start == g.end && l.r.Intn(3) != 0 {
			continue
		}
		space := []string{" ", "  ", "   ", "\t", " \t "}[l.r.Intn(5)]
		edits = append(edits, edit{g.start, g.end, space})
	}
	return apply(src, edits)
}

// breakLines breaks lines inside expressions, where the lexer reads a line
// break as no break.
func (l *layout) breakLines(src string, p float64) string {
	var edits []edit
	for _, g := range gaps(src, sourceTokens(src)) {
		if g.breakable() && !strings.Contains(g.text(src), "\n") && l.r.Float64() < p {
			edits = append(edits, edit{g.start, g.end, "\n" + l.indent()})
		}
	}
	return apply(src, edits)
}

// joinLines joins lines that continue an expression, making long lines.
func (l *layout) joinLines(src string, p float64) string {
	var edits []edit
	for _, g := range gaps(src, sourceTokens(src)) {
		if g.breakable() && strings.Contains(g.text(src), "\n") && l.r.Float64() < p {
			edits = append(edits, edit{g.start, g.end, " "})
		}
	}
	return apply(src, edits)
}

// comment puts comments between tokens: at the end of a line and on a line
// of their own between statements, and, once per pass at most, on a line
// break inside an expression, where few constructs take one.
func (l *layout) comment(src string, p float64) string {
	var edits []edit
	// inside is set once an in-expression comment is placed, or from the
	// start in three passes of four.
	inside := l.r.Intn(4) != 0
	for _, g := range gaps(src, sourceTokens(src)) {
		if l.r.Float64() >= p || g.next.interp || g.prev.interp {
			continue
		}
		text := g.text(src)
		nl := strings.IndexByte(text, '\n')
		switch {
		case nl < 0 && g.breakable() && !inside:
			inside = true
			edits = append(edits, edit{g.start, g.end, " " + l.note("c") + "\n" + l.indent()})
		case nl < 0 || g.breakable() && g.prev.typ != token.LBRACE:
		case l.r.Intn(2) == 0 && g.prev.typ != token.COMMENT && g.prev.typ != token.DOC_COMMENT:
			edits = append(edits, edit{g.start, g.start, " " + l.note("t")})
		default:
			edits = append(edits, edit{g.start + nl, g.start + nl, "\n" + l.indent() + l.note("s")})
		}
	}
	return apply(src, edits)
}

// operand reports whether a token may come before an expression. `/`, `<`,
// `>` and `:` are left out: in an import path, a type argument list or an
// annotation the next token is no expression.
func operand(t token.TokenType) bool {
	switch t {
	case token.EQ, token.COMMA, token.LPAREN, token.LBRACKET, token.PLUS, token.MINUS, token.STAR,
		token.PERCENT, token.EQEQ, token.BANGEQ, token.LTEQ, token.GTEQ,
		token.AND, token.OR, token.RETURN, token.FAT_ARROW, token.PIPE, token.BANG,
		token.IF, token.CASE, token.ASSERT, token.REFUTE, token.TRY, token.BAR, token.DOTDOT, token.DOTDOTEQ:
		return true
	}
	return false
}

// matching is the index of the delimiter closing toks[i], or -1.
func matching(toks []vtok, i int) int {
	open, close := toks[i].typ, token.RPAREN
	switch open {
	case token.LBRACKET:
		close = token.RBRACKET
	case token.LBRACE:
		close = token.RBRACE
	}
	depth := 0
	for j := i; j < len(toks); j++ {
		switch toks[j].typ {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// wrap puts parentheses around operands: a name or number, with the field
// accesses, calls and struct literal that follow it.
func (l *layout) wrap(src string, p float64) string {
	toks := sourceTokens(src)
	var edits []edit
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		switch t.typ {
		case token.IDENT, token.TYPE_IDENT, token.INT, token.FLOAT:
		default:
			continue
		}
		if !operand(toks[i-1].typ) || t.interp || t.prompt || l.r.Float64() >= p {
			continue
		}
		j := i
		end := t.start + len(t.text)
		for j+1 < len(toks) {
			next := toks[j+1]
			if next.typ == token.DOT && j+2 < len(toks) && (toks[j+2].typ == token.IDENT || toks[j+2].typ == token.TYPE_IDENT) {
				j += 2
				end = toks[j].start + len(toks[j].text)
				continue
			}
			if next.typ == token.LPAREN || next.typ == token.LBRACE && toks[j].typ == token.TYPE_IDENT {
				k := matching(toks, j+1)
				if k < 0 {
					break
				}
				j = k
				end = toks[k].start + 1
				continue
			}
			break
		}
		edits = append(edits, edit{t.start, t.start, "("}, edit{end, end, ")"})
		i = j
	}
	return apply(src, edits)
}

// unwrap removes parentheses that group, rather than call or declare.
func (l *layout) unwrap(src string, p float64) string {
	toks := sourceTokens(src)
	var edits []edit
	for i := 1; i < len(toks); i++ {
		if toks[i].typ != token.LPAREN || !operand(toks[i-1].typ) || toks[i].interp || toks[i].prompt || l.r.Float64() >= p {
			continue
		}
		k := matching(toks, i)
		if k <= i+1 {
			continue
		}
		edits = append(edits, edit{toks[i].start, toks[i].start + 1, ""}, edit{toks[k].start, toks[k].start + 1, ""})
		i = k
	}
	return apply(src, edits)
}

// lengthen renames names the file binds (`name =`, `name:`) to long ones,
// everywhere but after a dot, so lines grow past the formatter's width.
func (l *layout) lengthen(src string, p float64) string {
	toks := sourceTokens(src)
	bound := map[string]string{}
	for i := 0; i+1 < len(toks); i++ {
		t := toks[i]
		if t.typ != token.IDENT || (toks[i+1].typ != token.EQ && toks[i+1].typ != token.COLON) || bound[t.text] != "" {
			continue
		}
		if l.r.Float64() < p {
			bound[t.text] = t.text + strings.Repeat("_rather_long", 1+l.r.Intn(4))
		}
	}
	var edits []edit
	for i, t := range toks {
		if long := bound[t.text]; long != "" && t.typ == token.IDENT && (i == 0 || toks[i-1].typ != token.DOT) {
			edits = append(edits, edit{t.start, t.start + len(t.text), long})
		}
	}
	return apply(src, edits)
}
