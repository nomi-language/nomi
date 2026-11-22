package irbuild

import (
	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
)

// A user file's `host fn`, as the VM calls it.
//
// Every call site reaches the declaration through the ordinary direct,
// sibling and bare-sibling routes, under the declaration symbol of the shell
// hostShellOf interns. irHostFnRetain builds an `ir.Func` under that symbol
// whose body is one crossing into the host. So a retained caller links
// against it by the identity it already names, and no call site learns the
// callee is the host's.
//
// The crossing names the key the host registers the function under
// (irHostBindingKey), which is what the VM's host table is keyed on. The
// table's function takes and answers the VM's own values, so it may take a
// function value and call it back through the machine, and may answer a
// function value it was handed earlier: a REPL session reads a function an
// earlier input stored (vmhost.Session).

// irHostBinding is the token for a `host fn`'s extern key, distinct from the
// shell's own declaration symbol.
type irHostBinding struct{ ef *ast.ExternFunc }

// irHostFnRetain builds the VM body of a user file's `host fn` that
// hostFnDecl declared under sig.
func (g *gen) irHostFnRetain(ef *ast.ExternFunc, sig *fnSig) {
	// A std module's `host fn` gets no body here.
	if g.stdModule != "" {
		return
	}
	// Each refusal below is recorded as the declaration's decline.
	decline := func(reason string) {
		g.irDeclineOpen(ef.Name)
		g.dc.note(reason)
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
	if why := g.irHostCrossingBody(ef, sh, sig.result, g.irHostBindingKey(ef)); why != "" {
		decline(why)
		return
	}
	// A defaulted parameter is filled in Nomi before the crossing, as a
	// `fn`'s is: by the caller in this file (callDefaults), and through
	// the accessors built here when another file calls a `pub` one
	// (siblingdefault.go).
	g.irFileSlotDefaults(shell, sig)
}

// irHostCrossingBody fills sh, the VM function of a `host fn`, with its one
// crossing into the host under key, and adds it to the module. It answers
// the reason when the graph does not lint.
func (g *gen) irHostCrossingBody(ef *ast.ExternFunc, sh *irFuncShell, result kind, key string) string {
	args := make([]ir.Temp, 0, len(sh.fn.Params()))
	for _, p := range sh.fn.Params() {
		args = append(args, p.Temp)
	}
	at := g.irNodePos(hostShellOf(ef))
	// A project's crossing is named apart from every std row's
	// (hostadapt.ProjectName): its key may equal one.
	binding := g.irCalleeSym(irHostBinding{ef}, hostadapt.ProjectName(key))
	call := ir.NewHostCall(at, sh.fn.NewTemp(), ir.OrdinaryCall, binding, args...)
	g.irTypeTemp(sh.fn, call.Dst(), result)
	sh.entry.Append(call)
	sh.entry.Append(ir.NewCopy(at, sh.result, call.Dst()))
	sh.entry.SetTerm(ir.NewReturn(at, sh.result))
	g.irRequireLint(sh.fn, "a host crossing")
	g.irModule().AddFunc(sh.fn)
	if err := g.irLintAdded(); err != nil {
		panic("irbuild: host fn body: " + err.Error())
	}
	g.irHostKeys = append(g.irHostKeys, key)
	return ""
}

// irHostBindingKey is the extern key a file-level `host fn` is registered
// under, the key an embedder's table names it by (vmhost.WithHosts):
// frontend.HostKey of the declaring file's module key, which the checker
// records as the file's Origin, and bare in the entry unit.
func (g *gen) irHostBindingKey(ef *ast.ExternFunc) string {
	if g.fileUnit == 0 {
		return ef.Name
	}
	return frontend.HostKey(g.fa.Origin, ef.Name)
}
