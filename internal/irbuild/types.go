package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Named types: a Nomi `struct` or `enum` as a real Go type.
//
// # Identity is a pointer, not a string
//
// A type's runtime identity as a STRING assembled at construction sites and
// re-parsed at lookup sites fails silently: shortening `json.Json.DecodeError`
// to `DecodeError` would make two impls share one dispatch slot. Here a Nomi
// type's identity IS the
// `*typeDef` its declaration produced: `kind` carries that pointer, kind
// equality is Go pointer equality, and two same-named structs declared in two
// modules are two pointers, two Go type declarations, and two Go packages.
// There is no string anywhere in the path, so there is nothing to collapse.
// TestNamedTypeIdentityIsAGoCompileError builds the deliberate mix-up and
// asserts the Go compiler rejects it.
//
// # Structs
//
// One Go struct, one Go field per Nomi field, in declaration order. Field
// access is a Go field selector. A field DEFAULT is a Nomi expression
// evaluated at each construction site that omits it, and evaluated in the
// DECLARING module's
// scope, not the constructor's, so it is emitted with the local scopes hidden.
//
// # Enums: a tagged struct, tag 0 reserved invalid
//
// Two properties of this representation are load-bearing enough to be
// enforced rather than commented:
//
// **The payload dedup key is the identical underlying Go type, NOT the
// layout.** Merging by layout is a silent-wrong-value bug: `Int` and `Float`
// are both 8 bytes with no pointers, so a layout key merges them into one
// field and payload recovery either bit-casts or truncates. Here the key is a
// `kind` VALUE compared with `==`, and `kind` is `{tag, *typeDef}` — so
// `kindInt == kindFloat` is false by construction and no amount of coincident
// size or alignment can make the two share a slot. See assignSlots, and
// TestEnumSlotsDedupByGoTypeNotLayout.
//
// Dedup is sound because only one variant is live at a time; it is NOT sound
// WITHIN one variant, because a struct-shaped variant's two `Int` fields are
// live together. So a slot is reused across variants and never inside one, and
// the slot count for a Go type is the max over variants of how many payloads
// of that type the variant carries.
//
// **Tags start at 1 and 0 means "never constructed".** Go's zero value would
// otherwise make an uninitialized enum silently equal to the first variant,
// and `make([]Maybe[T], n)` would produce n copies of it. Nomi has no zero
// values, so the reservation costs nothing and converts a silent wrong answer
// into a detectable one.
//
// A payload whose Go type is ZERO-SIZED gets no slot at all. That is what
// makes `embeds`-of-a-zero-sized-type contribute nothing, and it is the
// mechanism behind `Bool` being a Go `bool` rather than a tagged struct — the
// same rule, applied to the one enum the compiler special-cases.
//
// # Recursion
//
// A variant or field referring to its own type, directly or transitively,
// cannot be a flat inline struct: the size is infinite and Go rejects it. Such
// an edge is BOXED — the slot holds `*T` and construction allocates. Cycles
// are found by reachability over the resolved component graph, and every cycle
// is broken because every edge that can reach its own container is boxed.

// typeDef is one Nomi struct or enum declaration.
//
// A pointer to one of these IS the type's identity: see the package comment
// above. Nothing keys off the Nomi or Go name.
type typeDef struct {
	// nomi is the Nomi spelling, for refusal text.
	nomi string
	// decl is the declaration node, for refusal positions.
	decl ast.Node
	line int

	isEnum bool
	// isDistinct marks a `type Name Inner` wrapper or a `type Name` marker.
	// A distinct type is nominally its own type and structurally its inner
	// one, which is exactly what a Go DEFINED type is, so it lowers to
	// `type NomiT_X int64` rather than to a wrapper struct — no storage, no
	// indirection, and the conversion in either direction is free.
	isDistinct bool
	// inner is a wrapping distinct's wrapped kind, kindInvalid for a
	// zero-sized marker (`type Expired`), which has no inner at all.
	inner kind
	// fields are a struct's fields in declaration order.
	fields []fieldDef
	// variants are an enum's variants; variants[i].tag is i+1.
	variants []variantDef
	// slots are an enum's payload storage, deduped by identical Go type.
	slots []slotDef
	// tagName overrides the Go field holding an enum's variant tag. Empty —
	// meaning `tag` — for every enum this package DECLARES; set for a prelude
	// enum, whose Go type is hand-written in rt and whose fields therefore
	// have to be exported for another package to name. See prelude.go.
	tagName string

	// refusals are the reasons this declaration itself was not lowered.
	refusals []refusal
	// blockedBy names the type whose refusal makes this one unlowerable, when
	// this declaration is fine and a component is not, and blocker is that
	// type. The pointer is what lets a use site report the ROOT reason rather
	// than a placeholder naming two types and neither gap; the string stays
	// because it is what the detail spells. See declRefusal.
	blockedBy string
	blocker   *typeDef
	lowerable bool
	// foreign is the Nomi FILE key of the declaring file when this def is
	// another file's type seen from here, a MIRROR. Empty for a declaration
	// this file makes. See foreign.go.
	foreign string
	// why and whyDetail are the refusal a MIRROR reports at every use site.
	// A mirror carries no `refusals`, because the declaration's own reasons
	// are replayed at the declaration's own position in its own file; what a
	// use here needs is one reason, and it may be a reason the declaring file
	// does not have (a type cycle).
	why       string
	whyDetail string
	// preludeOf is the prelude enum this def instantiates and preludeArgs its
	// type arguments, both nil for an ordinary declaration. Read only when a
	// mirror has to REBUILD the instance at another package's spelling of the
	// arguments: `rt.Maybe` is package-neutral and the `T` inside it is not.
	// See prelude.go and foreign.go's importNamed.
	preludeOf   *preludeAnchor
	preludeArgs []kind
	// genStructOf is the GENERIC STDLIB STRUCT this def instantiates and
	// genStructArgs its type arguments, both nil for everything else.
	//
	// Separate from preludeOf rather than folded into it: the two families
	// differ in the declaration kind they are anchored against (`*ast.StructDef`
	// against `*ast.EnumDef`) and in the layout the def carries (fields against
	// variants, tags and slots), so one field serving both would be a pointer
	// whose meaning depends on which table built it.
	//
	// Read by kind.packageNeutral, so that predicate answers about this def from
	// the def rather than by trusting that no non-neutral instance was ever
	// built, and by genStructOf, so an operation arm identifies a Set by SPEC
	// POINTER instead of by rendered name. See stdgenstruct.go.
	genStructOf   *stdGenStructSpec
	genStructArgs []kind
	// genHostOf is the GENERIC STDLIB HOST TYPE this def instantiates and
	// genHostArgs its type arguments, both nil for everything else.
	//
	// A third pair rather than a widening of either above it, for the reason
	// stated one field up: the three families differ in the declaration kind
	// they are anchored against (`*ast.ExternType` against `*ast.StructDef`
	// against `*ast.EnumDef`) and in what the def carries (nothing at all,
	// against fields, against variants), so one field serving all three would
	// be a pointer whose meaning depends on which table built it.
	//
	// Read by kind.packageNeutral for genStructOf's reason, and by genHostOf so
	// an operation arm identifies a `Sender` by SPEC POINTER rather than by
	// rendered name. See stdgenhost.go.
	genHostOf   *stdGenHostSpec
	genHostArgs []kind
	// genericOf is the USER generic struct declaration this def monomorphizes
	// and genericArgs its type arguments, both nil for everything else.
	//
	// Separate from preludeOf and genStructOf rather than folded into either,
	// for the reason genStructOf gives for being separate from preludeOf: the
	// three families differ in what anchors them — a prelude enum spec, a std
	// struct spec, a user *ast.StructDef — so one field serving all three
	// would be a pointer whose meaning depends on which table built it.
	//
	// Read by unifyTypeParams, which recovers a nested instance's arguments at
	// a struct literal by reading them back off the def rather than
	// re-deriving them. See generictype.go.
	genericOf   *genericTemplate
	genericArgs []kind
	// instOrigin is, on a MIRROR of another file's generic instance, the
	// owner's own def for that instance, and nil everywhere else. It is how
	// a mirror handed back to the owner (a generic function's argument kind
	// passed to the file that declares both) resolves to the owner's def
	// rather than to a second mirror. See importGenericInstance.
	instOrigin *typeDef
	// ownerDef is, on any MIRROR, the declaring file's def it names (for an
	// instance, the same def as instOrigin). A field default the mirror
	// carries is that def's, and only its gen can lower it; see
	// foreignfielddefault.go.
	ownerDef *typeDef
	// rtDeclared marks a def whose Go type is hand-written in rt rather than
	// emitted: a stdlib opaque newtype (`rt.Duration`). typeDecl emits nothing
	// for one, because a second declaration would be a second Go type for one
	// Nomi type. See opaque.go.
	rtDeclared bool
	// rtOpaque marks a def that is a LEAF this builder may not look inside: a
	// blessed stdlib primitive whose Go type rt declares (`rt.Bytes`). See
	// stdhost.go.
	//
	// It exists because `isDistinct && inner == kindInvalid` already MEANS
	// something — a zero-sized marker (`type Expired`) — and a leaf with a
	// PAYLOAD reaches every arm written for that meaning. Without the flag
	// `impl Equatable for Bytes`'s `a == b` would answer true for every pair,
	// because two markers ARE always equal; the hash would be `rt.HashUnit`, the
	// Debug rendering the bare type name, and `zeroSized()` would answer true for
	// a value with contents.
	//
	// Where this flag is read, the answer is to DECLINE rather than to invent
	// one. Go `==` on rt.Bytes would in fact agree with rt.Equal, but the
	// HASH must agree with `rt.Hash`'s structural scheme, and equality
	// without a matching hash is a wrong bucket rather than a wrong answer. So
	// both decline together and a `Map<Bytes, _>` refuses.
	//
	// The three arms that merely REFUSE for a marker (a distinct pattern, a
	// distinct destructuring, a constructor call) are deliberately left alone:
	// the front end reserves these type names and gives a program no way to
	// construct or destructure one, so nothing can reach them to be told the
	// wrong reason.
	rtOpaque bool

	// components are the named types stored INLINE inside this one, used for
	// cycle detection and therefore for boxing. A type reached only through a
	// pointer — a list's element — is deliberately absent: it cannot make this
	// type's size infinite, so boxing the field would be pure waste.
	components []*typeDef
	// mentions are the named types this one NAMES anywhere in its kind, at any
	// depth through any composite, used to propagate unlowerability. A
	// superset of components, and the distinction matters: `List<Bad>` must
	// refuse this declaration (its kind would name an unlowered type) and must
	// not box it. See collections.go's note.
	mentions []*typeDef
	// NO `visiting` FIELD, AND THAT IS THE POINT. The zero-size,
	// reachability and inspectability walks each need a cycle guard, and it
	// cannot be a mutable bool here. A *typeDef is interned PROCESS-WIDE for
	// every stdlib and rt-declared type — opaqueDefs, stdEnumDefs,
	// stdMarkerDefs, hostTypeDefs are each a sync.OnceValue — so per-walk
	// state here would be a property of data every concurrent `Generate`
	// shares. See typePath.
}

// refusal is one recorded reason a declaration was not lowered, replayed at the
// declaration's own position when the node is reached.
type refusal struct {
	construct string
	detail    string
	// probe is the subtree whose own blockers must still be collected, so a
	// refused declaration does not hide the constructs underneath it.
	probe []ast.Node
}

type fieldDef struct {
	nomi string
	k    kind
	// deflt is the field's default expression, or nil when the field is
	// required at every construction site. It is lowered only by the
	// declaring file's gen: at the construction site when that is the
	// declaring file, and otherwise in that gen's accessor
	// (foreignfielddefault.go).
	deflt ast.Node
	// stdDeflt is a STDLIB field's default, stated in the builder's own
	// vocabulary rather than as std's AST. Set only for a stdStructSpecs-
	// anchored def, and never together with `deflt`. A stdlib type is always
	// in another file, and its default is a constant that resolves no names.
	// See stdstruct.go's stdFieldDefault.
	stdDeflt *stdFieldDefault
	// boxed is set when the field's type can reach the containing type, so a
	// flat struct would have infinite size.
	boxed bool
}

// variantDef is one enum variant. Kind is the parser's spelling — "bare",
// "positional", "struct" or "embedded" — kept verbatim so a refusal names what
// the programmer wrote.
type variantDef struct {
	nomi string
	tag  int
	kind string
	// payloads are the values the variant carries: none for bare, one for
	// positional and embedded, one per field for struct-shaped.
	payloads []payload
	// embeds is the struct an `embeds` variant wraps, nil otherwise. It is
	// what makes `Shape.Circle{radius: 1.0}` construct a Circle and wrap it.
	embeds *typeDef
}

// payload is one value a variant carries.
type payload struct {
	// nomi is the field name for a struct-shaped variant's payload, empty for
	// a positional or embedded one.
	nomi string
	k    kind
	// deflt is a struct-shaped variant field's default, or nil.
	deflt ast.Node
	// goDeflt is a GO expression filling this field when a literal omits it, or
	// "".
	//
	// The other half of `deflt`, and the two are exclusive: `deflt` is the
	// declaring file's Nomi expression, lowered in the gen that constructs the
	// value, and this is a Go expression emitted verbatim. A SHARED def needs
	// this one, because a std enum's *typeDef is process-wide and has no gen to
	// lower Nomi source in — which is precisely the reason stdenum.go's
	// payloadStructScalar rejects a defaulted std field outright. See
	// payloadStructFields for why the Go form does not weaken that reason.
	goDeflt string
	// slot indexes typeDef.slots, or -1 for a zero-sized payload, which is
	// stored nowhere at all.
	slot int
}

type slotDef struct {
	k     kind
	boxed bool
	// users names the variants sharing this slot, for the generated comment.
	// It is the only place the dedup is legible to somebody reading the Go.
	users []string
}

func (d *typeDef) variant(name string) *variantDef {
	for i := range d.variants {
		if d.variants[i].nomi == name {
			return &d.variants[i]
		}
	}
	return nil
}

func (d *typeDef) field(name string) *fieldDef {
	for i := range d.fields {
		if d.fields[i].nomi == name {
			return &d.fields[i]
		}
	}
	return nil
}

// typePath is the chain of named types a structural walk is currently INSIDE,
// and it is where a cycle guard belongs.
//
// NOT A FIELD ON THE TYPE. A `(*typeDef).visiting` bool set on entry and
// cleared by a deferred assignment on exit, guarded by a lock or replaced by a
// per-gen `map[*typeDef]bool`, would be wrong at the layer, for reasons that
// have nothing to do with the goroutines:
//
//   - The marker is not a fact about the TYPE. It is a fact about one
//     traversal's position, true for the duration of a stack frame. A field on
//     the type is the wrong home for it however carefully the field is guarded,
//     and the *typeDefs are interned process-wide, so that home would be
//     shared by every concurrent `Generate` in the process.
//
//   - ONE FIELD WOULD SERVE THREE DIFFERENT WALKS. `zeroSized`, `reaches`
//     (here) and `firstUninspectable` (inspect.go) each need the guard. The
//     moment one called another, the inner walk's deferred clear would
//     silently unmark a def the outer walk was still inside and the outer
//     guard would stop working. That hazard is single-threaded and a lock
//     does not touch it.
//
// A PATH RATHER THAN A VISITED SET: it marks the current DFS path and not
// everything already seen. A visited set would suppress a second, legitimate
// visit to the same type down a different branch.
//
// A SLICE RATHER THAN A MAP. The paths are type-nesting deep, which is single
// digits in practice; a linear scan over that beats hashing, and
// the nil path allocates nothing at all. Appending to the caller's slice and
// passing it down is the standard sequential DFS-path idiom: sibling branches
// overwrite each other's tail slot, which is correct because a sibling only
// runs after the previous one has returned.
type typePath []*typeDef

// on reports whether the walk is already inside d.
func (p typePath) on(d *typeDef) bool {
	for _, seen := range p {
		if seen == d {
			return true
		}
	}
	return false
}

// zeroSizedOn reports whether values of this type occupy no storage, inside a
// walk already standing on path. `kind.zeroSized` (native.go) is the entry
// point; a *typeDef is only ever asked this from within a walk.
//
// An enum never is: it carries a tag byte. A struct is when every field is,
// which bottoms out at rt.Unit (`struct{}`) and at the empty struct. A boxed
// field is a pointer and never zero-sized, which is also what terminates this
// walk on a recursive type — every cycle contains a boxed edge.
//
// A `type Expired` marker has no inner at all and is the purest case: it
// carries no information, so an enum embedding one gets no slot for it and the
// `case` arm that binds it has to MATERIALIZE the value from its type. That is
// the general rule behind `Bool` being a Go `bool`.
//
// The DISTINCT arm pushes d before descending, so a cycle closing through a
// distinct is guarded like any other and answers false at the back edge.
func (d *typeDef) zeroSizedOn(path typePath) bool {
	if d.isEnum || path.on(d) {
		return false
	}
	path = append(path, d)
	if d.isDistinct {
		// An rt-opaque leaf has a PAYLOAD rt owns (rt.Bytes is a string), so
		// it is not zero-sized even though it looks like a marker from here.
		// Asked first, because the marker arm below would otherwise answer for
		// it. See the rtOpaque field.
		if d.rtOpaque {
			return false
		}
		// kindInvalid: marker — a marker distinct is zero-sized; a layout question, not a refusal.
		return d.inner == kindInvalid || d.inner.zeroSizedOn(path)
	}
	for _, f := range d.fields {
		if f.boxed || !f.k.zeroSizedOn(path) {
			return false
		}
	}
	return true
}

// reaches reports whether target is stored inside d, at any depth.
func (d *typeDef) reaches(target *typeDef) bool {
	return d.reachesOn(target, nil)
}

// reachesOn is reaches inside a walk already standing on path.
func (d *typeDef) reachesOn(target *typeDef, path typePath) bool {
	if path.on(d) {
		return false
	}
	path = append(path, d)
	for _, c := range d.components {
		if c == target || c.reachesOn(target, path) {
			return true
		}
	}
	return false
}

// --- building the module's type table ---------------------------------------

// buildTypes registers every struct, enum and distinct type the module declares.
//
// Two passes, because Nomi has no forward-declaration rule: `struct A { b: B }`
// may precede `struct B`, and two types may refer to each other. So every
// declaration gets its shell first and the components are resolved afterwards
// against a complete table.
func (g *gen) buildTypes(nodes []ast.Node) {
	order := make([]*typeDef, 0, len(nodes))
	for _, n := range nodes {
		var name string
		isEnum, isDistinct := false, false
		switch t := n.(type) {
		case *ast.StructDef:
			name = t.Name
		case *ast.EnumDef:
			name, isEnum = t.Name, true
		case *ast.TypeDef:
			name, isDistinct = t.Name, true
		default:
			continue
		}
		if _, dup := g.types[name]; dup {
			// Two declarations of one name is a front-end error; the second
			// shell would silently win. Refuse rather than pick.
			g.types[name].refusals = append(g.types[name].refusals,
				refusal{construct: "duplicate type declaration", detail: name})
			continue
		}
		if td, isDistinct := n.(*ast.TypeDef); isDistinct {
			if i := g.opaqueDeclared(td); i >= 0 {
				// A stdlib opaque newtype. It adopts the PROCESS-WIDE def
				// rather than getting a shell, and stays out of `order`: its
				// representation is fixed in rt, so there is nothing for
				// resolveDistinct to resolve and nothing for settleLowerable
				// to retract — and a per-gen shell would give two gens two
				// pointers for one Nomi type, which is the identity a
				// stdFunc's kinds are compared by. See opaque.go.
				g.types[name] = opaqueDefs()[i]
				continue
			}
			if i := g.stdMarkerDeclared(td); i >= 0 {
				// A stdlib nil-inner MARKER, same rule and the same reasons.
				// Beside the opaque arm rather than folded into it because the
				// two tables answer about the same declaration KIND and are
				// distinguished by whether there is an inner type at all. See
				// stdgenhost.go.
				g.types[name] = stdMarkerDefs()[i]
				continue
			}
		}
		if sd, isStructDecl := n.(*ast.StructDef); isStructDecl {
			// A stdlib opaque STRUCT, same rule and the same reasons: the
			// representation is fixed in rt, so there is nothing for
			// resolveStruct to resolve, nothing for the boxing walk to box (a
			// field's kind is a scalar and cannot reach the container), and a
			// per-gen shell would be a second def for one rt type. See
			// stdstruct.go.
			if i := g.stdStructDeclared(sd); i >= 0 {
				g.types[name] = stdStructDefs()[i]
				continue
			}
		}
		if ed, isEnumDecl := n.(*ast.EnumDef); isEnumDecl {
			// A monomorphic stdlib enum, same rule and the same reasons: the
			// representation is fixed in rt, so there is nothing for
			// resolveEnum to resolve, and a per-gen shell would be a second def
			// for one rt type. See stdenum.go.
			if i := g.stdEnumDeclared(ed); i >= 0 {
				g.types[name] = stdEnumDefs()[i]
				continue
			}
		}
		d := &typeDef{nomi: name, decl: n, line: n.LineNum(), isEnum: isEnum, isDistinct: isDistinct, // Optimistic: resolution has to resolve a field whose type is
			// declared later in the file, and settleLowerable below retracts
			// this for every declaration that turns out refused.
			lowerable: true}
		g.types[name] = d
		order = append(order, d)
	}
	// The GENERIC struct declarations, registered before any field is resolved
	// so a template whose field names another template (`struct Crate<T> { box:
	// Box<T> }`) sees a complete table — the same reason the two passes above
	// exist. Registration only: an instance is built when an instantiation is
	// reached, which is never during this loop, because a field's type argument
	// inside a generic declaration is a type PARAMETER and genericInstance
	// declines one. See generictype.go.
	g.collectGenericTemplates(nodes)
	for _, d := range order {
		switch t := d.decl.(type) {
		case *ast.StructDef:
			g.resolveStruct(d, t)
		case *ast.EnumDef:
			g.resolveEnum(d, t)
		case *ast.TypeDef:
			g.resolveDistinct(d, t)
		}
	}
	// The declarations written INSIDE a body, resolved AFTER the module's own
	// for the reason the module's two passes exist at all: a block-local
	// struct may have a field typed by a module-level declaration, and the
	// reverse cannot happen — a module-level declaration is out of scope of
	// every block, so no module type can name a block-local one. So one
	// direction suffices and the shells do not have to interleave. See
	// blocklocaltype.go.
	g.blockLocalOrder = g.collectBlockTypeDecls(nodes)
	g.resolveBlockTypeDecls()
	all := append(order[:len(order):len(order)], g.blockLocalOrder...)
	// Boxing before slot assignment: a boxed payload is a pointer, which is
	// never zero-sized, and zero-sizedness decides whether a payload gets a
	// slot at all.
	for _, d := range all {
		for i := range d.fields {
			for _, c := range appendInlineDefs(nil, d.fields[i].k) {
				if c == d || c.reaches(d) {
					d.fields[i].boxed = true
					break
				}
			}
		}
	}
	for _, d := range all {
		d.assignSlots()
	}
	// Prelude instances created while resolving these declarations join the
	// relaxation: `struct A { m: Maybe<B> }` must be refused when B is, or the
	// emitted `rt.Maybe[NomiT_B]` would name a type nobody declared. They
	// carry no refusals of their own, so they can only ever be retracted
	// through a payload. See prelude.go.
	//
	// The per-gen GENERIC STD instances join for the identical reason and it is
	// the same sentence one family over: `struct Counter { inbox: Channel<Bad> }`
	// would emit `rt.Channel[NomiT_Bad]`. A shared instance is never in either
	// order slice, because every argument admitted to the shared table is
	// lowerable by construction. See stdgenstruct.go and stdgenhost.go.
	settle := append(all[:len(all):len(all)], g.preludeOrder...)
	settle = append(settle, g.genStructOrder...)
	settle = append(settle, g.genHostOrder...)
	g.settleLowerable(settle)
	g.typeOrder = order
}

func (g *gen) resolveStruct(d *typeDef, t *ast.StructDef) {
	g.declModifiers(d, t.Decorators, t.TypeParams, t.WhereClauses, t.Items, "struct")
	if tpl := g.genericTemplates[t.Name]; tpl != nil && tpl.decl == t && tpl.why == "" {
		// An admitted generic TEMPLATE. Its fields are resolved per
		// INSTANTIATION, under a substitution, and this def is never emitted —
		// typeDecl returns early for it — so walking the fields here resolves
		// annotations nothing will use.
		//
		// It is a FABRICATED REFUSAL and not merely waste, which is why this is
		// a guard rather than an optimization. An annotation resolver may
		// `g.reject` DIRECTLY rather than returning a silent kindInvalid, and
		// several do: `struct Bag<T> { items: Set<T> }` would report
		// `generic std struct at a package-relative type argument (Set<T>)`
		// from this walk, with `T` unresolved — a refusal naming a type
		// argument the program never wrote, on a declaration this builder does
		// not need to lower, in a file that would otherwise lower. That is
		// the same failure mode as a projection inventing a blocker for a
		// representable type, arriving from the opposite direction.
		//
		// The def still carries declModifiers' `generic type`, which is what a
		// BARE `Box` mention reports through unloweredReason. See
		// generictype.go and TestGenericTemplateDeclarationFabricatesNoRefusal.
		return
	}
	g.resolveStructFields(d, t, typeParamNames(t.TypeParams))
}

// resolveStructFields is the field walk, split out from resolveStruct so a
// MONOMORPHIZED instance can reuse it without replaying declModifiers.
//
// tps is the set of type-parameter names whose mention makes an unrepresentable
// field kind somebody else's refusal. A DECLARATION passes its own parameters;
// an INSTANCE passes nil, because every parameter has been substituted and an
// invalid field kind is therefore a genuine `non-scalar field type` on the
// instantiation. That difference is the whole reason the split exists.
func (g *gen) resolveStructFields(d *typeDef, t *ast.StructDef, tps map[string]bool) {
	for _, f := range t.Fields {
		k := g.typeOf(f.TypeAnnotation)
		// kindInvalid: reports — records `non-scalar field type` on the declaration.
		if k == kindInvalid {
			if namesTypeParam(f.TypeAnnotation, tps) {
				// `struct Box<T> { value: T }`. The declaration is ALREADY
				// refused as `generic type` by declModifiers, and `T` is not a
				// type this builder cannot represent — it is not a type at
				// all. Reporting it again under a type-shaped key would file
				// one obstacle under two names. Keeping one obstacle under
				// one key is the same rule foreign.go's blockedMirror and
				// impl.go's implCall are written to.
				continue
			}
			d.refusals = append(d.refusals, refusal{
				construct: "non-scalar field type",
				detail:    d.nomi + "." + f.Name + ": " + typeText(f.TypeAnnotation),
				probe:     []ast.Node{f.Default},
			})
			continue
		}
		d.fields = append(d.fields, fieldDef{nomi: f.Name, k: k, deflt: f.Default})
		d.note(k)
	}
}

// typeParamNames is a declaration's own type-parameter names, as a set.
func typeParamNames(tps []ast.TypeParam) map[string]bool {
	if len(tps) == 0 {
		return nil
	}
	out := make(map[string]bool, len(tps))
	for _, tp := range tps {
		out[tp.Name] = true
	}
	return out
}

// namesTypeParam reports whether an annotation is, or is built over, one of
// the enclosing declaration's type parameters.
//
// Recursive because `items: List<T>` is the same gap as `item: T`: the
// declaration is generic and that is the whole reason neither has a kind.
func namesTypeParam(te ast.TypeExpr, tps map[string]bool) bool {
	if len(tps) == 0 || te == nil {
		return false
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		return tps[t.Name]
	case *ast.GenericType:
		for _, a := range t.Params {
			if namesTypeParam(a, tps) {
				return true
			}
		}
	case *ast.FuncType:
		for _, a := range t.Params {
			if namesTypeParam(a, tps) {
				return true
			}
		}
		return namesTypeParam(t.Return, tps)
	}
	return false
}

func (g *gen) resolveEnum(d *typeDef, t *ast.EnumDef) {
	g.declModifiers(d, t.Decorators, t.TypeParams, t.WhereClauses, t.Items, "enum")
	if tpl := g.genericTemplates[t.Name]; tpl != nil && tpl.decl == t && tpl.why == "" {
		// An admitted generic TEMPLATE. Its variants are resolved per
		// INSTANTIATION, under a substitution, and this def is never emitted —
		// typeDecl returns early for it — so walking the variants here resolves
		// annotations nothing will use.
		//
		// It is a FABRICATED REFUSAL and not merely waste, which is the same
		// reason resolveStruct's identical guard is a guard rather than an
		// optimization, arriving one declaration kind over. Without this
		// clause `enum Wrapper<T> { Wrapped T }` would report
		// `non-scalar variant payload | Wrapper.Wrapped carries T` from this
		// walk with `T` unresolved — a refusal naming a payload type the
		// program never wrote, on a declaration this builder does not need to
		// lower, in a file that would otherwise lower.
		//
		// The def still carries declModifiers' `generic type`, which is what a
		// BARE `Wrapper` mention reports through unloweredReason. See
		// generictype.go.
		return
	}
	g.resolveEnumVariants(d, t, typeParamNames(t.TypeParams))
}

// resolveEnumVariants is the variant walk, split out of resolveEnum so a
// MONOMORPHIZED instance can reuse it without replaying declModifiers.
//
// Exactly the resolveStruct / resolveStructFields split, and the parameter
// means exactly the same thing: tps is the set of type-parameter names whose
// mention makes an unrepresentable payload kind somebody else's refusal. A
// DECLARATION passes its own parameters; an INSTANCE passes nil, because every
// parameter has been substituted and an invalid payload kind is therefore a
// genuine `non-scalar variant payload` on the instantiation.
//
// The variant-count fence lives HERE rather than beside declModifiers so both
// callers get it. It is a property of the declaration and therefore the same
// answer for every instance, but a fence only one of two entry points passes
// through is a fence with a hole, and the tag is a uint8 either way.
func (g *gen) resolveEnumVariants(d *typeDef, t *ast.EnumDef, tps map[string]bool) {
	if len(t.Variants) > maxEnumTag {
		d.refusals = append(d.refusals, refusal{
			construct: "enum with too many variants",
			detail:    d.nomi + " has " + strconv.Itoa(len(t.Variants)) + " variants; the tag is a uint8 and 0 is reserved",
		})
		return
	}
	for _, v := range t.Variants {
		vd := variantDef{nomi: v.Name, tag: len(d.variants) + 1, kind: v.Kind}
		switch v.Kind {
		case "bare":

		case "positional":
			// A TUPLE payload — `Pos (Int, Int)`, spelled `*ast.FuncType` with a
			// nil Return — needs no arm of its own here: `typeOf` routes exactly
			// that node shape to `structuralTypeOf`, which answers for a tuple
			// annotation. What the payload's TYPE cannot say is how the
			// value is written and how the slot is stored; both live elsewhere —
			// see enumtuple.go and assignSlots.
			k := g.typeOf(v.DataTypeExpr)
			// kindInvalid: reports — records `non-scalar variant payload` on the declaration.
			if k == kindInvalid {
				if namesTypeParam(v.DataTypeExpr, tps) {
					// `enum Wrapper<T> { Wrapped T }` on a declaration whose
					// instantiation this builder does NOT admit. The
					// declaration is already refused `generic type` by
					// declModifiers, and `T` is not a type this builder cannot
					// represent — it is not a type at all. resolveStructFields'
					// rule verbatim, one declaration kind over: reporting it
					// again under a type-shaped key files one gap under two
					// names and inflates the second. It measurably did:
					// `non-scalar variant payload` has exactly two corpus sites
					// and BOTH are this shape.
					continue
				}
				d.refusals = append(d.refusals, refusal{
					construct: "non-scalar variant payload",
					detail:    d.nomi + "." + v.Name + " carries " + typeText(v.DataTypeExpr),
				})
				continue
			}
			vd.payloads = append(vd.payloads, payload{k: k})

		case "struct":
			bad := false
			for _, f := range v.Fields {
				k := g.typeOf(f.TypeAnnotation)
				// kindInvalid: reports — records `non-scalar variant payload` on the declaration.
				if k == kindInvalid {
					if namesTypeParam(f.TypeAnnotation, tps) {
						// The struct-shaped variant's half of the rule above.
						// Both arms or neither: one gap reported at one of two
						// payload spellings is the missing-arm defect, and the
						// corpus happens to contain only the positional one.
						continue
					}
					d.refusals = append(d.refusals, refusal{
						construct: "non-scalar variant payload",
						detail:    d.nomi + "." + v.Name + "." + f.Name + ": " + typeText(f.TypeAnnotation),
						probe:     []ast.Node{f.Default},
					})
					bad = true
					continue
				}
				vd.payloads = append(vd.payloads, payload{nomi: f.Name, k: k, deflt: f.Default})
			}
			if bad {
				continue
			}

		case "embedded":
			// `embeds T`, where T is a struct, a distinct type or an ENUM. T
			// may be declared in a SIBLING FILE, in which case it is reached as
			// a mirror and its layout is the declaring package's — see
			// foreign.go. A type from anywhere else has no *typeDef and is
			// refused by name.
			//
			// The payload KIND is not always T, and getting that wrong is a
			// silent wrong-value bug rather than a compile error:
			//
			//   embeds <struct>            payload is the struct
			//   embeds <wrapping distinct> payload is the DISTINCT, and a
			//                              `case` arm binds its INNER value,
			//                              because the checker's contract is
			//                              that the variant's payload type is
			//                              the wrapped type (spec §8)
			//   embeds <marker>            payload is the marker, which is
			//                              zero-sized, so it gets no slot and
			//                              a binding must materialize it
			//   embeds <enum>              payload is the enum, and no value
			//                              widens into it — see below
			//
			// The distinct is stored rather than its inner so the enum's Go
			// field keeps the distinct's nominal identity; the single unwrap
			// lives at one place, the pattern.
			//
			// # THE EMBEDDED ENUM
			//
			// Unlike `embeds <struct>`, which widens, an embedded enum takes
			// no value: `f(Shape.Circle(1.5))` where a Drawable is wanted is
			// a front-end error, and `Drawable.Shape(Shape.Circle(1.5))`
			// traps ("cannot call Shape"). Its one value is
			// `Drawable.Shape{}`, which has no payload; the
			// VM lays the variant out bare and builds and matches it that way
			// (irEmbedsEnum). See testdata/enum_embeds_enum.nomi.
			st, ok := v.EmbeddedTypeExpr.(*ast.SimpleType)
			if !ok {
				d.refusals = append(d.refusals, refusal{
					construct: "enum embeds variant",
					detail:    d.nomi + "." + v.Name + " embeds " + typeText(v.EmbeddedTypeExpr),
				})
				continue
			}
			inner, found := g.namedType(st.Name)
			if !found || !inner.lowerable {
				d.refusals = append(d.refusals, refusal{
					construct: "enum embeds variant",
					detail:    d.nomi + "." + v.Name + " embeds " + st.Name + ", which is not a lowerable type",
				})
				continue
			}
			vd.embeds = inner
			vd.payloads = append(vd.payloads, payload{k: named(inner)})

		default:
			d.refusals = append(d.refusals, refusal{
				construct: "enum variant kind",
				detail:    d.nomi + "." + v.Name + " is " + v.Kind,
			})
			continue
		}
		for _, p := range vd.payloads {
			d.note(p.k)
		}
		d.variants = append(d.variants, vd)
	}
}

// resolveDistinct handles `type Name Inner` and the zero-sized `type Name`.
//
// A distinct type is nominally its own type and structurally its inner one,
// which is precisely a Go DEFINED type: `type NomiT_Meters int64` is not
// assignable to or from `int64` without a conversion, so the analyzer's
// nominal rule and Go's are the same rule and there is nothing to enforce
// twice. The zero-sized marker gets `struct{}` instead, so an enum embedding
// one pays nothing for it.
//
// An OPAQUE distinct reaches here too: opacity is a use-site rule the front end
// enforces, so it changes nothing about the layout. The one thing it does
// change — the `<opaque T>` auto-Debug rendering — is produced in the ANALYZER
// as ordinary Nomi source. See declModifiers and opaque.go.
//
// A STDLIB opaque distinct never reaches here at all: buildTypes gives it the
// process-wide def whose Go type rt declares. Only a USER declaration does.
func (g *gen) resolveDistinct(d *typeDef, t *ast.TypeDef) {
	g.declModifiers(d, t.Decorators, nil, nil, t.Items, "type")
	if t.InnerTypeExpr == nil {
		// A marker. inner stays kindInvalid, which zeroSized reads as "no
		// storage at all" — distinct from a refusal, which also leaves it
		// kindInvalid but records a reason alongside.
		return
	}
	k := g.typeOf(t.InnerTypeExpr)
	// kindInvalid: reports — records `non-scalar distinct inner type` on the declaration.
	if k == kindInvalid {
		d.refusals = append(d.refusals, refusal{
			construct: "non-scalar distinct inner type",
			detail:    "type " + d.nomi + " " + typeText(t.InnerTypeExpr),
		})
		return
	}
	if k.def != nil && k.def.isEnum {
		// `type T SomeEnum` would make T structurally a tagged struct, and
		// `case` on a T would have to decide whether a variant pattern
		// addresses T or the enum inside it. Refused rather than guessed.
		d.refusals = append(d.refusals, refusal{
			construct: "distinct over an enum",
			detail:    "type " + d.nomi + " " + k.nomi(),
		})
		return
	}
	d.inner = k
	d.note(k)
}

// maxEnumTag is the largest tag a uint8 tag field can carry. Tag 0 is reserved
// invalid, so an enum may have at most 255 variants.
const maxEnumTag = 255

// declModifiers records the declaration-level constructs this builder refuses,
// each under its own name so the tally can size them separately.
func (g *gen) declModifiers(d *typeDef, decs []ast.Decorator, tps []ast.TypeParam, wcs []ast.WhereConstraint, items []ast.Node, keyword string) {
	if len(tps) > 0 || len(wcs) > 0 {
		d.refusals = append(d.refusals, refusal{construct: "generic type", detail: keyword + " " + d.nomi})
	}
	// `opaque` is deliberately NOT refused. The name-only auto-Debug
	// rendering for an opaque type is produced in the ANALYZER, as an ordinary Nomi
	// `impl Debug for T { fn inspect(value: T): String { "<opaque T>" } }` node
	// appended before the builder is handed the tree
	// (analysis.synthesizeOpaqueNameOnlyDebug), so the builder lowers it
	// through the impl path it already has and there is nothing to reproduce.
	// TestOpaqueDebugRenderingIsSynthesized pins that the synthesis is where
	// this claim says it is, so moving it into the builder fails a test rather
	// than silently reverting the rendering.
	//
	// Everything else `opaque` means is a USE-SITE rule the front end enforces:
	// the constructor, the unwrap and the destructuring are private to the
	// declaring file, so a program that reaches one from outside never gets as
	// far as codegen. See opaque.go.
	for _, dec := range decs {
		if dec.Name == "derive" {
			// `derive Equatable for P` records itself on P as a decorator AND
			// is synthesized into an ordinary `impl Equatable for P` block,
			// which is lowered through the same path a hand-written impl
			// takes. The decorator is therefore metadata about work already
			// done, not a construct still owed: refusing it would report a gap
			// that does not exist and would make the type unlowerable for it.
			// If the synthesized body does NOT lower, its impl is marked
			// unlowerable and every site that would dispatch to it refuses at
			// its own position. See impl.go.
			continue
		}
		d.refusals = append(d.refusals, refusal{construct: "decorator", detail: d.nomi + " @" + dec.Name})
	}
	if len(items) > 0 {
		d.refusals = append(d.refusals, refusal{
			construct: "type body item",
			detail:    keyword + " " + d.nomi,
			probe:     items,
		})
	}
	// An attached `//!` test is deliberately NOT refused here. It is the
	// `derive` argument above applied to the other kind of metadata, and it is
	// stdStructSpec.matches' argument verbatim: the prompt is a test case of its
	// own (tests.go's attachedTestCases), retained or declined as its body
	// allows. Charging the TYPE for it would double-charge it, and the second
	// charge cascades: the def would stop being lowerable and every mention of
	// it would refuse under a type-shaped key instead.
	//
	// A `//!` is a line in a doc comment. It cannot change what the type IS,
	// which is exactly what separates it from `items` above — a type-body item
	// is layout-relevant and stays refused.
}

// assignSlots lays out an enum's payload storage.
//
// One slot per DISTINCT UNDERLYING GO TYPE, reused across variants because only
// one variant is live at a time, and never reused within one variant because a
// struct-shaped variant's payloads are live together. The comparison below —
// `d.slots[s].k == p.k` — is the whole enforcement of the dedup key: `kind` is
// `{tag, *typeDef}`, so two payloads share a slot only when they are the same
// Go type. `Int` and `Float` differ in the tag; two same-named structs from two
// modules differ in the pointer. Neither can be merged by coincident layout.
//
// BOXING GOES THROUGH appendInlineDefs, which is the SAME rule buildTypes
// applies to a struct FIELD, and the two being one rule is the point. A check
// on `p.k.def` alone would see only a payload whose type is NAMED — `def` is
// nil for every structural kind — so `enum Node { Leaf Int; Branch (Node,
// Int, Node) }` would get an inline slot of a struct containing itself, a type
// of infinite size, rather than a boxed one.
//
// appendInlineDefs is the right rule and not merely a wider one: it follows
// exactly the kinds that are stored INLINE (tagNamed, tagTuple, tagAnonStruct)
// and deliberately does NOT follow a list or a map, because those are already
// indirect and boxing them would add a pointer to a pointer.
func (d *typeDef) assignSlots() {
	for i := range d.variants {
		v := &d.variants[i]
		used := map[int]bool{}
		for j := range v.payloads {
			p := &v.payloads[j]
			if p.k.zeroSized() {
				// Contributes nothing, so it is stored nowhere: a zero-sized
				// value carries no information and reconstructs from its type
				// alone. This is what lets an `embeds` of a zero-sized type
				// cost zero bytes.
				p.slot = -1
				continue
			}
			p.slot = -1
			for s := range d.slots {
				if d.slots[s].k == p.k && !used[s] {
					p.slot = s
					break
				}
			}
			if p.slot < 0 {
				boxed := false
				for _, c := range appendInlineDefs(nil, p.k) {
					if c == d || c.reaches(d) {
						boxed = true
						break
					}
				}
				d.slots = append(d.slots, slotDef{k: p.k, boxed: boxed})
				p.slot = len(d.slots) - 1
			}
			used[p.slot] = true
			label := v.nomi
			if p.nomi != "" {
				label += "." + p.nomi
			}
			d.slots[p.slot].users = append(d.slots[p.slot].users, label)
		}
	}
}

// settleLowerable propagates unlowerability from a component to its container.
//
// A struct whose field type was refused cannot be emitted either, and every use
// of it has to be refused rather than emitted against a type that was never
// written. Relaxation rather than a topological order because the graph may
// contain cycles.
func (g *gen) settleLowerable(order []*typeDef) {
	for _, d := range order {
		d.lowerable = len(d.refusals) == 0
	}
	for changed := true; changed; {
		changed = false
		for _, d := range order {
			if !d.lowerable {
				continue
			}
			for _, c := range d.mentions {
				if !c.lowerable {
					d.lowerable = false
					d.blockedBy, d.blocker = c.nomi, c
					changed = true
					break
				}
			}
		}
	}
}

// typeOf is the kind a declared type annotation names, or kindInvalid.
//
// Four forms name a lowerable type. *ast.SimpleType names a scalar, a declared
// struct/enum/distinct, or an interface existential. *ast.FuncType names a
// function type; the SAME node with a nil Return names a TUPLE type, because
// `(Int, String)` and `(Int) -> String` differ only in the arrow.
// *ast.GenericType names `List<T>`. All three of the latter are STRUCTURAL —
// their identity is interned (see composite.go), not declared.
//
// A *ast.QualifiedType names a NAMESPACED declaration — `pub enum Probe.Reading`
// declared beside its owner `Probe` — and resolves through the same table under
// its whole dotted name. See namespacedType in sigreason.go for why the lookup
// cannot be confused with an `Enum.Variant` reading.
//
// An *ast.AnonStructType names a RECORD — structural like the tuple form, and
// canonicalized by field name so `{x: Int, y: Int}` and `{y: Int, x: Int}` are
// one kind. See anonstruct.go.
//
// A SelfType or any other GenericType is by construction outside the subset;
// sigreason.go names WHICH of those it was, so a signature position reports the
// type's own gap rather than the position's shape.
func (g *gen) typeOf(te ast.TypeExpr) kind {
	if ft, isFunc := te.(*ast.FuncType); isFunc {
		if ft.Return == nil {
			return g.structuralTypeOf(ft)
		}
		params := make([]kind, len(ft.Params))
		for i, p := range ft.Params {
			params[i] = g.typeOf(p)
		}
		return g.funcKind(params, g.typeOf(ft.Return))
	}
	if gt, isGeneric := te.(*ast.GenericType); isGeneric {
		// `Maybe<Int>`, `Result<Int, String>`. Ahead of structuralTypeOf
		// because a prelude enum is NAMED — its identity is a declaration —
		// while everything structuralTypeOf answers for is interned. See
		// prelude.go.
		if k, isPrelude := g.preludeTypeOf(gt); isPrelude {
			return k
		}
		// `Set<Int>` — a GENERIC std struct, named for the same reason and
		// resolved the same way. Beside the prelude arm rather than after
		// structuralTypeOf because it is the same question about a fourth
		// declaration kind, and structuralTypeOf would answer `List`/`Map` for a
		// name it does not know rather than declining. See stdgenstruct.go.
		if k, isGenStruct := g.stdGenStructTypeOf(gt); isGenStruct {
			return k
		}
		// `Sender<Int>` — a GENERIC std HOST type, beside the two arms above
		// because it is the same question about a FIFTH declaration kind, an
		// `*ast.ExternType`. See stdgenhost.go.
		if k, isGenHost := g.stdGenHostTypeOf(gt); isGenHost {
			return k
		}
		// `Box<Inner>` — a USER generic struct, MONOMORPHIZED per
		// instantiation. LAST of the three named arms, so a program declaring
		// its own `Set` or `Maybe` cannot shadow the std or prelude family
		// through this door; the analyzer's own precedence is the reverse only
		// for names it resolves, and neither of the arms above answers for a
		// name it has no anchor for. See generictype.go.
		if k, isUserGeneric := g.userGenericTypeOf(gt.Name, gt.Params); isUserGeneric {
			return k
		}
	}
	if qt, isQualified := te.(*ast.QualifiedType); isQualified {
		// `shapes.Holder<Int>` — a generic template named through an imported
		// module's qualifier. Ahead of namespacedType, which declines a
		// generic member outright (qualifiedTypeParts requires a plain name)
		// and leaves `non-local type`. See foreigngeneric.go.
		if gt, isGeneric := qt.Member.(*ast.GenericType); isGeneric {
			if k, isUserGeneric := g.userGenericTypeOf(qt.Module+"."+gt.Name, gt.Params); isUserGeneric {
				return k
			}
		}
		// A dotted name is one declaration's whole name, so a MISS refuses
		// rather than falling through to structuralTypeOf — a structural
		// answer for `Probe.Reading` would be an answer about a type nobody
		// declared.
		// `shapes.Board` — a module-level `typealias` named through its
		// file's qualifier. Transparent, so no def is involved; see
		// typealias.go.
		if k, isAlias := g.qualifiedAliasKind(qt); isAlias {
			return k
		}
		d, found := g.namespacedType(qt)
		if !found {
			// `random.Error` — a std type named through its module's qualifier.
			// See modulequaltype.go.
			if k, isStd := g.qualifiedStdKind(qt); isStd {
				return k
			}
		}
		// kindInvalid: reports — an undeclared or refused dotted name is named
		// by rejectTypeAnnotation, which reaches the same def.
		if !found || !d.lowerable {
			return kindInvalid
		}
		return named(d)
	}
	if at, isAnon := te.(*ast.AnonStructType); isAnon {
		return g.anonStructTypeOf(at)
	}
	st, ok := te.(*ast.SimpleType)
	if !ok {
		return g.structuralTypeOf(te)
	}
	switch st.Name {
	case "Int":
		return kindInt
	case "Float":
		return kindFloat
	case "String":
		return kindString
	case "Bool":
		return kindBool
	case "Unit":
		return kindUnit
	}
	// A type parameter of the generic STRUCT being monomorphized, substituted
	// by its argument. Asked FIRST of the three shadowing lookups because an
	// instance's field walk is the innermost activation there is: `struct
	// Box<T> { item: T }` instantiated at `Int` inside a generic function that
	// also declares a `T` must read the STRUCT's argument. See generictype.go.
	if k, isSubst := g.genericSubstKind(st.Name); isSubst {
		return k
	}
	// A BOUNDED type parameter of the generic function being lowered. Asked
	// ahead of every declaration lookup because a type parameter SHADOWS a
	// same-named type for the length of its activation — which is what
	// installTypeParamConcrete's own shadowing rule says — and asked at all
	// only while a generic signature or body is open, so nothing else pays for
	// it. See dict.go: `T` is an existential over its bound, so `List<T>` and
	// `(T) -> Bool` follow from this one arm rather than needing their own.
	if k, isTypeParam := g.dictTypeParamKind(st.Name); isTypeParam {
		return k
	}
	// A type declared in an enclosing BLOCK. Ahead of the module table for the
	// reason blocklocaltype.go states: innermost-first is the analyzer's own
	// resolution order.
	if k, isAlias := g.blockLocalAlias(st.Name); isAlias {
		// A `typealias` is TRANSPARENT: `Count` IS `Int`, so this returns the
		// target's kind and no def is involved. kindInvalid propagates, and the
		// alias DECLARATION is what named the unrepresentable target.
		return k
	}
	if d, isBlockLocal := g.blockLocalNamed(st.Name); isBlockLocal {
		if d.lowerable {
			return named(d)
		}
		return kindInvalid
	}
	if d, found := g.types[st.Name]; found {
		if d.lowerable {
			return named(d)
		}
		return kindInvalid
	}
	// A MODULE-LEVEL `typealias`, which is TRANSPARENT exactly as the
	// block-local arm above is: `Count` IS `Int`, so this returns the target's
	// kind and no def is involved. Beside the module table because an alias and
	// a type declaration cannot share a name, and ABOVE every stdlib lookup so
	// a program's own alias wins. See typealias.go.
	if k, isAlias := g.moduleAliasKind(st.Name); isAlias {
		return k
	}
	if d, isOpaque := g.opaqueNamed(st.Name); isOpaque {
		// A stdlib opaque newtype this module MENTIONS without declaring:
		// `timer.sleep(d: Duration)`, or a user program's `Duration` parameter.
		// Its Go type is declared in rt, so it needs no mirror and no import.
		// See opaque.go.
		return named(d)
	}
	if d, isStdEnum := g.stdEnumNamed(st.Name); isStdEnum {
		// A monomorphic stdlib enum this module MENTIONS without declaring:
		// `fn compare(a: Priority, b: Priority): Ordering`. Its Go type is
		// declared in rt, so it needs no mirror and no import. Asked beside
		// the opaque lookup because it is the same question about a different
		// declaration kind. See stdenum.go.
		return named(d)
	}
	if d, isStdStruct := g.stdStructNamed(st.Name); isStdStruct {
		// A stdlib opaque STRUCT this module MENTIONS without declaring: a user
		// program's `DateTime` parameter or binding annotation. Its Go type is
		// declared in rt, so it needs no mirror and no import. See
		// stdstruct.go.
		return named(d)
	}
	if d, isStdHost := g.stdHostNamed(st.Name); isStdHost {
		// A stdlib `pub host type` this module MENTIONS: a `Bytes` parameter or
		// binding annotation. Its Go type is declared in rt, so it needs no
		// mirror and no import. Asked beside the three lookups above because it
		// is the same question about a fourth declaration kind — and unlike
		// them there is no DECLARED case to ask first, because this builder has
		// no declaration path for `*ast.ExternType`. See stdhost.go.
		return named(d)
	}
	if d, isMarker := g.stdMarkerNamed(st.Name); isMarker {
		// A stdlib nil-inner MARKER this module MENTIONS: `ChannelClosed` as
		// `Sender.send`'s error type. Beside the four lookups above because it
		// is the same question about a `*ast.TypeDef` with no inner, which
		// opaqueNamed's table cannot hold. See stdgenhost.go.
		return named(d)
	}
	if d, isHost := g.hostTypeNamed(st.Name); isHost {
		// A USER `host type` bound to a Go type in a `gopkg` package:
		// `opaque type RawBox go ffi.Box`. Its Go type is declared in the
		// user's own Go package, so it needs no mirror: hostTypeDef interns one
		// def per bound Go type, process-wide.
		//
		// BELOW all five stdlib families and above nothing that could shadow
		// it. The order is free by construction rather than by measurement:
		// the table this reads is built ONLY from this module's own
		// `*ast.ExternType` nodes carrying a `go alias.Symbol` binding, and no
		// std declaration carries one — so the populations are disjoint and
		// neither order can change an answer. Placed here anyway, because a
		// FUTURE std `host type` acquiring a Go binding should keep its own
		// family's representation rather than being adopted by this one. See
		// hostpkg.go.
		return named(d)
	}
	if d, found := g.ifaces[st.Name]; found {
		// An interface-typed position is an EXISTENTIAL: the concrete type is
		// erased and only the interface survives. See impl.go.
		if d.lowerable {
			return existential(d)
		}
		return kindInvalid
	}
	// A name this file does not declare may still be a sibling file's
	// INTERFACE, and an existential over one is `rt.Dyn` in every package —
	// the same Go type, so nothing has to be mirrored except the interface's
	// own method shape. Asked ahead of the foreign TYPE lookup only because
	// the two populations are disjoint and this is the cheaper question; the
	// order carries no meaning. See existential.go.
	if d, isForeign := g.foreignIface(st.Name); isForeign {
		if d.lowerable {
			return existential(d)
		}
		return kindInvalid
	}
	// A STDLIB interface this module MENTIONS: `fn show(d: Display)`. Its
	// method shape is a spec row and its dispatch table lives in rt, so —
	// exactly like the opaque and monomorphic-enum lookups above — it needs no
	// mirror and no import. Asked AFTER the sibling-file lookup so a user
	// declaration of the same name always wins, which is the analyzer's own
	// "project wins" shadowing rule and is what makes a name-keyed collapse
	// impossible here. See stdiface.go.
	if d, isStd := g.stdIfaceNamed(st.Name); isStd {
		return existential(d)
	}
	// A name this file does not declare may still be another user FILE's type.
	// Asked LAST, so a local declaration and a local interface both win, and
	// asked silently: typeOf never rejects — the caller names the position, and
	// a mirror's own reason is reported by the sites that construct or match a
	// value of it. See foreign.go.
	if d, isForeign := g.foreignType(st.Name); isForeign && d.lowerable {
		return named(d)
	}
	return kindInvalid
}

func typeText(te ast.TypeExpr) string {
	if te == nil {
		return "<none>"
	}
	return te.TypeString()
}

// --- emitting the declarations ---------------------------------------------

// typeDecl replays a declaration's recorded refusals at its own position and,
// if it survived them, emits its Go type declaration.
func (g *gen) typeDecl(n ast.Node) {
	var name string
	switch t := n.(type) {
	case *ast.StructDef:
		name = t.Name
	case *ast.EnumDef:
		name = t.Name
	case *ast.TypeDef:
		name = t.Name
	}
	if d := g.types[name]; d != nil && d.rtDeclared {
		// A stdlib opaque newtype: its Go type is hand-written in rt, so there
		// is nothing to emit and no refusal to replay. Ahead of the identity
		// check below because the shared def carries no declaration node — it
		// outlives the AST that anchored it. See opaque.go.
		return
	}
	if tpl := g.genericTemplates[name]; tpl != nil && tpl.why == "" {
		// A generic struct declaration whose instantiations lower. There is
		// nothing to EMIT — a template is not a Go type, only its instances are
		// — and nothing to REPLAY: the def's `generic type` refusal is exactly
		// the gap generictype.go closes, and reporting it here would refuse
		// every file that declares one however well its instances lower.
		//
		// The refusal is left ON the def rather than removed, because a bare
		// `Box` mention (a generic name with no arguments, which is not a type)
		// resolves through namedType to this def and needs a reason to report.
		// unloweredReason reads the LIST; removing the entry there would make
		// it synthesize a placeholder instead, which is the cascade
		// cascadereason_test.go exists to prevent. See generictype.go and
		// TestGenericTemplateWithoutArgumentsKeepsItsOwnRefusal.
		return
	}
	d := g.types[name]
	if d == nil || d.decl != n {
		// A duplicate declaration: the table kept the first one and recorded
		// the refusal there.
		g.reject("duplicate type declaration", name, n)
		return
	}
	g.replayTypeRefusals(d, n)
}

// replayTypeRefusals reports at `at` everything the declaration was refused
// for while its table entry was built, and probes each refusal's subtree.
//
// Split from typeDecl because a BLOCK-LOCAL declaration replays at its
// statement position (blockTypeDeclStmt) while its Go type is emitted at
// package level, so the two halves do not happen together.
func (g *gen) replayTypeRefusals(d *typeDef, at ast.Node) {
	for _, r := range d.refusals {
		g.reject(r.construct, r.detail, at)
	}
}

// --- construction and access ------------------------------------------------

// --- `embeds` subtype coercion ----------------------------------------------

// --- the untyped literal at an equality -------------------------------------

// untypedLiteral reports whether k is one of the five literals whose type the
// CHECKER solves from context rather than the value carrying it: `[]`, a bare
// `None`, `Map.empty()`, `#{}` and `#[]`.
//
// The five are one concept and are listed together everywhere they appear
// (native.go's tag comments, stdprelude.go's signature rule, generic.go's type
// argument rule), so they are asked about through one predicate rather than
// repeated switches that can drift apart. `Map.empty()` has no corpus site at
// the equality position, which is exactly the hole a partial list would
// leave.
func untypedLiteral(k kind) bool {
	switch k.tag {
	case tagEmptyList, tagBareNone, tagEmptyMap, tagEmptySet, tagEmptyVector:
		return true
	}
	return false
}

// embedsVariant finds the variant of `want` that embeds a value of kind `from`.
//
// Split out of coerce so a caller can ASK whether the widening applies without
// performing it. coerce is not side-effect free — a boxed payload takes the
// address of a temporary, which emits a statement — so a speculative call would
// leave stray Go behind. The list-literal element scan (collections.go's
// elementKind) asks this question once per candidate.
//
// An enum names each `embeds` variant after the type it embeds, and variant
// names are unique, so the match is unambiguous by construction rather than by
// first-wins.
func embedsVariant(want, from kind) (*typeDef, *variantDef, bool) {
	d := want.def
	if d == nil || !d.isEnum || from.def == nil {
		return nil, nil, false
	}
	for i := range d.variants {
		if d.variants[i].embeds == from.def {
			return d, &d.variants[i], true
		}
	}
	return nil, nil, false
}

// --- distinct construction --------------------------------------------------

// isSynthesized reports whether a node was produced by the front end rather
// than written by a programmer.
//
// It matters here because SynthesizeUniversalDebug appends an `impl Debug for
// T` block for EVERY declared type, so refusing "impl block" would make every
// struct and enum declaration unlowerable — and would blame a fabricated
// position (the synth band is around 2^30) for a construct nobody wrote.
// Dropping them is safe only because every site that could DISPATCH to one is
// itself refused: rendering a named value needs `impl Display`, `==` on a named
// type needs `impl Equatable`, and a type-qualified call is refused by name. If
// any of those is ever lowered, the impl it dispatches to must be lowered in
// the same change.
func isSynthesized(n ast.Node) bool {
	return analysis.IsSynthesizedLine(n.LineNum())
}
