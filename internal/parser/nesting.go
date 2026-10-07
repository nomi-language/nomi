package parser

// MaxNesting is how deeply expressions, patterns and types may sit inside one
// another: `((…))`, `[[…]]`, `{ {…} }`, `|| || …`, `if … { if … {` and
// `List<List<…>>` each count a level per nesting, and a flat chain of
// operators or `|>` stages does not.
//
// Real code stays far below it. Each level costs a few Go frames in the
// parser and in every later pass that walks the tree, and several of those
// passes do work per level that grows with the depth below it, so an input
// with thousands of levels (a fuzzer's, or a file being typed with every
// `{` still open) cost seconds where an error costs nothing.
const MaxNesting = 256

// enterNested counts one more level of nesting and fails past MaxNesting.
// A call that succeeds is paired with leaveNested; a failing one undoes
// itself.
//
// Past the limit, every later call fails too, until the top-level
// declaration being parsed ends (parseWithRecovery clears tooDeep). The
// parser tries other readings of a construct when one fails, and each of
// those would descend to the limit again; failing them at once keeps an
// over-deep input to one descent.
func (p *Parser) enterNested() error {
	if !p.tooDeep {
		p.nesting++
		if p.nesting <= MaxNesting {
			return nil
		}
		p.nesting--
		p.tooDeep = true
	}
	tok := p.peek()
	return errorAt(tok.Line, tok.Col, "nesting deeper than %d levels; move the inner part into a binding or a function", MaxNesting)
}

func (p *Parser) leaveNested() { p.nesting-- }
