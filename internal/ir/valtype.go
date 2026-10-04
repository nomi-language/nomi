package ir

// THE STORED VALUE TYPE OF A TEMPORARY.
//
// Every temporary, parameter, slot and cell carries a ValType: what the value
// held there IS, in Nomi's terms, and therefore which register class a
// bytecode compiler puts it in. It is stored by the producer when the
// temporary is written, and `RuleTempTyped` requires one on every temporary a
// graph names.
//
// # WHAT IT DESCRIBES
//
// The checker's type, reduced to what execution needs. Nominal types keep
// their identity (the `*Symbol` the IR already uses for the declaration, whose
// name is the module-qualified runtime identity `shapes.Point`); structural
// types keep their components; an erased position is `Any`. Nothing here is a
// Go spelling.
//
// # HOW IT RELATES TO `Type` AND `ValShape`
//
// `Type` (table.go) is a name plus a form, interned per lowering, and exists
// for overload selection. It may carry a ValType (`Type.Val`), which is how a
// slot and a cell state theirs. `ValShape` (shape.go) is the coarse class the
// operand-shape rule reads; `ValType.Shape` answers it, and the lint checks
// that the stored type and every writer's derived shape agree.

import (
	"strconv"
	"strings"
)

// ValKind is which member of the vocabulary a ValType is.
type ValKind uint8

const (
	// KindAny is an erased position: a bound-free type parameter, or the
	// element of an untyped literal (`[]`, `#{}`, `None`) whose type argument
	// Nomi has not fixed at that point. Its value describes itself at run
	// time.
	KindAny ValKind = iota + 1
	KindUnit
	KindBool
	KindInt
	KindFloat
	KindByte
	KindString
	KindBytes
	KindDecimal
	// KindStruct is a declared struct. Sym is its identity; Elems are the
	// type arguments of a generic instance.
	KindStruct
	// KindEnum is a declared enum, including the prelude's Maybe, Result and
	// Fragment. Sym is its identity; Elems are the type arguments.
	KindEnum
	// KindDistinct is `type Name Inner` (Elems[0] is Inner) or a zero-sized
	// marker `type Name` (no Elems).
	KindDistinct
	// KindTuple is `(A, B, ...)`; Elems are the components in order.
	KindTuple
	// KindRecord is an anonymous struct `{x: A, y: B}`; Names are the field
	// names in canonical (sorted) order and Elems their types, parallel.
	KindRecord
	KindList
	KindSet
	KindVector
	KindMap
	KindRange
	// KindSeq is a lowered `Iter<T>`.
	KindSeq
	// KindFunc is a function value; Elems are the parameters and Result the
	// result.
	KindFunc
	// KindIface is an existential: a value whose static type is an interface
	// and whose concrete type travels with it. Sym is the interface.
	KindIface
	// KindHandle is an opaque host value the IR only passes around: a
	// Context, a Task, a channel half, a Supervisor, a Regex, a Dynamic.
	// Sym is the host type's declaration; Elems its type arguments.
	KindHandle
)

var valKindNames = [...]string{
	KindAny: "Any", KindUnit: "Unit", KindBool: "Bool", KindInt: "Int",
	KindFloat: "Float", KindByte: "Byte", KindString: "String",
	KindBytes: "Bytes", KindDecimal: "Decimal", KindStruct: "struct",
	KindEnum: "enum", KindDistinct: "distinct", KindTuple: "tuple",
	KindRecord: "record", KindList: "List", KindSet: "Set",
	KindVector: "Vector", KindMap: "Map", KindRange: "Range", KindSeq: "Iter",
	KindFunc: "function", KindIface: "dyn", KindHandle: "handle",
}

func (k ValKind) String() string {
	if int(k) < len(valKindNames) && valKindNames[k] != "" {
		return valKindNames[k]
	}
	return "ValKind?"
}

// RegClass is the register bank a value of a type lives in.
type RegClass uint8

const (
	// ClassNone is Unit: no storage.
	ClassNone RegClass = iota + 1
	// ClassWord is a 64-bit scalar: Int, Float, Bool, Byte, and a distinct
	// over one of those.
	ClassWord
	// ClassStr is a Go string: String, Bytes, and a distinct over one.
	ClassStr
	// ClassRef is everything else, and every erased position.
	ClassRef
)

func (c RegClass) String() string {
	switch c {
	case ClassNone:
		return "none"
	case ClassWord:
		return "word"
	case ClassStr:
		return "str"
	case ClassRef:
		return "ref"
	}
	return "class?"
}

// ValType is one value type. Build it with the constructors below; the zero
// value is not a type.
type ValType struct {
	kind   ValKind
	sym    *Symbol
	elems  []*ValType
	names  []string
	result *ValType
	// layout is a declared struct's fields or a declared enum's variants,
	// stated by the producer after construction so that a type can reach
	// itself through a field. nil until stated. See SetFields.
	layout *Layout
}

// Field is one field of a declared struct or of a struct-shaped variant, or
// the one positional payload of a variant (Name empty), with its value type.
type Field struct {
	Name string
	Type *ValType
}

// VariantForm is how an enum variant carries its payload.
type VariantForm uint8

const (
	// VariantBare carries nothing: `Dot`.
	VariantBare VariantForm = iota + 1
	// VariantPositional carries one positional payload: `Circle(5)`.
	VariantPositional
	// VariantFields carries named payload fields: `Rect{w: 1, h: 2}`.
	VariantFields
	// VariantEmbedded is an `embeds` variant: its one payload is the value.
	VariantEmbedded
)

func (f VariantForm) String() string {
	switch f {
	case VariantBare:
		return "bare"
	case VariantPositional:
		return "positional"
	case VariantFields:
		return "fields"
	case VariantEmbedded:
		return "embedded"
	}
	return "variant?"
}

// Variant is one enum variant, in declaration order: a variant's index is its
// tag.
type Variant struct {
	Name   string
	Form   VariantForm
	Fields []Field
}

// Layout is what a descriptor builder needs to know about a declared type
// beyond its identity: a struct's fields in declaration order, or an enum's
// variants in declaration order.
type Layout struct {
	Fields   []Field
	Variants []Variant
}

// The scalar types, shared.
var (
	AnyType     = &ValType{kind: KindAny}
	UnitType    = &ValType{kind: KindUnit}
	BoolType    = &ValType{kind: KindBool}
	IntType     = &ValType{kind: KindInt}
	FloatType   = &ValType{kind: KindFloat}
	ByteType    = &ValType{kind: KindByte}
	StringType  = &ValType{kind: KindString}
	BytesType   = &ValType{kind: KindBytes}
	DecimalType = &ValType{kind: KindDecimal}
)

func requireTypeSym(sym *Symbol, who string) {
	if sym == nil {
		panic("ir: " + who + ": a nominal type with no declaration identity names nothing")
	}
}

func requireElems(elems []*ValType, who string) []*ValType {
	for _, e := range elems {
		if e == nil {
			panic("ir: " + who + ": a component type is missing")
		}
	}
	return append([]*ValType(nil), elems...)
}

// NewStructType is a declared struct, with a generic instance's arguments.
func NewStructType(sym *Symbol, args ...*ValType) *ValType {
	requireTypeSym(sym, "NewStructType")
	return &ValType{kind: KindStruct, sym: sym, elems: requireElems(args, "NewStructType")}
}

// NewEnumType is a declared enum, with a generic instance's arguments.
func NewEnumType(sym *Symbol, args ...*ValType) *ValType {
	requireTypeSym(sym, "NewEnumType")
	return &ValType{kind: KindEnum, sym: sym, elems: requireElems(args, "NewEnumType")}
}

// NewDistinctType is a distinct type over inner, or a marker when inner is nil.
func NewDistinctType(sym *Symbol, inner *ValType) *ValType {
	requireTypeSym(sym, "NewDistinctType")
	t := &ValType{kind: KindDistinct, sym: sym}
	if inner != nil {
		t.elems = []*ValType{inner}
	}
	return t
}

// NewTupleType is a tuple of parts.
func NewTupleType(parts ...*ValType) *ValType {
	return &ValType{kind: KindTuple, elems: requireElems(parts, "NewTupleType")}
}

// NewRecordType is an anonymous struct. names must be in canonical order and
// parallel to parts.
func NewRecordType(names []string, parts []*ValType) *ValType {
	if len(names) != len(parts) {
		panic("ir: NewRecordType: names and parts differ in length")
	}
	return &ValType{kind: KindRecord, names: append([]string(nil), names...),
		elems: requireElems(parts, "NewRecordType")}
}

func newElemType(k ValKind, who string, elems ...*ValType) *ValType {
	return &ValType{kind: k, elems: requireElems(elems, who)}
}

// NewListType is List<elem>.
func NewListType(elem *ValType) *ValType { return newElemType(KindList, "NewListType", elem) }

// NewSetType is Set<elem>.
func NewSetType(elem *ValType) *ValType { return newElemType(KindSet, "NewSetType", elem) }

// NewVectorType is Vector<elem>.
func NewVectorType(elem *ValType) *ValType { return newElemType(KindVector, "NewVectorType", elem) }

// NewRangeType is Range<elem>.
func NewRangeType(elem *ValType) *ValType { return newElemType(KindRange, "NewRangeType", elem) }

// NewSeqType is a lowered Iter<elem>.
func NewSeqType(elem *ValType) *ValType { return newElemType(KindSeq, "NewSeqType", elem) }

// NewMapType is Map<key, val>.
func NewMapType(key, val *ValType) *ValType { return newElemType(KindMap, "NewMapType", key, val) }

// NewFuncType is a function value's type.
func NewFuncType(params []*ValType, result *ValType) *ValType {
	if result == nil {
		panic("ir: NewFuncType: a function type with no result")
	}
	return &ValType{kind: KindFunc, elems: requireElems(params, "NewFuncType"), result: result}
}

// NewIfaceType is an existential over interface sym.
func NewIfaceType(sym *Symbol) *ValType {
	requireTypeSym(sym, "NewIfaceType")
	return &ValType{kind: KindIface, sym: sym}
}

// NewHandleType is an opaque host value of host type sym.
func NewHandleType(sym *Symbol, args ...*ValType) *ValType {
	requireTypeSym(sym, "NewHandleType")
	return &ValType{kind: KindHandle, sym: sym, elems: requireElems(args, "NewHandleType")}
}

// SetFields states a declared struct's fields, in declaration order. Once.
func (t *ValType) SetFields(fields []Field) {
	if t.kind != KindStruct {
		panic("ir: ValType.SetFields: " + t.String() + " is not a struct")
	}
	t.setLayout(&Layout{Fields: requireFields(fields, "SetFields")})
}

// SetVariants states a declared enum's variants, in declaration order. Once.
func (t *ValType) SetVariants(variants []Variant) {
	if t.kind != KindEnum {
		panic("ir: ValType.SetVariants: " + t.String() + " is not an enum")
	}
	vs := make([]Variant, len(variants))
	for i, v := range variants {
		if v.Name == "" || v.Form == 0 {
			panic("ir: ValType.SetVariants: a variant with no name or no form")
		}
		n := len(v.Fields)
		if (v.Form == VariantBare && n != 0) ||
			((v.Form == VariantPositional || v.Form == VariantEmbedded) && n != 1) {
			panic("ir: ValType.SetVariants: " + v.Name + " is " + v.Form.String() +
				" and carries " + strconv.Itoa(n) + " payloads")
		}
		vs[i] = Variant{Name: v.Name, Form: v.Form, Fields: requireFields(v.Fields, "SetVariants")}
	}
	t.setLayout(&Layout{Variants: vs})
}

func (t *ValType) setLayout(l *Layout) {
	if t.layout != nil {
		panic("ir: ValType: the layout of " + t.String() + " is already stated")
	}
	t.layout = l
}

func requireFields(fields []Field, who string) []Field {
	for _, f := range fields {
		if f.Type == nil {
			panic("ir: ValType." + who + ": field " + f.Name + " has no type")
		}
	}
	return append([]Field(nil), fields...)
}

// Layout is a declared struct's or enum's stated layout, or nil when the
// producer stated none.
func (t *ValType) Layout() *Layout { return t.layout }

// Kind is which member of the vocabulary t is.
func (t *ValType) Kind() ValKind { return t.kind }

// Sym is a nominal type's declaration identity, or nil.
func (t *ValType) Sym() *Symbol { return t.sym }

// NumElems is how many component types t has.
func (t *ValType) NumElems() int { return len(t.elems) }

// Elem is component i: a type argument, a tuple or record part, an element,
// a map's key (0) or value (1), a function parameter, a distinct's inner type.
func (t *ValType) Elem(i int) *ValType { return t.elems[i] }

// Names are a record's field names, parallel to its Elems.
func (t *ValType) Names() []string { return t.names }

// Result is a function type's result, or nil.
func (t *ValType) Result() *ValType { return t.result }

// Class is the register bank a value of t lives in.
func (t *ValType) Class() RegClass {
	switch t.kind {
	case KindUnit:
		return ClassNone
	case KindBool, KindInt, KindFloat, KindByte:
		return ClassWord
	case KindString, KindBytes:
		return ClassStr
	case KindDistinct:
		// A distinct over a scalar is the scalar in a register; its identity
		// matters only in an erased position. A marker stores nothing.
		if len(t.elems) == 0 {
			return ClassNone
		}
		if c := t.elems[0].Class(); c == ClassWord || c == ClassStr {
			return c
		}
	}
	return ClassRef
}

// Shape is the coarse ValShape a value of t has. Types the operand-shape rule
// has no member for answer ValUnknown.
func (t *ValType) Shape() ValShape {
	switch t.kind {
	case KindUnit:
		return ValUnit
	case KindBool:
		return ValBool
	case KindInt:
		return ValInt
	case KindFloat:
		return ValFloat
	case KindDecimal:
		return ValDecimal
	case KindString:
		return ValString
	case KindStruct, KindRecord:
		return ValStruct
	case KindTuple:
		return ValTuple
	case KindEnum:
		return ValVariant
	case KindDistinct:
		return ValDistinct
	case KindList, KindSet, KindVector, KindMap, KindRange:
		return ValContainer
	case KindFunc:
		return ValFunc
	}
	return ValUnknown
}

// Erased reports whether t, or any component of it, is Any: a position whose
// concrete type is not fixed by this graph.
func (t *ValType) Erased() bool {
	if t.kind == KindAny {
		return true
	}
	for _, e := range t.elems {
		if e.Erased() {
			return true
		}
	}
	return t.result != nil && t.result.Erased()
}

// Identical reports whether t and u are the same type. Nominal types compare
// by declaration identity and then by arguments.
func (t *ValType) Identical(u *ValType) bool {
	if t == u {
		return true
	}
	if t == nil || u == nil || t.kind != u.kind || t.sym != u.sym ||
		len(t.elems) != len(u.elems) || len(t.names) != len(u.names) {
		return false
	}
	for i := range t.elems {
		if !t.elems[i].Identical(u.elems[i]) {
			return false
		}
	}
	for i := range t.names {
		if t.names[i] != u.names[i] {
			return false
		}
	}
	if (t.result == nil) != (u.result == nil) {
		return false
	}
	return t.result == nil || t.result.Identical(u.result)
}

// Accepts reports whether a value of type have may be stored where want is
// stated: the two are identical, or they agree everywhere except where one
// side is Any. Any stands for "not fixed here" — an untyped literal before
// its context discharges it, or a type parameter — and matches anything.
func (want *ValType) Accepts(have *ValType) bool {
	if want == nil || have == nil {
		return false
	}
	if want.kind == KindAny || have.kind == KindAny {
		return true
	}
	// Widening to an existential: an erased value is its concrete value,
	// held where the interface is stated, so a declared type in the same
	// register class is stored as it is.
	if want.kind == KindIface && have.kind != KindIface && want.Class() == have.Class() {
		switch have.kind {
		case KindStruct, KindEnum, KindDistinct, KindHandle:
			return true
		}
	}
	if want.kind != have.kind || want.sym != have.sym ||
		len(want.elems) != len(have.elems) || len(want.names) != len(have.names) {
		return false
	}
	for i := range want.elems {
		if !want.elems[i].Accepts(have.elems[i]) {
			return false
		}
	}
	for i := range want.names {
		if want.names[i] != have.names[i] {
			return false
		}
	}
	if (want.result == nil) != (have.result == nil) {
		return false
	}
	return want.result == nil || want.result.Accepts(have.result)
}

// String is t's Nomi-like spelling, for diagnostics and dumps.
func (t *ValType) String() string {
	if t == nil {
		return "<untyped>"
	}
	var b strings.Builder
	t.write(&b)
	return b.String()
}

func (t *ValType) write(b *strings.Builder) {
	args := func(open, close string) {
		b.WriteString(open)
		for i, e := range t.elems {
			if i > 0 {
				b.WriteString(", ")
			}
			e.write(b)
		}
		b.WriteString(close)
	}
	switch t.kind {
	case KindStruct, KindEnum, KindHandle:
		b.WriteString(t.sym.Name())
		if len(t.elems) > 0 {
			args("<", ">")
		}
	case KindDistinct:
		b.WriteString(t.sym.Name())
		if len(t.elems) > 0 {
			args("(", ")")
		}
	case KindIface:
		b.WriteString("dyn ")
		b.WriteString(t.sym.Name())
	case KindTuple:
		args("(", ")")
	case KindRecord:
		b.WriteString("{")
		for i, e := range t.elems {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(t.names[i])
			b.WriteString(": ")
			e.write(b)
		}
		b.WriteString("}")
	case KindList, KindSet, KindVector, KindMap, KindRange, KindSeq:
		b.WriteString(t.kind.String())
		args("<", ">")
	case KindFunc:
		args("(", ")")
		b.WriteString(" -> ")
		t.result.write(b)
	default:
		b.WriteString(t.kind.String())
	}
}
