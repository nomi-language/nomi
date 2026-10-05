package rt

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Record is the VM's runtime value for every nominal or structural composite:
// a struct, an enum variant (embedded variants included), a tuple, an
// anonymous record, a distinct value, and the prelude's Maybe, Result and
// Fragment. Its layout, a descriptor plus split slot banks, is the fastest one
// available to a runtime that cannot create Go types.
//
//	Desc  identity and layout, shared by every value of one shape
//	Tag   the variant index for an enum; 0 otherwise
//	W     scalar fields: Int, Float (as bits), Bool (0 or 1), Byte
//	S     String and Bytes fields
//	R     every other field, as a Value
//
// What matters is the text these values render to and the answers equality
// and hashing give, and those kernels (value.go's Equal and Hash, render.go's
// three renderers) walk a Record by its descriptor.
//
// A Record is immutable once constructed. Construction writes slots directly
// (New, then W/S/R), or boxes through Make; an update copies (With, Clone).
type Record struct {
	Desc *TypeDesc
	Tag  int32
	W    []uint64
	S    []string
	R    []any
}

// SlotType is the storage type of one field. It decides the field's bank and,
// for the word bank, how the 64 bits read back.
type SlotType uint8

const (
	SlotRef    SlotType = iota // any Value, in R
	SlotInt                    // int64 in W
	SlotFloat                  // float64 bits in W
	SlotBool                   // 0 or 1 in W
	SlotByte                   // Byte in W
	SlotString                 // string in S
	SlotBytes                  // Bytes in S
)

// Bank is which of a record's three slices holds a field.
type Bank uint8

const (
	BankWord Bank = iota
	BankStr
	BankRef
)

// Bank answers where a field of this type is stored.
func (t SlotType) Bank() Bank {
	switch t {
	case SlotInt, SlotFloat, SlotBool, SlotByte:
		return BankWord
	case SlotString, SlotBytes:
		return BankStr
	}
	return BankRef
}

// SlotTypeOf is the split-bank slot a Value of this dynamic type would take.
// A converter or a descriptor builder that has only a value in hand uses it;
// the VM's bytecode compiler knows the static type instead.
func SlotTypeOf(v any) SlotType {
	switch v.(type) {
	case int64:
		return SlotInt
	case float64:
		return SlotFloat
	case bool:
		return SlotBool
	case Byte:
		return SlotByte
	case string:
		return SlotString
	case Bytes:
		return SlotBytes
	}
	return SlotRef
}

// FieldSpec names one field and its storage type, for building a descriptor.
// A positional field (a tuple slot, a variant's single payload, a distinct's
// inner value) has an empty Name.
type FieldSpec struct {
	Name string
	Type SlotType
}

// FieldDesc is one field of a layout: its name, its storage type and its
// offset within its bank.
type FieldDesc struct {
	Name string
	Type SlotType
	Slot int
}

// Layout is an ordered field list with bank offsets assigned in field order,
// and the size of each bank.
type Layout struct {
	Fields     []FieldDesc
	NW, NS, NR int
}

func newLayout(specs []FieldSpec) Layout {
	var l Layout
	l.Fields = make([]FieldDesc, len(specs))
	for i, s := range specs {
		f := FieldDesc{Name: s.Name, Type: s.Type}
		switch s.Type.Bank() {
		case BankWord:
			f.Slot = l.NW
			l.NW++
		case BankStr:
			f.Slot = l.NS
			l.NS++
		default:
			f.Slot = l.NR
			l.NR++
		}
		l.Fields[i] = f
	}
	return l
}

// FieldIndex is the index of the named field, or -1.
func (l *Layout) FieldIndex(name string) int {
	for i := range l.Fields {
		if l.Fields[i].Name == name {
			return i
		}
	}
	return -1
}

// RecordKind is which family of composite a descriptor describes. The family
// decides equality, hashing and every rendering.
type RecordKind uint8

const (
	KindStruct   RecordKind = iota + 1 // a named struct: `Point{x: 1}`
	KindAnon                           // an anonymous record: `{x: 1}`
	KindTuple                          // `(1, "a")`
	KindEnum                           // a variant of a named enum
	KindDistinct                       // a distinct value `Id(5)` or a marker `Expired`
)

// VariantShape is how one enum variant carries its payload.
type VariantShape uint8

const (
	VariantBare       VariantShape = iota // `Dot`: no payload
	VariantPositional                     // `Circle(5)`: one positional payload field
	VariantFields                         // `Rect{w: 1, h: 2}`: named payload fields
	// VariantEmbedded is an `embeds` variant: the one payload field IS the
	// value. It compares, hashes and renders in a `values:` row as its payload,
	// while its descriptor stays the enum's, which is what dispatch keys on.
	VariantEmbedded
)

// VariantSpec describes one variant for NewEnumDesc.
type VariantSpec struct {
	Name   string
	Shape  VariantShape
	Fields []FieldSpec
}

// VariantDesc is one variant of an enum descriptor.
type VariantDesc struct {
	Name  string
	Shape VariantShape
	Layout
	bare *Record // the shared value of a payload-free variant
}

// TypeDesc is a record's identity and layout.
//
// Name is the runtime identity, qualified by the declaring module
// (`shapes.Point`, `maybe.Maybe`); a tuple and an anonymous record have none.
// Short is the user-facing half, computed once (ShortTypeName). Two values of
// one Nomi type may carry two descriptors (a generic enum instantiated at two
// payload types), so the kernels compare identity by Short name and fields by
// name, and use descriptor pointer equality only as a fast path.
type TypeDesc struct {
	Name     string
	Short    string
	Kind     RecordKind
	Layout                 // struct, anonymous record, tuple, distinct
	Variants []VariantDesc // enum
	marker   *Record       // the shared value of a zero-sized distinct
}

// NewStructDesc describes a named struct with fields in declaration order.
func NewStructDesc(name string, fields []FieldSpec) *TypeDesc {
	return &TypeDesc{Name: name, Short: ShortTypeName(name), Kind: KindStruct, Layout: newLayout(fields)}
}

var anonDescs sync.Map // key -> *TypeDesc

// AnonDesc is the process-wide descriptor of an anonymous record. Fields are
// ordered by name, which is the order an anonymous record's type is keyed by
// and the order both of its renderings print, so the slice passed in is
// sorted by this call's copy and the caller's order does not matter.
func AnonDesc(fields []FieldSpec) *TypeDesc {
	sorted := append([]FieldSpec(nil), fields...)
	sortFieldSpecs(sorted)
	key := "{" + fieldKey(sorted)
	if d, ok := anonDescs.Load(key); ok {
		return d.(*TypeDesc)
	}
	d, _ := anonDescs.LoadOrStore(key, &TypeDesc{Kind: KindAnon, Layout: newLayout(sorted)})
	return d.(*TypeDesc)
}

var tupleDescs sync.Map // key -> *TypeDesc

// TupleDesc is the process-wide descriptor of a tuple with these slot types.
func TupleDesc(slots ...SlotType) *TypeDesc {
	specs := make([]FieldSpec, len(slots))
	for i, s := range slots {
		specs[i] = FieldSpec{Type: s}
	}
	key := "(" + fieldKey(specs)
	if d, ok := tupleDescs.Load(key); ok {
		return d.(*TypeDesc)
	}
	d, _ := tupleDescs.LoadOrStore(key, &TypeDesc{Kind: KindTuple, Layout: newLayout(specs)})
	return d.(*TypeDesc)
}

// NewEnumDesc describes an enum. A variant's index in variants is its Tag.
func NewEnumDesc(name string, variants []VariantSpec) *TypeDesc {
	d := &TypeDesc{Name: name, Short: ShortTypeName(name), Kind: KindEnum}
	d.Variants = make([]VariantDesc, len(variants))
	for i, v := range variants {
		fields := v.Fields
		switch v.Shape {
		case VariantBare:
			fields = nil
		case VariantPositional, VariantEmbedded:
			if len(fields) != 1 {
				panic(fmt.Sprintf("rt.NewEnumDesc: %s.%s: a %s variant has exactly one payload field, not %d",
					name, v.Name, shapeName(v.Shape), len(fields)))
			}
		}
		d.Variants[i] = VariantDesc{Name: v.Name, Shape: v.Shape, Layout: newLayout(fields)}
	}
	for i := range d.Variants {
		if d.Variants[i].Shape == VariantBare {
			d.Variants[i].bare = &Record{Desc: d, Tag: int32(i)}
		}
	}
	return d
}

// NewDistinctDesc describes a distinct type over one inner value, or a
// zero-sized marker when inner is nil.
func NewDistinctDesc(name string, inner *SlotType) *TypeDesc {
	d := &TypeDesc{Name: name, Short: ShortTypeName(name), Kind: KindDistinct}
	if inner != nil {
		d.Layout = newLayout([]FieldSpec{{Type: *inner}})
	} else {
		d.marker = &Record{Desc: d}
	}
	return d
}

// VariantIndex is the Tag of the named variant, or -1.
func (d *TypeDesc) VariantIndex(name string) int {
	for i := range d.Variants {
		if d.Variants[i].Name == name {
			return i
		}
	}
	return -1
}

// New allocates a struct, anonymous record, tuple or distinct value with zeroed
// slots, for the caller to fill by bank and offset. A marker returns its one
// shared value.
func (d *TypeDesc) New() *Record {
	if d.Kind == KindEnum {
		panic("rt: TypeDesc.New on enum " + d.Name + "; use NewVariant")
	}
	if d.marker != nil {
		return d.marker
	}
	return allocRecord(d, 0, &d.Layout)
}

// NewVariant allocates variant tag with zeroed payload slots. A payload-free
// variant returns its one shared value, so `Shape.Dot` allocates nothing.
func (d *TypeDesc) NewVariant(tag int) *Record {
	v := &d.Variants[tag]
	if v.bare != nil {
		return v.bare
	}
	return allocRecord(d, int32(tag), &v.Layout)
}

// Records whose word bank fits these sizes take their words in the same
// allocation as the header: one allocation per value instead of two.
type (
	recordW1 struct {
		Record
		w [1]uint64
	}
	recordW2 struct {
		Record
		w [2]uint64
	}
	recordW4 struct {
		Record
		w [4]uint64
	}
)

func allocRecord(d *TypeDesc, tag int32, l *Layout) *Record {
	var r *Record
	switch {
	case l.NW == 0:
		r = &Record{Desc: d, Tag: tag}
	case l.NW == 1:
		x := &recordW1{Record: Record{Desc: d, Tag: tag}}
		x.W = x.w[:]
		r = &x.Record
	case l.NW == 2:
		x := &recordW2{Record: Record{Desc: d, Tag: tag}}
		x.W = x.w[:]
		r = &x.Record
	case l.NW <= 4:
		x := &recordW4{Record: Record{Desc: d, Tag: tag}}
		x.W = x.w[:l.NW:l.NW]
		r = &x.Record
	default:
		r = &Record{Desc: d, Tag: tag, W: make([]uint64, l.NW)}
	}
	if l.NS > 0 {
		r.S = make([]string, l.NS)
	}
	if l.NR > 0 {
		r.R = make([]any, l.NR)
	}
	return r
}

// Make builds a non-enum value from boxed field values in layout order. The
// slow path, for converters and tests; it panics when a value does not fit its
// slot.
func (d *TypeDesc) Make(vals ...any) *Record {
	r := d.New()
	if r == d.marker {
		if len(vals) != 0 {
			panic("rt: marker " + d.Name + " takes no value")
		}
		return r
	}
	fillRecord(r, &d.Layout, vals)
	return r
}

// MakeVariant builds variant tag from boxed payload values in layout order.
func (d *TypeDesc) MakeVariant(tag int, vals ...any) *Record {
	r := d.NewVariant(tag)
	if r.Desc.Variants[tag].bare != nil {
		if len(vals) != 0 {
			panic("rt: bare variant " + d.Name + "." + d.Variants[tag].Name + " takes no payload")
		}
		return r
	}
	fillRecord(r, &d.Variants[tag].Layout, vals)
	return r
}

func fillRecord(r *Record, l *Layout, vals []any) {
	if len(vals) != len(l.Fields) {
		panic(fmt.Sprintf("rt: %s takes %d field(s), not %d", r.Desc.describe(), len(l.Fields), len(vals)))
	}
	for i, v := range vals {
		r.setSlot(&l.Fields[i], v)
	}
}

// Layout is the field layout this value's slots follow: the variant's for an
// enum, the descriptor's otherwise.
func (r *Record) Layout() *Layout {
	if r.Desc.Kind == KindEnum {
		return &r.Desc.Variants[r.Tag].Layout
	}
	return &r.Desc.Layout
}

// Variant is this value's variant descriptor; it panics on a non-enum.
func (r *Record) Variant() *VariantDesc { return &r.Desc.Variants[r.Tag] }

// NumFields is the number of fields in this value's layout.
func (r *Record) NumFields() int { return len(r.Layout().Fields) }

// Field reads field i as a boxed Value.
func (r *Record) Field(i int) any { return r.slot(&r.Layout().Fields[i]) }

// FieldNamed reads the named field.
func (r *Record) FieldNamed(name string) (any, bool) {
	l := r.Layout()
	i := l.FieldIndex(name)
	if i < 0 {
		return nil, false
	}
	return r.slot(&l.Fields[i]), true
}

// SlotValue reads the field f describes as a boxed Value. f must be a field
// of this value's layout; a caller that holds the FieldDesc (a cached field
// lookup) reads through it without a search by index or name.
func (r *Record) SlotValue(f *FieldDesc) any { return r.slot(f) }

func (r *Record) slot(f *FieldDesc) any {
	switch f.Type {
	case SlotInt:
		return int64(r.W[f.Slot])
	case SlotFloat:
		return math.Float64frombits(r.W[f.Slot])
	case SlotBool:
		return r.W[f.Slot] != 0
	case SlotByte:
		return Byte(r.W[f.Slot])
	case SlotString:
		return r.S[f.Slot]
	case SlotBytes:
		return Bytes(r.S[f.Slot])
	}
	return r.R[f.Slot]
}

func (r *Record) setSlot(f *FieldDesc, v any) {
	ok := true
	switch f.Type {
	case SlotInt:
		var x int64
		x, ok = v.(int64)
		r.W[f.Slot] = uint64(x)
	case SlotFloat:
		var x float64
		x, ok = v.(float64)
		r.W[f.Slot] = math.Float64bits(x)
	case SlotBool:
		var x bool
		x, ok = v.(bool)
		r.W[f.Slot] = BoolWord(x)
	case SlotByte:
		var x Byte
		x, ok = v.(Byte)
		r.W[f.Slot] = uint64(x)
	case SlotString:
		r.S[f.Slot], ok = v.(string)
	case SlotBytes:
		var x Bytes
		x, ok = v.(Bytes)
		r.S[f.Slot] = string(x)
	default:
		r.R[f.Slot] = v
	}
	if !ok {
		panic(fmt.Sprintf("rt: %s field %q is a %s slot and got %T",
			r.Desc.describe(), f.Name, slotName(f.Type), v))
	}
}

// Clone copies this value's slots into a fresh record of the same layout. A
// shared payload-free value clones to itself.
func (r *Record) Clone() *Record {
	if (r.Desc.marker == r) || (r.Desc.Kind == KindEnum && r.Desc.Variants[r.Tag].bare == r) {
		return r
	}
	c := allocRecord(r.Desc, r.Tag, r.Layout())
	copy(c.W, r.W)
	copy(c.S, r.S)
	copy(c.R, r.R)
	return c
}

// With is the immutable update behind struct spread and `Struct.update`: a
// copy with field idx[i] replaced by vals[i]. The receiver is unchanged.
func (r *Record) With(idx []int, vals []any) *Record {
	if len(idx) != len(vals) {
		panic("rt: Record.With: indices and values differ in length")
	}
	c := r.Clone()
	l := c.Layout()
	for i, fi := range idx {
		c.setSlot(&l.Fields[fi], vals[i])
	}
	return c
}

// Word encodings of the scalar slot types, for code that writes W directly.
func IntWord(v int64) uint64     { return uint64(v) }
func FloatWord(v float64) uint64 { return math.Float64bits(v) }
func ByteWord(v Byte) uint64     { return uint64(v) }
func BoolWord(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

// And their readings.
func WordInt(w uint64) int64     { return int64(w) }
func WordFloat(w uint64) float64 { return math.Float64frombits(w) }
func WordBool(w uint64) bool     { return w != 0 }
func WordByte(w uint64) Byte     { return Byte(w) }

func (d *TypeDesc) describe() string {
	switch d.Kind {
	case KindTuple:
		return "tuple"
	case KindAnon:
		return "anonymous record"
	}
	return d.Name
}

func shapeName(s VariantShape) string {
	switch s {
	case VariantPositional:
		return "positional"
	case VariantFields:
		return "field-shaped"
	case VariantEmbedded:
		return "embedded"
	}
	return "bare"
}

func slotName(t SlotType) string {
	return [...]string{"Ref", "Int", "Float", "Bool", "Byte", "String", "Bytes"}[t]
}

func fieldKey(specs []FieldSpec) string {
	var b strings.Builder
	for _, s := range specs {
		b.WriteString(s.Name)
		b.WriteByte(':')
		b.WriteByte('0' + byte(s.Type))
		b.WriteByte(',')
	}
	return b.String()
}

func sortFieldSpecs(specs []FieldSpec) {
	for i := 1; i < len(specs); i++ {
		for j := i; j > 0 && specs[j].Name < specs[j-1].Name; j-- {
			specs[j], specs[j-1] = specs[j-1], specs[j]
		}
	}
}

// ShortTypeName is the user-facing half of a runtime type name: the name the
// declaration wrote, with the module qualifier removed.
//
// Runtime type names carry the declaring module as a qualifier
// (`shapes.Point`, `calendar.Date`) because that is what keeps two same-named
// types apart. Nothing user-facing shows it: a value inspects as
// `Point{x: 1}`, not `shapes.Point{x: 1}`.
//
// A declared name can contain dots — a namespaced declaration
// (`pub struct Json.DecodeError`) owns both segments. Module names come from
// file names and are lower-case; declared names are upper-case. So the
// qualifier is exactly the leading run of lower-case segments:
//
//	json.Json.DecodeError -> Json.DecodeError
//	shapes.Point          -> Point
//	Json.DecodeError      -> Json.DecodeError
//
// Cutting at the last dot instead would collapse `Json.DecodeError` to
// `DecodeError` and hand std/json's decode error and std/dynamic's one the
// same dispatch slot.
func ShortTypeName(name string) string {
	for {
		dot := strings.Index(name, ".")
		if dot < 0 || startsUpper(name) {
			return name
		}
		name = name[dot+1:]
	}
}

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}
