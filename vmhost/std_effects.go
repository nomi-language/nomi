package vmhost

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

// DeclAt is a declaration's position in its file: the line and column of
// its name, as the checker records it.
type DeclAt struct{ Line, Col int }

// StdEffects is, for a std module LoadEmbeddedStdlib loaded, the name a
// call to each function the module declares, or a read of each `once` it
// declares, is an effect by, as EffectCalls names a program's call to it
// and an effects list holds it (`std::fs`), keyed by the declaration's
// position; a function or `once` with no effect is not in names. answered
// is each declaration the walk answers for, with an effect or none.
//
// A `host fn` is answered by its crossing: it is an effect call when the
// crossing is one (internal/stdlibbindings, "Effects and World"), an
// impl's function among them. A function written in Nomi, and a `once`,
// whose initializer is a body like a function's, is answered when the
// walk has its body, and has an effect when the effect check's walk,
// started from that body, reaches an effect crossing without leaving std
// (vm.StdEffectCrossings), so it over-approximates as the check does. A
// `once`'s effect is its initializer's, which runs the first time the
// value is read (`io.stdout` is `std::io`).
// A generic function is walked from each instance the module's lowering
// holds, which are those its own tests and examples use, and from its
// abstract body, which reaches what its body does whatever its type
// arguments are: for a shared module irbuild.StdAbstractBodies, and for
// one lowered with each program (programStdModules) the program's own
// (irbuild.Result.Abstract). An effect reached only through a function
// the call passes is the caller's, as a higher-order function's is. A
// function every call of which the builder lowers to an operation of the
// machine (irbuild.StdLowersToMachine: Iter.map, Set.insert,
// Range.step_by) is answered by the operation, with no effect: its body,
// which reads a representation the machine does not have, never runs, and
// the operation computes and calls only the functions it is passed. A
// generic function with none of these is not answered.
func (p *Program) StdEffects() (names map[DeclAt]string, answered map[DeclAt]bool) {
	out := map[DeclAt]string{}
	answered = map[DeclAt]bool{}
	file := p.prog.Entry().Path
	// A module with no tests lowers nothing of its own, and its functions
	// are the shared lowering's module of its file.
	entry := p.entry
	var roots []*ir.Func
	abstract, _ := p.res.Abstract()
	mods := append(append(slices.Clip(p.res.IRModules()), irbuild.StdAbstractBodies(p.stdModule)...), abstract...)
	for _, m := range mods {
		if entry == nil && m.Name() == file {
			entry = m
		}
		bodies := slices.Clone(m.Funcs())
		for _, c := range m.Cells() {
			if init := c.Initializer(); init != nil {
				bodies = append(bodies, init)
			}
		}
		for _, f := range bodies {
			if f.Pos().File() == file {
				roots = append(roots, f)
				answered[DeclAt{f.Pos().Line(), f.Pos().Col()}] = true
			}
		}
	}
	for f, o := range vm.StdEffectCrossings(entry, mods, roots, compilerCrossing) {
		out[DeclAt{f.Pos().Line(), f.Pos().Col()}] = effects.Name(o.Std, o.Crossing)
	}
	std := strings.ReplaceAll(p.stdModule, "/", "::")
	fa := p.prog.Entry().FA
	for decl, id := range fa.StdFuncs() {
		// A function every call of which the builder lowers to an operation
		// of the machine is answered by the operation, which runs where its
		// body never does: it computes and calls what it is passed.
		if fd, ok := decl.(*ast.FuncDef); ok && irbuild.StdLowersToMachine(id) {
			at := DeclAt{fd.Line, fd.Col}
			answered[at] = true
			delete(out, at)
		}
	}
	for _, node := range p.prog.Entry().Nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			ef, ok := n.(*ast.ExternFunc)
			if !ok || fa == nil {
				return true
			}
			id := fa.StdFuncOf(ef)
			if id.IsZero() {
				return false
			}
			answered[DeclAt{ef.Line, ef.Col}] = true
			qualified := id.Name
			if id.Owner != "" {
				qualified = id.Owner + "." + id.Name
			}
			if key := vm.EffectHostOf(std, qualified, compilerCrossing); key != "" {
				out[DeclAt{ef.Line, ef.Col}] = effects.Name(std, key)
			}
			return false
		})
	}
	return out, answered
}

// compilerCrossing answers whether a crossing neither an intrinsic nor a
// std row answers is one an effects list names (vm.ReachedEffects'
// other): std/compiler's hosts answer for themselves, and any other key is
// a project's or an embedder's, which a list names and no std module
// declares.
func compilerCrossing(key string) (listed bool, std string) {
	if listed, known := compilerhosts.Effect(key); known {
		return listed, "compiler"
	}
	return true, ""
}
