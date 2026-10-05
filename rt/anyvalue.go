package rt

import "reflect"

// Value is what the VM's reference and erased registers hold: a Go `any` over
// the runtime's own representations. The dynamic types are a closed set, and
// every kernel in this file and in render.go switches over exactly this set:
//
//	Int, Float, Bool, String   int64, float64, bool, string
//	Byte, Bytes, Decimal       Byte, Bytes, Decimal
//	Unit, Dynamic              Unit, Dynamic
//	List, Vector, Map          *List[Value], Vector[Value], Map[Value, Value]
//	composites                 *Record (struct, enum, tuple, record, distinct)
//	an opaque host value       HostHandle
//	a lazy Iter                Seq[Value]
//	anything else              Opaque (closures are Opaque plus Closure)
//
// A Set is std's `struct Set { items: Map<T, Bool> }`, so it is a *Record over
// a `sets.Set` descriptor. A Range is
// likewise std's struct.
//
// An alias rather than an interface with methods, so an int64 in a register is
// an int64 and nothing wraps it; the cost of erasure is the interface header,
// and it is paid only in positions whose static type is erased.
type Value = any

// Opaque is a runtime object with no structure a kernel may look into: a
// closure, a task, a channel, a context, a supervisor, a module. It reads the
// same in all three renderings, is equal to nothing (Equal answers false for
// every such kind, even against itself), and hashes to 0 because it can never
// be a Map key.
type Opaque interface {
	OpaqueText() string
}

// Closure is a function value. rt can hold one, store it in a collection or a
// record and render it, but not call it: calling is the VM's business.
type Closure interface {
	Opaque
	NomiClosure()
}

// HostHandle is an opaque Go value behind a Nomi `host type` whose Go type rt
// does not know (a registered extern type). It reads as `<Name>`, and two
// handles are equal only when their type names match and their Go values are
// identical: pointer identity for pointers, `==` for comparable non-pointers,
// never for slices, maps and funcs. It is not a Map key.
type HostHandle struct {
	TypeName string
	Value    any
}

// EmbeddedPayload returns the value an `embeds` variant carries, or v itself
// for any other value. An `embeds` variant is its payload for equality,
// hashing and the `values:` row.
func EmbeddedPayload(v any) any {
	if r, ok := v.(*Record); ok && r.Desc.Kind == KindEnum && r.Desc.Variants[r.Tag].Shape == VariantEmbedded {
		return r.Field(0)
	}
	return v
}

// Equal is Nomi's structural equality over Values. Reflexive on NaN,
// -0.0 == 0.0, Decimal by mathematical value, Maps order-insensitive, an
// `embeds` variant equal to its payload, and nominal identity by the short
// type name. User `Equatable` impls are the VM's to dispatch before this, or
// to hand in through Keys.
func Equal(a, b any) bool { return (*Keys)(nil).Equal(a, b) }

// Keys is how a program's own `Equatable` and `Hashable` impls reach the
// structural kernels. Equal and Hash consult it at every declared record,
// at the top and nested in a container, a tuple, a record's field or an
// enum's payload, so a Map key, a Set element and `==` on a container hold
// the element type's impl wherever the type appears. A nil *Keys is
// structural throughout: Equal and Hash below.
//
// Everything the hooks do not own compares and hashes structurally, which is
// what a derived impl answers.
type Keys struct {
	Hooks KeyHooks
}

// KeyHooks answers for the records whose type has a hand-written impl.
// EqualRecord answers a == b for two records when it owns their type (a
// hand-written `impl Equatable`); HashRecord answers r's hash when it owns
// r's type.
type KeyHooks interface {
	EqualRecord(a, b *Record) (equal, owned bool)
	HashRecord(r *Record) (hash uint64, owned bool)
}

// EqualFunc is k.Equal as a kernel argument: Equal itself for a nil k, so the
// structural path allocates no method value.
func (k *Keys) EqualFunc() func(a, b any) bool {
	if k == nil {
		return Equal
	}
	return k.Equal
}

// HashFunc is k.Hash as a kernel argument; see EqualFunc.
func (k *Keys) HashFunc() func(any) uint64 {
	if k == nil {
		return Hash
	}
	return k.Hash
}

// Equal is Nomi's equality over Values with k's impls; see Keys.
func (k *Keys) Equal(a, b any) bool {
	a, b = EmbeddedPayload(a), EmbeddedPayload(b)
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && EqFloat(av, bv)
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case Decimal:
		bv, ok := b.(Decimal)
		return ok && EqDecimal(av, bv)
	case Byte:
		bv, ok := b.(Byte)
		return ok && av == bv
	case Bytes:
		bv, ok := b.(Bytes)
		return ok && av == bv
	case Unit:
		_, ok := b.(Unit)
		return ok
	case *List[any]:
		bv, ok := b.(*List[any])
		return ok && ListCellsEqual[any, List[any]](av, bv, k.EqualFunc())
	case Vector[any]:
		bv, ok := b.(Vector[any])
		return ok && VectorEqual(av, bv, k.EqualFunc())
	case Map[any, any]:
		bv, ok := b.(Map[any, any])
		return ok && MapEqual(av, bv, k.HashFunc(), k.EqualFunc(), k.EqualFunc())
	case *Record:
		bv, ok := b.(*Record)
		if !ok {
			return false
		}
		if k != nil && k.Hooks != nil {
			if eq, owned := k.Hooks.EqualRecord(av, bv); owned {
				return eq
			}
		}
		return k.recordsEqual(av, bv)
	case HostHandle:
		bv, ok := b.(HostHandle)
		return ok && hostHandlesEqual(av, bv)
	}
	return false
}

// recordsEqual has no identity shortcut: a record holding a Dynamic or an
// opaque value is unequal to itself, because those are unequal to themselves.
func (k *Keys) recordsEqual(a, b *Record) bool {
	af, bf := a.Desc.Kind.family(), b.Desc.Kind.family()
	if af != bf {
		return false
	}
	switch a.Desc.Kind {
	case KindTuple:
		return k.fieldsEqualInOrder(a, b)
	case KindEnum:
		if a.Desc.Short != b.Desc.Short {
			return false
		}
		av, bv := a.Variant(), b.Variant()
		if av.Name != bv.Name {
			return false
		}
		aBare, bBare := av.Shape == VariantBare, bv.Shape == VariantBare
		if aBare || bBare {
			return aBare && bBare
		}
		switch {
		case av.Shape == VariantFields && bv.Shape == VariantFields:
			return k.namedFieldsEqual(a, b)
		case av.Shape != VariantFields && bv.Shape != VariantFields:
			return k.Equal(a.Field(0), b.Field(0))
		}
		return k.Equal(payloadValue(a), payloadValue(b))
	case KindDistinct:
		if a.Desc.Short != b.Desc.Short {
			return false
		}
		an, bn := a.NumFields() == 0, b.NumFields() == 0
		if an || bn {
			return an && bn
		}
		return k.Equal(a.Field(0), b.Field(0))
	}
	// A named struct or an anonymous record.
	return a.Desc.Short == b.Desc.Short && k.namedFieldsEqual(a, b)
}

// family groups the kinds whose values can compare equal: a named struct and
// an anonymous record are one family, told apart by their (empty) short name.
func (k RecordKind) family() RecordKind {
	if k == KindAnon {
		return KindStruct
	}
	return k
}

func (k *Keys) fieldsEqualInOrder(a, b *Record) bool {
	al, bl := a.Layout(), b.Layout()
	if len(al.Fields) != len(bl.Fields) {
		return false
	}
	if al == bl {
		for i := range al.Fields {
			if !k.slotsEqual(a, b, &al.Fields[i]) {
				return false
			}
		}
		return true
	}
	for i := range al.Fields {
		if !k.Equal(a.slot(&al.Fields[i]), b.slot(&bl.Fields[i])) {
			return false
		}
	}
	return true
}

// namedFieldsEqual compares two field sets by name, whatever their order and
// layout: the same count, and every field of a present in b and equal.
func (k *Keys) namedFieldsEqual(a, b *Record) bool {
	al, bl := a.Layout(), b.Layout()
	if len(al.Fields) != len(bl.Fields) {
		return false
	}
	if al == bl {
		for i := range al.Fields {
			if !k.slotsEqual(a, b, &al.Fields[i]) {
				return false
			}
		}
		return true
	}
	for i := range al.Fields {
		j := bl.FieldIndex(al.Fields[i].Name)
		if j < 0 || !k.Equal(a.slot(&al.Fields[i]), b.slot(&bl.Fields[j])) {
			return false
		}
	}
	return true
}

// slotsEqual compares one field of two records that share a layout, reading
// the typed slot directly. Float goes through EqFloat, never a bit compare:
// NaN has many encodings and -0.0 is not +0.0's bits.
func (k *Keys) slotsEqual(a, b *Record, f *FieldDesc) bool {
	switch f.Type {
	case SlotInt, SlotBool, SlotByte:
		return a.W[f.Slot] == b.W[f.Slot]
	case SlotFloat:
		return EqFloat(WordFloat(a.W[f.Slot]), WordFloat(b.W[f.Slot]))
	case SlotString, SlotBytes:
		return a.S[f.Slot] == b.S[f.Slot]
	}
	return k.Equal(a.R[f.Slot], b.R[f.Slot])
}

// payloadValue is a variant's payload as one Value: the single field of a
// positional or embedded variant, and for a field-shaped variant a struct
// named after the variant. Only a comparison across two shapes needs the second; the common
// paths read the fields in place.
func payloadValue(r *Record) any {
	v := r.Variant()
	if v.Shape != VariantFields {
		return r.Field(0)
	}
	specs := make([]FieldSpec, len(v.Fields))
	vals := make([]any, len(v.Fields))
	for i := range v.Fields {
		specs[i] = FieldSpec{Name: v.Fields[i].Name, Type: v.Fields[i].Type}
		vals[i] = r.slot(&v.Fields[i])
	}
	return NewStructDesc(v.Name, specs).Make(vals...)
}

func hostHandlesEqual(a, b HostHandle) bool {
	if ShortTypeName(a.TypeName) != ShortTypeName(b.TypeName) {
		return false
	}
	if a.Value == nil || b.Value == nil {
		return a.Value == b.Value
	}
	av, bv := reflect.ValueOf(a.Value), reflect.ValueOf(b.Value)
	if av.Kind() == reflect.Pointer && bv.Kind() == reflect.Pointer {
		return av.Pointer() == bv.Pointer()
	}
	if av.Type() != bv.Type() || !av.Type().Comparable() {
		return false
	}
	return a.Value == b.Value
}

// Hash is the structural hash consistent with Equal: Equal(a, b) implies
// Hash(a) == Hash(b). It is the hash rt.Map buckets Values on. Kinds that
// cannot be keys hash to 0.
func Hash(v any) uint64 { return (*Keys)(nil).Hash(v) }

// Hash is the hash consistent with k.Equal; see Keys.
func (k *Keys) Hash(v any) uint64 {
	switch x := v.(type) {
	case int64:
		return HashInt(x)
	case float64:
		return HashFloat(x)
	case bool:
		return HashBoolVariant(x)
	case string:
		return HashString(x)
	case Decimal:
		return HashDecimal(x)
	case Byte:
		return HashByte(x)
	case Bytes:
		return HashBytes(x)
	case Unit:
		return HashUnit(x)
	case *List[any]:
		return HashListCells[any, List[any]](x, k.HashFunc())
	case Vector[any]:
		return HashVector(x, k.HashFunc())
	case Map[any, any]:
		return MapHash(x, k.HashFunc(), k.HashFunc())
	case *Record:
		if k != nil && k.Hooks != nil && x != nil {
			// Equal compares an `embeds` variant by its payload, so the
			// payload is what owns the hash.
			if p, ok := EmbeddedPayload(x).(*Record); ok && p != nil {
				if h, owned := k.Hooks.HashRecord(p); owned {
					return h
				}
			}
		}
		return k.hashRecord(x)
	}
	return 0
}

func (k *Keys) hashRecord(r *Record) uint64 {
	switch r.Desc.Kind {
	case KindTuple:
		h := HashTupleSeed
		l := r.Layout()
		for i := range l.Fields {
			h = HashMix(h, k.hashSlot(r, &l.Fields[i]))
		}
		return h
	case KindEnum:
		v := r.Variant()
		switch v.Shape {
		case VariantEmbedded:
			return k.Hash(r.Field(0))
		case VariantBare:
			return HashVariantHead(r.Desc.Short, v.Name)
		case VariantPositional:
			return HashMix(HashVariantHead(r.Desc.Short, v.Name), k.hashSlot(r, &v.Fields[0]))
		}
		return HashMix(HashVariantHead(r.Desc.Short, v.Name), k.hashFields(r, ShortTypeName(v.Name)))
	case KindDistinct:
		h := HashString(r.Desc.Short)
		if r.NumFields() == 1 {
			h = HashMix(h, k.hashSlot(r, &r.Desc.Fields[0]))
		}
		return h
	}
	return k.hashFields(r, r.Desc.Short)
}

// hashFields is a struct's hash: its short name, plus an order-insensitive
// sum over its fields, because Equal compares fields by name.
func (k *Keys) hashFields(r *Record, short string) uint64 {
	h := HashString(short)
	l := r.Layout()
	for i := range l.Fields {
		h += HashFieldEntry(l.Fields[i].Name, k.hashSlot(r, &l.Fields[i]))
	}
	return h
}

func (k *Keys) hashSlot(r *Record, f *FieldDesc) uint64 {
	switch f.Type {
	case SlotInt:
		return HashInt(WordInt(r.W[f.Slot]))
	case SlotFloat:
		return HashFloat(WordFloat(r.W[f.Slot]))
	case SlotBool:
		return HashBoolVariant(WordBool(r.W[f.Slot]))
	case SlotByte:
		return HashByte(WordByte(r.W[f.Slot]))
	case SlotString:
		return HashString(r.S[f.Slot])
	case SlotBytes:
		return HashBytes(Bytes(r.S[f.Slot]))
	}
	return k.Hash(r.R[f.Slot])
}

// HashFieldEntry is one named field's contribution to a struct's structural
// hash. Hash sums these, so the fold is order-insensitive.
func HashFieldEntry(name string, h uint64) uint64 { return HashMix(HashString(name), h) }

// HashVariantHead is an enum variant's structural hash before its payload is
// mixed in: the enum's short name and the variant's name.
func HashVariantHead(enumShort, variant string) uint64 {
	return HashMix(HashString(enumShort), HashString(variant))
}

var (
	hashTrue  = HashVariantHead("Bool", "True")
	hashFalse = HashVariantHead("Bool", "False")
)

// HashBoolVariant is a Bool's structural hash as a Map key: the hash of the
// prelude enum's variant, since Bool is that enum.
func HashBoolVariant(b bool) uint64 {
	if b {
		return hashTrue
	}
	return hashFalse
}
