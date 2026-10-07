package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// mapEntryPossible reports whether an expression starting at token i could
// be followed by `=>`, which is what detectMapPatternEntry's speculative
// parse looks for. It is a necessary condition, answered without parsing.
//
// Every `{` is probed for a map entry, and the probe parses a whole
// expression and throws it away. The probe at a `{` parses everything nested
// in it, and so does the parse that follows, so each level of nested braces
// doubled the work: `x = {{{…{` with 20 levels took over a minute to report
// its missing `}`. Most braces are blocks and struct literals, whose
// contents hold no `=>` at their own level, and those now skip the probe.
//
// The condition: an expression the parser accepts opens and closes its
// brackets in pairs, so if it starts at i and ends right before a `=>`, the
// `=>` is at i's bracket depth, and no bracket closes the depth i is at in
// between. Brackets are counted as one kind, so the answer holds however
// broken the source is.
func (p *Parser) mapEntryPossible(i int) bool {
	if p.arrowAhead == nil {
		p.arrowAhead = arrowAheadTable(p.tokens)
	}
	return i < len(p.arrowAhead) && p.arrowAhead[i]
}

// arrowAheadTable answers, for each token, whether scanning forward from it
// at its own bracket depth meets a `=>` before a bracket that closes that
// depth, stepping over each nested bracket pair whole.
func arrowAheadTable(toks []token.Token) []bool {
	n := len(toks)
	match := make([]int, n)
	var open []int
	for i, t := range toks {
		match[i] = -1
		switch t.Type {
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			open = append(open, i)
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			if len(open) > 0 {
				match[open[len(open)-1]] = i
				open = open[:len(open)-1]
			}
		}
	}
	ahead := make([]bool, n+1)
	for i := n - 1; i >= 0; i-- {
		switch toks[i].Type {
		case token.FAT_ARROW:
			ahead[i] = true
		case token.RBRACE, token.RPAREN, token.RBRACKET:
			ahead[i] = false
		case token.LBRACE, token.LPAREN, token.LBRACKET:
			ahead[i] = match[i] >= 0 && ahead[match[i]+1]
		default:
			ahead[i] = ahead[i+1]
		}
	}
	return ahead[:n]
}

// probeAt is where detectMapPatternEntry probed: the position, and the
// parser state a parse there depends on besides the tokens.
type probeAt struct {
	pos         int
	nesting     int
	noStructLit bool
}

// probeResult is a probe's answer. key is the map key it parsed, nil when the
// probe failed; end is the position after the key and recoveries the
// recoveries its parse made.
type probeResult struct {
	key        ast.Node
	end        int
	recoveries int
}

func (p *Parser) probeHere() probeAt {
	return probeAt{pos: p.pos, nesting: p.nesting, noStructLit: p.noStructLit}
}

// rememberProbe keeps a probe's answer. The parse after a probe reaches the
// same position again, the probes under it with it, and each probe parses
// everything nested in its key: answering them again made nested map keys
// (`{{{1 => 1} => 1} => 1}`) and nested braces past the nesting limit cost
// twice as much per level.
//
// A successful probe also keeps the key it parsed. Every caller that sees
// the probe succeed parses that key next, with parseExpr(1) from the same
// position, and takeKey hands it the probe's key instead of a second parse.
func (p *Parser) rememberProbe(at probeAt, r probeResult) {
	if p.probes == nil {
		p.probes = map[probeAt]probeResult{}
	}
	p.probes[at] = r
}

// takeKey answers the key a successful probe parsed at the current position
// and state, once, and moves past it.
func (p *Parser) takeKey() (ast.Node, bool) {
	at := p.probeHere()
	r, ok := p.probes[at]
	if !ok || r.key == nil {
		return nil, false
	}
	delete(p.probes, at)
	p.pos = r.end
	p.recoveries += r.recoveries
	return r.key, true
}
