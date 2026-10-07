package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnresolvedExpr is an expression the checker left without a usable type.
type UnresolvedExpr struct {
	Node ast.Node
	Line int
	Col  int
	// Where is the node's position in its parent: "in Call", or a role
	// such as "as a subject-less case's condition".
	Where string
	// Missing says what is wrong: "no type recorded", "a nil type
	// recorded", or "unsolved type variable in T".
	Missing string
}

func (u UnresolvedExpr) String() string {
	return fmt.Sprintf("%d:%d: %s %s has %s", u.Line, u.Col, exprLabel(u.Node), u.Where, u.Missing)
}

// UnresolvedExprs is the post-check invariant that the IR builder can read
// every value expression's type from the checker instead of deriving it
// again. It walks nodes, the top-level declarations of the file fa analyzed
// (irbuild.Module.Nodes, std.StdLib.Nodes), and returns each value
// expression with no entry in fa.ExprTypes, a nil entry, or an entry whose
// type is an unsolved variable. Call it on a file the checker accepted; a
// rejected file stops checking at its errors.
//
// What counts as a value expression is decided by position, not only by
// node type, because the parser reuses expression nodes for things that are
// not values:
//
//   - a pattern (a case arm's, a destructuring binding's, `if P = e`'s, a
//     parameter's): its literals are compared and its names bound, and the
//     bindings' types live on their symbols (FileAnalysis.Definitions,
//     LambdaPatternTypes). An ad-hoc conditional's arm (`case { x > 1 ->
//     ... }`) is a condition, so it does count.
//   - a name: the field of `x.f`, the type or file qualifier of `T.f` and
//     `io.print`, a field accessor's path, the target of `with App.f = v`,
//     an import, a derive decorator and its options.
//   - a pipe stage, the right of `|>` (`f(a)`, `try f()`, `then |x| ...`,
//     `dbg`, an `if` or `case` stage): it is a call with its subject
//     missing, and the pipe's own type is recorded. Its arguments count.
//   - a partial application's `_`, a `todo`'s message, and a block: a
//     function's, test's, lambda's or branch's body block, and an `else if`
//     (the chain's first `if` carries the type). A block's statements and
//     tail count.
//
// A node on a derive-synthesis line is skipped: recordExprType never
// records one, and it has no source position to report.
//
// An unsolved variable nested in a type (`List<?1>` for `[]`, `Result<Int,
// ?2>` for `Ok(1)`) is not reported. The checker allows one where no value
// of that type is made (undetermined.go) and the builder represents it
// without knowing it. A value whose own type is an unsolved variable, or a
// function whose parameter or result is one, has no representation, and is
// reported.
func UnresolvedExprs(fa *FileAnalysis, nodes []ast.Node) []UnresolvedExpr {
	w := &unresolvedWalk{
		fa: fa, skip: map[ast.Node]bool{}, notValue: map[ast.Node]bool{},
		stage: map[ast.Node]bool{}, patch: map[ast.Node]bool{}, where: map[ast.Node]string{},
	}
	for _, top := range nodes {
		ast.Inspect(top, w.visit)
	}
	return w.out
}

type unresolvedWalk struct {
	fa *FileAnalysis
	// skip marks subtrees that hold no value expression; notValue marks a
	// node that is not a value while its children may be. stage marks a
	// pipe stage, and patch a brace literal that patches a struct.
	skip, notValue, stage, patch map[ast.Node]bool
	// where is the position each node holds in its parent, for the report.
	where map[ast.Node]string
	out   []UnresolvedExpr
}

func (w *unresolvedWalk) visit(n ast.Node) bool {
	if w.skip[n] || IsSynthesizedLine(n.LineNum()) {
		return false
	}
	ast.Children(n, func(c ast.Node) {
		if _, ok := w.where[c]; !ok {
			w.where[c] = "in " + exprLabel(n)
		}
	})
	switch n := n.(type) {
	case ast.TypeExpr, *ast.ImportStmt, *ast.ImportBlock, *ast.Decorator, *ast.FieldAccessor,
		*ast.Todo:
		return false
	case *ast.FieldAccess:
		w.skip[n.Field] = true
		switch obj := n.Object.(type) {
		case *ast.TypeIdent:
			w.skip[obj] = true
		case *ast.Ident:
			if w.isFileQualifier(obj) {
				w.skip[obj] = true
			}
		case *ast.FieldAccess:
			if w.isTypePath(obj) {
				w.skip[obj] = true
			}
		}
	case *ast.Binary:
		if n.Op == "|>" {
			w.notValue[n.Right] = true
			w.stage[n.Right] = true
			switch st := n.Right.(type) {
			case *ast.TryOp:
				w.notValue[st.Expr] = true
			case *ast.Then:
				w.notValue[st.Lambda] = true
			case *ast.If:
				w.notValue[st.Cond] = true
			}
		}
	case *ast.If:
		if n.CondPattern != nil {
			w.skip[n.CondPattern] = true
		}
		if n.Else != nil {
			w.notValue[n.Else] = true
		}
	case *ast.Case:
		conditions := n.Value == nil && !w.stage[n]
		for i := range n.Branches {
			b := &n.Branches[i]
			if _, wild := b.Pattern.(*ast.WildcardPattern); !conditions || wild {
				w.skip[b.Pattern] = true
			} else if b.Pattern != nil {
				w.where[b.Pattern] = "as a subject-less case's condition"
			}
		}
	case *ast.BindingElse:
		for i := range n.Arms {
			w.skip[n.Arms[i].Pattern] = true
		}
	case *ast.PatternDestructure:
		w.skip[n.Pattern] = true
	case *ast.PatternBinding:
		w.skip[n.Pattern] = true
	case *ast.TupleDestructure:
		for _, b := range n.Bindings {
			if b != nil {
				w.skip[b] = true
			}
		}
	case *ast.DistinctDestructure:
		if n.Binding != nil {
			w.skip[n.Binding] = true
		}
	case *ast.StructDestructure, *ast.MapDestructure:
		w.skipPatternFields(n)
	case *ast.With:
		w.skip[n.Target] = true
	case *ast.Lambda:
		w.skipParamPatterns(n.Params)
	case *ast.FuncDef:
		w.skipParamPatterns(n.Params)
	case *ast.InterfaceMethod:
		w.skipParamPatterns(n.Params)
	case *ast.StructLit:
		// A bare brace at a field of a patch is itself a patch, not a value
		// (checkStructLitAgainstStruct).
		if n.TypeName == nil && (n.Spread != nil || w.patch[n]) {
			for _, f := range n.Fields {
				if lit, ok := f.Value.(*ast.StructLit); ok && lit.TypeName == nil && lit.Spread == nil {
					w.patch[lit] = true
					w.notValue[lit] = true
				}
			}
		}
	}
	if w.notValue[n] || !isValueExpr(n) {
		return true
	}
	missing := ""
	t, ok := w.fa.ExprTypes[n]
	switch {
	case !ok:
		missing = "no type recorded"
	case t == nil:
		missing = "a nil type recorded"
	case unrepresentable(t):
		missing = "an unsolved type variable in " + t.String()
	}
	if missing == "" {
		return true
	}
	line, col := nodePos(n)
	w.out = append(w.out, UnresolvedExpr{Node: n, Line: line, Col: col, Where: w.where[n], Missing: missing})
	// A node with no type was most likely never checked, and neither was
	// anything under it: its root is the one report.
	return ok
}

// skipParamPatterns marks each destructuring parameter's pattern. A
// parameter's default value counts.
func (w *unresolvedWalk) skipParamPatterns(params []ast.Param) {
	for _, p := range params {
		if p.Destructure != nil {
			w.skip[p.Destructure] = true
		}
	}
}

// skipPatternFields marks the patterns of a struct or map destructuring,
// leaving its value.
func (w *unresolvedWalk) skipPatternFields(n ast.Node) {
	var value ast.Node
	switch d := n.(type) {
	case *ast.StructDestructure:
		value = d.Value
	case *ast.MapDestructure:
		value = d.Value
	}
	ast.Children(n, func(c ast.Node) {
		if c != value {
			w.skip[c] = true
		}
	})
}

// isFileQualifier reports whether id names an imported file (`io` in
// `io.print`) rather than a value.
func (w *unresolvedWalk) isFileQualifier(id *ast.Ident) bool {
	if _, ok := w.fa.ExprTypes[id]; ok {
		return false
	}
	sym := w.fa.References[Pos{Line: id.Line, Col: id.Col}]
	return sym == nil || sym.Kind == SymbolModule
}

// isTypePath reports whether n is a qualified type or file path
// (`Json.Case`, `geo.Shape`) rather than a value.
func (w *unresolvedWalk) isTypePath(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.TypeIdent:
		return true
	case *ast.Ident:
		return w.isFileQualifier(n)
	case *ast.FieldAccess:
		if _, ok := w.fa.ExprTypes[n]; ok {
			return false
		}
		return w.isTypePath(n.Object)
	}
	return false
}

// unrepresentable reports whether t is an unsolved variable, or a function
// type with one as a parameter or result.
func unrepresentable(t Type) bool {
	switch t := resolveTypeVar(t).(type) {
	case *TypeVar:
		return true
	case *FuncType:
		for _, p := range t.Params {
			if _, ok := resolveTypeVar(p).(*TypeVar); ok {
				return true
			}
		}
		_, ok := resolveTypeVar(t.Return).(*TypeVar)
		return ok
	}
	return false
}

// exprLabel names n's kind in a report: its node type, or Pipe for `|>`.
func exprLabel(n ast.Node) string {
	if b, ok := n.(*ast.Binary); ok && b.Op == "|>" {
		return "Pipe"
	}
	return n.NodeType()
}

func nodePos(n ast.Node) (int, int) {
	if s, ok := n.(ast.HasSpan); ok {
		if sp := s.GetSpan(); !sp.IsZero() {
			return sp.StartLine, sp.StartCol
		}
	}
	return n.LineNum(), 0
}

// isValueExpr reports whether n is a node type that evaluates to a value in
// expression position.
func isValueExpr(n ast.Node) bool {
	switch n.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit, *ast.StringLit,
		*ast.StringInterp, *ast.TaggedString, *ast.ListLit, *ast.VectorLit, *ast.SetLit,
		*ast.ListSpreadLit, *ast.MapLit, *ast.TupleLit, *ast.Ident, *ast.TypeIdent, *ast.Unary,
		*ast.Binary, *ast.GroupedExpr, *ast.If, *ast.Call, *ast.DotVariant,
		*ast.FieldAccess, *ast.StructLit, *ast.RangeLit, *ast.Lambda, *ast.TryOp, *ast.Then,
		*ast.Case, *ast.ConcurrentBlock, *ast.Dbg, *ast.Return, *ast.Break, *ast.Continue:
		return true
	}
	return false
}
