package irbuild

import (
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// A user file's `host fn` and `host type` declarations: what an embedder's
// host table answers (vmhost.WithHosts).
//
//	host type Counter
//	host fn counter_new() -> Counter
//	host fn counter_add(c: Counter, n: Int) -> Counter
//
// The builder does not convert anything at this boundary. A `host fn`
// becomes an ordinary function whose VM body is one crossing under the key
// the host registers it by (irHostFnRetain), and the table's function takes
// and answers the VM's own values. A `host type` is a leaf def the VM holds
// as a handle.
//
// hostShellOf normalizes such an `*ast.ExternFunc` to an `*ast.FuncDef`
// shell, INTERNED on the extern node, so every pass that resolves the
// declaration holds one pointer and a call to it lowers like a call to any
// other function in its module.

// hostTableFn reports whether ef is a plain `host fn` the builder gives a VM
// body when a user file declares it: one with no type parameters and no
// `where` clause, answered at run time by whatever host table the program is
// loaded with. Stdlib `host fn`s are the language's own and never take this
// path.
func hostTableFn(ef *ast.ExternFunc) bool {
	return ef != nil && len(ef.TypeParams) == 0 && len(ef.WhereClauses) == 0
}

// --- shells ----------------------------------------------------------------

var (
	hostShellMu    sync.Mutex
	hostShellByExt = map[*ast.ExternFunc]*ast.FuncDef{}
	hostExtByShell = map[*ast.FuncDef]*ast.ExternFunc{}
)

// hostShellOf is the `*ast.FuncDef` every pass sees in place of a user
// file's `host fn`, interned on the extern node.
//
// The shell carries no Body, which is what every "is this lowerable?" clause in
// the package keys on, so each of those clauses asks hostExtOf before refusing.
func hostShellOf(ef *ast.ExternFunc) *ast.FuncDef {
	hostShellMu.Lock()
	defer hostShellMu.Unlock()
	if fd, ok := hostShellByExt[ef]; ok {
		return fd
	}
	fd := &ast.FuncDef{
		Name:           ef.Name,
		Visibility:     ef.Visibility,
		Params:         ef.Params,
		ReturnTypeExpr: ef.ReturnTypeExpr,
		Line:           ef.Line,
		Col:            ef.Col,
	}
	hostShellByExt[ef] = fd
	hostExtByShell[fd] = ef
	return fd
}

// hostExtOf recovers the `host fn` a shell stands for.
func hostExtOf(fd *ast.FuncDef) (*ast.ExternFunc, bool) {
	if fd == nil {
		return nil, false
	}
	hostShellMu.Lock()
	defer hostShellMu.Unlock()
	ef, ok := hostExtByShell[fd]
	return ef, ok
}

// --- host types ------------------------------------------------------------

// collectHostTypes builds the defs of a user file's `host type` declarations.
//
// Called from declareTypes, ahead of the type table, because a `host fn`
// signature may name a host type and a struct field may hold one.
func (g *gen) collectHostTypes() {
	if g.stdModule != "" {
		return
	}
	for _, n := range g.nodes {
		if et, isType := n.(*ast.ExternType); isType && hostTableType(et) {
			if g.hostTypes == nil {
				g.hostTypes = map[string]*typeDef{}
			}
			g.hostTypes[et.Name] = hostTableTypeDef(g.nomiPath, et)
		}
	}
}

// hostTypeDefs interns one *typeDef per declared host type, PROCESS-WIDE.
//
// Shared for opaque.go's reason, restated because it is a precondition rather
// than a preference: a `host fn`'s parameter and result kinds are built in the
// DECLARING gen and compared BY POINTER against a call site's kinds in a
// different gen, so a per-gen def would make `util.counter_add(c, 1)`
// mismatch against the very declaration it names. A leaf def has no
// components, so it holds nothing of one gen's.
var (
	hostTypeMu   sync.Mutex
	hostTypeDefs = map[string]*typeDef{}
)

// hostTableType reports whether et is a plain `host type` in a user file:
// nothing inside it. Its values are opaque handles the program's host-table
// functions (hostTableFn) create and read, so the VM holds whatever Go value
// the embedder's function answered.
func hostTableType(et *ast.ExternType) bool {
	return et != nil && len(et.TypeParams) == 0 && len(et.WhereClauses) == 0 &&
		len(et.Decorators) == 0
}

// hostTableTypeDef is the def for a plain `host type`, interned process-wide
// by the declaring file and name so a sibling file's call site and the
// declaring file's `host fn` hold one kind.
//
// The shape is stdgenhost.go's `Sender<T>`: `isDistinct` with
// `inner: kindInvalid` (the LEAF shape typeDef.inner's own comment names),
// plus `rtOpaque`, because a host handle has CONTENTS this builder may not look
// inside, and `rtDeclared`, which marks the def as process-wide: it is
// package-neutral, and a file that imports the type holds this same def.
//
// `rtOpaque` is the load-bearing one (see stdhost.go): without it, four arms
// written for a zero-sized MARKER answer for a value with contents, so `a == b`
// on two handles answers true, the hash is `rt.HashUnit`, the Debug rendering
// is the bare type name, and `irRetainedMarker` says true, so an enum payload
// of one gets no slot. A host handle is an
// opaque descriptor with no structural equality, no hash and no Display in
// the language, so declining all three is the right answer and not merely
// the safe one.
func hostTableTypeDef(file string, et *ast.ExternType) *typeDef {
	key := "host table " + file + " " + et.Name
	hostTypeMu.Lock()
	defer hostTypeMu.Unlock()
	if d, ok := hostTypeDefs[key]; ok {
		return d
	}
	d := &typeDef{nomi: et.Name, decl: et, line: et.Line, isDistinct: true, inner: kindInvalid, rtOpaque: true, rtDeclared: true, lowerable: true}
	hostTypeDefs[key] = d
	return d
}

// hostDefHeld reports whether d is a def in the process-wide host type table.
//
// Identity on the PROCESS-WIDE table by pointer, never on the rendered name:
// a user's own type spelled the same way produces a different def and
// answers false here.
func hostDefHeld(d *typeDef) bool {
	hostTypeMu.Lock()
	defer hostTypeMu.Unlock()
	for _, held := range hostTypeDefs {
		if held == d {
			return true
		}
	}
	return false
}

// --- declarations ----------------------------------------------------------

// hostFnDecl declares one `host fn` and retains its body: a crossing under the
// key its host registers it by.
//
// The signature is the NOMI one, so every call site in the package (local,
// module-qualified, or through a selective import) reaches it through the
// path it already uses.
func (g *gen) hostFnDecl(ef *ast.ExternFunc) {
	g.at(ef.Line)
	if g.stdModule != "" || !hostTableFn(ef) {
		// A std `host fn`, or a shape this family does not claim: a generic
		// `host fn`. Each is refused under the declaration's own key rather
		// than silently dropped.
		g.hostFnReject("host fn declaration", ef.Name, ef)
		return
	}
	sig := g.funcs[ef.Name]
	if sig == nil || sig.decl != hostShellOf(ef) {
		sig = g.hostSignature(ef)
	}
	if !sig.lowerable {
		// The reason is on the signature, recorded where it was decided, so
		// this and hostSignature cannot disagree about which of them judged.
		g.hostFnReject(sig.genericWhy, ef.Name, ef)
		return
	}
	g.irHostFnRetain(ef, sig)
}

// hostFnReject refuses a `host fn` declaration and records the refusal as its
// decline, so a VM that finds it unretained names this reason rather than
// "no decline reason recorded".
func (g *gen) hostFnReject(construct, detail string, n ast.Node) {
	g.reject(construct, detail, n)
	ef, _ := n.(*ast.ExternFunc)
	if ef == nil {
		return
	}
	reason := construct
	if detail != "" && detail != ef.Name {
		reason += ": " + detail
	}
	g.irDeclineOpen(ef.Name)
	g.dc.note(reason)
}

// hostSignature is a user file's `host fn` as its call sites see it.
//
// Built here rather than by signature() because the shell has no body, and
// EVERY lowerability clause in signature() keys on that. The clauses this one
// keeps are the ones that are still true of a host declaration: an
// unrepresentable parameter or result type. A defaulted parameter is not
// one: its default is Nomi code the caller lowers before the crossing, as
// it does a `fn`'s (callDefaults, siblingdefault.go).
func (g *gen) hostSignature(ef *ast.ExternFunc) *fnSig {
	shell := hostShellOf(ef)
	sig := &fnSig{decl: shell, lowerable: true}
	for _, p := range ef.Params {
		k := g.paramKind(p)
		// kindInvalid: reports — records lowerable=false; hostFnDecl rejects under the type's own reason.
		if k == kindInvalid {
			sig.lowerable = false
			sig.genericWhy = "host fn declaration"
		}
		sig.params = append(sig.params, k)
	}
	sig.result = kindUnit
	if ef.ReturnTypeExpr != nil {
		sig.result = g.typeOf(ef.ReturnTypeExpr)
		// kindInvalid: reports — records lowerable=false; hostFnDecl rejects under the type's own reason.
		if sig.result == kindInvalid {
			sig.lowerable = false
			sig.genericWhy = "host fn declaration"
		}
	}
	return sig
}

// hostTypeDecl is the `host type` declaration itself. A plain one in a user
// file is an embedder's handle, whose def collectHostTypes built, and
// lowers to nothing. Any other is refused.
func (g *gen) hostTypeDecl(et *ast.ExternType) {
	g.at(et.Line)
	if g.stdModule == "" && hostTableType(et) {
		return
	}
	g.rejectWhole("host type declaration", et.Name, et)
}

// hostHandleDef reports whether d is a USER host handle: a def this file's
// process-wide table built from a `host type` declaration.
//
// Identity on the TABLE by pointer, never on the rendered name or on
// `rtOpaque`, which every std leaf also carries. It is the predicate
// irHostHandleKind asks first, and asking `rtOpaque` instead would answer
// true for `Bytes`, `Decimal`, `Dynamic`, `Sender<T>` and every other rt
// leaf.
func hostHandleDef(d *typeDef) bool {
	return hostDefHeld(d)
}
