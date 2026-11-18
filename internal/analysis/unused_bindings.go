package analysis

import (
	"fmt"
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnusedBindingCode tags diagnostics for local bindings and parameters that are never read.
const UnusedBindingCode = "unused-binding"

// CheckUnusedBindings reports every local binding or body parameter whose symbol is never
// referenced after its definition, among the file's top-level nodes. It intentionally runs at the same point as
// CheckUnusedImports: after the builder has walked bodies and before CheckTypes
// replaces reference entries with call-site proxy symbols.
func CheckUnusedBindings(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	if fa == nil || len(fa.Definitions) == 0 {
		return nil
	}

	used := make(map[*Symbol]bool)
	for _, ref := range fa.References {
		for s := ref; s != nil; s = s.Resolved {
			used[s] = true
		}
	}

	markComparisonsWrittenAsPatterns(fa, nodes, used)

	// A pattern name refused for naming a `once` is reported there.
	onceShadows := make(map[Pos]bool)
	for _, e := range fa.TypeErrors {
		if e.Code == OnceShadowCode {
			onceShadows[Pos{Line: e.Line, Col: e.Col}] = true
		}
	}

	var errs []TypeError
	// A name an `or` pattern binds is one symbol defined at each
	// alternative's site; it is reported once.
	seen := make(map[*Symbol]bool)
	for _, sym := range fa.Definitions {
		if sym == nil || ast.IsDiscardName(sym.Name) || seen[sym] {
			continue
		}
		seen[sym] = true
		kind := "binding"
		owner := sym.Node
		switch {
		case sym.Kind == SymbolBinding && sym.ParamOf != nil:
			// A name a destructured parameter binds (`fn (word, count) -> count`)
			// is a parameter of the function the pattern belongs to.
			owner = sym.ParamOf
			if !isUnusedParameterCandidate(owner) {
				continue
			}
			kind = "parameter"
		case sym.Kind == SymbolBinding:
			if !isUnusedBindingCandidate(sym.Node) {
				continue
			}
		case sym.Kind == SymbolParam:
			if !isUnusedParameterCandidate(sym.Node) {
				continue
			}
			kind = "parameter"
		default:
			continue
		}
		if IsSynthesizedLine(sym.Pos.Line) {
			continue
		}
		if used[sym] || onceShadows[Pos{Line: sym.Pos.Line, Col: sym.Pos.Col}] {
			continue
		}
		if kind == "parameter" && unfinishedBody(fa, owner) {
			// A body with a `todo` in it is not written yet, so a parameter
			// it does not read yet is expected, not a mistake.
			continue
		}
		if _, isTest := sym.PatternBody.(*ast.Is); isTest {
			// A name an `is` test's pattern binds is the checker's error
			// (rejectIsBindings): the test binds nothing.
			continue
		}
		if sym.PatternBody != nil && containsTodo(fa, sym.PatternBody) {
			// The same for an arm: `.Rect{w, h} -> todo`, as the editor's
			// fill-arms action writes it.
			continue
		}
		hint := "prefix it with '_' if the value is intentionally ignored"
		if kind == "parameter" {
			hint = "read it in the body, or prefix it with '_' if the function does not need it"
		}
		e := TypeError{
			Line:    sym.Pos.Line,
			Col:     sym.Pos.Col,
			Message: fmt.Sprintf("%s '%s' is never read", kind, sym.Name),
			Code:    UnusedBindingCode,
		}
		if fa.dollarReads[sym] {
			// The only use is a `$name` in a string, which is text.
			e = e.WithHint(fmt.Sprintf("`$%s` in a string is text and reads nothing: Nomi interpolates with `${%s}`", sym.Name, sym.Name))
		}
		errs = append(errs, e.WithHint(hint))
	}
	// Definitions is a map; report in source order so the output is stable.
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Line != errs[j].Line {
			return errs[i].Line < errs[j].Line
		}
		return errs[i].Col < errs[j].Col
	})
	return errs
}

func isUnusedBindingCandidate(node ast.Node) bool {
	switch node.(type) {
	case *ast.Binding,
		*ast.TupleDestructure,
		*ast.StructDestructure,
		*ast.DistinctDestructure,
		*ast.PatternDestructure,
		*ast.PatternBinding,
		// A pattern with no statement around it: a `case` arm, an `else`
		// arm, an `if Some(x) = ...` condition, a test's setup binding.
		// definePattern records the pattern node that binds the name.
		*ast.IdentPattern,
		*ast.AsPattern,
		*ast.EnumPattern,
		*ast.StructPattern:
		return true
	default:
		return false
	}
}

// Declarations without Nomi bodies describe call contracts; they have no reads.
func isUnusedParameterCandidate(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.FuncDef:
		return n.Body != nil && !n.AutoSynth
	case *ast.Lambda:
		return n.Body != nil
	case *ast.InterfaceMethod:
		return n.Body != nil
	default:
		return false
	}
}

// unfinishedBody reports whether the function or lambda node declares has a
// `todo` anywhere in its body.
func unfinishedBody(fa *FileAnalysis, node ast.Node) bool {
	switch n := node.(type) {
	case *ast.FuncDef:
		return n.Body != nil && containsTodo(fa, n.Body)
	case *ast.Lambda:
		return n.Body != nil && containsTodo(fa, n.Body)
	case *ast.InterfaceMethod:
		return n.Body != nil && containsTodo(fa, n.Body)
	}
	return false
}

// markComparisonsWrittenAsPatterns counts as read the names in an `if n = 1`
// written for `if n == 1`: the name the pattern binds and the one of the same
// name it hides. The checker rejects the pattern, since a bare name always
// matches, and its help says to write `==` (reportIrrefutableIfPattern); an
// unread name reported beside it would come first and point the other way.
func markComparisonsWrittenAsPatterns(fa *FileAnalysis, nodes []ast.Node, used map[*Symbol]bool) {
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			f, ok := n.(*ast.If)
			if !ok {
				return true
			}
			p, ok := f.CondPattern.(*ast.IdentPattern)
			if !ok {
				return true
			}
			own := Pos{Line: p.Line, Col: p.Col}
			if sym := fa.Definitions[own]; sym != nil {
				used[sym] = true
			}
			for scope := fa.ScopeAt(Pos{Line: f.Line, Col: f.Col}); scope != nil; scope = scope.Parent {
				if sym := scope.LookupLocal(p.Name); sym != nil && sym.Pos != own {
					used[sym] = true
					break
				}
			}
			return true
		})
	}
}
