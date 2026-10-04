package irbuild

// The source extent of a multi-line construct.
//
// A node's line may differ from its statement's in two ways:
//
//  1. A triple-quoted literal with a hole. The `${…}` node sits below the
//     statement's line (`escape_program` in
//     `15-app-and-defer/deadline_floor/deadline_floor_test.nomi`).
//  2. A pipe chain written one stage per line. The parser continues the
//     statement across each newline because the next line opens with `|>`,
//     so every stage below the first carries a line the statement is not on.
//
// The per-node check is `emittableLine`: a node on a synthesized line is a
// position no consumer can report. No corpus or std node reaches it, because
// derive synthesis produces whole synthesized bodies and `irScalarBuild`
// already checks every statement's line, so it is a fence.
//
// What the end gives:
//
//   - The start and the extent are different facts: "the construct starts on
//     25" and "the construct's text reaches 43".
//   - A breakpoint on an interior line resolves. `ir.Pos.Covers(30)` is true
//     of the instruction that computes `escape_program`'s literal and of
//     nothing else in the graph.
//   - `ir.Lint`'s `RulePositionSpan` asks that an end is present and not
//     before its start, of every position in every retained function.
//
// # The extent is the node extent, and that is a lower bound
//
// `ast` records no end position for anything. A triple-quoted literal carries
// its opening line and its DECODED value, so its closing `"""` is not
// derivable here without re-reading source. On the witness the node extent
// is 25..43 and the text extent is 25..61, because the last hole sits
// eighteen lines above the closing delimiter. So a span is exact where the
// construct's last line holds a node and short where it does not; the text
// extent needs an end position in the parser. `ir.Pos`'s own header says the
// same.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irSpan is `irPos` for a construct whose text reaches past its start.
//
// A SYNTHESIZED START OR A NON-EMITTABLE END ANSWERS A POINT, and neither is
// hypothetical. `irPos` routes a synthesized line to `ir.AtSynthesized`, which
// is deliberately a point — an origin has no extent — and derive synthesis
// allocates from a band around 2^30, so an end taken from a synthesized node
// under a real statement would claim a span a billion lines long. Both fall
// back to the start.
func (g *gen) irSpan(line, col, endLine, endCol int) ir.Pos {
	start := g.irPos(line, col)
	if start.Synthesized() || !emittableLine(endLine) {
		return start
	}
	if endLine < line || (endLine == line && endCol < col) {
		return start
	}
	return ir.Spanning(g.nomiPath, line, col, endLine, endCol)
}

// irNodeSpan is a node's position WITH its source extent: a span for the node
// classes whose text reaches past their start line, and a point for every
// other node in the language.
//
// TWO CLASSES HAVE A SPAN AND ONE OF THEM HAS NO CONSTRUCTION SITE, which is
// said here so nobody prices work off `ir.Spanning`'s generality:
//
//	*ast.StringInterp   `"""…${x}…"""` — the holes are nodes with their own
//	*ast.TaggedString    lines, so the extent is derivable
//	a pipe chain        multi-line, and NOT here: the desugaring hands each
//	                    STAGE's own `*ast.Call` to `bl.call` and never
//	                    positions the chain, so there is no site that could
//	                    take a span for it
//
// The pipe's absence is not a gap to close for its own sake. Its stages' own
// positions are what a consumer blames, and each of those is a point that is
// already right.
func (g *gen) irNodeSpan(n ast.Node) ir.Pos {
	line, col := nodePos(n)
	endLine, endCol := astNodeExtent(n)
	return g.irSpan(line, col, endLine, endCol)
}

// astNodeExtent is the last position any node in n's own construct occupies.
//
// A CLOSED SWITCH OVER THE TWO CLASSES THAT CAN SPAN, and not a reflective
// subtree walk. `childNodes` would answer the general question, and its own
// header says cost is irrelevant there because it runs for files already being
// skipped; this runs on the lowering path. More importantly the general answer
// is not wanted: a node's extent is a claim about ITS OWN construct, so a
// `+` whose left operand is a multi-line literal is a point at the operator
// and the literal carries the span. Anything this switch does not name answers
// its own position.
func astNodeExtent(n ast.Node) (line, col int) {
	line, col = nodePos(n)
	switch t := n.(type) {
	case *ast.StringInterp:
		return stringPartsExtent(t.Parts, line, col)
	case *ast.TaggedString:
		return stringPartsExtent(t.Parts, line, col)
	}
	return line, col
}

// stringPartsExtent is the furthest position an interpolation's parts reach.
//
// ONLY THE HOLES CONTRIBUTE. `ast.StringText` has no position of its own — it
// is one field and it is the string — which `irinterp.go` records as the one
// enumeration row asking for something the AST cannot supply. So a literal
// whose text continues below its last hole reports the hole's line, and that
// is the lower bound `irspan.go`'s header and `ir.Pos`'s both name.
//
// A HOLE IS LOWERED RECURSIVELY because a hole's expression can itself be an
// interpolation: `"""a${ "b${c}" }d"""` is legal and its inner literal's own
// extent is the outer one's.
func stringPartsExtent(parts []ast.StringPart, line, col int) (int, int) {
	for _, p := range parts {
		e, isExpr := p.(ast.StringExpr)
		if !isExpr || isNilNode(e.Expr) {
			continue
		}
		l, c := astNodeExtent(e.Expr)
		if l > line || (l == line && c > col) {
			line, col = l, c
		}
	}
	return line, col
}
