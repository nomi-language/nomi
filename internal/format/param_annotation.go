package format

import "github.com/nomi-language/nomi/internal/ast"

// stripSelfTypingAnnotations removes a destructuring parameter's `: T` when
// the pattern's head already names T: `Dur(x): Dur` becomes `Dur(x)`,
// `#Point{x, y}: Point` becomes `#Point{x, y}`, `E.V(x): E` becomes `E.V(x)`.
// The checker types such a parameter by its head when it has no annotation
// (analysis.paramPatternType), so the annotation says nothing. The rewrite
// is syntactic: `nomi fmt` runs without analysis. It covers the parameters
// of every `fn`, lambda, interface function and host or Go-bound function.
//
// A head with nothing after it, `Label: Label`, keeps its annotation.
// Without one, `fn f(Label)` is a parameter with a type and no name, which
// the checker rejects (analysis.unnamedParam). An annotation that carries a
// comment also stays, so the comment keeps its place.
func stripSelfTypingAnnotations(nodes []ast.Node) {
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDef:
				stripParamAnnotations(n.Params)
			case *ast.Lambda:
				stripParamAnnotations(n.Params)
			case *ast.InterfaceMethod:
				stripParamAnnotations(n.Params)
			case *ast.ExternFunc:
				stripParamAnnotations(n.Params)
			}
			return true
		})
	}
}

func stripParamAnnotations(params []ast.Param) {
	for i := range params {
		if redundantSelfTypingAnnotation(params[i]) {
			params[i].TypeAnnotation = nil
		}
	}
}

// redundantSelfTypingAnnotation reports whether a destructuring param's
// `: T` annotation merely restates the type its pattern head already names,
// and dropping it leaves a parameter the checker reads the same way.
func redundantSelfTypingAnnotation(p ast.Param) bool {
	if p.Destructure == nil || p.TypeAnnotation == nil || hasAnyComment(p.TypeAnnotation) {
		return false
	}
	head := patternHeadTypeString(p.Destructure)
	return head != "" && head == p.TypeAnnotation.TypeString()
}

// patternHeadTypeString returns the type name a self-typing pattern head
// names, or "" if the pattern has none (tuple, anonymous struct,
// dot-variant, a head with no payload, plain name). Mirrors the analyzer's
// paramPatternHeadType, on the AST.
func patternHeadTypeString(pattern ast.Node) string {
	switch p := pattern.(type) {
	case *ast.EnumPattern:
		switch v := p.Variant.(type) {
		case *ast.SimpleType:
			if p.Payload == nil && p.Binding == "" {
				return "" // `Label` alone is a type with no parameter name
			}
			return v.Name // `Dur(x)` → Dur
		case *ast.QualifiedType:
			return v.Module // `E.V(x)` → E (the qualifier is the type)
		}
	case *ast.StructPattern:
		if p.TypeName != nil {
			return p.TypeName.TypeString() // `#Point{x, y}` → Point
		}
	}
	return ""
}

// hasAnyComment reports whether a comment sits on t or on a node inside it.
func hasAnyComment(t ast.TypeExpr) bool {
	found := false
	ast.Inspect(t, func(n ast.Node) bool {
		if itemComments(n) != nil {
			found = true
		}
		if h, ok := n.(ast.HasDangling); ok && (len(h.GetDanglingBefore()) > 0 || len(h.GetDanglingAfter()) > 0) {
			found = true
		}
		return !found
	})
	return found
}
