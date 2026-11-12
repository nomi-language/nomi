package lsp

import "github.com/nomi-language/nomi/internal/token"

// bodySpans pairs each token that opens a body closed by `end` with the
// index of that `end`, or -1 while the body is unclosed, reading the tokens
// alone as a document mid-edit may not parse: a declaration keyword
// starting a line (`fn name(`, `struct`, `enum`, `interface`, `impl`,
// `test`), `case`, `begin`, and a `then`, a lambda's `->`, a binding's
// `else`, `import` or `concurrent` that ends its line. An `else` of an
// `if`'s block form opens nothing: it shares the `if`'s `end`. A function
// signature inside an interface opens nothing either, since a required one
// has no body.
func bodySpans(tokens []token.Token) map[int]int {
	spans := map[int]int{}
	type opener struct {
		at          int
		then, iface bool
	}
	var open []opener
	endsLine := func(i int) bool {
		if i+1 >= len(tokens) {
			return true
		}
		n := tokens[i+1]
		return n.Type == token.NEWLINE || n.Type == token.COMMENT || n.Type == token.EOF || n.Line > tokens[i].Line
	}
	startsLine := func(i int) bool {
		return i == 0 || tokens[i-1].Line < tokens[i].Line || tokens[i-1].Type == token.PUB || tokens[i-1].Type == token.OPAQUE
	}
	push := func(i int, then, iface bool) {
		open = append(open, opener{at: i, then: then, iface: iface})
		spans[i] = -1
	}
	for i, t := range tokens {
		switch t.Type {
		case token.END:
			if len(open) > 0 {
				spans[open[len(open)-1].at] = i
				open = open[:len(open)-1]
			}
		case token.CASE, token.BEGIN:
			push(i, false, false)
		case token.THEN:
			if endsLine(i) && (i == 0 || tokens[i-1].Type != token.PIPE) {
				push(i, true, false)
			}
		case token.ELSE:
			if endsLine(i) && (len(open) == 0 || !open[len(open)-1].then) {
				push(i, false, false)
			}
		case token.ARROW:
			if !endsLine(i) {
				continue
			}
			for j := i - 1; j >= 0 && tokens[j].Line == t.Line; j-- {
				if tokens[j].Type == token.FN {
					push(i, false, false)
					break
				}
			}
		case token.STRUCT, token.ENUM, token.INTERFACE, token.IMPL, token.TEST:
			if startsLine(i) {
				push(i, false, t.Type == token.INTERFACE)
			}
		case token.FN:
			inIface := len(open) > 0 && open[len(open)-1].iface
			if !inIface && startsLine(i) && i+2 < len(tokens) && tokens[i+1].Type == token.IDENT &&
				(tokens[i+2].Type == token.LPAREN || tokens[i+2].Type == token.LT) {
				push(i, false, false)
			}
		case token.IMPORT:
			if endsLine(i) {
				push(i, false, false)
			}
		case token.IDENT:
			switch {
			case (t.Lexeme == "concurrent" || t.Lexeme == "group") && endsLine(i) && startsLine(i):
				push(i, false, false)
			case t.Lexeme == "group" && startsLine(i) && i+1 < len(tokens) && tokens[i+1].Line == t.Line &&
				(tokens[i+1].Type == token.STRING_LITERAL || tokens[i+1].Type == token.STRING_START):
				push(i, false, false)
			}
		}
	}
	return spans
}

// innermostBody is the index of the token opening the innermost body that
// holds the token at index at, by spans (bodySpans), or -1.
func innermostBody(spans map[int]int, at int) int {
	best := -1
	for open, close := range spans {
		if open < at && (close < 0 || close >= at) && open > best {
			best = open
		}
	}
	return best
}
