package vm

// Operands built directly in the machine's representation (see values.go):
// the VM's values are rt's, so a test states them as rt values.

import (
	"sort"

	"github.com/nomi-language/nomi/rt"
)

// rtVariant is a variant of enum (a qualified runtime name): bare with no
// payload, positional with one. Its descriptor carries only this variant, as
// a value built without the enum's declared layout does.
func rtVariant(enum, variant string, payload ...any) *rt.Record {
	spec := rt.VariantSpec{Name: variant, Shape: rt.VariantBare}
	if len(payload) > 0 {
		spec = rt.VariantSpec{Name: variant, Shape: rt.VariantPositional,
			Fields: []rt.FieldSpec{{Type: rt.SlotTypeOf(payload[0])}}}
	}
	return rt.NewEnumDesc(enum, []rt.VariantSpec{spec}).MakeVariant(0, payload...)
}

// rtStruct is a struct named name (an anonymous record when name is ""), its
// fields given as alternating names and values and laid out in name order.
func rtStruct(name string, fields ...any) *rt.Record {
	type field struct {
		name string
		v    any
	}
	fs := make([]field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		fs = append(fs, field{fields[i].(string), fields[i+1]})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].name < fs[j].name })
	specs := make([]rt.FieldSpec, len(fs))
	vals := make([]any, len(fs))
	for i, f := range fs {
		specs[i] = rt.FieldSpec{Name: f.name, Type: rt.SlotTypeOf(f.v)}
		vals[i] = f.v
	}
	if name == "" {
		return rt.AnonDesc(specs).Make(vals...)
	}
	return rt.NewStructDesc(name, specs).Make(vals...)
}

// rtTuple is a tuple of items.
func rtTuple(items ...any) *rt.Record {
	slots := make([]rt.SlotType, len(items))
	for i, it := range items {
		slots[i] = rt.SlotTypeOf(it)
	}
	return rt.TupleDesc(slots...).Make(items...)
}

// rtDistinct is a distinct type's value over inner, or its marker when inner
// is nil.
func rtDistinct(name string, inner any) *rt.Record {
	if inner == nil {
		return rt.NewDistinctDesc(name, nil).New()
	}
	t := rt.SlotTypeOf(inner)
	return rt.NewDistinctDesc(name, &t).Make(inner)
}
