package stdlibadapters

// The suites' operands are rt values built here, the way a producer other
// than the adapters lays them out: a named record's fields in NAME order, on
// a descriptor of this builder's own, and an enum's descriptor grown one
// variant shape at a time as values of it are built. So an adapter reads
// every record built here by name (its slow path); the suites re-lay records
// onto the adapters' own descriptors to reach the by-offset path.

import (
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

type recordBuilder struct {
	mu        sync.Mutex
	structs   map[string]*rt.TypeDesc
	distincts map[string]*rt.TypeDesc
	enums     map[string]*builtEnum
}

type builtEnum struct {
	specs []rt.VariantSpec
	tags  map[string]int
	desc  *rt.TypeDesc
}

func newRecordBuilder() *recordBuilder {
	return &recordBuilder{
		structs:   map[string]*rt.TypeDesc{},
		distincts: map[string]*rt.TypeDesc{},
		enums:     map[string]*builtEnum{},
	}
}

// rb is the builder the operand helpers use; each suite starts a fresh one.
var rb = newRecordBuilder()

// fieldsOf sorts name/value pairs by name.
func fieldsOf(kv []any) ([]any, []rt.FieldSpec) {
	type pair struct {
		name string
		val  any
	}
	ps := make([]pair, 0, len(kv)/2)
	for k := 0; k < len(kv); k += 2 {
		ps = append(ps, pair{kv[k].(string), kv[k+1]})
	}
	sort.Slice(ps, func(a, b int) bool { return ps[a].name < ps[b].name })
	vals := make([]any, len(ps))
	specs := make([]rt.FieldSpec, len(ps))
	for k, p := range ps {
		vals[k] = p.val
		specs[k] = rt.FieldSpec{Name: p.name, Type: rt.SlotTypeOf(p.val)}
	}
	return vals, specs
}

func specKey(specs []rt.FieldSpec) string {
	var b strings.Builder
	for _, s := range specs {
		b.WriteString(s.Name)
		b.WriteByte(':')
		b.WriteByte('0' + byte(s.Type))
		b.WriteByte(',')
	}
	return b.String()
}

// Struct is a named struct from name/value pairs.
func (b *recordBuilder) Struct(name string, kv ...any) *rt.Record {
	vals, specs := fieldsOf(kv)
	key := name + "|" + specKey(specs)
	b.mu.Lock()
	d, ok := b.structs[key]
	if !ok {
		d = rt.NewStructDesc(name, specs)
		b.structs[key] = d
	}
	b.mu.Unlock()
	return d.Make(vals...)
}

// Distinct wraps inner in the named distinct type.
func (b *recordBuilder) Distinct(name string, inner any) *rt.Record {
	t := rt.SlotTypeOf(inner)
	key := name + "|" + string(rune('0'+t))
	b.mu.Lock()
	d, ok := b.distincts[key]
	if !ok {
		d = rt.NewDistinctDesc(name, &t)
		b.distincts[key] = d
	}
	b.mu.Unlock()
	return d.Make(inner)
}

// Variant is a bare variant (no payload) or one carrying a positional
// payload. A Bool is a Go bool.
func (b *recordBuilder) Variant(enum, variant string, payload ...any) any {
	if len(payload) == 0 {
		if rt.ShortTypeName(enum) == "Bool" && (variant == "True" || variant == "False") {
			return variant == "True"
		}
		return b.variant(enum, rt.VariantSpec{Name: variant, Shape: rt.VariantBare}, nil)
	}
	p := payload[0]
	return b.variant(enum, rt.VariantSpec{Name: variant, Shape: rt.VariantPositional,
		Fields: []rt.FieldSpec{{Type: rt.SlotTypeOf(p)}}}, []any{p})
}

// FieldVariant is a variant whose payload is its own named fields.
func (b *recordBuilder) FieldVariant(enum, variant string, kv ...any) any {
	vals, specs := fieldsOf(kv)
	return b.variant(enum, rt.VariantSpec{Name: variant, Shape: rt.VariantFields, Fields: specs}, vals)
}

func (b *recordBuilder) variant(enum string, spec rt.VariantSpec, vals []any) any {
	key := spec.Name + "|" + string(rune('0'+spec.Shape)) + "|" + specKey(spec.Fields)
	b.mu.Lock()
	st := b.enums[enum]
	if st == nil {
		st = &builtEnum{tags: map[string]int{}}
		b.enums[enum] = st
	}
	tag, ok := st.tags[key]
	if !ok {
		st.specs = append(st.specs, spec)
		tag = len(st.specs) - 1
		st.tags[key] = tag
		st.desc = rt.NewEnumDesc(enum, append([]rt.VariantSpec(nil), st.specs...))
	}
	d := st.desc
	b.mu.Unlock()
	return d.MakeVariant(tag, vals...)
}

// Restyle rebuilds a value some adapter produced as this builder would have
// built it, so it can be handed back as an operand from another producer.
func (b *recordBuilder) Restyle(v any) any {
	switch x := v.(type) {
	case *rt.List[any]:
		var items []any
		for n := x; n != nil; n = n.Tail {
			items = append(items, b.Restyle(n.Head))
		}
		return rtList(items...)
	case rt.Map[any, any]:
		ents := rt.MapEntries(x)
		out := make([]rt.MapEntry[any, any], len(ents))
		for k, e := range ents {
			out[k] = rt.MapEntry[any, any]{Key: b.Restyle(e.Key), Val: b.Restyle(e.Val)}
		}
		return rt.MapOf(rt.Hash, rt.Equal, out)
	case *rt.Record:
		l := x.Layout()
		switch x.Desc.Kind {
		case rt.KindEnum:
			vr := x.Variant()
			switch vr.Shape {
			case rt.VariantBare:
				return b.Variant(x.Desc.Name, vr.Name)
			case rt.VariantFields:
				var kv []any
				for k, f := range l.Fields {
					kv = append(kv, f.Name, b.Restyle(x.Field(k)))
				}
				return b.FieldVariant(x.Desc.Name, vr.Name, kv...)
			}
			return b.Variant(x.Desc.Name, vr.Name, b.Restyle(x.Field(0)))
		case rt.KindDistinct:
			return b.Distinct(x.Desc.Name, b.Restyle(x.Field(0)))
		case rt.KindStruct:
			var kv []any
			for k, f := range l.Fields {
				kv = append(kv, f.Name, b.Restyle(x.Field(k)))
			}
			return b.Struct(x.Desc.Name, kv...)
		case rt.KindTuple:
			vals := make([]any, len(l.Fields))
			slots := make([]rt.SlotType, len(l.Fields))
			for k := range l.Fields {
				vals[k] = b.Restyle(x.Field(k))
				slots[k] = rt.SlotTypeOf(vals[k])
			}
			return rt.TupleDesc(slots...).Make(vals...)
		}
	}
	return v
}

// rtList is a list of the given elements.
func rtList(items ...any) *rt.List[any] {
	var out *rt.List[any]
	for k := len(items) - 1; k >= 0; k-- {
		out = rt.ConsCell[any, rt.List[any]](items[k], out)
	}
	return out
}
