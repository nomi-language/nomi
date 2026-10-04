package lsp

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/token"
)

// A parameter's type offered from its name: in a `fn` signature, typing a
// parameter's name offers `game: Game` for each type in scope, ranking the
// one the whole typed name spells first, and `game: ‸` ranks `Game` first
// among the types.
//
// The slot is read from the tokens of the spliced text, not from the
// reparsed tree: a signature being typed (`fn slay(game, mon`) usually has
// no closing `)` or body yet, and the reparse cannot keep it.

// paramSlot is where in a signature's parameter the cursor is.
type paramSlot int

const (
	paramSlotNone paramSlot = iota
	paramSlotName           // on the parameter's name
	paramSlotType           // after the parameter's `:`
)

// classifyParamSlot sets ctx.paramSlot and ctx.paramName when the sentinel
// in tokens sits in a parameter of a named function's signature: a module
// function, an impl function, an interface's function or a host function.
// A lambda's parameters, a pattern and a struct field are none of these.
// On the name, the context becomes ctxParamName when the cursor ends a
// typed name and ctxNone otherwise; after the `:` it becomes ctxType.
func classifyParamSlot(ctx *completionContext, tokens []token.Token) {
	idx := -1
	for i, t := range tokens {
		if (t.Type == token.IDENT || t.Type == token.TYPE_IDENT) && strings.Contains(t.Lexeme, completionSentinel) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	prev := prevSignificant(tokens, idx)
	if prev >= 0 && tokens[prev].Type == token.COLON {
		name := prevSignificant(tokens, prev)
		if name < 0 || tokens[name].Type != token.IDENT || !inSignatureParamList(tokens, prevSignificant(tokens, name)) {
			return
		}
		ctx.paramSlot = paramSlotType
		ctx.paramName = tokens[name].Lexeme
		ctx.kind = ctxType
		ctx.typeQualifier = ""
		return
	}
	if tokens[idx].Type != token.IDENT || !inSignatureParamList(tokens, prev) {
		return
	}
	if next := idx + 1; next < len(tokens) && tokens[next].Type == token.COLON {
		// `game‸: Int`: the parameter has its type.
		ctx.kind = ctxNone
		return
	}
	ctx.kind = ctxNone
	if ctx.prefix == "" || ctx.start+len(ctx.prefix) != ctx.end {
		return
	}
	ctx.kind = ctxParamName
	ctx.paramSlot = paramSlotName
	ctx.paramName = ctx.prefix
}

// prevSignificant returns the index of the token before i, skipping line
// breaks and comments, or -1.
func prevSignificant(tokens []token.Token, i int) int {
	for i--; i >= 0; i-- {
		switch tokens[i].Type {
		case token.NEWLINE, token.BLANK_LINE, token.COMMENT, token.DOC_COMMENT:
			continue
		}
		return i
	}
	return -1
}

// maxSignatureScan bounds the backward walk from a parameter to its
// function's `(`, so a position deep in a long file costs a fixed amount.
const maxSignatureScan = 400

// inSignatureParamList reports whether the token at i, the `(` or `,` just
// before a parameter, opens or separates the parameters of a named
// function's signature: `fn name(` or `fn name<T>(`.
func inSignatureParamList(tokens []token.Token, i int) bool {
	if i < 0 {
		return false
	}
	switch tokens[i].Type {
	case token.LPAREN:
	case token.COMMA:
		depth := 0
		for j := i - 1; ; j-- {
			if j < 0 || i-j > maxSignatureScan {
				return false
			}
			switch tokens[j].Type {
			case token.RPAREN, token.RBRACKET, token.RBRACE, token.GT:
				depth++
				continue
			case token.LBRACKET, token.LBRACE, token.LT:
				if depth == 0 {
					return false
				}
				depth--
				continue
			case token.LPAREN:
				if depth > 0 {
					depth--
					continue
				}
				i = j
			case token.BAR:
				if depth == 0 {
					// A lambda's parameter list.
					return false
				}
				continue
			default:
				continue
			}
			break
		}
	default:
		return false
	}
	// tokens[i] is the list's `(`: before it the function's name, and
	// before that `fn`, with an optional `<...>` after the name.
	j := prevSignificant(tokens, i)
	if j >= 0 && tokens[j].Type == token.GT {
		depth := 0
		for ; j >= 0; j-- {
			if tokens[j].Type == token.GT {
				depth++
			} else if tokens[j].Type == token.LT {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		j = prevSignificant(tokens, j)
	}
	if j < 0 || tokens[j].Type != token.IDENT {
		return false
	}
	f := prevSignificant(tokens, j)
	return f >= 0 && tokens[f].Type == token.FN
}

// pascalCase spells a snake_case name as a type name: `user_id` is
// `UserId`.
func pascalCase(name string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(name, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// paramTypeCandidate is the type the parameter's name names, when one is in
// scope. After the `:` it is offered ahead of the other types.
func (r *completionRequest) paramTypeCandidate() (candidate, bool) {
	typeName := pascalCase(r.ctx.paramName)
	if typeName == "" {
		return candidate{}, false
	}
	sym := r.scope.Lookup(typeName)
	if sym == nil || !typeSymbol(sym) {
		return candidate{}, false
	}
	return r.paramTypeFor(r.ctx.paramName, typeName, sym, 1), true
}

// paramNameCandidates offers, on a parameter's name, `name: Type` for every
// type in scope, under the name its snake_case spelling gives: `game: Game`,
// `user_id: UserId`. Every type, not only the one the typed text spells: a
// client asks once, at the name's first letter, and filters that list
// itself as the name grows, so `p` must already hold `place: Place`. The
// type the whole typed name spells ranks first.
func (r *completionRequest) paramNameCandidates() []candidate {
	var out []candidate
	for _, c := range r.scopeCandidates(typeSymbol) {
		name := snakeCase(c.label)
		if name == "" {
			continue
		}
		out = append(out, r.paramTypeFor(name, c.label, c.sym, c.locality))
	}
	return out
}

// paramTypeFor is the candidate giving parameter paramName the type
// typeName: on the name it inserts `name: Type`, after the `:` the type
// alone. A generic type's arguments are snippet tab stops.
func (r *completionRequest) paramTypeFor(paramName, typeName string, sym *analysis.Symbol, locality int) candidate {
	text := typeName
	snippet := ""
	if n := typeArity(realSymbol(sym)); n > 0 {
		stops := make([]string, n)
		for i := range stops {
			stops[i] = fmt.Sprintf("${%d}", i+1)
		}
		snippet = typeName + "<" + strings.Join(stops, ", ") + ">"
	}
	c := candidate{
		label:    typeName,
		kind:     symbolKindToCompletionKind(realSymbol(sym).Kind),
		sym:      sym,
		locality: locality,
		first:    paramName == r.ctx.paramName,
		noCall:   true,
	}
	switch r.ctx.paramSlot {
	case paramSlotName:
		c.label = paramName + ": " + typeName
		c.filter = paramName
		text = paramName + ": " + text
		if snippet != "" {
			snippet = paramName + ": " + snippet
		}
	case paramSlotType:
		// `game:‸` gets the space the formatter would put there.
		if r.ctx.start > 0 && r.doc.Content[r.ctx.start-1] == ':' {
			text = " " + text
			if snippet != "" {
				snippet = " " + snippet
			}
			c.filter = typeName
		}
	}
	c.insert = text
	c.snippet = snippet
	return c
}

// snakeCase spells a type name as a parameter name: `UserId` is `user_id`,
// and a run of capitals is one word, so `HTTPClient` is `http_client`.
func snakeCase(typeName string) string {
	var b strings.Builder
	for i := 0; i < len(typeName); i++ {
		c := typeName[i]
		upper := c >= 'A' && c <= 'Z'
		if upper && i > 0 {
			prev := typeName[i-1]
			nextLower := i+1 < len(typeName) && typeName[i+1] >= 'a' && typeName[i+1] <= 'z'
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') || (prev >= 'A' && prev <= 'Z' && nextLower) {
				b.WriteByte('_')
			}
		}
		if upper {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// typeArity is the number of type parameters a type's declaration takes.
func typeArity(sym *analysis.Symbol) int {
	switch t := sym.Type.(type) {
	case *analysis.StructType:
		return len(t.TypeParams)
	case *analysis.EnumType:
		return len(t.TypeParams)
	case *analysis.InterfaceType:
		return len(t.TypeParams)
	case *analysis.DistinctType:
		return len(t.TypeParams)
	}
	return 0
}
