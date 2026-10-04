package vm

// THE VM'S VALUES ARE rt's.
//
// A register in the word or string bank holds a scalar unboxed; everything
// in the reference bank, and every value a handler, a host call, a collection or a record slot
// holds, is an `rt.Value`: the closed set of Go types rt/anyvalue.go lists.
//
//	Int, Float, Bool, String   int64, float64, bool, string
//	Byte, Bytes, Decimal       rt.Byte, rt.Bytes, rt.Decimal
//	Unit                       rt.Unit
//	List, Vector, Map          *rt.List[any], rt.Vector[any], rt.Map[any, any]
//	struct, enum, tuple,       *rt.Record over an rt.TypeDesc
//	record, distinct, Set,
//	Range, Maybe, Result
//	a lazy Iter                rt.Seq[any]
//	a host type's value        rt.HostHandle
//	function values, tasks,    this package's types, each an rt.Opaque
//	channels, contexts,        (a function value is an rt.Closure)
//	supervisors
//
// Equality, hashing and the three renderings are rt's kernels (rt.Equal,
// rt.Hash, rt.RowText, rt.DisplayText, rt.DebugText); rtkernels_test.go pins
// their answers over one value of each kind the machine holds.
//
// # DESCRIPTORS
//
// A record's descriptor is built from the IR's stored value types
// (`ir.ValType`): a declared struct's or enum's layout gives its fields and
// variants in declaration order, and each field's stored type gives its slot
// (a scalar field in the word or string bank, everything else a reference).
// Where a type states no layout, the construction's own operands decide.
// Descriptors are interned process-wide by their name and layout, so two
// modules that lower one type share one descriptor. A type can still have two
// descriptors (a generic enum at two payload types, or a layout the producer
// did not state), which is why rt's kernels compare identity by name and
// fields by name, and use descriptor identity only as a fast path.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// list is the VM's List: rt's cons cells over any.
type list = rt.List[any]

// vmap is the VM's Map: rt's persistent map over any, hashed and compared by
// rt's structural kernels.
type vmap = rt.Map[any, any]

func cons(head any, tail *list) *list { return rt.ConsCell[any, list](head, tail) }

// listOf builds a list from items, in order.
func listOf(items []any) *list {
	var out *list
	for i := len(items) - 1; i >= 0; i-- {
		out = cons(items[i], out)
	}
	return out
}

// listSlice copies a list's elements into a Go slice.
func listSlice(xs *list) []any {
	if xs == nil {
		return nil
	}
	out := make([]any, 0, xs.Len)
	for ; xs != nil; xs = xs.Tail {
		out = append(out, xs.Head)
	}
	return out
}

// --- descriptors ---------------------------------------------------------

// descs interns every descriptor by its name and layout.
var descs sync.Map // string -> *rt.TypeDesc

func internDesc(key string, build func() *rt.TypeDesc) *rt.TypeDesc {
	if d, ok := descs.Load(key); ok {
		return d.(*rt.TypeDesc)
	}
	d, _ := descs.LoadOrStore(key, build())
	return d.(*rt.TypeDesc)
}

func fieldsKey(specs []rt.FieldSpec) string {
	var b strings.Builder
	for _, s := range specs {
		b.WriteString(s.Name)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(int(s.Type)))
		b.WriteByte(',')
	}
	return b.String()
}

// structDesc is the descriptor of a named struct with these fields, in order.
func structDesc(name string, fields []rt.FieldSpec) *rt.TypeDesc {
	return internDesc("S|"+name+"|"+fieldsKey(fields), func() *rt.TypeDesc { return rt.NewStructDesc(name, fields) })
}

// enumDesc is the descriptor of an enum with these variants, in tag order.
func enumDesc(name string, variants []rt.VariantSpec) *rt.TypeDesc {
	var b strings.Builder
	for _, v := range variants {
		b.WriteString(v.Name)
		b.WriteByte('/')
		b.WriteString(strconv.Itoa(int(v.Shape)))
		b.WriteByte('/')
		b.WriteString(fieldsKey(v.Fields))
		b.WriteByte(';')
	}
	return internDesc("E|"+name+"|"+b.String(), func() *rt.TypeDesc { return rt.NewEnumDesc(name, variants) })
}

// distinctDesc is the descriptor of a distinct type over one value of slot
// type inner, or of a zero-sized marker when inner is nil.
func distinctDesc(name string, inner *rt.SlotType) *rt.TypeDesc {
	key := "D|" + name + "|"
	if inner != nil {
		key += strconv.Itoa(int(*inner))
	}
	return internDesc(key, func() *rt.TypeDesc { return rt.NewDistinctDesc(name, inner) })
}

// markerValue is the one value of the zero-sized distinct type name.
func markerValue(name string) *rt.Record { return distinctDesc(name, nil).New() }

// slotOf is the record slot a field of stored type t takes: a scalar in its
// bank, everything else (a distinct included) a reference.
func slotOf(t *ir.ValType) rt.SlotType {
	if t == nil {
		return rt.SlotRef
	}
	switch t.Kind() {
	case ir.KindInt:
		return rt.SlotInt
	case ir.KindFloat:
		return rt.SlotFloat
	case ir.KindBool:
		return rt.SlotBool
	case ir.KindByte:
		return rt.SlotByte
	case ir.KindString:
		return rt.SlotString
	case ir.KindBytes:
		return rt.SlotBytes
	}
	return rt.SlotRef
}

func slotPtr(s rt.SlotType) *rt.SlotType { return &s }

// typeDescs caches descOfType by the ValType pointer; a type with no
// descriptor caches noDesc.
var typeDescs sync.Map // *ir.ValType -> *rt.TypeDesc

var noDesc = &rt.TypeDesc{}

// descOfType is the descriptor a value of stored type t is built with: a
// declared struct or enum with a stated layout, a distinct or marker, a tuple
// or an anonymous record. Nil for any other type, and for a struct or enum
// whose layout the producer did not state.
func descOfType(t *ir.ValType) *rt.TypeDesc {
	if t == nil {
		return nil
	}
	if d, ok := typeDescs.Load(t); ok {
		if d == noDesc {
			return nil
		}
		return d.(*rt.TypeDesc)
	}
	d := buildDescOfType(t)
	if d == nil {
		typeDescs.Store(t, noDesc)
		return nil
	}
	typeDescs.Store(t, d)
	return d
}

func buildDescOfType(t *ir.ValType) *rt.TypeDesc {
	switch t.Kind() {
	case ir.KindStruct:
		l := t.Layout()
		if l == nil || t.Sym() == nil {
			return nil
		}
		specs := make([]rt.FieldSpec, len(l.Fields))
		for i, f := range l.Fields {
			specs[i] = rt.FieldSpec{Name: f.Name, Type: slotOf(f.Type)}
		}
		return structDesc(t.Sym().Name(), specs)
	case ir.KindEnum:
		l := t.Layout()
		if l == nil || t.Sym() == nil {
			return nil
		}
		variants := make([]rt.VariantSpec, len(l.Variants))
		for i, v := range l.Variants {
			spec := rt.VariantSpec{Name: v.Name}
			switch v.Form {
			case ir.VariantBare:
				spec.Shape = rt.VariantBare
			case ir.VariantPositional:
				spec.Shape = rt.VariantPositional
				spec.Fields = []rt.FieldSpec{{Type: slotOf(v.Fields[0].Type)}}
			case ir.VariantEmbedded:
				spec.Shape = rt.VariantEmbedded
				spec.Fields = []rt.FieldSpec{{Type: rt.SlotRef}}
			case ir.VariantFields:
				spec.Shape = rt.VariantFields
				spec.Fields = make([]rt.FieldSpec, len(v.Fields))
				for j, f := range v.Fields {
					spec.Fields[j] = rt.FieldSpec{Name: f.Name, Type: slotOf(f.Type)}
				}
			default:
				return nil
			}
			variants[i] = spec
		}
		return enumDesc(t.Sym().Name(), variants)
	case ir.KindDistinct:
		if t.Sym() == nil {
			return nil
		}
		if t.NumElems() == 0 {
			return distinctDesc(t.Sym().Name(), nil)
		}
		return distinctDesc(t.Sym().Name(), slotPtr(slotOf(t.Elem(0))))
	case ir.KindTuple:
		slots := make([]rt.SlotType, t.NumElems())
		for i := range slots {
			slots[i] = slotOf(t.Elem(i))
		}
		return rt.TupleDesc(slots...)
	case ir.KindRecord:
		names := t.Names()
		specs := make([]rt.FieldSpec, len(names))
		for i, name := range names {
			specs[i] = rt.FieldSpec{Name: name, Type: slotOf(t.Elem(i))}
		}
		return rt.AnonDesc(specs)
	}
	return nil
}

// The descriptors of values this package builds in Go rather than from a
// graph: the prelude's carrying enums, and the std structs and distincts a
// host answers. Every payload is a reference slot, because a host holds the
// payload boxed.
var (
	maybeDesc = enumDesc("maybe.Maybe", []rt.VariantSpec{
		{Name: "Some", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
		{Name: "None", Shape: rt.VariantBare},
	})
	resultDesc = enumDesc("results.Result", []rt.VariantSpec{
		{Name: "Ok", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
		{Name: "Err", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
	})
	orderingDesc = enumDesc("comparable.Ordering", []rt.VariantSpec{
		{Name: "Less", Shape: rt.VariantBare},
		{Name: "Equal", Shape: rt.VariantBare},
		{Name: "Greater", Shape: rt.VariantBare},
	})
	outcomeDesc = enumDesc("tasks.Outcome", []rt.VariantSpec{
		{Name: "Completed", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
		{Name: "Cancelled", Shape: rt.VariantBare},
		{Name: "Failed", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
	})
	failureDesc = enumDesc("tasks.Failure", []rt.VariantSpec{
		{Name: "Panicked", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
		{Name: "Errored", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
	})
	channelDesc = structDesc("channels.Channel", []rt.FieldSpec{
		{Name: "sender", Type: rt.SlotRef}, {Name: "receiver", Type: rt.SlotRef}})
	startupDesc = structDesc("startup.Startup", []rt.FieldSpec{
		{Name: "env", Type: rt.SlotRef}, {Name: "args", Type: rt.SlotRef}})
	setDesc   = structDesc("sets.Set", []rt.FieldSpec{{Name: "items", Type: rt.SlotRef}})
	rangeDesc = structDesc("ranges.Range", []rt.FieldSpec{
		{Name: "start", Type: rt.SlotRef}, {Name: "end", Type: rt.SlotRef}, {Name: "inclusive", Type: rt.SlotBool}})
)

// The prelude's carrying enums, as values a host answers.
var noneValue any = maybeDesc.NewVariant(1)

func some(v any) any     { return maybeDesc.MakeVariant(0, v) }
func okValue(v any) any  { return resultDesc.MakeVariant(0, v) }
func errValue(v any) any { return resultDesc.MakeVariant(1, v) }

// maybeOf boxes an rt Maybe as the prelude's Maybe.
func maybeOf[T any](m rt.Maybe[T]) any {
	if m.Tag == rt.TagSome {
		return some(m.Some)
	}
	return noneValue
}

// enumRecord answers v as an enum variant.
func enumRecord(v any) (*rt.Record, bool) {
	r, ok := v.(*rt.Record)
	if !ok || r == nil || r.Desc.Kind != rt.KindEnum {
		return nil, false
	}
	return r, true
}

// variantName is an enum record's variant.
func variantName(r *rt.Record) string { return r.Desc.Variants[r.Tag].Name }

// isEnum reports whether r is a variant of the enum named name (qualified).
func isEnum(r *rt.Record, name string) bool { return r.Desc.Name == name }

// payloadOf is a positional or embedded variant's one payload; ok is false
// for a bare or field-shaped variant.
func payloadOf(r *rt.Record) (any, bool) {
	switch r.Desc.Variants[r.Tag].Shape {
	case rt.VariantPositional, rt.VariantEmbedded:
		return r.Field(0), true
	}
	return nil, false
}

// maybeParts reads a Maybe: its payload and whether it is Some.
func maybeParts(v any) (payload any, isSome, ok bool) {
	r, isEnumRec := enumRecord(v)
	if !isEnumRec || !isEnum(r, "maybe.Maybe") {
		return nil, false, false
	}
	switch variantName(r) {
	case "Some":
		p, has := payloadOf(r)
		return p, true, has
	case "None":
		return nil, false, true
	}
	return nil, false, false
}

// orderingOf reads a prelude Ordering.
func orderingOf(v any) (rt.Ordering, bool) {
	r, ok := enumRecord(v)
	if !ok || !isEnum(r, "comparable.Ordering") {
		return rt.Ordering{}, false
	}
	tag := rt.OrderingTag(variantName(r))
	if tag == rt.TagInvalid {
		return rt.Ordering{}, false
	}
	return rt.Ordering{Tag: tag}, true
}

// structRecord answers v as a struct or anonymous record named name (a
// qualified runtime name; "" for an anonymous record).
func structRecord(v any, name string) (*rt.Record, bool) {
	r, ok := v.(*rt.Record)
	if !ok || r == nil || (r.Desc.Kind != rt.KindStruct && r.Desc.Kind != rt.KindAnon) || r.Desc.Name != name {
		return nil, false
	}
	return r, true
}

// newSet is std's Set over items: a membership map, a duplicate keeping its
// first position.
func newSet(items []any) *rt.Record { return newSetKeyed(nil, items) }

// newSetKeyed is newSet hashing and comparing with k (keys.go).
func newSetKeyed(k *rt.Keys, items []any) *rt.Record {
	entries := make([]rt.MapEntry[any, any], len(items))
	for i, it := range items {
		entries[i] = rt.MapEntry[any, any]{Key: it, Val: true}
	}
	return setFromMap(mapOf(k, entries))
}

// setFromMap wraps a membership map in std's Set.
func setFromMap(items vmap) *rt.Record {
	r := setDesc.New()
	r.R[0] = items
	return r
}

// setItems is a Set's membership map.
func setItems(v any) (vmap, error) {
	r, ok := structRecord(v, "sets.Set")
	if !ok {
		return vmap{}, fmt.Errorf("vm: set receiver is %T, want Set", v)
	}
	raw, _ := r.FieldNamed("items")
	items, ok := raw.(vmap)
	if !ok {
		return vmap{}, fmt.Errorf("vm: Set.items is %T, want Map", raw)
	}
	return items, nil
}

// runtimeTypeName is the declared type a dispatched call selects its body by:
// a struct's or distinct's qualified name. An `embeds` variant is its payload,
// so a widened struct dispatches as itself.
func runtimeTypeName(v any) string {
	switch v.(type) {
	case int64:
		return "Int"
	case float64:
		return "Float"
	case bool:
		return "Bool"
	case string:
		return "String"
	}
	r, ok := v.(*rt.Record)
	if !ok || r == nil {
		return ""
	}
	switch r.Desc.Kind {
	case rt.KindStruct, rt.KindDistinct:
		return r.Desc.Name
	}
	return ""
}

// --- registers -----------------------------------------------------------

// boxWord is a word register's value as the rt value of its type.
func boxWord(w uint64, ty *ir.ValType) any {
	switch ty.Kind() {
	case ir.KindInt:
		return int64(w)
	case ir.KindFloat:
		return math.Float64frombits(w)
	case ir.KindBool:
		return w != 0
	case ir.KindByte:
		return rt.Byte(w)
	case ir.KindDistinct:
		// A distinct over a word scalar: its identity is its stored type's.
		if d := descOfType(ty); d != nil && len(d.Fields) == 1 && d.NW == 1 {
			r := d.New()
			r.W[0] = w
			return r
		}
	}
	panic("vm: a word register holds a " + ty.String())
}

// unboxWord is the word a scalar value occupies.
func unboxWord(v any) (uint64, bool) {
	switch x := v.(type) {
	case int64:
		return uint64(x), true
	case float64:
		return math.Float64bits(x), true
	case bool:
		return b2w(x), true
	case rt.Byte:
		return uint64(x), true
	case *rt.Record:
		if x != nil && x.Desc.Kind == rt.KindDistinct && len(x.Desc.Fields) == 1 {
			if len(x.W) == 1 {
				return x.W[0], true
			}
			return unboxWord(x.Field(0))
		}
	}
	return 0, false
}

func boxStr(s string, ty *ir.ValType) any {
	switch ty.Kind() {
	case ir.KindString:
		return s
	case ir.KindBytes:
		return rt.Bytes(s)
	case ir.KindDistinct:
		if d := descOfType(ty); d != nil && len(d.Fields) == 1 && d.NS == 1 {
			r := d.New()
			r.S[0] = s
			return r
		}
	}
	panic("vm: a string register holds a " + ty.String())
}

func unboxStr(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case rt.Bytes:
		return string(x), true
	case *rt.Record:
		if x != nil && x.Desc.Kind == rt.KindDistinct && len(x.Desc.Fields) == 1 {
			if len(x.S) == 1 {
				return x.S[0], true
			}
			return unboxStr(x.Field(0))
		}
	}
	return "", false
}

// boxNone is the value of a type that needs no storage: Unit, or a zero-sized
// marker, whose one value is its descriptor's.
func boxNone(ty *ir.ValType) any {
	if ty != nil && ty.Kind() == ir.KindDistinct {
		if d := descOfType(ty); d != nil {
			return d.New()
		}
	}
	return rt.Unit{}
}

// boolValue is Nomi's Bool as a value: a Go bool, which boxing into an
// interface does not allocate.
func boolValue(b bool) any { return b }

// asBool reads a Bool.
func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func rtMapSize(m vmap) int64 { return rt.MapSize(m) }
