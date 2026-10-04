package analysis

import (
	"fmt"
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// UnusedBindingCode tags diagnostics for local bindings and parameters that are never read.
const UnusedBindingCode = "unused-binding"

// CheckUnusedBindings reports every local binding or body parameter whose symbol is never
// referenced after its definition. It intentionally runs at the same point as
// CheckUnusedImports: after the builder has walked bodies and before CheckTypes
// replaces reference entries with call-site proxy symbols.
func CheckUnusedBindings(fa *FileAnalysis) []TypeError {
	if fa == nil || len(fa.Definitions) == 0 {
		return nil
	}

	used := make(map[*Symbol]bool)
	for _, ref := range fa.References {
		for s := ref; s != nil; s = s.Resolved {
			used[s] = true
		}
	}

	var errs []TypeError
	for _, sym := range fa.Definitions {
		if sym == nil || ast.IsDiscardName(sym.Name) {
			continue
		}
		kind := "binding"
		switch sym.Kind {
		case SymbolBinding:
			if !isUnusedBindingCandidate(sym.Node) {
				continue
			}
		case SymbolParam:
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
		if used[sym] {
			continue
		}
		if kind == "parameter" && unfinishedBody(sym.Node) {
			// A body with a `todo` in it is not written yet, so a parameter
			// it does not read yet is expected, not a mistake.
			continue
		}
		hint := "prefix it with '_' if the value is intentionally ignored"
		if kind == "parameter" {
			hint = "use a discard name or discard the value explicitly"
		}
		errs = append(errs, TypeError{
			Line:    sym.Pos.Line,
			Col:     sym.Pos.Col,
			Message: fmt.Sprintf("%s '%s' is never read", kind, sym.Name),
			Code:    UnusedBindingCode,
		}.WithHint(hint))
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
		*ast.MapDestructure,
		*ast.DistinctDestructure,
		*ast.PatternDestructure,
		*ast.PatternBinding:
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
func unfinishedBody(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.FuncDef:
		return n.Body != nil && containsTodo(n.Body)
	case *ast.Lambda:
		return n.Body != nil && containsTodo(n.Body)
	case *ast.InterfaceMethod:
		return n.Body != nil && containsTodo(n.Body)
	}
	return false
}
