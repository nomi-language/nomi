package irbuild

import (
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

func (bl *irScalarBuilder) stdMethodCallPlan(t *ast.Call, args irQualArgs, owner, method string) *irQualPlan {
	if _, local := bl.g.types[owner]; local && bl.g.stdModule != "" {
		// Inside a stdlib module its own types are local, which the method
		// reference resolver reads as a user shadow. Native stdlibCall picks
		// from this module's sibling index by operand kinds, so this does too.
		if !args.ok {
			return nil
		}
		return bl.stdFuncPlan(t, args, stdPick(bl.g.std.byType[owner+"."+method], args.kinds))
	}
	f, why, _, handled := bl.g.stdMethodRefTarget(owner, method)
	if handled && why == "function reference with a defaulted parameter" {
		// A call that supplies every argument needs no default, which is the
		// only thing a function reference cannot carry.
		f, why = bl.g.stdFullArityTarget(owner, method, len(t.Args)), ""
	}
	if !handled || why != "" || f == nil {
		// An overloaded spelling (`NaiveDateTime.add` over eleven operand
		// types) names no single function to reference, and a call selects
		// the one its operand kinds fit, as the operator route does.
		if _, local := bl.g.types[owner]; !local && args.ok && len(bl.g.std.byType[owner+"."+method]) > 1 {
			if pick := stdPick(bl.g.std.byType[owner+"."+method], args.kinds); pick != nil {
				return bl.stdFuncPlan(t, args, pick)
			}
		}
		return nil
	}
	return bl.stdFuncPlan(t, args, f)
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
	bl.coerceEmptyListArgs(t, args, f.params)
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
			return &irQualPlan{token: irExternBinding(name), name: name, result: f.result, host: true}
		}
		if name, ok := irCompilerHost(f); ok {
			return &irQualPlan{token: irExternBinding(name), name: name, result: f.result, host: true}
		}
		return nil
	}
	if f.irBody == nil {
		// The callee's module is still being lowered, so only this module's
		// own table can name it. The view carries the Go name and package the
		// call spells.
		sym := bl.g.irStdSiblingSym(view)
		if sym == nil {
			return nil
		}
		return &irQualPlan{token: view, name: view.key, result: view.result, sym: sym}
	}
	return &irQualPlan{token: f, name: f.key, result: f.result, sym: f.irBody.Sym()}
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
	_, row := stdlibHostFuncs[f.key]
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
// take whichever name the declaration was first interned under, and its Go
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

// stdFullArityTarget is the one lowerable stdlib function spelled
// owner.member whose declared arity is n, for a call that writes every
// argument.
func (g *gen) stdFullArityTarget(owner, member string, n int) *stdFunc {
	var only *stdFunc
	for _, f := range g.std.byType[owner+"."+member] {
		if f.lowerable() {
			if only != nil {
				return nil
			}
			only = f
		}
	}
	if only == nil || len(only.params) != n {
		return nil
	}
	return only
}

// coerceEmptyListArgs types each empty list operand (`String.join([], ", ")`)
// as the list parameter it is passed to, when every other operand already has
// its parameter's kind. The operands share args' slices, so the plan and any
// recorded row read the coerced temporaries.
func (bl *irScalarBuilder) coerceEmptyListArgs(t *ast.Call, args irQualArgs, params []kind) {
	if !args.ok || len(args.kinds) != len(params) || len(t.Args) != len(params) {
		return
	}
	empty := false
	for i, k := range args.kinds {
		switch {
		case k == params[i]:
		case k == kindEmptyList && params[i].tag == tagList:
			empty = true
		default:
			return
		}
	}
	if !empty {
		return
	}
	for i, k := range args.kinds {
		if k != kindEmptyList {
			continue
		}
		v, got, ok := bl.coerceEmpty(t.Args[i], args.temps[i], k, params[i])
		if !ok {
			return
		}
		args.temps[i], args.kinds[i] = v, got
	}
}
