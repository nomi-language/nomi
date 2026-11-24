package irbuild

import (
	"slices"
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ir"
)

// StdAbstractBodies is the abstract body (abstract.go) of each generic
// function the shared std module `module` declares (`iter`, `fs/host`):
// the instance a program's call would build (stdInstances), at the
// function's own type parameters and its impl block's, each left open and
// held as its bounds. A monomorphic function with a body the shared
// lowering builds none of has one too, its instance with no arguments: one
// whose signature no gen shares (stdOutsideSubsetWhy, a function over a
// namespaced type), and one a program builds for itself
// (stdUnretainedMonoBody, std/signatures' `PrivateKey.from_der`, which
// reaches a generic template). So every function of the module with a body
// is answered whether or not its tests reach it. Each is in a module of its
// own, marked abstract, as a program's abstract bodies are, and so are the
// instances they reach.
// Nothing runs one, and no program links them, so the gen that asks for
// them builds a def of each std interface a type parameter is held as, as a
// program's gen does (stdProgBuilds): `T: Hashable` in std/lists is held
// as a Hashable value, and `Hashable.hash(x)` dispatches on it.
//
// The body is what the function does whatever its type arguments are: a
// call through a type parameter's interface function is a call through
// the bound, which the effects walk answers by every implementation, and
// a call of a function the body was given calls a value, so what it does
// belongs to whoever made the value. The stdlib reference marks a generic
// function no lowering of its module's tests instantiates from it
// (vmhost.Program.StdEffects).
//
// A function whose abstract body declines is left out. One whose calls the
// builder lowers to an operation of the machine declines here, as its body
// reads a representation the machine does not have; it is answered by the
// operation instead (StdLowersToMachine). A module in programStdModules is
// lowered with each program rather than shared, so its generic functions'
// abstract bodies are the program's (Result.Abstract), and this answers
// none.
func StdAbstractBodies(module string) []*ir.Module {
	ensureStdModule(module)
	if isProgramStd(module) {
		return nil
	}
	lib := stdlibLowering()
	v := lib.views[module]
	if v == nil {
		return nil
	}
	// One table, so two templates that reach one instance share it. Each
	// template's closure commits whole or not at all, and its declines reach
	// no census (attemptClosure). The module's own gen asks for every
	// instance, as a program's gen asks for those its calls need, so the
	// instance gens intern their structural kinds in one set of tables.
	dc := newDeclineCensus(DeclineHooks{})
	dc.abstract, dc.open = true, map[*analysis.TypeParam_]kind{}
	insts := newStdInstances(lib, dc)
	var templates []*stdFunc
	for _, f := range lib.byID {
		// In an abstract lowering a monomorphic body the shared lowering
		// builds none of is an unretained one (stdUnretainedMonoBody), its
		// signature shared or not.
		if f.module == module && (stdGenericTemplateUsable(f) || insts.stdUnretainedMonoBody(f)) {
			templates = append(templates, f)
		}
	}
	sort.Slice(templates, func(i, j int) bool { return templates[i].key < templates[j].key })
	caller := v.gen(lib)
	caller.stdInsts, caller.dc = insts, dc
	for _, f := range templates {
		tps, ib := insts.stdTemplateParams(f)
		params := caller.declTypeParamsNamed(f.decl, tps)
		if ib != nil {
			// The block's type parameters a function's own type does not
			// name, a derived one's among them: the receiver's, as the
			// checker typed the block's receiver.
			for name, p := range caller.typeExprParams(ib.Receiver) {
				if params[name] == nil {
					params[name] = p
				}
			}
		}
		args := make([]kind, len(tps))
		types := make([]analysis.Type, len(tps))
		ok := true
		for i, name := range tps {
			p := params[name]
			if p == nil {
				ok = false
				break
			}
			if ib != nil {
				p.Bounds = append(slices.Clip(p.Bounds), caller.whereBounds(ib.WhereClauses, name)...)
			}
			// kindInvalid: reports — the template is left out, unanswered.
			if args[i] = caller.abstractHeldKind(p); args[i] == kindInvalid {
				ok = false
				break
			}
			types[i] = p
			dc.open[p] = args[i]
		}
		if ok {
			_, _ = insts.instantiateTyped(f, tps, args, types, caller)
		}
	}
	return insts.modules()
}
