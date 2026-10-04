package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// Whether a pipe-valued binding's pipeline stages can ever be READ.
//
// # Why this gate exists
//
// The stages are `rt.AssertionPipelineStage{Expr, Value}` rows and the Value is
// the prefix's value INSPECTED, so recording stages for every recordable
// pipe-valued initializer would make `seen = counts |> Map.get(cell)` inside a
// fold render the whole map once per element and then discard the text. Cost is
// O(size of the piped subject) per element: on a Game of Life board fold,
// 705 ms against 13 ms for the same two expressions written as nested calls.
//
// No output comparison catches that, because the rendered value is DISCARDED
// and the printed bytes are the same.
//
// # The bound, and why it is exact rather than conservative
//
// The stages have exactly one reader: gen.bindingContext, for an assertion
// whose subject is the binding's BARE NAME. It reaches the binding through
// g.lookup, so the reader must be somewhere the binding is still in scope —
// which is the remainder of the block that introduced it, nested blocks and
// lambda bodies included. An assertion outside that extent cannot name this
// binding at all: g.lookup would answer with a different local or with nothing.
//
// So "an assertion in the rest of this block names it bare" is not a heuristic
// approximating the reader; it is the reader's own precondition, read off the
// syntax before the reader exists. When the builder cannot say what the rest of
// the block is — gen.probe re-walking a node with lowering suppressed — the
// answer is YES, and every stage is recorded.
//
// # The three positions, which are bindingContext's callers and not a guess
//
//   - an *ast.Assertion's subject (tests.go, assertable.go)
//   - a `testing.check(x)` argument (stdcheck.go)
//   - an *ast.PatternDestructure's value (patternassert.go)
//
// `testing.check` is matched on its SPELLING — qualifier literally `testing` —
// which is stdcheck.go's own gate. An aliased or selectively-imported spelling
// is refused there rather than lowered, so a name that reaches only that
// spelling has no reader either way.

// assertsBareName reports whether n, anywhere inside it, names `name` as the
// bare subject of an assertion, a `testing.check` or a pattern assertion.
//
// The walk is childNodes', for its reason: the alternative is a switch over
// every node type whose only failure mode is silent, and a kind somebody forgot
// would answer "no reader" and drop rows the report should print.
func assertsBareName(n ast.Node, name string) bool {
	if isNilNode(n) {
		return false
	}
	switch t := n.(type) {
	case *ast.Assertion:
		if isBareName(t.Expr, name) {
			return true
		}
	case *ast.PatternDestructure:
		if isBareName(t.Value, name) {
			return true
		}
	case *ast.Call:
		if isTestingCheckCallee(t.Func) && len(t.Args) == 1 && isBareName(t.Args[0], name) {
			return true
		}
	}
	for _, c := range childNodes(n) {
		if assertsBareName(c, name) {
			return true
		}
	}
	return false
}

func isBareName(n ast.Node, name string) bool {
	ident, ok := n.(*ast.Ident)
	return ok && ident.Name == name
}

// isTestingCheckCallee matches the ONE spelling stdcheck.go claims: the qualifier written literally `testing`.
func isTestingCheckCallee(n ast.Node) bool {
	fa, ok := n.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "check" {
		return false
	}
	owner, ok := fa.Object.(*ast.Ident)
	return ok && owner.Name == "testing"
}
