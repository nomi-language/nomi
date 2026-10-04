package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A bare brace at a position that expects a nominal struct builds that
// struct (`a: Address = {street: ...}`), and under a spread it patches the
// field it stands at (`{..p, address: {city: ...}}`). Until a field is
// written it does not parse as a struct literal: `{‸}` and `{st‸}` reparse
// as a block holding one name. bareBraceHop finds such a block from the
// tree; promoteBareBrace asks the position's expected type whether it is a
// struct literal after all.

// bareBraceHop returns the hop of the block the sentinel's name opens, when
// that block stands where a value goes, or 0. The name must be the block's
// first statement, and either its only one or on the brace's line: an
// unclosed `{` swallows the lines after it, but a name on a later line of a
// longer block is a statement of that block.
func bareBraceHop(h *sentinelHit) int {
	if h == nil {
		return 0
	}
	ident, _ := h.at(0)
	stmt, stmtField := h.at(1)
	block, blockField := h.at(2)
	parent, _ := h.at(3)
	id, ok1 := ident.(*ast.Ident)
	es, ok2 := stmt.(*ast.ExprStmt)
	b, ok3 := block.(*ast.Block)
	if !ok1 || !ok2 || !ok3 || stmtField != "Stmts" || len(b.Stmts) == 0 || any(b.Stmts[0]) != any(es) {
		return 0
	}
	if len(b.Stmts) > 1 && id.Line != b.Line {
		return 0
	}
	switch parent.(type) {
	case *ast.ExprStmt, *ast.GroupedExpr:
		if blockField != "Expr" {
			return 0
		}
	case *ast.Binding, *ast.StructFieldVal, *ast.NamedArg, *ast.Return, *ast.With:
		if blockField != "Value" {
			return 0
		}
	case *ast.Call:
		if blockField != "Args" {
			return 0
		}
	case *ast.ListLit:
		if blockField != "Items" {
			return 0
		}
	case *ast.CaseBranch:
		if blockField != "Body" {
			return 0
		}
	default:
		// A function's, a lambda's or an if's own block, and anything
		// else that is a block by its syntax.
		return 0
	}
	return 2
}

// promoteBareBrace makes a bare brace at a struct-typed position a struct
// literal's field position. Any other expected type (none, a block's value,
// a map, an anonymous struct, an interface) leaves the position as it is.
func (r *completionRequest) promoteBareBrace() {
	if r.ctx.kind != ctxStatement {
		return
	}
	hop := bareBraceHop(r.ctx.hit)
	if hop == 0 {
		return
	}
	t := r.outerExpected(r.ctx.hit, hop)
	if _, ok := analysis.ResolveTypeVar(t).(*analysis.StructType); !ok {
		return
	}
	r.ctx.kind = ctxStructField
	r.braceType = t
	r.braceHop = hop
}

// underPatch reports whether the literal or bare brace at hop is a patch:
// it carries a spread, or it is a bare brace at a field of a patch, where
// the base supplies every field it leaves out.
func underPatch(h *sentinelHit, hop int) bool {
	for {
		node, field := h.at(hop)
		if lit, ok := node.(*ast.StructLit); ok {
			if lit.Spread != nil {
				return true
			}
			if lit.TypeName != nil {
				return false
			}
		}
		fv, _ := h.at(hop + 1)
		if _, ok := fv.(*ast.StructFieldVal); !ok || field != "Value" {
			return false
		}
		hop += 2
	}
}
