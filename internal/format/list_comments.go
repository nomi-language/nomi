package format

import "github.com/nomi-language/nomi/internal/ast"

// itemComments is n's comments as an item of a list, nil when it has none.
func itemComments(n ast.Node) *ast.ItemComments {
	h, ok := n.(ast.HasItemComments)
	if !ok {
		return nil
	}
	if c := h.GetItemComments(); !c.Empty() {
		return c
	}
	return nil
}

// nodesComments is the comments of each of nodes as list items, and
// whether any holds one.
func nodesComments(nodes []ast.Node) ([]*ast.ItemComments, bool) {
	var out []*ast.ItemComments
	for i, n := range nodes {
		if c := itemComments(n); c != nil {
			if out == nil {
				out = make([]*ast.ItemComments, len(nodes))
			}
			out[i] = c
		}
	}
	return out, out != nil
}

// typesComments is nodesComments for type expressions.
func typesComments(types []ast.TypeExpr) ([]*ast.ItemComments, bool) {
	nodes := make([]ast.Node, len(types))
	for i, t := range types {
		nodes[i] = t
	}
	return nodesComments(nodes)
}

// anyComments reports whether any of comments holds a comment.
func anyComments(comments []*ast.ItemComments) bool {
	for _, c := range comments {
		if !c.Empty() {
			return true
		}
	}
	return false
}

// emitCommentedList writes a bracketed list that holds a comment, one
// element per line, each followed by a comma: an item's Before comments on
// lines above it, its Trailing comment after its comma, its After comments
// on lines below it, and end, the list's comments before its closing
// bracket, last. comments is indexed like elems and may be shorter.
//
// LocalBroken, as forceBrokenBraced, so the list breaking does not break
// the groups around it.
func emitCommentedList(open, close string, elems []Doc, comments []*ast.ItemComments, end []ast.Trivia) Doc {
	return commentedList(open, close, elems, comments, end, true)
}

// emitCommentedListNoLastComma is emitCommentedList for a list whose syntax
// has no trailing comma: its last element ends without one.
func emitCommentedListNoLastComma(open, close string, elems []Doc, comments []*ast.ItemComments) Doc {
	return commentedList(open, close, elems, comments, nil, false)
}

func commentedList(open, close string, elems []Doc, comments []*ast.ItemComments, end []ast.Trivia, lastComma bool) Doc {
	body := make([]Doc, 0, len(elems)*4+1)
	for i, e := range elems {
		var c *ast.ItemComments
		if i < len(comments) {
			c = comments[i]
		}
		body = append(body, HardLine())
		if c != nil {
			for _, t := range c.Before {
				body = append(body, Text(t.Text), HardLine())
			}
		}
		body = append(body, e)
		if lastComma || i < len(elems)-1 {
			body = append(body, Text(","))
		}
		if c != nil {
			body = append(body, emitSameLineComments(c.Trailing)...)
			for _, t := range c.After {
				body = append(body, HardLine(), Text(t.Text))
			}
		}
	}
	if end = withoutBlankTrivia(end); len(end) > 0 {
		body = append(body, emitEndTrivia(end))
	}
	return LocalBroken(Concat(
		Text(open),
		Nest(defaultIndent, Concat(body...)),
		HardLine(),
		Text(close),
	))
}

// forceBrokenItems is forceBrokenBracedWithEndTrivia for a list of nodes,
// elems their documents, writing each node's comments as list items.
func forceBrokenItems(open, close string, elems []Doc, nodes []ast.Node, end []ast.Trivia) Doc {
	cs, _ := nodesComments(nodes)
	return emitCommentedList(open, close, elems, cs, end)
}

// nodesHaveComments reports whether any of nodes holds a comment as a list
// item.
func nodesHaveComments(nodes []ast.Node) bool {
	_, ok := nodesComments(nodes)
	return ok
}

// lambdaParamsNeedList reports whether a lambda's parameters are written as
// a list one per line: when the header holds any comment but one ending
// the line of a parameter other than the last, after its comma. after is
// the comments the body kept after each parameter
// (gatherLambdaHeaderComments). With only those, the header keeps its own
// layout: the first parameter on the `|` line, the comment ending each
// such line, and the next parameter on the next line.
func lambdaParamsNeedList(params []ast.Param, after [][]ast.Trivia) bool {
	for i, p := range params {
		if len(p.Comments.Before) > 0 || len(p.Comments.After) > 0 || len(after[i]) > 0 {
			return true
		}
		if len(p.Comments.Trailing) > 0 && i == len(params)-1 || len(p.Comments.Trailing) > 1 {
			return true
		}
		// A comment inside a parameter (its pattern, annotation or
		// default) may be written where the next format reads it as one
		// of the list's, so the list is written one per line either way.
		inner := p
		inner.Comments = ast.ItemComments{}
		if paramHasComment(inner) {
			return true
		}
	}
	return false
}

// tupleDestructureComments is each binding's comments; a wildcard has none.
func tupleDestructureComments(v *ast.TupleDestructure) []*ast.ItemComments {
	out := make([]*ast.ItemComments, len(v.Bindings))
	for i, b := range v.Bindings {
		if b != nil {
			out[i] = itemComments(b)
		}
	}
	return out
}

// emitParamList writes a function's parenthesized parameter list from the
// parameters' documents, broken one per line when a parameter holds a
// comment, or as keepsBrokenLines says given open, the line of the
// function's name. end is the comments in a list with no parameter.
func emitParamList(open int, docs []Doc, params []ast.Param, end []ast.Trivia) Doc {
	if cs := paramsComments(params); anyComments(cs) || len(withoutBlankTrivia(end)) > 0 {
		return emitCommentedList("(", ")", docs, cs, end)
	}
	first := 0
	if len(params) > 0 {
		first = params[0].Line
	}
	return emitBracedListKeepingBreaks("(", ")", docs, keepsBrokenLines(open, first, docs))
}

// emitCommentedArgs writes a call's argument list holding a comment. One
// argument has no trailing comma, as in emitArgList's broken form, so a
// comment above it reads the same whether it is the argument's or one the
// argument's own expression kept.
func emitCommentedArgs(elems []Doc, cs []*ast.ItemComments) Doc {
	return commentedList("(", ")", elems, cs, nil, len(elems) > 1)
}

// emitEmptyArgList writes the parentheses of a call with no argument, with
// the comments between them on lines of their own.
func emitEmptyArgList(v *ast.Call) Doc {
	if end := withoutBlankTrivia(v.EndTrivia); len(end) > 0 {
		return emitCommentedList("(", ")", nil, nil, end)
	}
	return Text("()")
}

// groupedHasComments reports whether g, or parentheses nested in it,
// holds a comment as the expression inside them.
func groupedHasComments(g *ast.GroupedExpr) bool {
	for g != nil && g.Expr != nil {
		if itemComments(g.Expr) != nil {
			return true
		}
		g, _ = g.Expr.(*ast.GroupedExpr)
	}
	return false
}

// emitTupleItems writes a tuple literal from its elements' documents,
// broken one per line when an element holds a comment or the source breaks
// the line after its `(` (brokenAfterOpen).
func emitTupleItems(elems []Doc, v *ast.TupleLit) Doc {
	open := shapeOpen("#", v.TypeName, "(")
	if cs, ok := nodesComments(v.Items); ok || itemsBrokenAfterOpen(v.Line, v.Items) {
		return emitCommentedList(open, ")", elems, cs, nil)
	}
	return emitBracedList(open, ")", elems)
}

// spreadOperandDocs is the document of each of a literal's elements and
// spreads in the order written (ast.Operands), a spread's with its `..`,
// each from child.
func spreadOperandDocs(items []ast.Node, spreads []ast.Spread, child func(ast.Node) Doc) []Doc {
	ops := ast.Operands(items, spreads)
	elems := make([]Doc, 0, len(ops))
	for _, it := range ops {
		if ast.IsSpreadHead(it, spreads) {
			// A spread of a range is written in parentheses, `..(1..4)`, so
			// the spread's `..` and the range's do not run together.
			if _, isRange := it.(*ast.RangeLit); isRange {
				elems = append(elems, Concat(Text("..("), child(it), Text(")")))
				continue
			}
			elems = append(elems, Concat(Text(".."), child(it)))
			continue
		}
		elems = append(elems, child(it))
	}
	return elems
}

// endsInSpread reports whether a literal of n elements with spreads is
// written ending in a spread.
func endsInSpread(n int, spreads []ast.Spread) bool {
	return len(spreads) > 0 && spreads[len(spreads)-1].At == n
}

// firstOperandLine is the line of a literal's first element or spread, 0
// for an empty literal.
func firstOperandLine(items []ast.Node, spreads []ast.Spread) int {
	switch {
	case len(spreads) > 0 && spreads[0].At == 0:
		return spreads[0].Line
	case len(items) > 0:
		return nodeStartLine(items[0])
	}
	return 0
}

// mapLitParts is a map literal's list items in the order written, the
// nodes holding their comments (a spread's head, an entry's key), and the
// document of each: a spread's with its `..`, an entry's `key => value`.
func mapLitParts(v *ast.MapLit, child func(ast.Node) Doc) ([]ast.Node, []Doc) {
	nodes := make([]ast.Node, 0, len(v.Entries)+len(v.Spreads))
	docs := make([]Doc, 0, len(v.Entries)+len(v.Spreads))
	spreads := v.Spreads
	for i := 0; i <= len(v.Entries); i++ {
		for len(spreads) > 0 && spreads[0].At == i {
			nodes = append(nodes, spreads[0].Head)
			docs = append(docs, Concat(Text(".."), child(spreads[0].Head)))
			spreads = spreads[1:]
		}
		if i < len(v.Entries) {
			e := v.Entries[i]
			nodes = append(nodes, e.Key)
			docs = append(docs, Concat(child(e.Key), Text(" => "), child(e.Value)))
		}
	}
	return nodes, docs
}

// structPatternComments is each field's comments.
func structPatternComments(fields []ast.StructPatternField) []*ast.ItemComments {
	out := make([]*ast.ItemComments, len(fields))
	for i := range fields {
		out[i] = &fields[i].Comments
	}
	return out
}

// paramsComments is each parameter's comments.
func paramsComments(params []ast.Param) []*ast.ItemComments {
	out := make([]*ast.ItemComments, len(params))
	for i := range params {
		out[i] = &params[i].Comments
	}
	return out
}

// typeParamsComments is each type parameter's comments.
func typeParamsComments(params []ast.TypeParam) []*ast.ItemComments {
	out := make([]*ast.ItemComments, len(params))
	for i := range params {
		out[i] = &params[i].Comments
	}
	return out
}

// mapPatternKeys is mapKeys for a map pattern.
func mapPatternKeys(entries []ast.MapPatternEntry) []ast.Node {
	out := make([]ast.Node, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	return out
}

// patternItems writes each of nodes as an item of a pattern's brackets (an
// enum payload, a tuple's or a list's elements) and gives each item's
// comments, reporting whether any holds one. A pattern in parentheses that
// only group it (`.Some((` then `// why` then `v))`) keeps the comments
// inside them on its DanglingBefore and DanglingAfter. As an item those are
// written inside the brackets, above and below it, where the next parse
// reads them as the item's own. Written at the pattern instead, a comment
// after it is a LineSuffix, which below an arm that ends in a comment
// leaves the arm.
func patternItems(nodes []ast.Node) ([]Doc, []*ast.ItemComments, bool) {
	docs := make([]Doc, len(nodes))
	var cs []*ast.ItemComments
	for i, n := range nodes {
		c := itemComments(n)
		docs[i] = emitPatternItem(n)
		if d, ok := n.(ast.HasDangling); ok && patternTakesDangling(n) {
			before, after := d.GetDanglingBefore(), d.GetDanglingAfter()
			if len(before) > 0 || len(after) > 0 {
				merged := &ast.ItemComments{}
				if c != nil {
					merged.Trailing = c.Trailing
					merged.Before = append(merged.Before, c.Before...)
					merged.After = append(merged.After, c.After...)
				}
				merged.Before = append(merged.Before, before...)
				merged.After = append(append([]ast.Trivia(nil), after...), merged.After...)
				c = merged
			}
		}
		if c != nil {
			if cs == nil {
				cs = make([]*ast.ItemComments, len(nodes))
			}
			cs[i] = c
		}
	}
	return docs, cs, cs != nil
}

// patternTakesDangling reports whether emitPattern writes n's dangling
// comments around it (withDangling).
func patternTakesDangling(n ast.Node) bool {
	switch n.(type) {
	case *ast.IdentPattern, *ast.WildcardPattern, *ast.TuplePattern, *ast.StructPattern,
		*ast.EnumPattern, *ast.ListPattern, *ast.MapPattern, *ast.AsPattern, *ast.OrPattern:
		return true
	}
	return false
}

// emitPatternItem is emitPattern without the dangling comments
// patternItems writes as the item's.
func emitPatternItem(n ast.Node) Doc {
	if !patternTakesDangling(n) {
		return emitPattern(n)
	}
	return emitPatternNode(n)
}

// emitGroupedPattern writes a pattern that is not an item of brackets: an
// arm's pattern, an `as` pattern's inner pattern, a field's or a map
// entry's pattern. When it holds the comments of parentheses that only
// group it (patternItems), it is written in those parentheses, the
// comments one per line inside them, where the next parse finds them
// again.
func emitGroupedPattern(n ast.Node) Doc {
	elems, cs, ok := patternItems([]ast.Node{n})
	if !ok {
		return elems[0]
	}
	return emitCommentedListNoLastComma("(", ")", elems, cs)
}

// brokenAfterOpen reports whether the source breaks the line between a
// literal's opening bracket, on line open, and its first element, which
// starts on line first (0 when it has none). A list, vector, set, map,
// tuple or struct literal, or a list, map or struct pattern, broken there
// stays broken whatever its width, as Prettier keeps an object expanded;
// the author joins it again by deleting that one line break. Only this
// break counts: line breaks between later elements do not. The broken form
// starts its first element on the line below the bracket, so a second
// format finds the same break and keeps it.
func brokenAfterOpen(open, first int) bool {
	return open > 0 && first > open
}

// itemsBrokenAfterOpen is brokenAfterOpen for a literal whose first element
// is the first of items.
func itemsBrokenAfterOpen(open int, items []ast.Node) bool {
	return len(items) > 0 && brokenAfterOpen(open, nodeStartLine(items[0]))
}

// keepsBrokenList reports whether a list is written one element per line
// because its source is: the first of items starts on a line below open,
// the line of the list's opening bracket, and one of elems, the items'
// documents, breaks across lines whatever the width (a literal written
// across lines, an element holding a comment).
//
// Such an element is LocalBroken, hidden from the list's fits check, so
// on its own the list would fit and hug it (`f(Point{`). When the list
// broke for width instead, the next format would read the element as
// written across lines and hug it then; a list already broken this way
// stays broken.
func keepsBrokenList(open int, items []ast.Node, elems []Doc) bool {
	return len(items) > 0 && keepsBrokenLines(open, nodeStartLine(items[0]), elems)
}

// keepsBrokenLines is keepsBrokenList given the line the first element
// starts on.
func keepsBrokenLines(open, first int, elems []Doc) bool {
	if open <= 0 || first <= open {
		return false
	}
	for _, e := range elems {
		if containsHardLine(e) {
			return true
		}
	}
	return false
}

// emitBracedListKeepingBreaks is emitBracedList, broken when broken is
// set.
func emitBracedListKeepingBreaks(open, close string, elems []Doc, broken bool) Doc {
	if broken {
		return brokenList(open, close, elems, true)
	}
	return emitBracedList(open, close, elems)
}

// brokenList is the broken form of emitBracedList's group, one element per
// line, with a comma after the last when lastComma is set. It is a group
// that breaks, as one too wide for its line does, so the groups around it
// break as they would around that one; it is not LocalBroken.
func brokenList(open, close string, elems []Doc, lastComma bool) Doc {
	parts := make([]Doc, 0, len(elems)*2+1)
	for i, e := range elems {
		if i > 0 {
			parts = append(parts, Text(","), HardLine())
		}
		parts = append(parts, e)
	}
	if lastComma {
		parts = append(parts, Text(","))
	}
	return Group(Concat(
		Text(open),
		Nest(defaultIndent, Concat(HardLine(), Concat(parts...))),
		HardLine(),
		Text(close),
	))
}
