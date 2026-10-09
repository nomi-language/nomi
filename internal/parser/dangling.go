package parser

import (
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// attachDangling gives each comment the parse kept nowhere to the node
// nearest it, so the formatter can write it back. A comment in a position
// the tree has no trivia slot for, such as `fn f( // why` or `(1, 2 // why`,
// is stepped over by the parse and would otherwise be lost.
//
// A comment is kept nowhere when no Trivia in the tree or in fileEnd, the
// trivia after the last top-level node, holds it. It goes to
// the innermost node whose extent contains it: after the last node inside
// that one which ends before the comment (DanglingAfter), else before the
// first which starts after it (DanglingBefore), else after the container
// itself. A comment outside every node goes after the top-level node before
// it, or before the one after it.
//
// Excluded is a `//` with no text, which a doc comment may have absorbed as a
// separator and which is layout to the formatter.
func attachDangling(nodes []ast.Node, fileEnd []ast.Trivia, tokens []token.Token) {
	var orphans []token.Token
	var claimed map[[2]int]bool
	for _, tok := range tokens {
		if tok.Type != token.COMMENT || strings.TrimRight(tok.Lexeme, " \t\r") == "//" {
			continue
		}
		if claimed == nil {
			claimed = claimedComments(nodes, fileEnd)
		}
		if !claimed[[2]int{tok.Line, tok.Col}] {
			orphans = append(orphans, tok)
		}
	}
	if len(orphans) == 0 {
		return
	}
	cands := danglingTargets(nodes)
	for _, tok := range orphans {
		attachOrphan(cands, nodes, tok)
	}
}

var (
	triviaValueType = reflect.TypeOf(ast.Trivia{})
	nodeIfaceType   = reflect.TypeOf((*ast.Node)(nil)).Elem()
)

// claimedComments is the position of every comment a Trivia in the tree
// holds.
func claimedComments(nodes []ast.Node, fileEnd []ast.Trivia) map[[2]int]bool {
	out := map[[2]int]bool{}
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Ptr:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem())
		case reflect.Struct:
			if v.Type() == triviaValueType {
				t := v.Interface().(ast.Trivia)
				if t.Kind == ast.TriviaComment {
					out[[2]int{t.Line, t.Col}] = true
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		}
	}
	for _, n := range nodes {
		walk(reflect.ValueOf(n))
	}
	walk(reflect.ValueOf(fileEnd))
	return out
}

// danglingTarget is a node a dangling comment can go to: one with an extent that
// carries trivia.
type danglingTarget struct {
	node  ast.HasDangling
	span  ast.Span
	depth int
}

// danglingTargets lists every node in the tree with an extent, outermost first.
func danglingTargets(nodes []ast.Node) []danglingTarget {
	var out []danglingTarget
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value, depth int)
	walk = func(v reflect.Value, depth int) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), depth)
			}
		case reflect.Ptr:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if v.Type().Implements(nodeIfaceType) {
				if hs, ok := v.Interface().(ast.HasSpan); ok {
					if d, ok := v.Interface().(ast.HasDangling); ok && !hs.GetSpan().IsZero() {
						out = append(out, danglingTarget{node: d, span: hs.GetSpan(), depth: depth})
						depth++
					}
				}
			}
			walk(v.Elem(), depth)
		case reflect.Struct:
			if v.Type() == triviaValueType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), depth)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), depth)
			}
		}
	}
	for _, n := range nodes {
		walk(reflect.ValueOf(n), 0)
	}
	return out
}

// pos is a source position, ordered by line then column.
type pos struct{ line, col int }

func (a pos) less(b pos) bool { return a.line < b.line || a.line == b.line && a.col < b.col }

func spanStart(s ast.Span) pos { return pos{s.StartLine, s.StartCol} }
func spanEnd(s ast.Span) pos   { return pos{s.EndLine, s.EndCol} }

func attachOrphan(cands []danglingTarget, nodes []ast.Node, tok token.Token) {
	c := pos{tok.Line, tok.Col}
	trivia := ast.Trivia{Kind: ast.TriviaComment, Text: tok.Lexeme, Line: tok.Line, Col: tok.Col}

	// The innermost container: the latest start, then the earliest end,
	// then the deepest.
	container := -1
	for i, n := range cands {
		if c.less(spanStart(n.span)) || !c.less(spanEnd(n.span)) {
			continue
		}
		if container < 0 || innerThan(n, cands[container]) {
			container = i
		}
	}
	inside := func(n danglingTarget) bool {
		if container < 0 {
			return true
		}
		outer := cands[container]
		return n != outer && !spanStart(n.span).less(spanStart(outer.span)) && !spanEnd(outer.span).less(spanEnd(n.span))
	}

	// The node that ends last before the comment, the outermost of those
	// that end there.
	before := -1
	for i, n := range cands {
		if !inside(n) || c.less(spanEnd(n.span)) {
			continue
		}
		if before < 0 {
			before = i
			continue
		}
		b := cands[before]
		e, be := spanEnd(n.span), spanEnd(b.span)
		if be.less(e) || e == be && (spanStart(n.span).less(spanStart(b.span)) || spanStart(n.span) == spanStart(b.span) && n.depth < b.depth) {
			before = i
		}
	}
	if before >= 0 {
		cands[before].node.AddDanglingAfter(trivia)
		return
	}

	// The node that starts first after the comment, the outermost of those
	// that start there.
	after := -1
	for i, n := range cands {
		if !inside(n) || !c.less(spanStart(n.span)) {
			continue
		}
		if after < 0 {
			after = i
			continue
		}
		a := cands[after]
		s, as := spanStart(n.span), spanStart(a.span)
		if s.less(as) || s == as && (spanEnd(a.span).less(spanEnd(n.span)) || spanEnd(a.span) == spanEnd(n.span) && n.depth < a.depth) {
			after = i
		}
	}
	if after >= 0 {
		cands[after].node.AddDanglingBefore(trivia)
		return
	}
	if container >= 0 {
		cands[container].node.AddDanglingAfter(trivia)
		return
	}
	// No node has an extent: keep the comment on the last top-level node.
	if len(nodes) > 0 {
		if d, ok := nodes[len(nodes)-1].(ast.HasDangling); ok {
			d.AddDanglingAfter(trivia)
		}
	}
}

// innerThan reports whether a lies inside b: it starts later, or ends
// earlier, or has the same extent deeper in the tree.
func innerThan(a, b danglingTarget) bool {
	as, bs := spanStart(a.span), spanStart(b.span)
	if as != bs {
		return bs.less(as)
	}
	ae, be := spanEnd(a.span), spanEnd(b.span)
	if ae != be {
		return ae.less(be)
	}
	return a.depth > b.depth
}
