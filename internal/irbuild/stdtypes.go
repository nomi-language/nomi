package irbuild

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// The process-wide defs of the types std declares in Nomi.
//
// A std type whose Go type rt declares has a spec row and one def built from
// it (package doc, "Std spec rows"). Every other struct, enum and distinct
// type a shared std module declares has one def too, built from std's
// declaration by the code that builds a program file's: a gen of the
// module's own (newStdTypesGen) declares the module's interfaces and runs
// buildTypes over its nodes, so a field, a variant's payload and an inner
// type resolve as they do in any file. That gen lowers no body. Its defs
// are the module's types for the process: the shared lowering's gen for the
// module adopts them in place of shells of its own (adoptedStdDef), and
// every other gen's projection of the checker's type answers them
// (localNamed), so a std function's kinds, built with no gen
// (projectShared), and a call site's in any program are one pointer.
//
// A def is found by its declaration's identity, the checker's own nominal
// rule: the declaring module's origin and the declared name (`std/json`,
// `Json.Case`), which names the declaration in that module's scope
// (declNode), and the gen that built the module holds the declaration's def
// (defsByDecl). The identity is the same in every process and for every
// program, and so is everything the def is named by where it reaches the
// IR: its run-time spelling is the module's qualifier and the name
// (`json.Json.Case`, stdSpecOf), and a type's symbol has no linkage name.
// A module's types are built once per process, from the stdlib's shared
// analysis, the first time any gen names one of them, and never again,
// since kinds are compared by pointer with ones made earlier.
//
// A def is admitted when it lowers and everything it holds is
// package-neutral (kind.packageNeutral): a scalar, a spec row's def,
// another admitted def, or a structural or prelude type over those. Its
// shell is marked process-wide (stdOrigin) before its fields resolve, so a
// field naming another type of the module, or a `List` of one, interns
// process-wide as the def does. A def that is not admitted loses the mark,
// and so does every def that holds it, until none changes; such a type has
// no kind with no gen, as before.
//
// A generic declaration is not admitted: its shell refuses `generic type`.
// Its instances are built where they are asked for, by the instance
// mechanism a program's generic type has (generictype.go): in the declaring
// module's own gen. So `Seq<Int>` in std/iter is std/iter's gen's, and a
// program reaches a shared module's generic type only through a spec row.
//
// A field's default is std's Nomi source, which only the declaring module's
// own gen lowers (fieldDefault). A literal elsewhere that omits a
// defaulted field of one of these types declines.
//
// The same gen declares the module's interfaces, and each one std declares
// in Nomi (not a spec row's, `Struct` or `Iter`) is marked process-wide too
// (ifaceDef.stdOrigin) before its signatures resolve. A non-generic one is
// admitted, as a type is, when it lowers and every kind its functions name
// is package-neutral; a type that holds one of its values is admitted with
// it, since one fixed point decides the module's types and interfaces
// together. An admitted interface is the one def every gen holds a value of
// it through (stdDeclaredIface): the declaring module's
// shared gen adopts it (adoptedStdIface), and the projection with no gen
// answers its existential, so a std function that takes or returns one is
// lowered once for the process. A generic interface's template is marked
// and kept, not admitted: each gen instantiates it at the arguments it
// needs (ifaceInstAt), and the instance is that gen's.
//
// A module declared with each program (programStdModules) keeps its types
// and interfaces in the program's registry, so this answers none of them.
// stdTypesListed, set only by a census test, answers them too, to count
// what projectShared would answer if those modules were shared.

// stdTypeKey is a std type's declaration identity: its declaring module's
// analyzer origin and its declared name.
type stdTypeKey struct{ origin, name string }

// stdTypes is the process's registry: by origin, the gen that built each
// std module's types (nil for an origin no std module has), whose
// defsByDecl and ifacesByDecl hold each declaration's def, marked
// process-wide when it is admitted; each marked interface by its
// declaration; and why each declaration with no shared def has none.
var stdTypes = struct {
	mu          sync.Mutex
	built       map[string]*gen
	byIfaceDecl map[*ast.InterfaceDef]*ifaceDef
	why         map[ast.Node]string
}{built: map[string]*gen{}, byIfaceDecl: map[*ast.InterfaceDef]*ifaceDef{}, why: map[ast.Node]string{}}

// stdTypesListed makes the registry answer the types of the modules
// declared with each program too. Only TestStdObjects_WhatStopsEachListedModule
// sets it, while no other test runs.
var stdTypesListed atomic.Bool

// stdTypeKeyOf is the key of the std declaration a checker type names, when
// it is a struct, enum or distinct type with no type arguments declared in a
// std module the registry answers for.
func stdTypeKeyOf(t analysis.Type) (stdTypeKey, bool) {
	var origin, name string
	switch ty := analysis.ResolveClosed(t).(type) {
	case *analysis.StructType:
		if len(ty.TypeArgs) > 0 {
			return stdTypeKey{}, false
		}
		origin, name = ty.Origin, ty.Name
	case *analysis.EnumType:
		if len(ty.TypeArgs) > 0 {
			return stdTypeKey{}, false
		}
		origin, name = ty.Origin, ty.Name
	case *analysis.DistinctType:
		if ty.Subtype || len(ty.TypeArgs) > 0 {
			return stdTypeKey{}, false
		}
		origin, name = ty.Origin, ty.Name
	default:
		return stdTypeKey{}, false
	}
	if !stdRegistryAnswers(origin) {
		return stdTypeKey{}, false
	}
	return stdTypeKey{origin: origin, name: name}, true
}

// stdRegistryAnswers reports whether the registry answers for declarations
// of the module with the given origin: a std module lowered once for the
// process, or under stdTypesListed one declared with each program.
func stdRegistryAnswers(origin string) bool {
	module, inStd := strings.CutPrefix(origin, "std/")
	if !inStd || analysis.IsBodyOrigin(origin) {
		return false
	}
	return !isProgramStd(module) || stdTypesListed.Load() && !strings.HasPrefix(module, "_fixtures/")
}

// stdDeclaredIface is the process-wide def of the interface the std module
// with the given origin declares as name, or nil when it has none: an
// admitted interface, or a generic one's template. g may be nil
// (projectShared); a gen building a module's types asks with the
// registry's lock already held.
func (g *gen) stdDeclaredIface(origin, name string) *ifaceDef {
	if !stdRegistryAnswers(origin) {
		return nil
	}
	if g == nil || !g.stdTypesBuild {
		stdTypes.mu.Lock()
		defer stdTypes.mu.Unlock()
	}
	bg, started := stdTypes.built[origin]
	if !started {
		bg = buildStdTypesLocked(origin)
	}
	if bg == nil {
		return nil
	}
	if d := bg.ifacesByDecl[bg.declNode(origin, name)]; d != nil && d.stdOrigin != "" {
		return d
	}
	return nil
}

// stdRegistryIface is the registry's def of the interface declaration decl,
// or nil: what tells std's generic interface template from a program's,
// since std's instances dispatch through std-owned symbols
// (stdProgMethodSym). A gen building a module's types asks with the
// registry's lock already held.
func (g *gen) stdRegistryIface(decl *ast.InterfaceDef) *ifaceDef {
	if !g.stdTypesBuild {
		stdTypes.mu.Lock()
		defer stdTypes.mu.Unlock()
	}
	d := stdTypes.byIfaceDecl[decl]
	if d == nil || d.stdOrigin == "" || !stdRegistryAnswers(d.stdOrigin) {
		return nil
	}
	return d
}

// stdIfaceMarked reports whether the types gen g marks interface id
// process-wide (declareIfaces): one std declares in Nomi, which no spec
// row describes, and neither the structural `Struct` nor `Iter`, which
// keep their own representations.
func (g *gen) stdIfaceMarked(id *ast.InterfaceDef) bool {
	if !g.stdTypesBuild || id.Name == structIfaceName || id.Name == "Iter" {
		return false
	}
	return stdSpecIface(analysis.StdType{Module: g.fa.Origin, Name: id.Name}) == nil
}

// stdDeclaredDef is the process-wide def of the std type t names, or nil
// when it has none. g may be nil (projectShared). A gen building a module's
// types (stdTypesBuild) asks with the registry's lock already held.
func (g *gen) stdDeclaredDef(t analysis.Type) *typeDef {
	key, ok := stdTypeKeyOf(t)
	if !ok {
		return nil
	}
	if g == nil || !g.stdTypesBuild {
		stdTypes.mu.Lock()
		defer stdTypes.mu.Unlock()
	}
	return stdTypeLocked(key)
}

// stdTypeLocked answers key, building its module's types first: the def of
// the declaration key's name has in the module's scope (declNode), when it
// is marked process-wide. A module whose types are being built (a field of
// a type one module declares names a type of another that names the first)
// answers its shells, marked, which the build under way admits or not.
func stdTypeLocked(key stdTypeKey) *typeDef {
	g, started := stdTypes.built[key.origin]
	if !started {
		g = buildStdTypesLocked(key.origin)
	}
	if g == nil {
		return nil
	}
	if d := g.defsByDecl[g.declNode(key.origin, key.name)]; d != nil && d.stdOrigin != "" {
		return d
	}
	return nil
}

// buildStdTypesLocked builds the types the std module with the given origin
// declares, admits each one that is process-wide, and returns the gen that
// holds them, or nil when no std module has the origin.
func buildStdTypesLocked(origin string) *gen {
	module := strings.TrimPrefix(origin, "std/")
	lib := std.Shared().Ensure(module)
	fa, nodes := lib.Files[module], lib.Nodes[module]
	if fa == nil || fa.Origin != origin {
		stdTypes.built[origin] = nil
		return nil
	}
	g := newStdTypesGen(module, nodes, fa)
	stdTypes.built[origin] = g
	g.declareIfaces(nodes)
	for _, d := range g.ifaceOrder {
		if d.stdOrigin != "" {
			stdTypes.byIfaceDecl[d.decl] = d
		}
	}
	g.buildTypes(nodes)
	g.resolveIfaces()
	admitted := map[*typeDef]bool{}
	for _, d := range g.typeOrder {
		if d.lowerable {
			admitted[d] = true
			continue
		}
		why := "unlowered type"
		if construct, _, named := declRefusal(d); named {
			why = construct
		}
		stdTypes.why[d.decl] = why
		unmarkStdDef(d)
	}
	ifaceAdmitted := map[*ifaceDef]bool{}
	for _, d := range g.ifaceOrder {
		switch {
		case d.stdOrigin == "" || stdProgTemplate(d) != nil:
			// A template stays marked: it is no value's kind.
		case d.lowerable:
			ifaceAdmitted[d] = true
		default:
			stdTypes.why[d.decl] = d.why
			unmarkStdIface(d)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, d := range g.typeOrder {
			if !admitted[d] {
				continue
			}
			if held, neutral := d.heldNeutral(); !neutral {
				delete(admitted, d)
				unmarkStdDef(d)
				stdTypes.why[d.decl] = "holds " + held.nomi()
				changed = true
			}
		}
		for _, d := range g.ifaceOrder {
			if !ifaceAdmitted[d] {
				continue
			}
			if held, neutral := d.signatureNeutral(); !neutral {
				delete(ifaceAdmitted, d)
				unmarkStdIface(d)
				stdTypes.why[d.decl] = "names " + held.nomi()
				changed = true
			}
		}
	}
	return g
}

// unmarkStdDef takes the process-wide mark off a def that is not admitted.
func unmarkStdDef(d *typeDef) {
	defer changingTypes()()
	d.stdOrigin = ""
}

// unmarkStdIface takes the process-wide mark off an interface that is not
// admitted.
func unmarkStdIface(d *ifaceDef) {
	defer changingTypes()()
	d.stdOrigin = ""
}

// signatureNeutral reports whether every kind d's functions name is
// package-neutral, and the first that is not. A position with no kind (a
// `self`, or one no erased receiver dispatches over) names nothing a gen
// holds.
func (d *ifaceDef) signatureNeutral() (kind, bool) {
	for _, m := range d.order {
		for _, k := range append(append([]kind(nil), m.params...), m.result) {
			// kindInvalid: marker — a position with no kind holds nothing.
			if k != kindInvalid && !k.packageNeutral() {
				return k, false
			}
		}
	}
	return kindInvalid, true
}

// heldNeutral reports whether every kind d holds (its inner type, fields,
// payloads, embedded structs and slots) is package-neutral, and the first
// that is not.
func (d *typeDef) heldNeutral() (kind, bool) {
	held := []kind{}
	// kindInvalid: marker — a zero-sized distinct holds nothing.
	if d.inner != kindInvalid {
		held = append(held, d.inner)
	}
	for _, f := range d.fields {
		held = append(held, f.k)
	}
	for _, v := range d.variants {
		for _, p := range v.payloads {
			held = append(held, p.k)
		}
		if v.embeds != nil {
			held = append(held, named(v.embeds))
		}
	}
	for _, s := range d.slots {
		held = append(held, s.k)
	}
	for _, k := range held {
		if !k.packageNeutral() {
			return k, false
		}
	}
	return kindInvalid, true
}

// stdTypeWhy is why the std declaration key names has no process-wide def,
// once its module's types are built, or "".
func stdTypeWhy(key stdTypeKey) string {
	stdTypes.mu.Lock()
	defer stdTypes.mu.Unlock()
	g := stdTypes.built[key.origin]
	if g == nil {
		return ""
	}
	return stdTypes.why[g.declNode(key.origin, key.name)]
}

// adoptedStdIface is the process-wide def this shared std module's
// declaration of interface id adopts, or nil: adoptedStdTypeDef's rule, by
// the module and the name, so the module checked from edited source
// adopts the def of its shared copy's declaration, as its types do.
func (g *gen) adoptedStdIface(id *ast.InterfaceDef) *ifaceDef {
	if g.stdModule == "" || g.stdTypesBuild || g.fa == nil || isProgramStd(g.stdModule) {
		return nil
	}
	if d := g.stdDeclaredIface(g.fa.Origin, id.Name); d != nil {
		return d
	}
	return nil
}

// adoptedStdTypeDef is the process-wide def this shared std module's
// declaration of name adopts, or nil: none for a gen that builds the
// registry's defs, a program's gen or a listed module's.
func (g *gen) adoptedStdTypeDef(name string) *typeDef {
	if g.stdModule == "" || g.stdTypesBuild || g.fa == nil || isProgramStd(g.stdModule) {
		return nil
	}
	key := stdTypeKey{origin: g.fa.Origin, name: name}
	if !strings.HasPrefix(key.origin, "std/") {
		return nil
	}
	stdTypes.mu.Lock()
	defer stdTypes.mu.Unlock()
	return stdTypeLocked(key)
}
