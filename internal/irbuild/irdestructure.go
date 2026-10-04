package irbuild

// Naming a destructured value. `match` and `destructure` are one pattern
// vocabulary in two POSITIONS (see `internal/ir/destructure.go`):
//
//	navigate      ir.Proj    irproj.go
//	refute        ir.Match   irmatch.go
//	name          ir.Bind    here
//
// An arm's failure falls through; a destructuring's cannot fail, because the
// checker admits only irrefutable patterns in a parameter.

import "github.com/nomi-language/nomi/internal/ast"

// irBindLocal introduces one Nomi name for an already-lowered value, as an
// `ir.Bind` whose symbol is minted: a `Bind` IS the declaration, and two
// bindings of one name in two arms are two declarations.
func (g *gen) irBindLocal(at ast.Node, nomi string, src expr) {
	b := g.irBindNode(at, nomi, g.irHold(src), src.k)
	g.bind(b.Sym().Name(), local{k: src.k})
}
