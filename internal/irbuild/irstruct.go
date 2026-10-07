package irbuild

// A declared struct is a value the retained grammar builds (`ir.MakeStruct`)
// and reads (`ir.ProjField`).
//
// # A field is a leaf or a retained struct
//
// A field's kind is the leaf domain (`irRetainedLeafKind`) or a declared
// struct that is itself retained, so `struct Person { address: Address }` is
// admitted when `Address` is. A nested struct literal is a second
// `MakeStruct` inside a field value, a nested read is a `ProjField` over a
// `ProjField`, and the VM carries the inner record inside the outer one's
// fields. A std struct row may also carry a scalar list field,
// `Json.ShapeError.path`. A user struct's fields also admit the containers,
// prelude wrappers, enums and functions irUserStructFieldKind and
// irComposedFieldKind list.
//
// # A struct may hold itself
//
// `struct Node { children: List<Node> }`, `next: Maybe<Link>`, `kids:
// Map<String, Dir>`, `rest: Iter<Stream>`, and two structs that hold each
// other through lists: the VM holds records, lists, maps and closures by
// reference, so the recursion is in the values and not in the layout. A
// field that reaches a type the walk is deciding (irKindReaches) is decided
// with the walk's stack, and that type is assumed retained
// (irNominalCoinductive). The stack-less predicates, such as irRetainedMapKind
// through irNominalElemKind, restart the walk at the struct itself, so they
// are never asked about such a field.
//
// # The identity the value carries, at two levels
//
// A struct value carries its type name and its fields, and impl dispatch,
// `Hash` and `Equal` key on the type name. A bare one would be a silent wrong
// answer: the value would miss every impl and `==` would fall back to
// structural comparison, with no error. Every `*typeDef` symbol goes through
// `irTypeSymName`, so the type name here is the module-qualified spelling.
//
// The field names are on the node. `ir.MakeStruct` names each operand's
// field, because `internal/vm` reads a field by name and an ordered operand
// list plus the declaration's identity does not give a consumer that builds
// the value the field names. See `internal/ir/make.go`.
//
// A field name is not qualified. A struct's fields are keyed by the bare
// declared name, and Display prints the bare name, so the qualification
// question lives at the type name and nowhere else.
//
// # What the shape declines
//
// Each condition below is a construction one MakeStruct cannot represent. A
// decline is not a refusal: the body is not retained, so the VM reports it as
// BLOCKED if a program reaches it.
//
//   - A boxed field outside `irRetainedStructKind`'s co-inductive rule.
//   - An omitted field with a stdlib or foreign default. A user default is
//     lowered in declaration scope by `fieldDefault`; the others decline.
//   - A field value whose kind is not the field's, except an empty literal,
//     which takes the field's kind through the shared empty-value coercion.
//     `g.coerce` is the identity only when `want == e.k`; anything else is a
//     widening or a discharge that changes the value's representation.
//   - A generic struct without concrete field metadata. Retention selects the
//     instance from checker field-label types; missing or unsupported types
//     decline.
//
// An impure field value is forced into an `ir.Copy` at its source position,
// as `bl.binary` does for an impure left operand and `bl.call` for a non-last
// argument (`Counter{value: c.value + 1}`).

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irRetainedStructKind reports whether d is a declared struct the retained
// grammar can build and carry: `struct Point { x: Int, y: Int }`,
// `pub opaque struct Token { value: String }`.
//
// Every field's kind must be a leaf or a retained declared struct, or for a
// std struct row a retained scalar list. See the file header.
//
// A boxed field is one the layout boxes to break a recursive type. It is
// retained when it names a user struct or enum decided co-inductively
// (irNominalCoinductive), because the VM holds records by reference; any
// other boxed field disqualifies the whole declaration.
func irRetainedStructKind(d *typeDef) bool {
	return irRetainedStructKindIn(d, nil)
}

// irRetainedStructKindIn is irRetainedStructKind inside a walk that is
// already deciding the types on `outer`. It is never asked about one of
// them: every path back to a type on the walk goes through
// irNominalCoinductive, which assumes that type retained.
func irRetainedStructKindIn(d *typeDef, outer []*typeDef) bool {
	if d == nil || d.isEnum || d.isDistinct || !d.lowerable {
		return false
	}
	for _, o := range outer {
		if o == d {
			return false
		}
	}
	// A field-less struct is a construction of no operands: the VM builds
	// an empty record.
	for i := range d.fields {
		f := &d.fields[i]
		if irCyclicNominal(f.k, f.boxed, outer) {
			// `struct Cell { next: Loop }` where Loop carries a Cell.
			if !irNominalCoinductive(f.k, append(outer, d)) {
				return false
			}
			continue
		}
		if deciding := append(outer, d); irKindReaches(f.k, deciding) {
			// `children: List<Node>`, `next: Maybe<Node>`, `kids: Map<String,
			// Node>`: a field that reaches a type this walk is deciding. It is
			// decided with the walk's stack, co-inductively, and never through
			// the stack-less predicates below, which would restart the walk
			// at a type already on it and never end.
			if !irRecursiveFieldKind(f.k, deciding) {
				return false
			}
			continue
		}
		if f.boxed {
			return false
		}
		if irRetainedFieldKind(f.k) {
			continue
		}
		if f.k.tag == tagNamed && irCompositeDistinct(f.k.def) {
			// `pair: Pair` for `type Pair (Int, Int)`, `w: Wrapped` for
			// `type Wrapped Maybe<Int>`: the distinct's record, carried as
			// an enum payload carries it. One reaching this struct back
			// was decided above.
			continue
		}
		if f.k.tag == tagFunc {
			// A function-valued field (`Generator<T>`'s `run`): a function
			// value the VM carries in the record like any other operand.
			if !irFuncFieldKind(f.k, append(outer, d)) {
				return false
			}
			continue
		}
		// A std struct's scalar list field, `Json.ShapeError.path`, is
		// carried as the list itself. Its Debug is std's own impl, and
		// irStructEqualityKind still requires scalar fields.
		if _, std := stdStructIndex(d); std {
			if !irStdStructFieldKind(d, f.k) {
				return false
			}
			continue
		}
		// A std generic struct's other fields are only what its spec builds.
		if d.genStructOf != nil || !(irUserStructFieldKind(f.k) || irComposedFieldKind(f.k, append(outer, d))) {
			return false
		}
	}
	return true
}

// irKindReaches reports whether a value of kind k can hold a value of one of
// the types on `deciding`: through a composite's parts, a Vector's or Set's
// element, a distinct's inner kind, or a declared type's fields and payloads.
// Each declared type is visited once, so the walk ends on recursive types.
func irKindReaches(k kind, deciding []*typeDef) bool {
	seen := map[*typeDef]bool{}
	var walk func(k kind) bool
	walk = func(k kind) bool {
		if elem, vector := vectorElem(k); vector {
			return walk(elem)
		}
		if elem, set := setElem(k); set {
			return walk(elem)
		}
		if k.comp != nil {
			for _, p := range k.comp.parts {
				if walk(p) {
					return true
				}
			}
		}
		d := k.def
		if d == nil || seen[d] {
			return false
		}
		seen[d] = true
		for _, o := range deciding {
			if o == d {
				return true
			}
		}
		if d.isDistinct {
			// kindInvalid: marker — a zero-sized marker has no inner value.
			return d.inner != kindInvalid && walk(d.inner)
		}
		for i := range d.fields {
			if walk(d.fields[i].k) {
				return true
			}
		}
		for _, v := range d.variants {
			for _, p := range v.payloads {
				if walk(p.k) {
					return true
				}
			}
		}
		return false
	}
	return walk(k)
}

// irRecursiveFieldKind decides a struct field that reaches a type on
// `deciding` (irKindReaches): a user struct or enum co-inductively, and
// anything else as a composition of retained parts. The VM holds records,
// variants, lists, maps and closures by reference, so a struct that holds
// itself through one of them is a finite layout.
func irRecursiveFieldKind(k kind, deciding []*typeDef) bool {
	switch {
	case irUserNominal(k):
		return irNominalCoinductive(k, deciding)
	case k.tag == tagFunc:
		return irFuncFieldKind(k, deciding)
	}
	return irComposedFieldKind(k, deciding)
}

// irUserNominal is a user struct or enum, or an instance of a user generic
// one: a type irNominalCoinductive decides.
func irUserNominal(k kind) bool {
	if k.tag != tagNamed || k.def == nil || k.def.isDistinct || k.def.preludeOf != nil || k.def.genStructOf != nil {
		return false
	}
	if _, vector := vectorElem(k); vector {
		return false
	}
	if _, std := stdStructIndex(k.def); std {
		return false
	}
	return true
}

// irComposedFieldKind is a user struct field composed of retained values:
// a tuple, a List, a Vector, a Set or a Map whose parts are field kinds
// themselves, a declared struct among them (`items: List<Point>`,
// `pair: (Int, Int)`). A struct reached through a part is decided
// co-inductively with `outer` extended, so a struct that names itself through
// a list (`kids: List<Tree>`) is decided once rather than recursing.
func irComposedFieldKind(k kind, outer []*typeDef) bool {
	var part func(p kind) bool
	part = func(p kind) bool {
		switch {
		case p.tag == tagNamed && p.def != nil && !p.def.isEnum && !p.def.isDistinct && p.def.genStructOf == nil && p.def.preludeOf == nil:
			if _, vector := vectorElem(p); !vector {
				return irNominalCoinductive(p, outer)
			}
		case p.tag == tagNamed && p.def != nil && p.def.isEnum && p.def.preludeOf == nil && p.def.genericOf == nil:
			// A user enum reached through a part (`xs: List<E>` where E
			// carries this struct): decided with `outer`, so the walk ends
			// at a type it is already deciding instead of recursing.
			return irNominalCoinductive(p, outer)
		case p.tag == tagFunc:
			return irFuncFieldKind(p, outer)
		case p.tag == tagSeq && p.comp != nil && len(p.comp.parts) == 1:
			// `xs: Iter<Point>`: a lowered sequence, held as the closure.
			return p.comp.parts[0] == kindUnit || part(p.comp.parts[0])
		}
		if elem, vector := vectorElem(p); vector && p != kindEmptyVector {
			return part(elem)
		}
		if elem, set := setElem(p); set && p != kindEmptySet && irKindReaches(p, outer) {
			// `kids: Set<Tree>` inside Tree: the element is decided with the
			// walk's stack; any other set keeps irRetainedSetKind's domain.
			return part(elem)
		}
		if p.tag == tagMap && p != kindEmptyMap && p.comp != nil && len(p.comp.parts) == 2 && irKindReaches(p, outer) {
			// `kids: Map<String, Tree>` inside Tree, as a set above.
			return part(p.comp.parts[0]) && part(p.comp.parts[1])
		}
		if (p.tag == tagTuple || p.tag == tagList) && p.comp != nil && len(p.comp.parts) > 0 {
			for _, c := range p.comp.parts {
				if !part(c) {
					return false
				}
			}
			return true
		}
		return irRetainedFieldKind(p) || irUserStructFieldKind(p) || irRetainedValueKind(p)
	}
	if k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil && irPreludeWrapperOver(k, outer) {
		// `fault: Maybe<Fault>`: a prelude wrapper over field kinds, a user
		// enum among them.
		for _, v := range k.def.variants {
			for _, p := range v.payloads {
				if p.k != kindUnit && !part(p.k) {
					return false
				}
			}
		}
		return true
	}
	if k.tag == tagNamed && k.def != nil && k.def.isEnum && k.def.preludeOf == nil && irEnumEmbeds(k.def) {
		// `shape: Shape` where Shape embeds a struct: the field holds the
		// enum's value, an embedded variant as its payload record.
		return irRetainedEnumKind(k.def)
	}
	if k.tag != tagTuple && k.tag != tagList && k.tag != tagSeq && k.tag != tagMap {
		_, vector := vectorElem(k)
		_, set := setElem(k)
		if !vector && !set {
			return false
		}
	}
	return part(k)
}

// irPreludeWrapperOver is a retained prelude enum instance (`Maybe<T>`,
// `Result<T, E>`) as a struct field's wrapper. One whose payloads reach a
// type the walk is deciding (`next: Maybe<Link>` inside Link) is checked only
// as a wrapper, since irRetainedEnumKind decides its payloads without the
// walk's stack; irComposedFieldKind decides those payloads itself.
func irPreludeWrapperOver(k kind, outer []*typeDef) bool {
	d := k.def
	if !irKindReaches(k, outer) {
		return irRetainedEnumKind(d)
	}
	return d.isEnum && d.lowerable && !d.rtOpaque && len(d.variants) != 0
}

// irFuncFieldKind is a function-typed struct field whose parameters and
// result are values the VM carries. A struct the signature names is decided
// co-inductively with `outer`, so a struct that names itself through a
// field's signature (`struct S { f: (S) -> Int }`) is decided once rather
// than recursing.
func irFuncFieldKind(k kind, outer []*typeDef) bool {
	if k.tag != tagFunc || k.comp == nil || len(k.comp.parts) == 0 {
		return false
	}
	var ok func(p kind) bool
	ok = func(p kind) bool {
		switch {
		case p.tag == tagNamed && p.def != nil && !p.def.isEnum && !p.def.isDistinct:
			return irNominalCoinductive(p, outer)
		case p.tag == tagFunc:
			return irFuncFieldKind(p, outer)
		case p.tag == tagTuple && p.comp != nil:
			for _, part := range p.comp.parts {
				if !ok(part) {
					return false
				}
			}
			return true
		}
		return irCallableValueKind(p)
	}
	for _, p := range funcParams(k) {
		if !ok(p) {
			return false
		}
	}
	r := funcResult(k)
	return r == kindUnit || ok(r)
}

// irUserStructFieldKind widens a user struct's field domain past
// irRetainedFieldKind to the containers and enums the VM carries as rt
// values: Byte and Bytes, scalar lists, maps, vectors and sets, records of
// leaves and scalar lists, a user enum without embedded variants, and a
// prelude enum over scalar leaves or scalar lists. None of them reaches a
// declared struct, so the check cannot re-enter one.
func irUserStructFieldKind(k kind) bool {
	switch {
	case irRetainedListKind(k), irRetainedMapKind(k), irRetainedVectorKind(k), irRetainedSetKind(k):
		return true
	case irByteValueKind(k):
		// A Byte or Bytes field (`body: Bytes`), in its register bank as a
		// std struct's is (irStdStructFieldKind).
		return true
	case isDecimalKind(k):
		// A Decimal field (`price: Decimal`), the rt.Decimal value.
		return true
	case irExistentialKind(k):
		// A local interface's existential: the VM holds the concrete value
		// and dispatch selects its impl through ir.Module.Implement.
		return true
	case k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1 && irExistentialKind(k.comp.parts[0]):
		// A list of existentials (`params: List<Display>`), the concrete
		// values themselves.
		return true
	case irContextKind(k), irSupervisorKind(k):
		// A Context field (`struct Config { context: Context }`) or a
		// Supervisor field (`audit: Supervisor`), the rt value itself.
		return true
	case irHostHandleKind(k):
		// A Go handle field (`struct Box { raw: RawBox }`), the rt.HostHandle
		// a generated adapter answered.
		return true
	case k.tag == tagAnonStruct:
		if k.comp == nil || len(k.comp.names) == 0 || len(k.comp.names) != len(k.comp.parts) {
			return false
		}
		for _, part := range k.comp.parts {
			if !irRetainedLeafKind(part) && !irRetainedListKind(part) {
				return false
			}
		}
		return true
	case k.tag != tagNamed || k.def == nil || !k.def.isEnum || irEnumEmbeds(k.def):
		return false
	case k.def.preludeOf == nil:
		return irRetainedEnumKind(k.def)
	}
	for _, v := range k.def.variants {
		for _, p := range v.payloads {
			if !irRetainedLeafKind(p.k) && !irRetainedListKind(p.k) && p.k != kindUnit && !irPlainUserEnumKind(p.k) {
				return false
			}
		}
	}
	return irRetainedEnumKind(k.def)
}

// irPlainUserEnumKind is a retained user enum with no embedded variants, the
// payload a prelude wrapper field may carry (`fault: Maybe<Fault>`). Its own
// payloads are decided by irRetainedEnumKind, which never re-enters a struct
// through an unboxed slot.
func irPlainUserEnumKind(k kind) bool {
	return k.tag == tagNamed && k.def != nil && k.def.isEnum && k.def.preludeOf == nil &&
		!irEnumEmbeds(k.def) && irRetainedEnumKind(k.def)
}

// irStdStructFieldKind widens a std struct's field domain past
// irRetainedFieldKind: a retained scalar list, a list of leaf structs or
// retained enums, a retained Map, and a retained enum or an anchored prelude
// enum carried in an unboxed slot. The VM carries each as an rt value and Go
// keeps the declared field layout for construction and field reads.
func irStdStructFieldKind(d *typeDef, k kind) bool {
	switch {
	case k.tag == tagList:
		if irRetainedListKind(k) {
			return true
		}
		// A list of leaf structs or of plain retained enums, such as
		// std/assertions' pipeline rows and std/dynamic's path. Only these,
		// so the check never re-enters a struct through a list.
		elem := k.comp.parts[0]
		if elem.tag != tagNamed || elem.def == nil || elem.def == d {
			return false
		}
		if elem.def.isEnum {
			return elem.def.preludeOf == nil && irRetainedEnumKind(elem.def) && !irEnumEmbeds(elem.def)
		}
		if irLeafStructKind(elem.def) {
			return true
		}
		// A list of std structs whose own list fields hold leaf structs:
		// std/assertions' `AssertionFailure.values`, each row carrying its
		// `pipeline` stages. One level only, so the check still never
		// re-enters a struct through a list.
		return irStdLeafListStructKind(elem.def)
	case k.tag == tagMap:
		return irRetainedMapKind(k)
	case k.tag == tagNamed && k.def != nil && k.def != d && k.def.isEnum:
		return irRetainedEnumKind(k.def) && !irEnumEmbeds(k.def)
	}
	return false
}

// irStdLeafListStructKind is a lowerable std struct whose unboxed fields are
// leaves or lists of leaf structs.
func irStdLeafListStructKind(d *typeDef) bool {
	if _, std := stdStructIndex(d); !std || d.isEnum || d.isDistinct || !d.lowerable || len(d.fields) == 0 {
		return false
	}
	for i := range d.fields {
		f := &d.fields[i]
		if f.boxed {
			return false
		}
		if irRetainedLeafKind(f.k) {
			continue
		}
		if f.k.tag != tagList || f.k.comp == nil || len(f.k.comp.parts) == 0 {
			return false
		}
		elem := f.k.comp.parts[0]
		if elem.tag != tagNamed || elem.def == nil || elem.def == d || !irLeafStructKind(elem.def) {
			return false
		}
	}
	return true
}

// irLeafStructKind is a lowerable struct whose unboxed fields are all
// leaves.
func irLeafStructKind(d *typeDef) bool {
	if d == nil || d.isEnum || d.isDistinct || !d.lowerable || len(d.fields) == 0 {
		return false
	}
	for i := range d.fields {
		if d.fields[i].boxed || !irRetainedLeafKind(d.fields[i].k) {
			return false
		}
	}
	return true
}

// irRetainedFieldKind is a struct field's domain: a leaf, a channel handle
// (irChannelFieldKind), or a declared struct that is itself retained. An
// unboxed struct field cannot contain its own declaration, so the recursion
// ends at the leaves; irChannelFieldKind bounds the one path through a
// channel's element.
func irRetainedFieldKind(k kind) bool {
	if irRetainedLeafKind(k) || irChannelFieldKind(k) {
		return true
	}
	return k.tag == tagNamed && k.def != nil && !k.def.isEnum && irRetainedStructKind(k.def)
}

// irRetainedValueKind admits scalar leaves, wrapping distincts, declared
// structs of those leaves and of retained structs, enums with unboxed
// scalar-leaf payloads or embedded retained structs and markers, lists whose
// leaves are scalars, plain scalar vectors, maps and sets, structural
// tuples/records of retained values, task and channel handles, the
// Context, a Supervisor and a Dynamic.
// Struct fields use irRetainedFieldKind and user enum payloads use
// irRetainedLeafKind. Prelude wrappers also admit retained lists and one
// represented user enum without embedded variants; recursive nominal layouts
// remain outside the retained domain.
func irRetainedValueKind(k kind) bool {
	if irRetainedRangeKind(k) || irHostHandleKind(k) || irConcHandleKind(k) || irContextKind(k) || irSupervisorKind(k) || irDynamicKind(k) || irTypeWitnessKind(k) {
		return true
	}
	if irByteValueKind(k) || isDecimalKind(k) || irRetainedLeafKind(k) || irRetainedSeqKind(k) || irRetainedListKind(k) || irListTransportKind(k) || irRetainedMapKind(k) || irMapTransportKind(k) || irRetainedVectorKind(k) || irRetainedSetKind(k) || irRetainedTupleKind(k) || irRetainedRecordKind(k) {
		return true
	}
	// A List or Vector of any value the callable domain holds (`List<(Int)
	// -> Int>`, `Vector<Pt>`, `List<List<Pt>>`): the VM's containers hold
	// values of every kind, and each operation over one checks the element
	// kinds it reads itself.
	if k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1 {
		return irCallableValueKind(k.comp.parts[0])
	}
	if elem, vector := vectorElem(k); vector {
		return irCallableValueKind(elem)
	}
	return k.tag == tagNamed && (irRetainedStructKind(k.def) || irRetainedEnumKind(k.def) || irCompositeDistinct(k.def))
}

// structMake lowers `Point{x: 3, y: 4}` as an `ir.Make` of kind MakeStruct.
//
// Every check here is a decline and none is a reject: a declined literal
// leaves its body unretained.
//
// THE FIELD VALUES ARE LOWERED IN SOURCE ORDER AND THE OPERANDS ARE IN
// DECLARATION ORDER. Field values evaluate in the order the literal writes
// them, so any Copy a field value needs is placed in source order, and the Go
// composite literal reads in declaration order.
//
// A nil TypeName delegates to recordMake. A namespaced struct such as
// `Json.ShapeError` shares the simple name's construction; struct-shaped
// variants and leading-dot shorthand go through variantStructLit.
func (bl *irScalarBuilder) structMake(t *ast.StructLit) (ir.Temp, kind, bool, bool) {
	if t.TypeName == nil {
		if st := bl.g.targetStruct(t); st != nil {
			// A target-typed brace literal (`a: Address = {...}`) builds the
			// nominal struct the checker chose, exactly as `Address{...}`.
			k := bl.g.project(st)
			if k.tag != tagNamed || k.def == nil {
				return ir.NoTemp, kindInvalid, false, false
			}
			return bl.structMakeOf(t, k.def)
		}
		return bl.recordMake(t)
	}
	if t.Spread != nil {
		// Only the untyped form is a spread; never fill a spread's fields
		// from declared defaults.
		return ir.NoTemp, kindInvalid, false, false
	}
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	switch tn := t.TypeName.(type) {
	case *ast.QualifiedType:
		// A namespaced struct's whole dotted name wins over the variant
		// reading. A generic template's dotted name declines.
		if tn.Member == nil || bl.g.genericTemplates[tn.Module+"."+tn.Member.TypeString()] != nil {
			return no()
		}
		if _, isSimple := tn.Member.(*ast.SimpleType); isSimple {
			// `span.Span{...}`: another file's generic struct, named through
			// its module.
			if tpl, instantiate, isTemplate := bl.g.genericTemplateNamed(tn.Module + "." + tn.Member.TypeString()); isTemplate && tpl.structDecl() != nil {
				return bl.genericStructMake(t, tpl, instantiate)
			}
		}
		if d, namespaced := bl.g.namespacedType(tn); namespaced {
			// `Json.ShapeError{...}` builds like a simple struct name.
			return bl.structMakeOf(t, d)
		}
		return bl.variantStructLit(t, tn.Module, tn.Member.TypeString())
	case *ast.DotVariantType:
		if tn.ResolvedEnum == "" {
			return no()
		}
		if d := bl.g.dotEnumDef(t, tn.ResolvedEnum, tn.Name); d != nil {
			// The enum the checked type names, whatever this file imports.
			return bl.variantStructLitOf(t, d, d.variant(tn.Name))
		}
		return bl.variantStructLit(t, bl.g.dotEnumName(tn.ResolvedEnum), tn.Name)
	}
	tn, isSimple := t.TypeName.(*ast.SimpleType)
	if !isSimple {
		return no()
	}
	if d, v, isVariant := bl.g.stdEnumVariantAt(tn.Name, tn.Line, tn.Col); isVariant {
		// A std enum's struct-shaped variant imported by name
		// (`import std/supervisors.Backoff.Exponential`, then
		// `Exponential{}`), which the checker types as the variant.
		return bl.variantStructLitOf(t, d, v)
	}
	if a := bl.stdGenStructLitAnchor(tn); a != nil {
		if a.decl.Name == "Range" && bl.g.stdModule == "ranges" {
			return bl.rangeStructLit(t)
		}
		return bl.stdGenStructMake(t, a)
	}
	// A user generic struct, declared here or selectively imported from
	// another file (`import span.Span`).
	if tpl, instantiate, isTemplate := bl.g.genericTemplateNamed(tn.Name); isTemplate {
		return bl.genericStructMake(t, tpl, instantiate)
	}
	d, found := bl.g.namedType(tn.Name)
	if !found {
		return no()
	}
	return bl.structMakeOf(t, d)
}

// genericStructMake builds a literal of the user generic struct tpl at the
// type arguments the checker solved for it. instantiate is the one
// genericTemplateNamed handed back, so an instance of another file's template
// is built there and mirrored here.
func (bl *irScalarBuilder) genericStructMake(t *ast.StructLit, tpl *genericTemplate, instantiate func([]kind) (kind, bool)) (ir.Temp, kind, bool, bool) {
	args, ok := bl.g.checkedStructTypeArgs(t, tpl)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	k, ok := instantiate(args)
	if !ok || k.tag != tagNamed {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.structMakeOf(t, k.def)
}

// structMakeOf builds the literal t of the declared struct d.
func (bl *irScalarBuilder) structMakeOf(t *ast.StructLit, d *typeDef) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if !(irRetainedStructKind(d) || bl.boot && irBootStructKind(d)) {
		return no()
	}
	// SOURCE ORDER, AND EVERY WRITTEN FIELD NAMED AT MOST ONCE. An unknown
	// or repeated field declines; an omitted one takes its default below.
	vals := make(map[string]ir.Temp, len(d.fields))
	for _, f := range t.Fields {
		fd := d.field(f.Name)
		if fd == nil {
			return no()
		}
		if _, dup := vals[f.Name]; dup {
			return no()
		}
		// Typed by the field, so a bare `None` or `Ok(v)` takes its kind.
		src, k, mobile, ok := bl.lowerTypedOperand(f.Value, fd.k)
		erased := bl.g.irErases(fd.k, k)
		if ok && mobile && k != fd.k && !erased {
			// An empty literal takes the field's kind.
			src, k, ok = bl.coerceEmpty(f.Value, src, k, fd.k)
		}
		if !ok || (k != fd.k && !erased) {
			return no()
		}
		if !mobile {
			// Every impure field value is forced, with no unforced-tail
			// exception, into an `ir.Copy`, which `irScalarHoist` spells as
			// `vN := <src>`.
			//
			// THE ORDER IS THE LITERAL'S, which is why the Copy is appended
			// here and not gathered later: the `vN :=` lines come out in
			// source order while the composite literal below reads in
			// DECLARATION order.
			cp := ir.NewCopy(bl.g.irNodePos(f.Value), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
			src = cp.Dst()
		}
		vals[f.Name] = src
	}
	// Omitted fields take their declared defaults after the written fields,
	// in declaration order.
	for i := range d.fields {
		f := &d.fields[i]
		if _, given := vals[f.nomi]; given {
			continue
		}
		v, ok := bl.fieldDefault(t, d, f)
		if !ok {
			return no()
		}
		vals[f.nomi] = v
	}
	if len(vals) != len(d.fields) {
		return no()
	}
	return bl.structMakeEmit(t, d, vals)
}

// structMakeEmit builds the struct d from every field's value, in
// declaration order.
func (bl *irScalarBuilder) structMakeEmit(t *ast.StructLit, d *typeDef, vals map[string]ir.Temp) (ir.Temp, kind, bool, bool) {
	// THE NOMI NAMES COME FROM `irStructFieldNames`. Two sites each answering "what are this struct's field
	// names" is the drift `irparam.go` refuses for a destructuring prologue;
	// the Go names and the operand order are this function's own, because the
	// operands are minted out of the retained function's namespace.
	names := irStructFieldNames(d)
	ops := make([]ir.Temp, len(d.fields))
	for i := range d.fields {
		ops[i] = vals[d.fields[i].nomi]
	}
	m := ir.NewMakeStruct(bl.g.irNodePos(t), bl.f.NewTemp(),
		bl.g.irTypeSym(d), names, ops)
	bl.b.Append(m)
	out := named(d)
	bl.side(m.Dst(), irScalarSide{k: out, pureMake: true})
	// MOBILE UNCONDITIONALLY. Every operand is pure by now: an impure field
	// value was forced into a Copy above, and coercion is the identity when
	// the kinds agree.
	//
	// A Go composite literal of pure operands re-evaluates to a value that
	// compares equal and allocates nothing that is not its operands, for a
	// flat struct with no pointer in it. `irmake.go` records that two of
	// the producer's own sites disagree with that rule for a tuple and a
	// range; this is not one of them, because the boxing arm that clears it
	// is refused at the DECLARATION by `irRetainedStructKind`.
	return m.Dst(), out, true, true
}

// fieldDefault lowers one omitted field's declared default with the local
// scopes hidden, since a default resolves against the declaring file, and
// forces an impure value. A stdlib default is std's anchored constant
// (irstdfielddefault.go); a default declared in another file is a call to the
// declaring file's accessor (foreignfielddefault.go).
func (bl *irScalarBuilder) fieldDefault(at ast.Node, d *typeDef, f *fieldDef) (ir.Temp, bool) {
	if f.stdDeflt != nil && f.deflt == nil {
		return bl.stdFieldDefault(at, f)
	}
	if f.deflt == nil || f.stdDeflt != nil {
		return ir.NoTemp, false
	}
	if d.foreign != "" {
		return bl.foreignFieldDefault(at, d, "", f.nomi, f.k)
	}
	return bl.declDefault(f.deflt, f.k)
}

// declDefault lowers a declared default expression with the local scopes
// hidden, forced when impure, and coerced to the field's kind.
func (bl *irScalarBuilder) declDefault(deflt ast.Node, want kind) (ir.Temp, bool) {
	bound, boundK, parent, syms := bl.bound, bl.boundK, bl.parent, bl.sh.syms
	bl.bound, bl.boundK, bl.parent, bl.sh.syms = map[string]ir.Temp{}, map[string]kind{}, nil, map[string]*ir.Symbol{}
	saved := bl.g.scopes
	bl.g.scopes = []map[string]local{{}}
	// Typed by the field, so `Maybe.None` or `None` is the field's Maybe.
	v, k, mobile, ok := bl.lowerTypedOperand(deflt, want)
	bl.g.scopes = saved
	bl.bound, bl.boundK, bl.parent, bl.sh.syms = bound, boundK, parent, syms
	if !ok {
		return ir.NoTemp, false
	}
	if !mobile {
		cp := ir.NewCopy(bl.g.irNodePos(deflt), bl.f.NewTemp(), v)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		v = cp.Dst()
	}
	if k != want && bl.g.irErases(want, k) {
		// A concrete implementer at an interface-typed field: the VM holds
		// it as the existential's value, as the literal's written fields do.
		return v, true
	}
	v, k, ok = bl.coerceEmpty(deflt, v, k, want)
	if !ok || k != want {
		return ir.NoTemp, false
	}
	return v, true
}

// stdGenStructLitAnchor is the anchored generic std struct a literal names
// (`Generator{run: ...}` inside std/random), or nil.
func (bl *irScalarBuilder) stdGenStructLitAnchor(tn *ast.SimpleType) *stdGenStructAnchor {
	g := bl.g
	g.loadStdGenStructs()
	a := g.genStructs[tn.Name]
	if a == nil || a.decl == nil {
		return nil
	}
	if _, local := g.types[tn.Name]; local && !g.stdGenStructDeclaringModule(a) {
		return nil
	}
	return a
}

// stdGenStructMake builds a literal of a generic std struct. The instance is
// solved from the written field values' kinds against the declaration's
// field annotations, as a generic call's arguments solve its parameters: the
// checker's field-label type names the struct's own parameters, which inside
// a generic function are not the caller's. Every field must be written, and
// the values are lowered, and forced, in source order.
func (bl *irScalarBuilder) stdGenStructMake(t *ast.StructLit, a *stdGenStructAnchor) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Fields) != len(a.decl.Fields) {
		return no()
	}
	params := map[string]bool{}
	for _, tp := range a.decl.TypeParams {
		params[tp.Name] = true
	}
	byName := map[string]ast.TypeExpr{}
	for _, f := range a.decl.Fields {
		byName[f.Name] = f.TypeAnnotation
	}
	vals := make(map[string]ir.Temp, len(t.Fields))
	kinds := make(map[string]kind, len(t.Fields))
	solved := map[string]kind{}
	for _, f := range t.Fields {
		ann := byName[f.Name]
		if ann == nil {
			return no()
		}
		if _, dup := vals[f.Name]; dup {
			return no()
		}
		src, k, mobile, ok := bl.lower(f.Value)
		if !ok {
			return no()
		}
		if !mobile {
			cp := ir.NewCopy(bl.g.irNodePos(f.Value), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
			src = cp.Dst()
		}
		bl.g.unifyTypeParams(ann, k, params, solved)
		vals[f.Name], kinds[f.Name] = src, k
	}
	args := make([]kind, len(a.decl.TypeParams))
	for i, tp := range a.decl.TypeParams {
		k, ok := solved[tp.Name]
		if !ok || !irCallableValueKind(k) {
			return no()
		}
		args[i] = k
	}
	k, ok := bl.g.genStructInstance(a.spec, args...)
	if !ok || k.tag != tagNamed || !irRetainedStructKind(k.def) {
		return no()
	}
	d := k.def
	if len(d.fields) != len(vals) {
		return no()
	}
	for i := range d.fields {
		if fk, given := kinds[d.fields[i].nomi]; !given || fk != d.fields[i].k {
			return no()
		}
	}
	return bl.structMakeEmit(t, d, vals)
}

// targetStruct is the nominal struct the checker gave a brace literal with no
// type name because its position expected one (analysis.FileAnalysis
// .TargetStructs), or nil for an anonymous struct literal.
func (g *gen) targetStruct(t *ast.StructLit) *analysis.StructType {
	if g.fa == nil || t.Spread != nil {
		return nil
	}
	return g.fa.TargetStructs[t]
}
