package hoverdoc

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Hover on a discard `_`: the type of the value it discards and, where the
// match may fail (a `case` arm, `if P = v`, a binding with `else`, `assert
// P = v`), what it matches.
//
//	#(_, coins) = pair      `Int`, discarded
//	_ = count()             `Int`, discarded
//	fn _, x -> x            `Int`, discarded
//	Some(_) -> 1            `String`, discarded: matches any `String`
//	_ -> 2                  `Coin`, discarded: matches any `Coin`
//
// A `_` whose type the checker could not infer is "discarded" alone. The
// `_` of a `case` with no subject is its last arm, which runs when no
// condition above it holds.

// discard claims a `_` pattern.
func (s *shapeSearch) discard(n *ast.WildcardPattern) {
	s.claim(n.Line, n.Col, 1, 1, func() string {
		if s.conditionArms[n] {
			return "Otherwise: runs when no condition above holds."
		}
		return s.renderDiscard(s.fa.PatternTypes[n], s.refutable[n])
	})
}

// discardParams claims each `_` among a function's or lambda's parameters,
// whose types are the function type ft's (nil when the checker gave none).
func (s *shapeSearch) discardParams(params []ast.Param, ft func() *analysis.FuncType) {
	for i, p := range params {
		if p.Name != "_" || p.Destructure != nil {
			continue
		}
		s.claim(p.Line, p.Col, 1, 1, func() string {
			var ty analysis.Type
			if t := ft(); t != nil && i < len(t.Params) {
				ty = t.Params[i]
			}
			return s.renderDiscard(ty, false)
		})
	}
}

// funcDefType is the type the checker gave the function fd declares.
func (s *shapeSearch) funcDefType(fd *ast.FuncDef) *analysis.FuncType {
	sym, _, _, ok := s.fa.TokenAt(analysis.Pos{Line: fd.Line, Col: fd.Col})
	if !ok || sym == nil {
		return nil
	}
	ft, _ := analysis.ResolveTypeVar(sym.Type).(*analysis.FuncType)
	return ft
}

// discardBinding claims the `_` of `_ = value`: the value's type, or the
// annotation's.
func (s *shapeSearch) discardBinding(b *ast.Binding) {
	if b.Name != "_" {
		return
	}
	s.claim(b.Line, b.Col, 1, 1, func() string {
		ty := s.fa.ExprTypes[b.Value]
		if b.TypeAnnotation != nil {
			if want := s.fa.ExpectedTypes[b.Value]; want != nil {
				ty = want
			}
		}
		return s.renderDiscard(ty, false)
	})
}

// renderDiscard is the hover of a `_` of type ty; refutable is whether it
// stands where a match may fail.
func (s *shapeSearch) renderDiscard(ty analysis.Type, refutable bool) string {
	spelled := s.pipeType(analysis.ResolveTypeVar(ty))
	switch {
	case spelled == "" && refutable:
		return "discarded: matches any value"
	case spelled == "":
		return "discarded"
	case refutable:
		return "`" + spelled + "`, discarded: matches any `" + spelled + "`"
	}
	return "`" + spelled + "`, discarded"
}

// noteRefutable records each `_` in pattern as standing where a match may
// fail.
func (s *shapeSearch) noteRefutable(pattern ast.Node) {
	if pattern == nil {
		return
	}
	ast.Inspect(pattern, func(n ast.Node) bool {
		if w, ok := n.(*ast.WildcardPattern); ok {
			if s.refutable == nil {
				s.refutable = map[*ast.WildcardPattern]bool{}
			}
			s.refutable[w] = true
		}
		return true
	})
}

// noteRefutablePositions records the `_`s of the patterns n holds that may
// fail to match, and the `_` arm of a `case` with no subject.
func (s *shapeSearch) noteRefutablePositions(n ast.Node) {
	switch n := n.(type) {
	case *ast.Case:
		for _, b := range n.Branches {
			if w, ok := b.Pattern.(*ast.WildcardPattern); ok && n.Value == nil {
				if s.conditionArms == nil {
					s.conditionArms = map[*ast.WildcardPattern]bool{}
				}
				s.conditionArms[w] = true
				continue
			}
			s.noteRefutable(b.Pattern)
		}
	case *ast.If:
		s.noteRefutable(n.CondPattern)
	case *ast.PatternBinding:
		if n.Else != nil {
			s.noteRefutable(n.Pattern)
			for _, arm := range n.Else.Arms {
				s.noteRefutable(arm.Pattern)
			}
		}
	case *ast.PatternDestructure:
		s.noteRefutable(n.Pattern)
	}
}
