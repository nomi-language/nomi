package irbuild

import (
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// recordedStdFunc is the std function the call t's record names
// (analysis.Callee.Std), read at the instance being built (calleeAt), or
// nil when it names none the std index holds (stdFuncByID).
func (bl *irScalarBuilder) recordedStdFunc(t *ast.Call) *stdFunc {
	return bl.g.stdFuncByID(bl.g.calleeAt(t).Std)
}

// stdSiblingShortPlan plans `Type.method(args)` written inside the std
// module that declares it, omitting trailing defaulted parameters
// (`NaiveDateTime.from_instant(i)` in std/calendar). This gen is the
// declaring module's, so each omitted default is lowered at the call as a
// module `fn` call's is (callDefaults): the written operands first, then
// the defaults, in the scope the default was written in. A caller in
// another module goes through the module's default accessors instead
// (stdShortCall), which this module builds from the same defaults.
func (bl *irScalarBuilder) stdSiblingShortPlan(t *ast.Call, args irQualArgs, f *stdFunc) *irQualPlan {
	if f.why != "" || f.decl == nil || len(f.decl.Params) != len(f.params) || namedArgNode(t.Args) != nil {
		return nil
	}
	full, ok := bl.filledOperands(t, args, f.params, f.decl.Params, defaultHome{})
	if !ok {
		return nil
	}
	// The plan checks a call as wide as the declaration: each written
	// operand at its slot, and each defaulted slot's default expression.
	plan, planned := bl.g.callPlan(t, len(f.params))
	if !planned {
		return nil
	}
	wide := &ast.Call{Func: t.Func, Args: make([]ast.Node, len(f.params)), Line: t.Line, Col: t.Col}
	for i, slot := range plan.slots {
		wide.Args[slot] = t.Args[i]
	}
	for _, slot := range plan.defaulted {
		wide.Args[slot] = f.decl.Params[slot].Default
	}
	p := bl.stdFuncPlan(wide, full, f)
	if p == nil {
		return nil
	}
	p.args = &full
	return p
}

// stdFuncPlan plans a call to one selected stdlib function.
func (bl *irScalarBuilder) stdFuncPlan(t *ast.Call, args irQualArgs, f *stdFunc) *irQualPlan {
	if f == nil || f.why != "" {
		return nil
	}
	view := f
	if f.canon != nil {
		f = f.canon
	}
	if !bl.qualSignature(t, args, f.params, f.result) {
		return nil
	}
	if f.rtCall != "" {
		if irScalarHost(f) {
			return &irQualPlan{token: f, name: f.key, result: f.result, host: true}
		}
		if irFrameHost(f) {
			return &irQualPlan{token: f, name: f.key, result: f.result, host: true}
		}
		if name, ok := irExternHost(f); ok {
			return &irQualPlan{token: irExternBinding(name), name: name, result: f.result, host: true, target: view.node}
		}
		if name, ok := irCompilerHost(f); ok {
			return &irQualPlan{token: irExternBinding(name), name: name, result: f.result, host: true, target: view.node}
		}
		return nil
	}
	if f.irBody == nil {
		// The callee's module is still being lowered, so only this module's
		// own table can name it. The view carries the module's namespace key,
		// which irStdSiblingSym compares with this gen's.
		sym := bl.g.irStdSiblingSym(view)
		if sym == nil {
			p := bl.stdUnretainedPlan(view)
			if p == nil {
				bl.g.noteStdBodyDeclined(f)
			}
			return p
		}
		return &irQualPlan{token: view, name: view.key, result: view.result, sym: sym}
	}
	return &irQualPlan{token: f, name: f.key, result: f.result, sym: f.irBody.Sym()}
}

// noteStdBodyDeclined notes, as the reason a program's call to f declines,
// why f's own body did not lower in its std module, when that module
// recorded one. Without it the program's error names only the call's
// shape, and the cause is in std's source.
func (g *gen) noteStdBodyDeclined(f *stdFunc) {
	if g.stdModule != "" || g.std == nil {
		return
	}
	// emitStdFunc records a declined body under its key.
	if _, why, found := strings.Cut(g.std.unlowered[f.pkg][f.key], "|"); found {
		g.dc.note("the std body of " + f.key + " did not lower: " + why)
	}
}

// stdUnretainedPlan plans a call to a monomorphic stdlib body the cache did
// not retain because it reaches a generic template (stdUnretainedMonoBody):
// the program builds it as an instance with no arguments, as a qualified
// call to it does (stdInstCallAt), whichever route selected it: a sibling's
// bare call, an interface-qualified call, or an `assert` on a std type's
// `Assertable` impl. nil when the body is not one of those or does not
// build.
func (bl *irScalarBuilder) stdUnretainedPlan(f *stdFunc) *irQualPlan {
	s := bl.g.stdInsts
	if s == nil || !s.stdUnretainedMonoBody(f) {
		return nil
	}
	inst, why := s.instantiate(f, nil, nil, bl.g)
	if inst == nil {
		bl.g.dc.note(why)
		return nil
	}
	if len(inst.view.params) != len(f.params) {
		return nil
	}
	if f.why != stdProgSigWhy {
		// An Iter or a std interface value has no kind in the index
		// (stdProgSigWhy); every other
		// signature is the one the index resolved.
		if inst.view.result != f.result {
			return nil
		}
		for i, k := range f.params {
			if inst.view.params[i] != k {
				return nil
			}
		}
	}
	return &irQualPlan{token: f, name: f.key, result: inst.view.result, sym: inst.sym}
}

// irStdSiblingSym is the declaration symbol of a same-module stdlib body that
// this module has already retained, or nil.
//
// The symbol is the one emitStdFunc's shell interns, (decl, decl.Name), in the
// module gen's own table, so the call and the cached ir.Func are one pointer.
// A callee must be in the module, or pending in the prebuild that assumes it
// builds (irPrebuildStdBodies), so a callee whose body was not retained
// declines here instead of naming a declaration the cached module would not
// hold.
func (g *gen) irStdSiblingSym(f *stdFunc) *ir.Symbol {
	if g.stdModule == "" || g.irMod == nil || f.rtCall != "" || f.decl == nil || f.pkg != g.pkg {
		return nil
	}
	sym := g.irCalleeSym(f.decl, f.decl.Name)
	if g.irMod.FuncFor(sym) == nil && !g.irStdPending[f.key] {
		return nil
	}
	return sym
}

// irScalarHost admits an RtFuncs row that does not take the caller's frame:
// the VM calls it through its generated adapter (internal/stdlibadapters).
// Most rows are rt functions; std/io's and String.normalize live in their own
// host packages.
func irScalarHost(f *stdFunc) bool {
	_, row := stdlibHostFunc(f.key)
	return row && !f.rtFrame && f.rtCall != ""
}

// irExternHost is the internal/stdlibbindings Funcs name of a host function
// in a first-party adapter package. The VM reaches it through the generated
// adapter of the same row.
func irExternHost(f *stdFunc) (string, bool) {
	if f.rtFrame || f.rtHostPkg == "" || f.rtHostPkg == rtModulePath || f.rtHostPkg == compilerHostsPath {
		return "", false
	}
	b, ok := externBindingFor(f.key)
	return b.Name, ok
}

// externBindingFor is the Funcs row a std key binds to. A method binds as
// `Type.method` and a free function keeps its module,
// `calendar.date_parse_raw`.
func externBindingFor(key string) (stdlibbindings.Binding, bool) {
	rows := stdlibExternRows()
	if _, method, found := strings.Cut(key, "."); found {
		if b, ok := rows[method]; ok {
			return b, true
		}
	}
	b, ok := rows[key]
	return b, ok
}

// irCompilerHost is the crossing name of a std/compiler host function:
// its stdlib key, `compiler.check`. These are not internal/stdlibbindings
// rows, because their answer depends on the running program's project root
// and virtual files; internal/compilerhosts answers them and nomi/vmhost binds
// its table on every machine it opens, so a machine without it reports the
// crossing as having no binding.
func irCompilerHost(f *stdFunc) (string, bool) {
	if f.rtFrame || f.rtHostPkg != compilerHostsPath {
		return "", false
	}
	return f.key, true
}

// compilerHostNames are the std/compiler keys internal/compilerhosts answers.
var compilerHostNames = sync.OnceValue(func() map[string]bool {
	names := map[string]bool{}
	for _, n := range compilerhosts.Names() {
		names[n] = true
	}
	return names
})

// irExternBinding is the symbol token of an extern crossing, which is the
// binding and not the std declaration. A token keyed on the *stdFunc would
// take whichever name the declaration was first interned under, and its
// host key (`calendar.Date.to_string`) is a name no extern table holds.
type irExternBinding string

// stdlibExternRows are internal/stdlibbindings' Funcs rows by name.
var stdlibExternRows = sync.OnceValue(func() map[string]stdlibbindings.Binding {
	rows := map[string]stdlibbindings.Binding{}
	for _, b := range stdlibbindings.Funcs() {
		rows[b.Name] = b
	}
	return rows
})
