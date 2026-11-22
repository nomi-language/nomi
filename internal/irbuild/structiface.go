package irbuild

import (
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// `std/structs.Struct` as an EXISTENTIAL — the universal structural marker.
//
// # Why this is not a stdIfaceSpecs row
//
// stdiface.go's table lists the std interfaces whose methods the builder
// dispatches. Every row is a set of method shapes, and its `matches` refuses
// a declaration carrying a field, a variant, an `Extern` method or a
// self-typed result. `Struct` fails three of those at once: its only method
// is a `host fn`.
//
// So `Struct` is not a smaller version of `Display`. It is a different KIND of
// stdlib interface, and the two differences are what this file is:
//
//  1. CONFORMANCE IS STRUCTURAL, NOT DECLARED. Spec §"the interface every
//     struct satisfies": "Every struct value — a named `struct` type or an
//     anonymous `{...}` — satisfies `Struct`, so its functions are callable on
//     any of them; no non-struct type satisfies it." There is no
//     `impl Struct for X` ANYWHERE — the analyzer rejects one outright
//     ("`Struct` is a built-in structural marker and cannot be implemented",
//     pinned by 11/structural_interfaces' own test), so `bindsImpl`'s two impl
//     indexes are both empty for it BY CONSTRUCTION and every boxing site would
//     answer no. That is why `universal` exists on `ifaceDef` and why it is
//     asked in `bindsImpl` rather than worked around at each erasure site.
//
//  2. It dispatches nothing. `update` is `host fn update(original: self,
//     updates: Partial<self>) -> self`: self in the return plus a
//     compiler-known type operator in a parameter. A `stdIfaceMethodSpec`
//     cannot express a self-typed return. So the def carries zero methods,
//     and `Struct.update` on an erased receiver has no method here to
//     dispatch.
//
// That keeps (2) small: `Struct.update` at a concrete receiver
// (`Struct.update(Point{x: 1, y: 2}, {x: 9})`) lowers with no `*ifaceDef`,
// because it resolves through the stdlib function index, and
// `fn echo<T>(v: T) -> T where T: Struct` lowers as an instance per type
// argument. What this file adds is the existential kind,
// `fn one(s: Struct) -> Int`.
//
// # IDENTITY
//
// `checkReservedTypeName` (analysis/builder.go) is reached for an interface.
// The 13 PRELUDE-INJECTED interface names (Add, Comparable, Debug, Discrete,
// Display, Divide, Equatable, Hashable, Iter, Multiply, Steppable, Struct,
// Subtract) are reserved, because a prelude re-export occupies every user
// file's scope unasked and Nomi has no syntax that disambiguates a local
// declaration from it, while the 8 that must be IMPORTED (App, Assertable,
// DateParts, TimeParts, Anchored, ToJson, FromJson, Literal) stay freely
// shadowable. So `pub interface Struct` in a user file is a front-end error,
// and the wrong-answer hazard for this interface is closed by the front end as
// well as by this file.
//
// The def is the same triple every other family's is — `(declaring module,
// name, validated declaration shape)` — and it is not a name: a type the
// checker solved, or an impl block's recorded interface, is std's `Struct`
// when its origin is `std/structs` and std declares it in this file's shape
// (stdSharedIface). `std` is unforgeable as a module name (`LoadManifest`
// rejects `[package].name = "std"`), so a local, sibling or dependency
// declaration of the name has another origin and never answers.
//
// # The shape check, and what it is FOR
//
// `structIfaceMatches` requires std's declaration to be exactly what this file
// believes: public, non-generic, no fields, no variants, and
// exactly ONE method — `update`, EXTERN, no type parameters, two parameters
// named `original` (self) and `updates` (`Partial<self>`), returning `self`.
//
// That is a check on a method this file does not dispatch. It protects the
// claim in (2): the def carries no methods because std declares exactly one
// and that one is not dispatchable. A std edit that added a dispatchable
// method would make the empty def wrong: `Struct.foo(erased)` would refuse
// for want of a method and never dispatch. The shape check turns that into
// no anchor at all, so every mention refuses under `stdlib interface`.
//
// If `std/structs.nomi`'s `interface Struct` gains a second method,
// or `update` stops being a `host fn`, or its `self`-typed return becomes
// concrete, `structIfaceMatches` stops matching and every `Struct` position
// refuses. TestStructIface_TheStdDeclarationStillHasTheShapeThisFileAssumes
// asserts the premise POSITIVELY against the real std source, so such a change
// fails a test rather than lowering against a shape nobody wrote.

// structIfaceModule is Struct's declaring module — the identity half the
// analyzer's InterfaceType.Origin would otherwise supply.
const structIfaceModule = "std/structs"

// structIfaceName is the other half.
const structIfaceName = "Struct"

// structIfaceUpdate is the one method std declares, held as a constant because
// the shape check and this file's comment must not disagree about the name.
const structIfaceUpdate = "update"

// structIfaceDef is the process-wide `*ifaceDef` for `Struct`.
//
// Process-wide for opaque.go's, stdenum.go's and stdiface.go's reason, which is
// a requirement and not an optimization: kinds are compared by pointer
// across gens, so two gens' `Struct` existentials must be one kind or a value
// erased in one file could not be passed to a function declared in another. It
// is safe to share because the def holds nothing that belongs to one module:
// no methods and no fields. `stdShared` is set for the same reason it is set
// on stdIfaceDefs' rows: `kind.packageNeutral` asks that question in O(1) and
// must not reach a spec table.
var structIfaceDef = sync.OnceValue(func() *ifaceDef {
	// A canonical declaration node no file contains, for stdIfaceDefs'
	// reason: a declaration node is what an interface identity is everywhere
	// else in this package (`implKeyFor` keys on `d.decl`), and borrowing
	// std's own node would make the def per-`std.Load()` and not
	// process-wide.
	//
	// It carries no methods, as (2) above says: the canonical AST and the def
	// agree that nothing dispatches, so no derivation over either can
	// disagree with the other.
	canon := &ast.InterfaceDef{Name: structIfaceName, Public: true}
	return &ifaceDef{
		nomi:      structIfaceName,
		decl:      canon,
		methods:   map[string]*ifaceMethod{},
		lowerable: true,
		stdShared: true,
		universal: true,
		// No unit of the program declares it. stdIfaceDefs' rows carry the
		// same value.
		unit: -1,
	}
})

// structIfaceMatches reports whether decl is the declaration this file
// describes. See the file comment for what the check is for and what voids it.
func structIfaceMatches(decl *ast.InterfaceDef) bool {
	switch {
	case decl == nil, decl.Name != structIfaceName, !decl.Public:
		return false
	case len(decl.TypeParams) > 0, len(decl.WhereClauses) > 0:
		return false
	// An attached `//!` test does not void the anchor, for declModifiers'
	// reason: a line in a doc comment cannot change what a declaration IS, and
	// this predicate asks only that. The other shape predicates follow the
	// same rule.
	case len(decl.Methods) != 1:
		return false
	}
	m := &decl.Methods[0]
	switch {
	case m.Name != structIfaceUpdate:
		return false
	case !m.Extern, m.Body != nil, m.Open:
		// A Nomi-bodied or `open` default is lowered once per implementing
		// block (inheritDefaults, ifacedefault.go), and there is no
		// implementing block for a structural marker, so there is nowhere
		// for a default to go.
		return false
	case len(m.TypeParams) > 0, len(m.WhereClauses) > 0:
		return false
	case len(m.Params) != 2:
		return false
	}
	// `original: self`.
	if _, isSelf := m.Params[0].TypeAnnotation.(*ast.SelfType); !isSelf {
		return false
	}
	// `updates: Partial<self>` — the compiler-known type OPERATOR, which is
	// checked by SPELLING because it has no declaration anywhere in stdlib to
	// resolve against: std/structs.nomi's own comment says it "has no import and
	// no declaration anywhere in stdlib (unlike `List`/`Map`, which are real
	// `host type`s): it has no inhabitants, so it's recognized by name in the
	// resolver". A spelling check is therefore the strongest check available
	// here, and saying so is better than implying a resolution happened.
	if !structIfacePartialSelf(m.Params[1].TypeAnnotation) {
		return false
	}
	if m.Params[0].Name != "original" || m.Params[1].Name != "updates" {
		return false
	}
	if m.Params[0].Default != nil || m.Params[1].Default != nil {
		return false
	}
	if m.Params[0].Destructure != nil || m.Params[1].Destructure != nil {
		return false
	}
	// `: self`, the self-typed return.
	_, selfResult := m.ReturnTypeExpr.(*ast.SelfType)
	return selfResult
}

// structIfacePartialSelf reports whether te spells `Partial<self>`.
func structIfacePartialSelf(te ast.TypeExpr) bool {
	gt, isGeneric := te.(*ast.GenericType)
	if !isGeneric || gt.Name != "Partial" || len(gt.Params) != 1 {
		return false
	}
	_, isSelf := gt.Params[0].(*ast.SelfType)
	return isSelf
}

// structIfaceSatisfiedBy reports whether k is a value the spec says satisfies
// `Struct`: "a named `struct` type or an anonymous `{...}`", and nothing else.
//
// A named DISTINCT, an ENUM and every scalar answer false. That is the language
// rule and not a conservatism: 11/structural_interfaces pins the negative half
// as a front-end diagnostic ("Int does not implement Struct"), so a `true` here
// for a non-struct would be the builder disagreeing with the checker rather
// than being generous.
//
// Reads the `*typeDef`, never a name — `d.isEnum`/`d.isDistinct` are fields of
// the type identity itself.
func structIfaceSatisfiedBy(k kind) bool {
	switch k.tag {
	case tagAnonStruct:
		return true
	case tagNamed:
		return k.def != nil && !k.def.isEnum && !k.def.isDistinct
	}
	return false
}
