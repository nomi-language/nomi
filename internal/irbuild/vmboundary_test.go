package irbuild

// Operands for a machine, at a TEST's boundary with the VM. The machine takes
// and answers rt values (internal/vm/values.go). A test that names a scalar
// passes it as rt's Go value (int64, float64, bool, string, rt.Byte, ...). A
// composite a test assembles piece by piece — the argument probes merge the
// fields two callees demand into one struct — is built as one of the probe
// nodes below and made an rt value by vmOperand when the machine is called.
//
// vmOperand lays a struct's fields out in name order and gives each slot the
// class of the value in it, which is what rt's kernels need: they compare
// identity by type name and fields by name, and use descriptor identity only
// as a fast path.

import (
	"sort"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// probeStruct is a named struct, or an anonymous record when name is "".
type probeStruct struct {
	name   string
	fields map[string]any
}

// probeVariant is an enum variant: bare when payload and fields are both nil,
// positional over payload, or field-shaped over fields.
type probeVariant struct {
	enum, variant string
	payload       any
	fields        map[string]any
}

// probeDistinct is a distinct over inner, or a marker when inner is nil.
type probeDistinct struct {
	name  string
	inner any
}

type probeTuple struct{ items []any }

type probeList struct{ items []any }

// probeLeaf wraps a trial leaf so a probe can compare leaves by identity: an
// rt value such as a Map is not comparable with ==.
type probeLeaf struct{ v any }

// vmOperand is v as an rt value. An rt value passes through unchanged.
func vmOperand(v any) any {
	switch x := v.(type) {
	case *probeLeaf:
		return vmOperand(x.v)
	case *probeStruct:
		vals, specs := vmOperandFields(x.fields)
		if x.name == "" {
			return rt.AnonDesc(specs).Make(vals...)
		}
		return rt.NewStructDesc(x.name, specs).Make(vals...)
	case *probeVariant:
		if x.payload == nil && x.fields == nil && (x.enum == "Bool" || x.enum == "bool.Bool") &&
			(x.variant == "True" || x.variant == "False") {
			return x.variant == "True"
		}
		var spec rt.VariantSpec
		var vals []any
		switch {
		case x.fields != nil:
			var specs []rt.FieldSpec
			vals, specs = vmOperandFields(x.fields)
			spec = rt.VariantSpec{Name: x.variant, Shape: rt.VariantFields, Fields: specs}
		case x.payload != nil:
			p := vmOperand(x.payload)
			vals = []any{p}
			spec = rt.VariantSpec{Name: x.variant, Shape: rt.VariantPositional,
				Fields: []rt.FieldSpec{{Type: rt.SlotTypeOf(p)}}}
		default:
			spec = rt.VariantSpec{Name: x.variant, Shape: rt.VariantBare}
		}
		return rt.NewEnumDesc(x.enum, []rt.VariantSpec{spec}).MakeVariant(0, vals...)
	case *probeDistinct:
		if x.inner == nil {
			return rt.NewDistinctDesc(x.name, nil).New()
		}
		inner := vmOperand(x.inner)
		t := rt.SlotTypeOf(inner)
		return rt.NewDistinctDesc(x.name, &t).Make(inner)
	case *probeTuple:
		vals := make([]any, len(x.items))
		slots := make([]rt.SlotType, len(x.items))
		for i, it := range x.items {
			vals[i] = vmOperand(it)
			slots[i] = rt.SlotTypeOf(vals[i])
		}
		return rt.TupleDesc(slots...).Make(vals...)
	case *probeList:
		var out *rt.List[any]
		for i := len(x.items) - 1; i >= 0; i-- {
			out = rt.ConsCell[any, rt.List[any]](vmOperand(x.items[i]), out)
		}
		return out
	}
	return v
}

func vmOperandFields(m map[string]any) ([]any, []rt.FieldSpec) {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	vals := make([]any, len(names))
	specs := make([]rt.FieldSpec, len(names))
	for i, n := range names {
		vals[i] = vmOperand(m[n])
		specs[i] = rt.FieldSpec{Name: n, Type: rt.SlotTypeOf(vals[i])}
	}
	return vals, specs
}

// vmOperands is vmOperand over a vector.
func vmOperands(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = vmOperand(a)
	}
	return out
}

// vmEmptyMap is an empty Map, hashed and compared by rt's kernels.
func vmEmptyMap() any { return rt.MapOf[any, any](rt.Hash, rt.Equal, nil) }

// vmRunV runs entry over operands and answers the machine's rt value.
func vmRunV(m *vm.Machine, entry string, args ...any) (any, error) {
	return m.Run(entry, vmOperands(args)...)
}

// vmRunSymV is vmRunV by declaration identity.
func vmRunSymV(m *vm.Machine, sym *ir.Symbol, args ...any) (any, error) {
	return m.RunSymbol(sym, vmOperands(args)...)
}

// vmRecord is v as a record, and its type name and (for an enum) variant name.
func vmRecord(v any) (r *rt.Record, typeName, variant string, ok bool) {
	r, ok = v.(*rt.Record)
	if !ok || r == nil {
		return nil, "", "", false
	}
	if r.Desc.Kind == rt.KindEnum {
		variant = r.Variant().Name
	}
	return r, r.Desc.Name, variant, true
}

// vmDisplay is v's structural Display, as `nomi run` would print it.
func vmDisplay(v any) string { return rt.DisplayText(v) }
