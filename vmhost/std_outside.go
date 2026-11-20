package vmhost

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// DeclAt is a declaration's position in its file: the line and column of
// its name, as the checker records it.
type DeclAt struct{ Line, Col int }

// StdOutsideCalls is, for a std module LoadEmbeddedStdlib loaded, the name
// a call to each function the module declares is an outside call by, as
// OutsideCalls names a program's call to it and a capability list holds it
// (`std::fs`), keyed by the function's position. A function a call to which
// is no outside call is not in it.
//
// A `host fn` that is an outside crossing (internal/stdlibbindings,
// "Outside calls") is one. A function written in Nomi is one when the
// capability check's walk, started from that function, reaches an outside
// crossing without leaving std (vm.StdOutsideCrossings), so it
// over-approximates as the check does. A generic function is walked from
// each instance the module's lowering holds, which are those its own tests
// and examples use; one with none is not in it.
func (p *Program) StdOutsideCalls() map[DeclAt]string {
	out := map[DeclAt]string{}
	file := p.prog.Entry().Path
	// A module with no tests lowers nothing of its own, and its functions
	// are the shared lowering's module of its file.
	entry := p.entry
	var roots []*ir.Func
	for _, m := range p.res.IRModules() {
		if entry == nil && m.Name() == file {
			entry = m
		}
		for _, f := range m.Funcs() {
			if f.Pos().File() == file {
				roots = append(roots, f)
			}
		}
	}
	for f, o := range vm.StdOutsideCrossings(entry, p.res.IRModules(), roots, compilerCrossing) {
		out[DeclAt{f.Pos().Line(), f.Pos().Col()}] = capability.Name(o.Std, "", o.Crossing)
	}
	std := strings.ReplaceAll(p.stdModule, "/", "::")
	fa := p.prog.Entry().FA
	for _, node := range p.prog.Entry().Nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			ef, ok := n.(*ast.ExternFunc)
			if !ok || fa == nil {
				return true
			}
			id := fa.StdFuncOf(ef)
			if id.IsZero() || id.Impl != "" {
				return false
			}
			qualified := id.Name
			if id.Owner != "" {
				qualified = id.Owner + "." + id.Name
			}
			if key := vm.OutsideHostOf(std, qualified, compilerCrossing); key != "" {
				out[DeclAt{ef.Line, ef.Col}] = capability.Name(std, "", key)
			}
			return false
		})
	}
	return out
}

// compilerCrossing answers whether a crossing neither an intrinsic nor a
// std row answers is outside (vm.ReachedOutsideCalls' other): std/compiler's
// hosts answer for themselves, and any other key is a project's or an
// embedder's, which is outside and declared in no std module.
func compilerCrossing(key string) (isOutside bool, std string) {
	if isOutside, known := compilerhosts.Outside(key); known {
		return isOutside, "compiler"
	}
	return true, ""
}
