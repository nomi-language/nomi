package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Named types: a Nomi `struct`, `enum` or distinct type as a `*typeDef`.
//
// # Identity is a pointer, not a string
//
// A type's runtime identity as a string assembled at construction sites and
// re-parsed at lookup sites fails silently: shortening `json.Json.DecodeError`
// to `DecodeError` would make it share a dispatch slot with any other file's
// `DecodeError`. Here a Nomi type's identity is the `*typeDef` its declaration
// produced: `kind` carries that pointer, kind equality is Go pointer equality,
// and two same-named structs declared in two modules are two pointers. There
// is no string anywhere in the path, so there is nothing to collapse.
//
// # Structs
//
// The fields, in declaration order. A field default is a Nomi expression
// evaluated at each construction site that omits it, in the declaring
// module's scope, not the constructor's, so only the declaring file's gen
// lowers it (fieldDef.deflt).
//
// # Enums: variants, tags and payload slots
//
// variants[i].tag is i+1, and 0 is reserved. assignSlots gives each payload a
// slot index, and two rules hold for it:
//
// **The slot key is the payload's kind, not its layout.** The key is a `kind`
// value compared with `==`, and `kind` is `{tag, *typeDef}`, so
// `kindInt == kindFloat` is false and the two never share a slot. See
// assignSlots, and TestEnumSlotsDedupByGoTypeNotLayout.
//
// A slot is reused across variants, where only one variant is live at a time,
// and never inside one, because a struct-shaped variant's two `Int` fields are
// live together. So the slot count for a kind is the max over variants of how
// many payloads of that kind the variant carries.
//
// **A payload of Unit or of a marker gets no slot.** Its slot is -1
// (irSlotlessPayload, which the retention walk asks too). The builder makes
// such a payload from its type where a pattern binds it (irSlotlessValue).
// Every other payload has a slot, a field-less struct and a tuple of Units
// among them.
//
// # Recursion
//
// A field or payload whose type can reach its own container, directly or
// transitively, is marked boxed (boxFields, assignSlots). Cycles are found by
// reachability over the resolved component graph. The predicates that decide
// whether a struct or enum is a retained value read the mark to find a
// recursive edge (irCyclicNominal; irstruct.go, irvariantvalues.go).

// typeDef is one Nomi struct or enum declaration.
//
// A pointer to one of these is the type's identity: see the comment above.
// Nothing keys off the Nomi name.
type typeDef struct {
	// nomi is the Nomi spelling, for refusal text.
	nomi string
	// layoutGroup is the type group whose layout phases will box and slot
	// this def: a mirror made while its owner's fields were unresolved, or a
	// generic instance built then (deferInstLayout). Nil once it is laid out.
	layoutGroup *typeGroup
	// decl is the declaration node, for refusal positions.
	decl ast.Node
	line int

	isEnum bool
	// isDistinct marks a `type Name Inner` wrapper or a `type Name` marker.
	// A distinct type is nominally its own type and structurally its inner
	// one. The VM holds a wrapping distinct as a record over its inner value
	// (ir.Make's MakeDistinct).
	isDistinct bool
	// spread says a distinct's inner type is written as a tuple of its
	// parts, `type Coord (Int, Int)`: the checker's record
	// (analysis.DistinctType.WrittenAsParts; ir.ValType.SetSpread).
	spread bool
	// inner is a wrapping distinct's wrapped kind, kindInvalid for a
	// zero-sized marker (`type Expired`), which has no inner at all.
	inner kind
	// fields are a struct's fields in declaration order.
	fields []fieldDef
	// variants are an enum's variants; variants[i].tag is i+1.
	variants []variantDef
	// slots are an enum's payload slots, deduped by identical kind.
	slots []slotDef

	// refusals are the reasons this declaration itself was not lowered.
	refusals []refusal
	// blocker is the type whose refusal makes this one unlowerable, when this
	// declaration is fine and a component is not. The pointer is what lets a
	// use site report the ROOT reason. See declRefusal.
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
	// type arguments, both nil for an ordinary declaration. Read when a
	// mirror has to rebuild the instance over another gen's kinds for the
	// arguments. See prelude.go and foreign.go's importNamed.
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
	// built, and by genStructOf, so an operation arm identifies a Set by spec
	// pointer instead of by name. See stdgenstruct.go.
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
	// an operation arm identifies a `Sender` by spec pointer rather than by
	// name. See stdgenhost.go.
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
	// genericTypes is genericArgs as the checker's types, from the first
	// construction or projection of the instance that read them (a type
	// the checker solved), nil until one has (substFrame).
	genericTypes []analysis.Type
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
	// rtDeclared marks a process-wide def of a type rt declares: a stdlib
	// opaque newtype (`rt.Duration`). typeDecl replays nothing for one. See
	// opaque.go.
	rtDeclared bool
	// stdOrigin is the declaring module's analyzer origin (`std/json`) of a
	// process-wide def stdtypes.go built from a type std declares in Nomi
	// with no spec row, and empty for every other def. Such a def is the
	// type's one def in the process: every gen that names the type holds
	// it, as it holds an rtDeclared one.
	stdOrigin string
	// rtOpaque marks a def that is a LEAF this builder may not look inside: a
	// blessed stdlib primitive whose Go type rt declares (`rt.Bytes`). See
	// stdhost.go.
	//
	// It exists because `isDistinct && inner == kindInvalid` already MEANS
	// something — a zero-sized marker (`type Expired`) — and a leaf with a
	// PAYLOAD reaches every arm written for that meaning. Without the flag
	// `impl Equatable for Bytes`'s `a == b` would answer true for every pair,
	// because two markers ARE always equal; the hash would be `rt.HashUnit`, the
	// Debug rendering the bare type name, and `irRetainedMarker` would answer
	// true for a value with contents, leaving an enum payload of it no slot.
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

	// components are the named types stored inline inside this one
	// (appendInlineDefs), used for cycle detection and therefore for the
	// boxed marks. A type reached only through a list or a map is absent.
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
	//
	// The memos below are not walk state: each holds an answer that depends
	// on the types alone, stored atomically. See kindmemo.go.
	retainedStruct, retainedEnum kindMemo
}

// refusal is one recorded reason a declaration was not lowered, replayed at the
// declaration's own position when the node is reached.
type refusal struct {
	construct string
	detail    string
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
	// boxed is set when the field's type can reach the containing type
	// (boxFields).
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
	// spread says a positional payload is written as a tuple of its parts,
	// `Node(Tree, Tree)`: the checker's record (writtenAsParts;
	// ir.Variant.Spread).
	spread bool
}

// payload is one value a variant carries.
type payload struct {
	// nomi is the field name for a struct-shaped variant's payload, empty for
	// a positional or embedded one.
	nomi string
	k    kind
	// deflt is a struct-shaped variant field's default, or nil.
	deflt ast.Node
	// stdDeflt is the value filling this field when a literal omits it, or
	// nil.
	//
	// The other half of `deflt`, and the two are exclusive: `deflt` is the
	// declaring file's Nomi expression, lowered in the gen that constructs the
	// value, and this is an rt constant a std enum's spec states
	// (stdEnumFieldSpec.deflt). A SHARED def needs this one, because a std
	// enum's *typeDef is process-wide and has no gen to lower Nomi source in,
	// which is the reason stdenum.go's payloadStructScalar rejects a
	// defaulted std field outright.
	stdDeflt *int64
	// slot indexes typeDef.slots, or is -1 for a payload of Unit or of a
	// marker, which is stored nowhere at all (irSlotlessPayload).
	slot int
}

type slotDef struct {
	k     kind
	boxed bool
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
//   - ONE FIELD WOULD SERVE SEVERAL WALKS. `reaches` (here) and the handle
//     walks (irchannels.go) each need the guard. The moment one called
//     another, the inner walk's deferred clear would
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
//
// The phases are separate functions because a group of files whose types
// refer to each other runs each phase over every file before the next one
// (typecycle.go). One file on its own runs them in a row.
func (g *gen) buildTypes(nodes []ast.Node) {
	grp := g.building
	if grp == nil {
		// A stdlib module, built outside the registry.
		grp = &typeGroup{}
		g.building = grp
	}
	grp.state = groupShells
	g.declareTypeShells(nodes)
	grp.state = groupResolving
	g.resolveTypeShells(nodes)
	grp.layOut([]*gen{g})
}

// declareTypeShells registers a shell for every struct, enum and distinct type
// the module declares, and its generic templates, without resolving any field.
func (g *gen) declareTypeShells(nodes []ast.Node) {
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
			if isDistinctRefinementDecl(t) {
				// Erased: a refinement is its base (refinement.go).
				continue
			}
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
		d := g.adoptedStdDef(n, name)
		if d == nil {
			d = &typeDef{nomi: name, decl: n, line: n.LineNum(), isEnum: isEnum, isDistinct: isDistinct, // Optimistic: resolution has to resolve a field whose type is
				// declared later in the file, and settleLowerable below retracts
				// this for every declaration that turns out refused.
				lowerable: true}
			if g.stdTypesBuild {
				// Marked before any field resolves, so a field naming it
				// interns process-wide (stdtypes.go).
				d.stdOrigin = g.fa.Origin
			}
			order = append(order, d)
		}
		g.types[name] = d
		g.fileDef(n, d)
	}
	// The GENERIC struct declarations, registered before any field is resolved
	// so a template whose field names another template (`struct Crate<T> { box:
	// Box<T> }`) sees a complete table — the same reason the two passes above
	// exist. Registration only: an instance is built when an instantiation is
	// reached, which is never during this loop, because a field's type argument
	// inside a generic declaration is a type PARAMETER and genericInstance
	// declines one. See generictype.go.
	g.collectGenericTemplates(nodes)
	g.typeOrder = order
}

// adoptedStdDef is the PROCESS-WIDE def a std declaration of name adopts
// instead of a shell, or nil: a spec row's, or the one stdtypes.go built
// from the declaration. Such a def stays out of the module's `typeOrder`:
// its representation is fixed, so there is nothing for resolution to
// resolve and nothing for settleLowerable to retract, and a per-gen shell
// would give two gens two pointers for one Nomi type, which is the
// identity a stdFunc's kinds are compared by.
func (g *gen) adoptedStdDef(n ast.Node, name string) *typeDef {
	if d := g.adoptedSpecDef(n); d != nil {
		return d
	}
	return g.adoptedStdTypeDef(name)
}

// adoptedSpecDef is adoptedStdDef's spec row, or nil.
func (g *gen) adoptedSpecDef(n ast.Node) *typeDef {
	switch t := n.(type) {
	case *ast.TypeDef:
		// A stdlib opaque newtype (opaque.go), or a nil-inner MARKER
		// (stdgenhost.go): two tables about one declaration kind, told apart
		// by whether there is an inner type at all.
		if i := g.opaqueDeclared(t); i >= 0 {
			return opaqueDefs()[i]
		}
		if i := g.stdMarkerDeclared(t); i >= 0 {
			return stdMarkerDefs()[i]
		}
	case *ast.StructDef:
		// A stdlib opaque STRUCT: nothing for the boxing walk to box either,
		// since a field's kind is a scalar and cannot reach the container.
		// See stdstruct.go.
		if i := g.stdStructDeclared(t); i >= 0 {
			return stdStructDefs()[i]
		}
	case *ast.EnumDef:
		// A monomorphic stdlib enum (stdenum.go), or a named payload enum
		// (`std/tasks.Failure`, taskoutcome.go).
		return g.sharedEnumDeclared(t)
	}
	return nil
}

// fileDef files d as this gen's def of the declaration n (defsByDecl).
func (g *gen) fileDef(n ast.Node, d *typeDef) {
	if g.defsByDecl == nil {
		// A fixture may build a gen literal and not go through newGen.
		g.defsByDecl = map[ast.Node]*typeDef{}
	}
	g.defsByDecl[n] = d
}

// resolveTypeShells resolves the fields, payloads and inner types of every
// shell declareTypeShells registered, then the declarations written inside
// bodies.
func (g *gen) resolveTypeShells(nodes []ast.Node) {
	for _, d := range g.typeOrder {
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
}

// declaredTypes is every type the module declares, its own and those written
// inside bodies, in the order the layout passes walk them.
func (g *gen) declaredTypes() []*typeDef {
	return append(g.typeOrder[:len(g.typeOrder):len(g.typeOrder)], g.blockLocalOrder...)
}

// boxTypeFields boxes every field that can reach its own container.
func (g *gen) boxTypeFields() {
	for _, d := range g.declaredTypes() {
		d.boxFields()
	}
}

// boxFields boxes each of d's fields that can reach d.
func (d *typeDef) boxFields() {
	defer changingTypes()()
	for i := range d.fields {
		for _, c := range appendInlineDefs(nil, d.fields[i].k) {
			if c == d || c.reaches(d) {
				d.fields[i].boxed = true
				break
			}
		}
	}
}

// assignTypeSlots lays out every declared enum's payload storage.
func (g *gen) assignTypeSlots() {
	for _, d := range g.declaredTypes() {
		d.assignSlots()
	}
}

// typeSettleOrder is the defs settleLowerable relaxes after the module's types
// are laid out.
//
// Prelude instances created while resolving these declarations join the
// relaxation: `struct A { m: Option<B> }` must be refused when B is. They
// carry no refusals of their own, so they can only ever be retracted
// through a payload. See prelude.go.
//
// The per-gen generic std instances join for the same reason:
// `struct Counter { inbox: Channel<Bad> }` must be refused when Bad is. A
// shared instance is never in either
// order slice, because every argument admitted to the shared table is
// lowerable by construction. See stdgenstruct.go and stdgenhost.go.
func (g *gen) typeSettleOrder() []*typeDef {
	settle := g.declaredTypes()
	settle = append(settle, g.preludeOrder...)
	settle = append(settle, g.genStructOrder...)
	return append(settle, g.genHostOrder...)
}

func (g *gen) resolveStruct(d *typeDef, t *ast.StructDef) {
	g.declModifiers(d, t.Decorators, t.TypeParams, t.WhereClauses, "struct")
	if planted := plantedRefusal(t.Name); planted != "" {
		// PlantTypeDeclineForTest.
		d.refusals = append(d.refusals, refusal{construct: planted, detail: d.nomi})
	}
	if tpl := g.templatesByDecl[t]; tpl != nil && tpl.why == "" {
		// An admitted generic template. Its fields are resolved per
		// instantiation, under a substitution, and no value has this def's
		// kind (typeDecl returns early for it), so walking the fields here
		// resolves annotations nothing will use.
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
		// generictype.go.
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
	defer changingTypes()()
	for _, f := range t.Fields {
		k := g.fieldKind(f)
		// kindInvalid: reports — records `non-scalar field type` on the declaration.
		if k == kindInvalid {
			if namesTypeParam(f.TypeAnnotation, tps) {
				// `struct Box<T> { value: T }`. The declaration is ALREADY
				// refused as `generic type` by declModifiers, and `T` is not a
				// type this builder cannot represent — it is not a type at
				// all. Reporting it again under a type-shaped key would file
				// one obstacle under two names. Keeping one obstacle under
				// one key is the same rule foreign.go's blockedMirror is
				// written to.
				continue
			}
			d.refusals = append(d.refusals, refusal{
				construct: "non-scalar field type",
				detail:    d.nomi + "." + f.Name + ": " + g.fieldTypeText(f),
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
	g.declModifiers(d, t.Decorators, t.TypeParams, t.WhereClauses, "enum")
	if tpl := g.templatesByDecl[t]; tpl != nil && tpl.why == "" {
		// An admitted generic template. Its variants are resolved per
		// instantiation, under a substitution, and no value has this def's
		// kind (typeDecl returns early for it), so walking the variants here
		// resolves annotations nothing will use.
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
	defer changingTypes()()
	if len(t.Variants) > maxEnumTag {
		d.refusals = append(d.refusals, refusal{
			construct: "enum with too many variants",
			detail:    d.nomi + " has " + strconv.Itoa(len(t.Variants)) + " variants; the tag is a uint8 and 0 is reserved",
		})
		return
	}
	et, _ := g.srcFA().DeclaredType(t).(*analysis.EnumType)
	for _, v := range t.Variants {
		vd := variantDef{nomi: v.Name, tag: len(d.variants) + 1, kind: v.Kind}
		switch v.Kind {
		case "bare":

		case "positional":
			// A TUPLE payload — `Pos (Int, Int)`, spelled `*ast.FuncType` with a
			// nil Return — needs no arm of its own here: `typeOf` routes exactly
			// that node shape to `typeOf`, which answers for a tuple
			// annotation. What the payload's TYPE cannot say is how the
			// value is written and how the slot is stored; both live elsewhere —
			// see irvariantvalues.go and assignSlots.
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
			vd.spread = writtenAsParts(et, v.Name)

		case "struct":
			bad := false
			for _, f := range v.Fields {
				k := g.fieldKind(f)
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
						detail:    d.nomi + "." + v.Name + "." + f.Name + ": " + g.fieldTypeText(f),
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
			// `embeds T`, where T is a struct or a distinct type (the checker
			// rejects an enum, spec §4 *Enums (Algebraic Data Types)*). T may be declared in a SIBLING FILE, in which case it is reached as
			// a mirror and its layout is the declaring file's — see
			// foreign.go. A type from anywhere else has no *typeDef and is
			// refused by name.
			//
			// The payload kind is not always T, and getting that wrong is a
			// silent wrong-value bug:
			//
			//   embeds <struct>            payload is the struct
			//   embeds <wrapping distinct> payload is the DISTINCT, and a
			//                              `case` arm binds its INNER value,
			//                              because the checker's contract is
			//                              that the variant's payload type is
			//                              the wrapped type (spec §4 *Enums (Algebraic Data Types)*)
			//   embeds <marker>            payload is the marker, which is
			//                              zero-sized, so it gets no slot and
			//                              a binding must materialize it
			//
			// The distinct is stored rather than its inner so the payload
			// keeps the distinct's nominal identity; the single unwrap lives
			// at one place, the pattern.
			st, ok := v.EmbeddedTypeExpr.(*ast.SimpleType)
			if !ok {
				d.refusals = append(d.refusals, refusal{
					construct: "enum embeds variant",
					detail:    d.nomi + "." + v.Name + " embeds " + typeText(v.EmbeddedTypeExpr),
				})
				continue
			}
			inner := g.writtenTypeDef(st)
			if inner == nil || !inner.lowerable {
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
// A distinct type is nominally its own type and structurally its inner one.
// The zero-sized marker has no inner, so an enum embedding one gives it no
// slot.
//
// An OPAQUE distinct reaches here too: opacity is a use-site rule the front end
// enforces, so it changes nothing about the layout. The one thing it does
// change — the `<opaque T>` auto-Debug rendering — is produced in the ANALYZER
// as ordinary Nomi source. See declModifiers and opaque.go.
//
// A STDLIB opaque distinct never reaches here at all: buildTypes gives it the
// process-wide def whose Go type rt declares. Only a USER declaration does.
func (g *gen) resolveDistinct(d *typeDef, t *ast.TypeDef) {
	g.declModifiers(d, t.Decorators, t.TypeParams, nil, "type")
	g.resolveDistinctInner(d, t, typeParamNames(t.TypeParams))
}

// resolveDistinctInner resolves a distinct's inner type: the declaration's,
// or a generic distinct instance's under its substitution frame
// (genericInstance). tps is a generic declaration's own parameters, whose
// inner type names them and so has no kind until an instance is built.
func (g *gen) resolveDistinctInner(d *typeDef, t *ast.TypeDef, tps map[string]bool) {
	defer changingTypes()()
	if t.InnerTypeExpr == nil {
		// A marker. inner stays kindInvalid, which irRetainedMarker reads as
		// "no storage at all", distinct from a refusal, which also leaves it
		// kindInvalid but records a reason alongside.
		return
	}
	k := g.typeOf(t.InnerTypeExpr)
	// kindInvalid: reports — records `non-scalar distinct inner type` on the declaration.
	if k == kindInvalid {
		if namesTypeParam(t.InnerTypeExpr, tps) {
			// `type Tagged<T> (String, T)`: refused as `generic type`
			// already, as resolveStructFields says for a field.
			return
		}
		d.refusals = append(d.refusals, refusal{
			construct: "non-scalar distinct inner type",
			detail:    "type " + d.nomi + " " + typeText(t.InnerTypeExpr),
		})
		return
	}
	d.inner = k
	dt, _ := g.srcFA().DeclaredType(t).(*analysis.DistinctType)
	d.spread = dt != nil && dt.WrittenAsParts
	d.note(k)
}

// writtenAsParts is the checker's record of whether et's variant name
// declares its payload as a tuple of its parts, `Node (Tree, Tree)`,
// which a rendering spreads: `Node(Leaf, Leaf)`
// (analysis.VariantDef.WrittenAsParts).
func writtenAsParts(et *analysis.EnumType, name string) bool {
	if et == nil {
		return false
	}
	for _, v := range et.Variants {
		if v.Name == name {
			return v.WrittenAsParts
		}
	}
	return false
}

// maxEnumTag is the largest tag a uint8 tag field can carry. Tag 0 is reserved
// invalid, so an enum may have at most 255 variants.
const maxEnumTag = 255

// declModifiers records the declaration-level constructs this builder refuses,
// each under its own name so the tally can size them separately.
func (g *gen) declModifiers(d *typeDef, decs []ast.Decorator, tps []ast.TypeParam, wcs []ast.WhereConstraint, keyword string) {
	if len(tps) > 0 || len(wcs) > 0 {
		d.refusals = append(d.refusals, refusal{construct: "generic type", detail: keyword + " " + d.nomi})
	}
	// `opaque` is deliberately NOT refused. The name-only auto-Debug
	// rendering for an opaque type is produced in the ANALYZER, as an ordinary Nomi
	// `impl Debug for T { fn inspect(value: T) -> String { "<opaque T>" } }` node
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
	// far as the builder. See opaque.go.
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
	// An attached `//!` test is deliberately NOT refused here. It is the
	// `derive` argument above applied to the other kind of metadata, and it is
	// stdStructSpec.mismatch' argument verbatim: the prompt is a test case of its
	// own (tests.go's attachedTestCases), retained or declined as its body
	// allows. Charging the TYPE for it would double-charge it, and the second
	// charge cascades: the def would stop being lowerable and every mention of
	// it would refuse under a type-shaped key instead.
	//
	// A `//!` is a line in a doc comment. It cannot change what the type is.
}

// assignSlots lays out an enum's payload storage.
//
// One slot per distinct payload kind, reused across variants because only one
// variant is live at a time, and never reused within one variant because a
// struct-shaped variant's payloads are live together. The comparison below,
// `d.slots[s].k == p.k`, is the dedup key: `kind` is `{tag, *typeDef}`, so two
// payloads share a slot only when they are the same kind. `Int` and `Float`
// differ in the tag; two same-named structs from two modules differ in the
// pointer.
//
// Boxing goes through appendInlineDefs, which is the same rule boxFields
// applies to a struct field. A check on `p.k.def` alone would see only a
// payload whose type is named (`def` is nil for every structural kind), so
// `enum Node { Leaf Int; Branch (Node, Int, Node) }` would not be marked.
//
// appendInlineDefs follows the kinds that are stored inline (tagNamed,
// tagTuple, tagAnonStruct) and does not follow a list or a map.
func (d *typeDef) assignSlots() {
	defer changingTypes()()
	for i := range d.variants {
		v := &d.variants[i]
		used := map[int]bool{}
		for j := range v.payloads {
			p := &v.payloads[j]
			p.slot = -1
			if irSlotlessPayload(p.k) {
				// Unit or a marker is stored nowhere: a read makes it from its
				// type alone (irSlotlessValue). Every other payload, a
				// field-less struct among them, has a slot.
				continue
			}
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
		}
	}
}

// settleLowerable propagates unlowerability from a component to its container.
//
// A struct whose field type was refused is refused too, and so is every use of
// it. Relaxation rather than a topological order because the graph may
// contain cycles.
func (g *gen) settleLowerable(order []*typeDef) {
	defer changingTypes()()
	for _, d := range order {
		// A generic instance whose impls could not be registered carries why
		// (typeGroup.registerInsts) and stays refused.
		d.lowerable = len(d.refusals) == 0 && d.why == ""
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
					d.blocker = c
					changed = true
					break
				}
			}
		}
	}
}

// typeOf is the kind a written type annotation names, or kindInvalid: the
// projection of the type the checker recorded for te (recordedTypeExpr,
// project). The builder resolves no written type by name; a type with no
// representation projects to kindInvalid, and sigreason.go names why.
func (g *gen) typeOf(te ast.TypeExpr) kind {
	rec, ok := g.recordedTypeExpr(te)
	if !ok {
		return kindInvalid
	}
	return g.project(rec)
}

func typeText(te ast.TypeExpr) string {
	if te == nil {
		return "<none>"
	}
	return te.TypeString()
}

// --- the declarations ------------------------------------------------------

// typeDecl replays a declaration's recorded refusals at its own position.
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
	d := g.defsByDecl[n]
	if d != nil && d.rtDeclared {
		// A stdlib opaque newtype: the process-wide def records no refusal to
		// replay. Ahead of the identity check below because the shared def
		// carries no declaration node; it outlives the AST that anchored it.
		// See opaque.go.
		return
	}
	if tpl := g.templatesByDecl[n]; tpl != nil && tpl.why == "" {
		// A generic struct declaration whose instantiations lower. There is
		// nothing to replay: the def's `generic type` refusal is the gap
		// generictype.go closes, and reporting it here would refuse every
		// file that declares one however well its instances lower.
		//
		// The refusal is left ON the def rather than removed, because a bare
		// `Box` mention (a generic name with no arguments, which is not a type)
		// resolves through this file's type table to this def and needs a
		// reason to report.
		// unloweredReason reads the LIST; removing the entry there would make
		// it synthesize a placeholder instead, which is the cascade
		// cascadereason_test.go exists to prevent. See generictype.go.
		return
	}
	if d == nil {
		// A duplicate declaration: the table kept the first one and recorded
		// the refusal there.
		g.reject("duplicate type declaration", name, n)
		return
	}
	g.replayTypeRefusals(d, n)
}

// replayTypeRefusals reports at `at` everything the declaration was refused
// for while its table entry was built.
func (g *gen) replayTypeRefusals(d *typeDef, at ast.Node) {
	for _, r := range d.refusals {
		g.reject(r.construct, r.detail, at)
	}
}

// --- construction and access ------------------------------------------------

// --- `embeds` subtype coercion ----------------------------------------------

// embedsVariant finds the variant of `want` that embeds a value of kind `from`.
//
// It only answers: a caller asks whether the widening applies, and builds
// the widening itself when it does (irlist.go). The list-literal element scan
// (collections.go) asks this question once per candidate.
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
// T` block for every opaque and host type, and derives write blocks too, so
// refusing "impl block" would make those declarations unlowerable — and would
// blame a fabricated position (the synth band is around 2^30) for a construct
// nobody wrote.
// Dropping them is safe only because every site that could DISPATCH to one is
// itself refused: rendering a named value needs `impl Display`, `==` on a named
// type needs `impl Equatable`, and a type-qualified call is refused by name. If
// any of those is ever lowered, the impl it dispatches to must be lowered in
// the same change.
func isSynthesized(n ast.Node) bool {
	return analysis.IsSynthesizedLine(n.LineNum())
}
