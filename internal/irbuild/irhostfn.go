package irbuild

import (
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A user `host fn` bound to a Go symbol, as the VM calls it.
//
//	gopkg "durationffiapp" as ffi
//	pub fn timeout(): Duration go ffi.Timeout
//
// Every call site reaches the declaration through the ordinary direct,
// sibling and bare-sibling routes, under the declaration symbol of the shell
// hostShellOf interns. irHostFnRetain builds an `ir.Func` under that symbol
// whose body is one crossing into Go. So a retained caller links against it by
// the identity it already names, and no call site learns the callee is
// foreign.
//
// The crossing names the key the binding is registered under, which is what
// the VM's host table is keyed on: the declaring file's module name and the
// declaration's name, or the bare name when the declaring file is the entry.
// That is ffirun's externDeclKey and entryScopedKey. ffirun's wrapper
// generates an adapter per binding (internal/ffirun/adapters.go) that does
// the conversion.
//
// The VM can run it only when its host supplies the binding, and an in-process
// host has it only if it linked the project's Go package. `nomi run` links it
// in ffirun's wrapper binary; a test binary inside this module does not.
//
// A callback parameter is built: the adapter turns the VM function value into
// a Go func that calls back through the machine's Invoker. A function RESULT
// is not, because a Go func has no VM function value to become.

// irHostBinding is the token for a Go-bound declaration's extern key, distinct
// from the shell's own declaration symbol.
type irHostBinding struct{ ef *ast.ExternFunc }

// irHostFnHasVMBody reports whether irHostFnRetain builds a VM body for a
// Go-bound declaration of this signature: one that does not return a
// function.
func irHostFnHasVMBody(_ []kind, result kind) bool {
	return result.tag != tagFunc
}

// irHostFnRetain builds the VM body of a Go-bound `host fn` whose native
// wrapper hostFnDecl just emitted under sig.
func (g *gen) irHostFnRetain(ef *ast.ExternFunc, sig *fnSig) {
	// VM-only: no Go names what the build resolves.
	if g.stdModule != "" {
		return
	}
	// Each refusal below is recorded as the declaration's decline.
	decline := func(reason string) {
		g.irDeclineOpen(ef.Name)
		irDeclineNote(reason)
	}
	// A host-table declaration's adapter answers the VM's own values, so it
	// can return a function value it was handed earlier: a REPL session
	// reads a function an earlier input stored (vmhost.Session).
	if !irHostFnHasVMBody(sig.params, sig.result) && !hostTableFn(ef) {
		decline("a `host fn` returning a function, which a Go func cannot become")
		return
	}
	shell := hostShellOf(ef)
	params := make([]kind, len(sig.params))
	for i, k := range sig.params {
		params[i] = k
	}
	isig := irFuncSig{result: sig.result, decl: shell, name: ef.Name, origin: irFromModule}
	sh := g.irFuncShellWithCallee(shell, isig, params, g.irCalleeSym(shell, ef.Name))
	if sh == nil {
		decline("no function shell")
		return
	}
	if why := g.irHostCrossingBody(ef, sh, sig.result, g.irHostBindingKey(ef.Name)); why != "" {
		decline(why)
	}
}

// irHostCrossingBody fills sh, the VM function of a Go-bound declaration,
// with its one crossing into Go under key, and adds it to the module. It
// answers the reason when the graph does not lint.
func (g *gen) irHostCrossingBody(ef *ast.ExternFunc, sh *irFuncShell, result kind, key string) string {
	args := make([]ir.Temp, 0, len(sh.fn.Params()))
	for _, p := range sh.fn.Params() {
		args = append(args, p.Temp)
	}
	at := g.irNodePos(hostShellOf(ef))
	binding := g.irCalleeSym(irHostBinding{ef}, key)
	call := ir.NewHostCall(at, sh.fn.NewTemp(), ir.OrdinaryCall, binding, args...)
	g.irTypeTemp(sh.fn, call.Dst(), result)
	sh.entry.Append(call)
	sh.entry.Append(ir.NewCopy(at, sh.result, call.Dst()))
	sh.entry.SetTerm(ir.NewReturn(at, sh.result))
	if err := ir.Lint(sh.fn); err != nil {
		return "a host crossing whose graph does not lint: " + err.Error()
	}
	g.irModule().AddFunc(sh.fn)
	if err := ir.LintModuleAdded(g.irModule()); err != nil {
		panic("irbuild: host fn body: " + err.Error())
	}
	g.irHostKeys = append(g.irHostKeys, key)
	return ""
}

// irHostBindingKey is the extern key a file-level Go-bound declaration named
// name is registered under: bare in the entry unit, module-qualified by the
// declaring file's name otherwise.
func (g *gen) irHostBindingKey(name string) string {
	if g.fileUnit == 0 {
		return name
	}
	return strings.TrimSuffix(filepath.Base(g.nomiPath), ".nomi") + "." + name
}
