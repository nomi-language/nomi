package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
)

// A USER Go package named from Nomi source, and the `host fn` / `host type`
// declarations bound to symbols in it.
//
//	gopkg "example.com/app/ffi" as ffi
//	opaque type RawBox go ffi.Box
//	pub fn echo_upper(s: String): String go ffi.EchoUpper
//
// The builder does not convert anything at this boundary. A Go-bound `host
// fn` becomes an ordinary function whose VM body is one crossing under the
// binding's key (irHostFnRetain), and the adapter internal/ffirun generates
// for that key (internal/hostgen) converts the arguments and the result. A
// `host type` bound to a Go type is a leaf def the VM holds as a handle.
//
// hostShellOf normalizes a Go-bound `*ast.ExternFunc` to an `*ast.FuncDef`
// shell, INTERNED on the extern node, so every pass that resolves the
// declaration holds one pointer and a call to it lowers like a call to any
// other function in its module.

// hostBound reports whether a declaration names a Go symbol in source.
//
// `ForeignName != ""` is the whole test, and it is the ONE gate that keeps this
// family from colliding with stdhost.go's. std declares `pub host type Bytes`
// and `pub host fn` by the hundred and NONE of them carries a `go alias.Symbol`
// binding: their implementations come from a host table. So a std `host fn`
// stays refused under its own keys and a user's Go-bound one lowers here, with
// no name-keyed test anywhere and therefore nothing for a same-named user
// declaration to shadow.
func hostBoundFn(ef *ast.ExternFunc) bool {
	return ef != nil && ef.ForeignName != "" && ef.GoBody == "" && !ef.ImplFunction &&
		len(ef.TypeParams) == 0 && len(ef.WhereClauses) == 0
}

// hostTableFn reports whether ef is a plain `host fn` in a user file: no `go`
// binding and no inline body, answered at run time by whatever host table the
// program is loaded with (an embedder's, through vmhost.WithHosts). It gets
// the same VM body a Go-bound one does (irHostFnRetain): a crossing under the
// key the host registers it by. Stdlib `host fn`s are the language's own and
// never take this path.
func hostTableFn(ef *ast.ExternFunc) bool {
	return ef != nil && ef.ForeignName == "" && ef.GoBody == "" && !ef.ImplFunction &&
		len(ef.TypeParams) == 0 && len(ef.WhereClauses) == 0
}

// hostBoundImplFn reports whether ef, an item of an impl block (inherent or
// interface), is bound to a Go symbol (`impl Box<T> { pub fn origin(): Int
// go ffi.Origin }`). The Go function is the same whatever the receiver's type
// arguments are, so its VM body is one crossing under the binding's key
// (hostImplKey), built wherever the block's items are.
func hostBoundImplFn(ef *ast.ExternFunc) bool {
	return ef != nil && ef.ForeignName != "" && ef.GoBody == "" &&
		len(ef.TypeParams) == 0 && len(ef.WhereClauses) == 0
}

// implItemFunc is an impl block item as a function declaration: a written
// function, or the shell (hostShellOf) of a Go-bound one.
func implItemFunc(item ast.Node) (*ast.FuncDef, bool) {
	switch it := item.(type) {
	case *ast.FuncDef:
		return it, true
	case *ast.ExternFunc:
		if hostBoundImplFn(it) {
			return hostShellOf(it), true
		}
	}
	return nil, false
}

// hostImplKey is the key the FFI wrapper registers a Go-bound impl function
// under (internal/ffirun's implOwnerKey and externDeclKey): the receiver's
// base name, the interface's instantiation when it has type arguments, and
// the function's name. No module prefix.
func hostImplKey(ib *ast.ImplBlock, name string) string {
	base := hostImplReceiverName(ib.Receiver)
	if ib.Interface != nil {
		if inst := stdImplKey(ib.Interface); inst != "" {
			base += "." + inst
		}
	}
	return base + "." + name
}

func hostImplReceiverName(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	case *ast.QualifiedType:
		return hostImplReceiverName(t.Member)
	}
	return ""
}

// hostCallable reports whether a user file's ef is a callable host function
// the builder gives a VM body: Go-bound or host-table.
func hostCallable(ef *ast.ExternFunc) bool {
	return hostBoundFn(ef) || hostTableFn(ef)
}

func hostBoundType(et *ast.ExternType) bool {
	return et != nil && et.ForeignName != "" && et.GoBody == "" &&
		len(et.TypeParams) == 0 && len(et.WhereClauses) == 0 &&
		!et.HasBody && len(et.Items) == 0 && len(et.Decorators) == 0
}

// --- shells ----------------------------------------------------------------

var (
	hostShellMu    sync.Mutex
	hostShellByExt = map[*ast.ExternFunc]*ast.FuncDef{}
	hostExtByShell = map[*ast.FuncDef]*ast.ExternFunc{}
)

// hostShellOf is the `*ast.FuncDef` every pass sees in place of a Go-bound
// `host fn`, interned on the extern node.
//
// The shell carries no Body, which is what every "is this lowerable?" clause in
// the package keys on — so each of those clauses asks hostExtOf before refusing.
// There are exactly three (signature, fileFuncShell, typeFileFunc) and a fourth
// appearing is a Go compile error rather than a silent refusal, because the
// shell has no body to emit from.
func hostShellOf(ef *ast.ExternFunc) *ast.FuncDef {
	hostShellMu.Lock()
	defer hostShellMu.Unlock()
	if fd, ok := hostShellByExt[ef]; ok {
		return fd
	}
	fd := &ast.FuncDef{
		Name:           ef.Name,
		Public:         ef.Public,
		Params:         ef.Params,
		ReturnTypeExpr: ef.ReturnTypeExpr,
		Line:           ef.Line,
		Col:            ef.Col,
	}
	hostShellByExt[ef] = fd
	hostExtByShell[fd] = ef
	return fd
}

// hostExtOf recovers the Go-bound declaration a shell stands for.
func hostExtOf(fd *ast.FuncDef) (*ast.ExternFunc, bool) {
	if fd == nil {
		return nil, false
	}
	hostShellMu.Lock()
	defer hostShellMu.Unlock()
	ef, ok := hostExtByShell[fd]
	return ef, ok
}

// --- host packages ---------------------------------------------------------

// hostPkg is one `gopkg` declaration as the builder needs it.
type hostPkg struct {
	// importPath is the Go import path the declaration names.
	importPath string
	// alias is the Go identifier every gen spells this package under. Derived from the import path and NOT from the Nomi alias, because
	// it is baked into the process-wide key of any host type in
	// this package: two files may bind one Go package under different Nomi
	// aliases, and a def whose Go text depended on which file was read first
	// would render differently in two packages for one type.
	alias string
	// dir is the directory of the Go module providing importPath, absolute,
	// and modPath that module's path. dir is empty for a required module the
	// project's go.mod does not replace with a local directory and for the
	// Go standard library; why is set when the module could not be located
	// at all (hostModuleOf).
	dir     string
	modPath string
	why     string
}

// hostAlias is the Go identifier an import path is spelled under, everywhere.
//
// Prefixed rather than derived from the last path segment: a Go package's NAME
// need not match its directory, so an unaliased import is a guess, and the
// prefix additionally makes the token unmistakable in Go-spelled text.
func hostAlias(importPath string) string {
	var b strings.Builder
	b.WriteString("hostpkg_")
	for _, r := range importPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// hostAliasRegistry is every alias minted in this process, with the import
// path it stands for.
//
// PROCESS-WIDE for the reason the alias itself is: a host type's *typeDef is
// shared across gens and its key already spells the alias, so the set of
// aliases in play is not a per-gen fact. Growth is bounded by the number of
// distinct `gopkg` import paths the process has parsed.
var (
	hostAliasMu sync.Mutex
	// Seeded with `time`, because a Duration or Instant boundary spells
	// `stdtime.Duration`.
	hostAliasPath = map[string]string{"stdtime": "time"}
)

func noteHostAlias(importPath string) string {
	alias := hostAlias(importPath)
	hostAliasMu.Lock()
	hostAliasPath[alias] = importPath
	hostAliasMu.Unlock()
	return alias
}

// collectHostPkgs resolves this module's `gopkg` declarations and the host types
// bound through them.
//
// Called from declareTypes, ahead of the type table, because a `host fn`
// signature may name a host type and a struct field may hold one.
func (g *gen) collectHostPkgs() {
	if g.stdModule == "" {
		for _, n := range g.nodes {
			if et, isType := n.(*ast.ExternType); isType && hostTableType(et) {
				if g.hostTypes == nil {
					g.hostTypes = map[string]*typeDef{}
				}
				g.hostTypes[et.Name] = hostTableTypeDef(g.nomiPath, et)
			}
		}
	}
	for _, n := range g.nodes {
		ep, isPkg := n.(*ast.ExternPackage)
		if !isPkg {
			continue
		}
		if g.hostPkgs == nil {
			g.hostPkgs = map[string]*hostPkg{}
		}
		hp := &hostPkg{importPath: ep.ImportPath, alias: noteHostAlias(ep.ImportPath)}
		hp.dir, hp.modPath, hp.why = hostModuleOf(g.nomiPath, ep.ImportPath)
		g.hostPkgs[ep.Alias] = hp
	}
	if g.hostPkgs == nil {
		return
	}
	for _, n := range g.nodes {
		et, isType := n.(*ast.ExternType)
		if !isType || !hostBoundType(et) {
			continue
		}
		hp := g.hostPkgs[et.ForeignAlias]
		if hp == nil || hp.why != "" {
			continue
		}
		if g.hostTypes == nil {
			g.hostTypes = map[string]*typeDef{}
		}
		g.hostTypes[et.Name] = hostTypeDef(hp, et)
	}
}

// hostTypeDefs interns one *typeDef per (import path, Go type name),
// PROCESS-WIDE.
//
// Shared for opaque.go's reason, restated because it is a precondition rather
// than a preference: a `host fn`'s parameter and result kinds are built in the
// DECLARING gen and compared BY POINTER against a call site's kinds in a
// different gen, so a per-gen def would make `ffi.box_label(box)` mismatch
// against the very declaration it names. A leaf def has no components, so
// nothing about it is package-relative — its Go spelling is
// `*hostpkg_x.Box`, which reads identically from every gen.
var (
	hostTypeMu   sync.Mutex
	hostTypeDefs = map[string]*typeDef{}
)

// hostTypeDef is the def for `opaque type RawBox go ffi.Box`.
//
// The shape is stdgenhost.go's `Sender<T>`: `isDistinct` with
// `inner: kindInvalid` — the LEAF shape typeDef.inner's own comment names —
// plus `rtOpaque`, because a host handle has CONTENTS this builder may not look
// inside, and `rtDeclared`, because the Go type is declared in the user's own
// package and a second declaration would be a second Go type for one Nomi type.
//
// `rtOpaque` is the load-bearing one (see stdhost.go): without it, four arms
// written for a zero-sized MARKER answer for a value with contents, so `a == b`
// on two handles answers true, the hash is `rt.HashUnit`, the Debug rendering
// is the bare type name, and `zeroSized()` says true. A host handle
// is exactly that hazard: `*ffi.Box` is a pointer, two of them are not always
// equal, and Nomi has no syntax that reaches inside one.
//
// Declining is also the RIGHT answer here and not merely the safe one. A host
// handle is an opaque descriptor with no structural equality, no hash and no
// Display in the language, so inventing any of the three would be inventing
// semantics.
//
// The Go type is the POINTER form, matching what `internal/ffirun`'s wrapper
// registers — `rt.RegisterExternType("ffi.RawBox", (*ffi.Box)(nil))`. A handle
// is a reference to state the Go side owns; copying the struct would give Nomi
// a detached snapshot, and `BoxLabel(box *Box)` would then be called on a
// different object than `MakeBox` returned.
func hostTypeDef(hp *hostPkg, et *ast.ExternType) *typeDef {
	goSym := "*" + hp.alias + "." + goSelectorName(et.ForeignName)
	key := hp.importPath + " " + goSym
	hostTypeMu.Lock()
	defer hostTypeMu.Unlock()
	if d, ok := hostTypeDefs[key]; ok {
		return d
	}
	d := &typeDef{nomi: et.Name, decl: et, line: et.Line, isDistinct: true, inner: kindInvalid, rtOpaque: true, rtDeclared: true, lowerable: true}
	hostTypeDefs[key] = d
	return d
}

// hostTableType reports whether et is a plain `host type` in a user file: no
// `go` binding, no inline body and nothing inside it. Its values are opaque
// handles the program's host-table functions (hostTableFn) create and read,
// so the VM holds whatever Go value the embedder's function answered.
func hostTableType(et *ast.ExternType) bool {
	return et != nil && et.ForeignName == "" && et.GoBody == "" &&
		len(et.TypeParams) == 0 && len(et.WhereClauses) == 0 &&
		!et.HasBody && len(et.Items) == 0 && len(et.Decorators) == 0
}

// hostTableTypeDef is the def for a plain `host type`: hostTypeDef's opaque
// leaf, interned process-wide by the declaring file and name so a sibling
// file's call site and the declaring file's `host fn` hold one kind.
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

// goSelectorName is the symbol half of a `go alias.Symbol` binding.
func goSelectorName(foreign string) string {
	if i := strings.LastIndex(foreign, "."); i >= 0 {
		return foreign[i+1:]
	}
	return foreign
}

// hostTypeNamed resolves a type NAME to this module's host handle def.
//
// The anchor was established from the module's OWN `gopkg` and `host type`
// declarations, which is exactly what the checker resolves the bare name
// through here, so no per-mention re-check is needed — opaqueNamed's reasoning.
func (g *gen) hostTypeNamed(name string) (*typeDef, bool) {
	d, ok := g.hostTypes[name]
	return d, ok
}

// --- the module graph ------------------------------------------------------

// hostModuleOf locates the Go module providing importPath, by walking up from
// the declaring .nomi file to the nearest go.mod exactly as
// `internal/ffirun`'s findGoModRoot does.
//
// Only a module whose path PREFIXES the import path answers, and the rule is
// deliberately narrow: a `gopkg` naming a published third-party package the
// project does not provide refuses by name here. A Go standard library
// package (ffirun.IsStdPackage) is the toolchain's and needs no go.mod: it
// answers with no directory and the module path "std", as `go list` names
// the standard library's module.
//
// `dir` is the module's ROOT DIRECTORY, not the package's, for both arms,
// because a module is named by its root. Every corpus FFI fixture binds its
// module ROOT package (`gopkg "callbackffiapp"` in a module named
// `callbackffiapp`), where the package directory and the module root are the
// same path, so the corpus cannot tell the two apart. See
// TestHostModuleOfSubPackageReplacesTheModuleRoot.
func hostModuleOf(nomiPath, importPath string) (dir, modPath, why string) {
	if ffirun.IsStdPackage(importPath) {
		return "", "std", ""
	}
	root, ok := goModRootAbove(filepath.Dir(nomiPath))
	if !ok {
		return "", "", "no go.mod above the declaring file"
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", "", "unreadable go.mod at " + root
	}
	f, err := modfile.Parse(filepath.Join(root, "go.mod"), data, nil)
	if err != nil || f.Module == nil || f.Module.Mod.Path == "" {
		return "", "", "unparseable go.mod at " + root
	}
	mod := f.Module.Mod.Path
	if hostPathIn(importPath, mod) {
		return root, mod, ""
	}
	// A SEPARATELY REQUIRED module (`require echobinding v0.0.0` with
	// `replace echobinding => ./echobinding`). The VM calls it through the
	// adapter internal/ffirun generates into the project's wrapper, which Go
	// builds against the project's own go.mod, so being required is what makes
	// the import resolvable. A local `replace` also gives the module's
	// directory; a module from the module cache has none.
	for _, rep := range f.Replace {
		if !hostPathIn(importPath, rep.Old.Path) || rep.New.Version != "" {
			continue
		}
		dir := rep.New.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		return dir, rep.Old.Path, ""
	}
	for _, req := range f.Require {
		if hostPathIn(importPath, req.Mod.Path) {
			return "", req.Mod.Path, ""
		}
	}
	return "", "", "outside the project's own Go module " + mod + " and every module its go.mod requires"
}

// hostPathIn reports whether importPath names module mod's root package or
// one of its sub-packages.
func hostPathIn(importPath, mod string) bool {
	return importPath == mod || strings.HasPrefix(importPath, mod+"/")
}

func goModRootAbove(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// hostDefGoType reports the Go type of a host handle def, and whether the def
// is one.
//
// Identity on the PROCESS-WIDE table by pointer, never on the rendered name:
// the def compared against was built from a validated `opaque type X go
// alias.Sym` declaration, so a user's own type spelled the same way produces a
// different def and answers false here. Same rule isDynamicKind states.
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
// binding's key.
//
// The signature is the NOMI one, so every call site in the package — local,
// module-qualified, or through a selective import — reaches it through the
// path it already uses.
func (g *gen) hostFnDecl(ef *ast.ExternFunc) {
	g.at(ef.Line)
	if g.stdModule == "" && hostTableFn(ef) {
		sig := g.funcs[ef.Name]
		if sig == nil || sig.decl != hostShellOf(ef) {
			sig = g.hostSignature(ef)
		}
		if !sig.lowerable {
			g.hostFnReject(sig.genericWhy, ef.Name, ef)
			return
		}
		g.irHostFnRetain(ef, sig)
		return
	}
	if !hostBoundFn(ef) {
		// Not Go-bound, or a shape this family does not claim: a generic
		// `host fn`, an impl item, an inline `go { }` body. Each is refused
		// under the declaration's own key rather than silently dropped.
		g.hostFnReject("host fn declaration", ef.Name, ef)
		return
	}
	hp := g.hostPkgs[ef.ForeignAlias]
	switch {
	case hp == nil:
		g.hostFnReject("host fn declaration", ef.Name+": no `gopkg` declares "+ef.ForeignAlias, ef)
		return
	case hp.why != "":
		g.hostFnReject("go import", hp.importPath+": "+hp.why, ef)
		return
	}
	sig := g.funcs[ef.Name]
	if sig == nil || sig.decl != hostShellOf(ef) {
		sig = g.hostSignature(ef)
	}
	if !sig.lowerable {
		// The reason is on the signature, recorded where it was decided, so
		// this and hostSignature cannot disagree about which of them judged.
		// funcDecl's clause structure exactly.
		g.hostFnReject(sig.genericWhy, ef.Name, ef)
		return
	}
	// The body is a crossing under the binding's key. Whether the Go
	// function's signature projects to the declaration is decided by the
	// adapter internal/ffirun generates for it (internal/hostgen), which
	// answers a refusal as the call's error; nothing here reads the Go side.
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
	irDeclineNote(reason)
}

// hostSignature is a Go-bound `host fn` as its call sites see it.
//
// Built here rather than by signature() because the shell has no body, and
// EVERY lowerability clause in signature() keys on that. The clauses this one
// keeps are the ones that are still true of a foreign declaration: an
// unrepresentable parameter or result type, and a defaulted parameter.
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
		if p.Default != nil {
			// A DEFAULTED parameter of a foreign function. The default is
			// evaluated at the CALL in the callee's own scope (sugar.go), and
			// the callee here is a Go function with no Nomi scope to evaluate
			// one in — so a call that omits the argument has nowhere to get it
			// from. Its own key, because it is the same gap
			// `stdlib host function with a defaulted parameter` names one
			// module over.
			sig.lowerable = false
			sig.genericWhy = "host fn with a defaulted parameter"
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

// hostTypeDecl is the `opaque type RawBox go ffi.Box` declaration itself.
//
// Nothing is declared for a bound one: the Go type is declared in the user's
// own package, which is what `rtDeclared` on the def says. A plain `host type`
// in a user file is an embedder's handle and is declared nowhere. Any other
// unbound one is refused.
func (g *gen) hostTypeDecl(et *ast.ExternType) {
	g.at(et.Line)
	if g.stdModule == "" && hostTableType(et) {
		// A plain `host type`: an embedder's handle, declared nowhere.
		return
	}
	if !hostBoundType(et) {
		g.rejectWhole("host type declaration", et.Name, et)
		return
	}
	hp := g.hostPkgs[et.ForeignAlias]
	switch {
	case hp == nil:
		g.reject("host type declaration", et.Name+": no `gopkg` declares "+et.ForeignAlias, et)
	case hp.why != "":
		g.reject("go import", hp.importPath+": "+hp.why, et)
	}
}

// hostPkgDecl is the `gopkg` declaration itself. Nothing is declared. A
// declaration whose module could not be located refuses here, at the position
// a programmer can act on.
func (g *gen) hostPkgDecl(ep *ast.ExternPackage) {
	g.at(ep.Line)
	if hp := g.hostPkgs[ep.Alias]; hp != nil && hp.why != "" {
		g.reject("go import", ep.ImportPath+": "+hp.why, ep)
	}
}

// hostHandleDef reports whether d is a USER FFI host handle — a def this file's
// process-wide table built from a validated `opaque type X go alias.Sym`.
//
// Identity on the TABLE by pointer, never on the rendered name or on
// `rtOpaque`, which every std leaf also carries. It is the predicate
// inspect.go's handle arm is gated on, and gating it on `rtOpaque` instead
// would hand `Bytes`, `Decimal`, `Dynamic`, `Sender<T>` and every future rt leaf
// a derived rendering that competes with the std `impl Debug` each of them
// writes.
func hostHandleDef(d *typeDef) bool {
	return hostDefHeld(d)
}
