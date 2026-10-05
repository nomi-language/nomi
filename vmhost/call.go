package vmhost

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"

	"github.com/nomi-language/nomi/rt"
)

// Value is a Nomi value as the VM holds it: int64 (Int), float64 (Float),
// bool, string, rt.Byte, rt.Bytes, *rt.Record (a struct, enum variant,
// Maybe, Result, tuple, anonymous record, distinct or Set), *rt.List[any],
// rt.Map[any, any], rt.Vector[any]. rt's DisplayText and DebugText render
// one, and (*rt.Record).FieldNamed and Variant read one.
type Value = rt.Value

// Fields is a struct argument spelled by field name, for a parameter whose
// type is a struct the program declares. Call builds the record with the
// machine's own descriptor. Every field must be given; a field default is
// not applied.
type Fields map[string]any

// Call boots the program (its `fn boot`, when it declares one) and calls the
// entry file's function name with args, on a frame of ctx, and answers its
// result. The boot runs again on every Call, as `nomi run` runs it once per
// process.
//
// An argument is converted by the parameter's declared type: any Go integer
// for an Int (refusing one out of range), float32 or float64 for a Float,
// uint8 for a Byte, Fields for a struct, and a Go slice for a List of those.
// Any other argument must already be the VM's Value.
//
// The error is a *Blocked when the program reaches something the VM cannot
// run, and otherwise the program's own failure (a fault or a failed
// assertion), which WriteFailure renders. A function that returns a Result
// answers its Err as a value, not as an error.
func (p *Program) Call(ctx context.Context, name string, args ...any) (Value, error) {
	f, err := p.callable(name)
	if err != nil {
		return nil, err
	}
	params := f.Params()
	if len(args) != len(params) {
		return nil, fmt.Errorf("%s takes %d argument(s), called with %d", name, len(params), len(args))
	}
	converted := make([]any, len(args))
	for i, prm := range params {
		v, err := toNomi(f.TempType(prm.Temp), args[i])
		if err != nil {
			return nil, fmt.Errorf("%s: argument %d: %w", name, i+1, err)
		}
		converted[i] = v
	}
	m := p.machine(p.out).WithHostEnv(p.env)
	var boots []*ir.Symbol
	if boot := p.entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	if found := m.Unretained([]*ir.Func{f}, boots); len(found) > 0 {
		return nil, p.blocked(found)
	}
	result, err := m.Call(ctx, f, converted)
	failure, limit := programFailure(err)
	if limit {
		return nil, &Blocked{Reasons: []string{machineLimit(failure)}}
	}
	if failure != nil {
		return nil, failure
	}
	return result, nil
}

// callable is the entry's function name. A function the source declares and
// the builder did not retain is Blocked.
func (p *Program) callable(name string) (*ir.Func, error) {
	var found *ir.Func
	if p.entry != nil {
		for _, f := range p.entry.Funcs() {
			if f.Name() != name {
				continue
			}
			if found != nil {
				return nil, fmt.Errorf("%s names two of the entry's declarations", name)
			}
			found = f
		}
	}
	if found != nil {
		return found, nil
	}
	if p.declares(name) {
		return nil, p.blockedName(name)
	}
	return nil, fmt.Errorf("the program declares no function %s", name)
}

// toNomi converts a Go argument to the Value a parameter of type t holds.
func toNomi(t *ir.ValType, v any) (any, error) {
	if t == nil {
		return v, nil
	}
	switch t.Kind() {
	case ir.KindInt:
		return toInt(v)
	case ir.KindFloat:
		switch x := v.(type) {
		case float64:
			return x, nil
		case float32:
			return float64(x), nil
		}
		return nil, fmt.Errorf("want a Float (float64), got %T", v)
	case ir.KindBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("want a Bool, got %T", v)
	case ir.KindString:
		if s, ok := v.(string); ok {
			return s, nil
		}
		return nil, fmt.Errorf("want a String, got %T", v)
	case ir.KindByte:
		switch x := v.(type) {
		case rt.Byte:
			return x, nil
		case uint8:
			return rt.Byte(x), nil
		}
		return nil, fmt.Errorf("want a Byte, got %T", v)
	case ir.KindStruct:
		fields, ok := v.(Fields)
		if !ok {
			return v, nil
		}
		return toStruct(t, fields)
	case ir.KindList:
		if _, ok := v.(*rt.List[any]); ok || v == nil {
			return v, nil
		}
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice {
			return v, nil
		}
		elem := t.Elem(0)
		var list *rt.List[any]
		for i := rv.Len() - 1; i >= 0; i-- {
			e, err := toNomi(elem, rv.Index(i).Interface())
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			list = rt.Cons[any](e, list)
		}
		return list, nil
	}
	return v, nil
}

func toStruct(t *ir.ValType, fields Fields) (any, error) {
	desc := vm.DescOf(t)
	layout := t.Layout()
	if desc == nil || layout == nil {
		return nil, fmt.Errorf("%s has no layout the VM can build", t)
	}
	vals := make([]any, len(layout.Fields))
	seen := 0
	for i, f := range layout.Fields {
		fv, ok := fields[f.Name]
		if !ok {
			return nil, fmt.Errorf("%s: missing field %s", t, f.Name)
		}
		v, err := toNomi(f.Type, fv)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t, f.Name, err)
		}
		vals[i] = v
		seen++
	}
	if seen != len(fields) {
		var extra []string
		for name := range fields {
			if !hasField(layout, name) {
				extra = append(extra, name)
			}
		}
		sort.Strings(extra)
		return nil, fmt.Errorf("%s has no field(s) %v", t, extra)
	}
	return desc.Make(vals...), nil
}

func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint:
		if uint64(x) > math.MaxInt64 {
			return 0, fmt.Errorf("%d does not fit an Int", x)
		}
		return int64(x), nil
	case uint64:
		if x > math.MaxInt64 {
			return 0, fmt.Errorf("%d does not fit an Int", x)
		}
		return int64(x), nil
	}
	return 0, fmt.Errorf("want an Int, got %T", v)
}

func hasField(l *ir.Layout, name string) bool {
	for _, f := range l.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
