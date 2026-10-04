package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

func (g *gen) bootDecl() *ast.FuncDef {
	if g.reg == nil || len(g.reg.gens) == 0 {
		return declaresBoot(g.nodes)
	}
	entry := g.reg.gens[0]
	if entry == nil {
		return nil
	}
	return declaresBoot(entry.nodes)
}

func declaresBoot(nodes []ast.Node) *ast.FuncDef {
	for _, n := range nodes {
		fd, isFunc := n.(*ast.FuncDef)
		if isFunc && fd.Name == "boot" && !fd.ImplFunction && len(fd.Params) <= 1 {
			return fd
		}
	}
	return nil
}

type scopedFieldToken struct {
	name string
	kind kind
}

// appRead answers the checked application-field read n, `MyApp.logger`,
// written as a read or as a `with` override's target.
func (g *gen) appRead(n ast.Node) (analysis.AppRead, bool) {
	access, ok := n.(*ast.FieldAccess)
	if g.fa == nil || !ok || access == nil {
		return analysis.AppRead{}, false
	}
	if read, ok := g.fa.AppReads[access]; ok {
		return read, true
	}
	if access.Field != nil {
		if read, ok := g.fa.AppReadsByPos[analysis.Pos{Line: access.Field.Line, Col: access.Field.Col}]; ok {
			return read, true
		}
	}
	return analysis.AppRead{}, false
}

// appFieldKind is the kind of an application field's value. The Context
// field is the prelude Context, whatever spelling the checker resolved.
func (g *gen) appFieldKind(read analysis.AppRead) kind {
	if read.Field.Type == nil {
		return kindInvalid
	}
	if analysis.SameScopedType(read.Field.Type, g.fa.ContextType) {
		return stdHostOriginKind("std/context", "Context")
	}
	return g.project(read.Field.Type)
}

func (g *gen) stageProgramApp(fd *ast.FuncDef) {
	g.pushScope()
	g.popScope()
	g.irBootRetain(fd)
}

// isProgramBoot reports whether fd is an entry boot this unit declares: the
// top-level `fn boot` of a file that defines `fn main`. The entry unit's is
// the program's boot; any unit's may be the boot a `tests` group's `boot`
// line starts. Either is lowered as a boot rather than as a function.
func (g *gen) isProgramBoot(fd *ast.FuncDef) bool {
	return fd != nil && declaresBoot(g.nodes) == fd && declaresMain(g.nodes)
}

// declaresMain reports whether nodes declare a top-level `fn main`.
func declaresMain(nodes []ast.Node) bool {
	for _, n := range nodes {
		if fd, isFunc := n.(*ast.FuncDef); isFunc && fd.Name == "main" && !fd.ImplFunction {
			return true
		}
	}
	return false
}
