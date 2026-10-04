package ffxadapters

// An outcome's canonical text, and the operand builder, for the parity suite.
// They are the stdlib adapter suites' (internal/stdlibadapters' rtfuncs_test.go
// and rtbuild_test.go), cut to what the fixture's bindings need.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

type hostOutcome struct {
	val   rt.Value
	panic string // a panic, "%T: %v"
	err   error
}

// classify runs call and sorts what it did into a hostOutcome.
func classify(call func() (rt.Value, error)) (out hostOutcome) {
	defer func() {
		if p := recover(); p != nil {
			out = hostOutcome{panic: fmt.Sprintf("%T: %v", p, p)}
		}
	}()
	v, err := call()
	if err != nil {
		return hostOutcome{err: err}
	}
	return hostOutcome{val: v}
}

// outcomeText is an outcome's canonical text: a failure by its text, a value
// by canonText.
func outcomeText(o hostOutcome, shapeOnly bool) string {
	switch {
	case o.panic != "":
		return "panic " + o.panic
	case o.err != nil:
		return "error " + o.err.Error()
	}
	return "value " + canonText(o.val, shapeOnly)
}

// canonText is stricter than rt.Equal: dynamic types, qualified record names,
// variants, fields by name, Float by bits. shapeOnly prints the types of
// scalars without their values.
func canonText(v rt.Value, shapeOnly bool) string {
	scalar := func(kind, text string) string {
		if shapeOnly {
			return kind
		}
		return kind + "(" + text + ")"
	}
	switch x := v.(type) {
	case int64:
		return scalar("Int", fmt.Sprint(x))
	case float64:
		if math.IsNaN(x) {
			return scalar("Float", "NaN")
		}
		return scalar("Float", fmt.Sprintf("%#x", math.Float64bits(x)))
	case bool:
		return scalar("Bool", fmt.Sprint(x))
	case string:
		return scalar("String", fmt.Sprintf("%q", x))
	case rt.Byte:
		return scalar("Byte", fmt.Sprint(uint8(x)))
	case rt.Bytes:
		return scalar("Bytes", fmt.Sprintf("%q", string(x)))
	case rt.Unit:
		return "Unit"
	case *rt.Record:
		var b strings.Builder
		fmt.Fprintf(&b, "%s/%d", x.Desc.Name, x.Desc.Kind)
		if x.Desc.Kind == rt.KindEnum {
			b.WriteString("." + x.Variant().Name)
		}
		l := x.Layout()
		var parts []string
		for k, f := range l.Fields {
			if f.Name == "" {
				parts = append(parts, fmt.Sprintf("%d=%s", k, canonText(x.Field(k), shapeOnly)))
				continue
			}
			fv, _ := x.FieldNamed(f.Name)
			parts = append(parts, f.Name+"="+canonText(fv, shapeOnly))
		}
		sort.Strings(parts)
		b.WriteString("{" + strings.Join(parts, ", ") + "}")
		return b.String()
	case *rt.List[any]:
		var parts []string
		for n := x; n != nil; n = n.Tail {
			parts = append(parts, canonText(n.Head, shapeOnly))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case rt.HostHandle:
		return fmt.Sprintf("Handle(%s %T)", x.TypeName, x.Value)
	case rt.Map[any, any]:
		// Entries by key text, so the text does not depend on the order a
		// producer inserted them in.
		var parts []string
		for _, e := range rt.MapEntries(x) {
			parts = append(parts, canonText(e.Key, shapeOnly)+" => "+canonText(e.Val, shapeOnly))
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprintf("%T", v)
}

// recordBuilder lays a record out as a producer other than the adapters
// does: a named struct's fields in NAME order, on a descriptor of its own,
// and an enum's descriptor grown one variant shape at a time.
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

var rb = newRecordBuilder()

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

// Variant is a bare variant (no payload) or one carrying a positional payload.
func (b *recordBuilder) Variant(enum, variant string, payload ...any) *rt.Record {
	spec := rt.VariantSpec{Name: variant, Shape: rt.VariantBare}
	var vals []any
	if len(payload) > 0 {
		spec = rt.VariantSpec{Name: variant, Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotTypeOf(payload[0])}}}
		vals = payload[:1]
	}
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

// rtMap is a Map from key/value pairs, over the VM's hash and equality.
func rtMap(kv ...any) rt.Map[any, any] {
	ents := make([]rt.MapEntry[any, any], 0, len(kv)/2)
	for k := 0; k < len(kv); k += 2 {
		ents = append(ents, rt.MapEntry[any, any]{Key: kv[k], Val: kv[k+1]})
	}
	return rt.MapOf(rt.Hash, rt.Equal, ents)
}

func rtList(items ...any) *rt.List[any] {
	var out *rt.List[any]
	for k := len(items) - 1; k >= 0; k-- {
		out = rt.ConsCell[any, rt.List[any]](items[k], out)
	}
	return out
}
