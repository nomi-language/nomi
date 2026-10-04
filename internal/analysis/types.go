package analysis

import (
	"fmt"
	"strings"
)

// Type represents a Nomi type.
type Type interface {
	String() string
	typeTag() // marker
}

// ---------------------------------------------------------------------------
// Primitive types — singletons — and non-generic host types
// ---------------------------------------------------------------------------

// PrimitiveType represents a type with no Nomi-visible structure: a built-in
// primitive (`Int`, `String`, `Unit`, ...) or a non-generic `host type`
// (`Regex`, `Context`, a user's `host type Handle`).
//
// The two differ in identity. A built-in is one process-wide singleton below,
// with no Origin, and is identified by pointer: `pub host type Int` in
// std/int.nomi resolves to TypeInt however many analyses read it, so there is
// no second object to tell apart. A host type is built per declaration per
// analysis (buildExternTypeShell), so it carries its declaring file's Origin
// and is identified by (Origin, Name), as a struct is: two analyses of
// std/regex.nomi produce two objects and one type. See samePrimitive.
type PrimitiveType struct {
	Name_ string
	// Origin is the declaring file's build key for a host type (see
	// StructType.Origin), and empty for a built-in singleton.
	Origin string
}

func (t *PrimitiveType) String() string { return t.Name_ }
func (t *PrimitiveType) typeTag()       {}

// samePrimitive reports whether a and b are one type: the same built-in
// singleton, or host types of one declaration. A built-in never equals a host
// type, because a host type that takes a built-in's name resolves to the
// built-in (buildExternTypeShell) and so has no object of its own.
func samePrimitive(a, b *PrimitiveType) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil || a.Origin == "" || b.Origin == "" {
		return false
	}
	return a.Origin == b.Origin && a.Name_ == b.Name_
}

var (
	TypeInt     = &PrimitiveType{Name_: "Int"}
	TypeFloat   = &PrimitiveType{Name_: "Float"}
	TypeDecimal = &PrimitiveType{Name_: "Decimal"}
	TypeString  = &PrimitiveType{Name_: "String"}
	TypeByte    = &PrimitiveType{Name_: "Byte"}
	TypeBytes   = &PrimitiveType{Name_: "Bytes"}
	// TypeBool is overwritten by installCanonicalStdTypes with the canonical
	// definition from bool.nomi, during the first build that declares it. This
	// fallback exists for unit tests that run without the full std.
	TypeBool Type = &EnumType{
		Name: "Bool",
		Variants: []VariantDef{
			{Name: "False"},
			{Name: "True"},
		},
	}
	TypeUnit       = &PrimitiveType{Name_: "Unit"}
	TypeInfallible = &PrimitiveType{Name_: "Infallible"}
	TypeTrue       = &PrimitiveType{Name_: "True"}
	TypeFalse      = &PrimitiveType{Name_: "False"}
	TypeAny        = &PrimitiveType{Name_: "Any"}
	// TypeOrdering is overwritten by installCanonicalStdTypes with the
	// canonical definition from comparable.nomi (mirroring TypeBool). It
	// backs the compiler-known resolution of `Ordering` in SYNTHESIZED
	// derive code: a `@derive Comparable` compare signature returns
	// `Ordering` whether or not the deriving file imports it — synthesized
	// code is compiler output and resolves through this route, not through
	// the file's imports. This fallback exists for builds that run without
	// the full std.
	TypeOrdering Type = &EnumType{
		Name: "Ordering",
		Variants: []VariantDef{
			{Name: "Less"},
			{Name: "Equal"},
			{Name: "Greater"},
		},
	}
	// TypeAssertionFailure is overwritten by installCanonicalStdTypes with the
	// canonical `AssertionFailure` struct from assertions.nomi (mirroring
	// TypeBool and TypeOrdering). It backs the compiler-known resolution of
	// the ERROR side of `assert`, `refute` and `testing.check`: the type is
	// decided by std/testing's own declaration —
	// `check<T>(subject: T): Result<T, AssertionFailure>` resolved in
	// std/testing's scope — and never by the file that calls it.
	//
	// Reading it out of the CALLING file's scope was a live defect. A program
	// declaring its own `struct AssertionFailure` retyped check's error to that
	// struct, so `case testing.check(1 == 2) { Err(f) -> f.bogus ... }` passed
	// analysis and then died at run time with "struct
	// 'assertions.AssertionFailure' has no field 'bogus'" — a type error
	// delivered as a runtime fault. A program that mentions the name NOWHERE
	// got the fallback below, a type that unifies with nothing.
	//
	// The fallback stays a PrimitiveType rather than a hand-written StructType,
	// and that is deliberate: the real declaration carries eight fields, three
	// of them naming sibling declarations, and a hand-copied shell would be a
	// second statement of a layout std already owns — wrong the first time
	// assertions.nomi changed, and wrong silently. Nothing may be READ off this
	// value until std.Load has replaced it; a build without std keeps a type
	// that names itself and answers no field.
	TypeAssertionFailure Type = &PrimitiveType{Name_: "AssertionFailure"}
	// TypeCodepoint is overwritten by installCanonicalStdTypes with std's
	// `pub opaque type Codepoint Int` from codepoints.nomi. It is the type of
	// a codepoint literal (`'a'`), which has that type whether or not the
	// file imports `Codepoint`: the literal names std's declaration, never
	// whatever `Codepoint` the file has in scope. The fallback, a type that
	// names itself and unifies with nothing else, serves builds without std,
	// for the reason TypeAssertionFailure's comment gives.
	TypeCodepoint Type = &PrimitiveType{Name_: "Codepoint"}
	// TypeRange is overwritten by installCanonicalStdTypes with std's generic
	// `pub opaque struct Range<T>` from ranges.nomi. A range literal (`1..=3`)
	// instantiates it whether or not the file has `Range` in scope: a std file
	// that never imports std/ranges writes range literals too. The fallback
	// is not a StructType, so a build without std types a range literal as
	// nothing, as it did when the name was missing from scope.
	TypeRange Type = &PrimitiveType{Name_: "Range"}
)

// primitiveTypes maps host type names to their singleton PrimitiveType.
var primitiveTypes = map[string]*PrimitiveType{
	"Int": TypeInt, "Float": TypeFloat, "Decimal": TypeDecimal, "String": TypeString, "Byte": TypeByte, "Bytes": TypeBytes,
	"Unit": TypeUnit, "Infallible": TypeInfallible,
	"True": TypeTrue, "False": TypeFalse,
}

// ---------------------------------------------------------------------------
// Named types
// ---------------------------------------------------------------------------

// FieldDef describes a named field with a type. HasDefault is set on
// nominal struct fields whose declaration carries a `= expr` default
// (e.g. `Response { status: Int = 200 }`); anonymous-struct fields, enum
// struct-variant fields, and any other non-defaulted slot leave it false.
// The flag drives the call-form constructor's missing-required-field
// check: `Foo({a: 1})` is OK iff every absent field has HasDefault==true.
//
// Line and Col are where an anonymous struct TYPE written in source names the
// field (`{logger: String}` in a signature or annotation), the position
// resolveTypeExpr records the field's symbol at. An anonymous literal checked
// against that type points its labels there (recordAnonLitFieldLabels). They
// are zero for every other field: nominal struct fields have their own
// declaration symbols, and an anonymous literal's own inferred type declares
// nothing. They take no part in type identity.
type FieldDef struct {
	Name       string
	Type       Type
	HasDefault bool
	Line, Col  int
}

// StructType represents a struct declaration.
type StructType struct {
	// Origin is the declaring file's build key. Together with Name it
	// forms the type's nominal identity; see Origin on FileAnalysis. Name stays
	// the short, user-facing name; nothing prefixes Origin into it.
	Origin        string
	Name          string
	Fields        []FieldDef
	TypeParams    []string      // for generics, empty for concrete
	TypeParamDefs []*TypeParam_ // the actual TypeParam_ pointers referenced by Fields
	TypeArgs      []Type        // concrete type arguments, e.g. [Int, String] for Pair<Int, String>
	// Opaque is true when the type's defining module exported it as
	// `opaque <name>`. Construction and field access are private to
	// that module; outside callers go through exported accessors.
	Opaque           bool
	OwningSourceFile string
}

func (t *StructType) String() string {
	if len(t.TypeArgs) > 0 {
		args := make([]string, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			args[i] = a.String()
		}
		return t.Name + "<" + strings.Join(args, ", ") + ">"
	}
	return t.Name
}
func (t *StructType) typeTag() {}

// VariantKind classifies how an enum variant carries its payload.
//
//   - VariantBare: no payload (e.g. `Point`, `None`).
//   - VariantPositional: a typed positional payload created via the variant's
//     own constructor (e.g. `Some(T)`).
//   - VariantStruct: named-field payload defined inline on the variant
//     (e.g. `Line{start: Point, end: Point}`).
//   - VariantEmbedded: payload is a standalone struct or distinct type
//     defined elsewhere; the variant name equals the embedded type's name
//     (e.g. `embeds Circle`). Embedded variants admit subtype coercion —
//     a value of the embedded type flows into the enum-typed position
//     without explicit qualified construction.
type VariantKind int

const (
	VariantBare VariantKind = iota
	VariantPositional
	VariantStruct
	VariantEmbedded
)

// VariantDef describes a single enum variant.
type VariantDef struct {
	Name     string
	Kind     VariantKind
	DataType Type       // nil for bare variants
	Fields   []FieldDef // for struct variants
	// Embedded is the declared type of an `embeds` variant — the struct or
	// distinct itself. DataType differs from it for a wrapping distinct,
	// whose payload is the distinct's inner type. Construction reads this
	// field: `Shape.Id(5)` builds an `Id`, so it is checked as `Id(5)` is.
	Embedded Type
}

// EnumType represents an enum declaration.
type EnumType struct {
	// Origin is the declaring file's build key; see StructType.Origin.
	Origin        string
	Name          string
	Variants      []VariantDef
	TypeParams    []string
	TypeParamDefs []*TypeParam_ // the actual TypeParam_ pointers referenced by Variants
	TypeArgs      []Type        // concrete type arguments, e.g. [Int] for Maybe<Int>
	// Opaque is true when the type's defining module exported it as
	// `opaque <name>`. Construction and pattern destructuring are
	// private to the defining module.
	Opaque           bool
	OwningSourceFile string
}

func (t *EnumType) String() string {
	if len(t.TypeArgs) > 0 {
		args := make([]string, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			args[i] = a.String()
		}
		return t.Name + "<" + strings.Join(args, ", ") + ">"
	}
	return t.Name
}
func (t *EnumType) typeTag() {}

// DistinctType represents a distinct type wrapping another type.
type DistinctType struct {
	// Origin is the declaring file's build key; see StructType.Origin.
	Origin string
	Name   string
	Inner  Type // nil for zero-sized
	// Opaque is true when the type's defining module exported it as
	// `opaque <name>`. The construction surface (constructor, unwrap,
	// destructuring, field access) is private to the defining module.
	Opaque bool
	// OwningSourceFile is the absolute path of the file that declared
	// the type. Empty for types declared in the current file under
	// analysis; non-empty for types imported from other modules.
	// Combined with Opaque, this lets the analyzer detect
	// outside-the-module access to an opaque type's representation.
	OwningSourceFile string
	// TypeParams / TypeParamDefs / TypeArgs carry generic-type-parameter
	// information for generic opaque externs (`host type Task<T>`,
	// `host type Channel<T>`). Empty on the non-generic case.
	// TypeParams is the name list ["T"]; TypeParamDefs is the parallel
	// list of TypeParam_ pointers shared between the declaration and
	// every annotation site so unification through `Task<T>` actually
	// binds T at call sites of `await<T>(t: Task<T>): T`. TypeArgs are
	// set at use sites by ResolveTypeExpr's instantiation path —
	// they're nil on the registry entry, populated when a `Task<Int>`
	// (or another concrete instantiation) annotation resolves.
	TypeParams    []string
	TypeParamDefs []*TypeParam_
	TypeArgs      []Type
}

func (t *DistinctType) String() string {
	if len(t.TypeArgs) > 0 {
		args := make([]string, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			args[i] = a.String()
		}
		return t.Name + "<" + strings.Join(args, ", ") + ">"
	}
	return t.Name
}
func (t *DistinctType) typeTag() {}

// ---------------------------------------------------------------------------
// Compound types
// ---------------------------------------------------------------------------

// FuncType represents a function type.
type FuncType struct {
	Params       []Type
	Return       Type
	DefaultCount int // total number of params with default values (any position)
	// WhereBounds carries `where T: Comparable` constraints. The checker walks
	// them at the call site to record the (concrete, Iface) dispatch demand and
	// reject non-conformance. See WhereBound and constrained interface
	// functions.
	//
	// "Empty for ordinary functions" was recorded here and is FALSE — an
	// ordinary `fn f<T>(x: T) where T: Display` carries its bound here too, and
	// checkFunc's own `pushWhereBounds(ft.WhereBounds)` only means anything
	// because it does. collectTypeParamsByName reads these to find a type
	// parameter the signature mentions NOWHERE ELSE, so a wrong comment here
	// would argue for dropping the only source of one.
	WhereBounds []WhereBound
}

// WhereBound is one resolved entry of a method-level `where` clause: the exact
// TypeParam_ pointer the named variable resolves to (the enclosing interface's
// own type param or an explicit method-local — the same pointer that lands in
// the call site's substitution map) and the interfaces it must implement.
type WhereBound struct {
	Param  *TypeParam_
	Bounds []*InterfaceType
}

func (t *FuncType) String() string {
	params := make([]string, len(t.Params))
	for i, p := range t.Params {
		if p != nil {
			params[i] = p.String()
		} else {
			params[i] = "?"
		}
	}
	ret := "Unit"
	if t.Return != nil {
		ret = t.Return.String()
	}
	return fmt.Sprintf("(%s) -> %s", strings.Join(params, ", "), ret)
}

func (t *FuncType) typeTag() {}

// TupleType represents a tuple type.
type TupleType struct{ Elems []Type }

func (t *TupleType) String() string {
	elems := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		elems[i] = e.String()
	}
	return fmt.Sprintf("(%s)", strings.Join(elems, ", "))
}

func (t *TupleType) typeTag() {}

// ListType represents a list type.
type ListType struct{ Elem Type }

func (t *ListType) String() string { return fmt.Sprintf("List<%s>", t.Elem.String()) }
func (t *ListType) typeTag()       {}

// MapType represents a map type.
type MapType struct {
	Key Type
	Val Type
}

func (t *MapType) String() string {
	return fmt.Sprintf("Map<%s, %s>", t.Key.String(), t.Val.String())
}

func (t *MapType) typeTag() {}

// AnonStructType represents an anonymous struct type.
type AnonStructType struct{ Fields []FieldDef }

func (t *AnonStructType) String() string {
	fields := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		fields[i] = fmt.Sprintf("%s: %s", f.Name, f.Type.String())
	}
	return fmt.Sprintf("{%s}", strings.Join(fields, ", "))
}

func (t *AnonStructType) typeTag() {}

// ---------------------------------------------------------------------------
// Generic / inference support
// ---------------------------------------------------------------------------

// TypeParam_ represents a type parameter (e.g. T in List<T>). Bounds
// carries the resolved interface bounds from `where T: A and B`; at use sites,
// when this TypeParam_ is solved to a concrete type, the checker
// verifies the concrete type implements every bound interface. Empty
// means "unbounded."
//
// AsWritten is the original textual form of each bound exactly as the
// user typed it — for `where T: ShowAndTag` it is ["ShowAndTag"], even
// though Bounds expands to [Showable, Tagged] after alias resolution.
// Hover and other display paths render from AsWritten so an alias
// appears as its name, not its expansion. The checker continues to use
// Bounds for enforcement.
type TypeParam_ struct {
	Name_     string
	Bounds    []*InterfaceType
	AsWritten []string
}

func (t *TypeParam_) String() string { return t.Name_ }
func (t *TypeParam_) typeTag()       {}

// BoundAliasType represents a `typealias Name A and B` declaration —
// a named conjunction of interfaces usable only in interface-bound
// positions (e.g. `where T: Name`). The expansion happens during type-param
// build: each BoundAliasType in a bound list is replaced by its
// underlying interfaces. Outside bound positions (struct fields,
// function parameters, etc.) BoundAliasType is rejected with a clear
// error pointing the user at `where T: Name`.
type BoundAliasType struct {
	Name_  string
	Bounds []*InterfaceType
}

func (t *BoundAliasType) String() string { return t.Name_ }
func (t *BoundAliasType) typeTag()       {}

// TypeVar represents a type variable used during type inference.
type TypeVar struct {
	ID       int
	Resolved Type
}

func (t *TypeVar) String() string {
	if t.Resolved != nil {
		return t.Resolved.String()
	}
	return fmt.Sprintf("?%d", t.ID)
}

func (t *TypeVar) typeTag() {}

// MethodSig describes a method signature within an interface.
//
// ParamNames runs alongside Params with the same length and ordering — needed
// to enforce Swift-style parameter-name matching at impl sites (the spec says
// non-self parameter names must match the interface declaration). HasDefault
// is true when the interface supplied a default body for the method, in which
// case impls may omit it. Open is true when the interface marked the default
// with the `open` modifier — only open defaults may be overridden by an
// `impl Iface for T { fn name(...) }`. Open is meaningless when HasDefault is false
// (the parser rejects `open` on non-default methods).
type MethodSig struct {
	Name       string
	ParamNames []string
	Params     []Type
	Return     Type
	HasDefault bool
	// DefaultCount is the number of trailing parameters that carry a default
	// value, so a call may omit them. Mirrors FuncType.DefaultCount; the
	// call-site arity check subtracts it to get the minimum argument count.
	DefaultCount int
	Open         bool
	// Extern marks a host-backed default (`host fn` in the interface body):
	// provided to every implementor by the runtime, so it is NOT a required
	// method even though it has no Nomi body. Treated like HasDefault for
	// conformance (an implementor need not supply it).
	Extern bool
	// WhereBounds carries this method's resolved `where` constraints (see
	// WhereBound). interfaceMethodFuncType copies them onto the call's FuncType
	// so the checker records the (concrete, Iface) demand per dispatch.
	WhereBounds []WhereBound
}

// InterfaceFieldDef describes a `field name: Type` requirement on an
// interface. A struct with an `impl Iface for Struct` block is required to declare
// a field with the same name and exact (Nomi-nominal) type. Defaults are
// not part of the interface contract — they live on the impl struct.
type InterfaceFieldDef struct {
	Name string
	Type Type
}

// InterfaceType represents an interface type.
type InterfaceType struct {
	// Origin is the declaring file's build key; see StructType.Origin.
	// An interface is nominal for exactly the reason a struct is: two
	// files may each declare a `Renderer`, and a `where T: Renderer`
	// bound names one of them. Without it, an impl written against one
	// declaration satisfied a bound naming the other — a wrong ANSWER,
	// because the call then dispatched to the other interface's method.
	Origin        string
	Name          string
	Methods       []MethodSig
	Fields        []InterfaceFieldDef
	TypeParams    []string      // interface's declared type parameter names (e.g. ["T"] for Iter<T>)
	TypeParamDefs []*TypeParam_ // the actual TypeParam_ pointers referenced by Methods
	TypeArgs      []Type        // concrete type arguments at a use site (e.g. [(K, V)] for Iter<(K, V)>)
	SelfParam     *TypeParam_   // the dedicated TypeParam_ for interface `self` referenced inside Methods (impl validation substitutes it for the implementing type)
}

func (t *InterfaceType) String() string {
	if len(t.TypeArgs) > 0 {
		args := make([]string, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			args[i] = a.String()
		}
		return t.Name + "<" + strings.Join(args, ", ") + ">"
	}
	return t.Name
}
func (t *InterfaceType) typeTag() {}

// ---------------------------------------------------------------------------
// TypesEqual — structural equality
// ---------------------------------------------------------------------------

// TypesEqual reports whether two types are structurally equal.
// ContainsTypeParam returns true if the type contains any unresolved type parameters.
func ContainsTypeParam(t Type) bool {
	if t == nil {
		return false
	}
	switch t := t.(type) {
	case *TypeParam_:
		return true
	case *ListType:
		return ContainsTypeParam(t.Elem)
	case *MapType:
		return ContainsTypeParam(t.Key) || ContainsTypeParam(t.Val)
	case *FuncType:
		for _, p := range t.Params {
			if ContainsTypeParam(p) {
				return true
			}
		}
		return ContainsTypeParam(t.Return)
	case *TupleType:
		for _, e := range t.Elems {
			if ContainsTypeParam(e) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, a := range t.TypeArgs {
			if ContainsTypeParam(a) {
				return true
			}
		}
		return false
	case *StructType:
		for _, a := range t.TypeArgs {
			if ContainsTypeParam(a) {
				return true
			}
		}
		return false
	case *AnonStructType:
		for _, f := range t.Fields {
			if ContainsTypeParam(f.Type) {
				return true
			}
		}
		return false
	case *InterfaceType:
		for _, a := range t.TypeArgs {
			if ContainsTypeParam(a) {
				return true
			}
		}
		return false
	case *DistinctType:
		// Generic opaque externs (`host type Task<T>`,
		// `host type Channel<T>`) carry TypeArgs at use sites — an
		// unsolved T inside Task<T> means the carrier is still generic.
		for _, a := range t.TypeArgs {
			if ContainsTypeParam(a) {
				return true
			}
		}
		return false
	case *PartialType:
		return ContainsTypeParam(t.Inner)
	default:
		return false
	}
}

func TypesEqual(a, b Type) bool {
	// nil means unknown — treat as compatible.
	if a == nil || b == nil {
		return true
	}

	// Follow TypeVar resolved chains.
	a = resolveTypeVar(a)
	b = resolveTypeVar(b)

	// Any is compatible with everything.
	if a == TypeAny || b == TypeAny {
		return true
	}

	// Infallible is compatible with everything (bottom type).
	if a == TypeInfallible || b == TypeInfallible {
		return true
	}

	// `embeds` subtype coercion: a value of an embedded type X (struct or
	// distinct) is also a value of the enum E that embeds X. Matches the
	// rule in unifyFull and the runtime's bare-embedded-value handling in
	// EnumPattern matches. Symmetric so callers don't have to argue about
	// which side is "expected".
	if et, ok := a.(*EnumType); ok {
		if isEmbeddedTypeOf(b, et) {
			return true
		}
	}
	if et, ok := b.(*EnumType); ok {
		if isEmbeddedTypeOf(a, et) {
			return true
		}
	}

	switch at := a.(type) {
	case *PrimitiveType:
		bt, ok := b.(*PrimitiveType)
		return ok && samePrimitive(at, bt)

	case *StructType:
		bt, ok := b.(*StructType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) {
			return false
		}
		// If either side has no type args (unparameterized), match on name alone.
		if len(at.TypeArgs) == 0 || len(bt.TypeArgs) == 0 {
			return true
		}
		if len(at.TypeArgs) != len(bt.TypeArgs) {
			return false
		}
		for i := range at.TypeArgs {
			if !TypesEqual(at.TypeArgs[i], bt.TypeArgs[i]) {
				return false
			}
		}
		return true

	case *EnumType:
		bt, ok := b.(*EnumType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) {
			return false
		}
		// If either side has no type args (unparameterized), match on name alone.
		if len(at.TypeArgs) == 0 || len(bt.TypeArgs) == 0 {
			return true
		}
		if len(at.TypeArgs) != len(bt.TypeArgs) {
			return false
		}
		for i := range at.TypeArgs {
			if !TypesEqual(at.TypeArgs[i], bt.TypeArgs[i]) {
				return false
			}
		}
		return true

	case *DistinctType:
		bt, ok := b.(*DistinctType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) {
			return false
		}
		// Generic opaque externs (`host type Task<T>` etc.) carry
		// TypeArgs at use sites — equality requires matching args.
		// Non-generic distinct types leave TypeArgs nil on both sides,
		// so the lengths-equal check below short-circuits cleanly.
		if len(at.TypeArgs) != len(bt.TypeArgs) {
			return false
		}
		for i := range at.TypeArgs {
			if !TypesEqual(at.TypeArgs[i], bt.TypeArgs[i]) {
				return false
			}
		}
		return true

	case *FuncType:
		bt, ok := b.(*FuncType)
		if !ok || len(at.Params) != len(bt.Params) {
			return false
		}
		for i := range at.Params {
			if !TypesEqual(at.Params[i], bt.Params[i]) {
				return false
			}
		}
		return TypesEqual(normalizeReturn(at.Return), normalizeReturn(bt.Return))

	case *TupleType:
		bt, ok := b.(*TupleType)
		if !ok || len(at.Elems) != len(bt.Elems) {
			return false
		}
		for i := range at.Elems {
			if !TypesEqual(at.Elems[i], bt.Elems[i]) {
				return false
			}
		}
		return true

	case *ListType:
		bt, ok := b.(*ListType)
		return ok && TypesEqual(at.Elem, bt.Elem)

	case *MapType:
		bt, ok := b.(*MapType)
		return ok && TypesEqual(at.Key, bt.Key) && TypesEqual(at.Val, bt.Val)

	case *AnonStructType:
		bt, ok := b.(*AnonStructType)
		if !ok || len(at.Fields) != len(bt.Fields) {
			return false
		}
		bByName := make(map[string]Type, len(bt.Fields))
		for _, f := range bt.Fields {
			bByName[f.Name] = f.Type
		}
		for _, f := range at.Fields {
			bTy, ok := bByName[f.Name]
			if !ok || !TypesEqual(f.Type, bTy) {
				return false
			}
		}
		return true

	case *TypeParam_:
		bt, ok := b.(*TypeParam_)
		return ok && at.Name_ == bt.Name_

	case *TypeVar:
		// Both must be unresolved at this point (resolved ones were followed).
		bt, ok := b.(*TypeVar)
		return ok && at.ID == bt.ID

	case *InterfaceType:
		bt, ok := b.(*InterfaceType)
		if !ok || !sameNominalIdentity(at.Origin, at.Name, bt.Origin, bt.Name) || len(at.TypeArgs) != len(bt.TypeArgs) {
			return false
		}
		for i := range at.TypeArgs {
			if !TypesEqual(at.TypeArgs[i], bt.TypeArgs[i]) {
				return false
			}
		}
		return true

	default:
		return false
	}
}

// resolveTypeVar follows the Resolved chain of TypeVars.
func resolveTypeVar(t Type) Type {
	for {
		tv, ok := t.(*TypeVar)
		if !ok || tv.Resolved == nil {
			return t
		}
		t = tv.Resolved
	}
}

// IsZeroSized returns true for types that carry no data.
// Zero-sized host types and zero-sized distinct types don't need
// constructor functions when embedded in enums.
func IsZeroSized(t Type) bool {
	switch t := t.(type) {
	case *PrimitiveType:
		switch t.Name_ {
		case "True", "False", "Unit", "Infallible":
			return true
		}
	case *DistinctType:
		return t.Inner == nil
	}
	return false
}

// normalizeReturn treats a nil return as TypeUnit. An empty tuple `()` is the
// structural twin of Unit (both denote "returns nothing") — the checker infers
// `()` for a FuncType return when the body is Unit-typed — so collapse it too,
// converging the two spellings everywhere this runs (and matching the same
// equivalence in unifyFull).
func normalizeReturn(t Type) Type {
	if t == nil || isEmptyTuple(t) {
		return TypeUnit
	}
	return t
}

// isEmptyTuple reports whether t (after resolving type vars) is the empty tuple
// `()`, the structural twin of the TypeUnit singleton.
func isEmptyTuple(t Type) bool {
	tt, ok := resolveTypeVar(t).(*TupleType)
	return ok && len(tt.Elems) == 0
}

// isUnitLike reports whether t denotes "no value" — the TypeUnit singleton or
// its empty-tuple `()` twin.
func isUnitLike(t Type) bool {
	return resolveTypeVar(t) == TypeUnit || isEmptyTuple(t)
}
