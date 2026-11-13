package irbuild

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestStdHandleTypesCoverTheHostSpecs holds rt.StdHandleTypes, the list the
// checker denies equality to (spec §5 *Equality*), to stdHostSpecs: every
// std host type stdHostSpecs anchors by origin is a handle there or one of
// the few host values (Regex, True, False), so a new std handle cannot slip
// in with equality the runtime does not answer.
func TestStdHandleTypesCoverTheHostSpecs(t *testing.T) {
	handles := map[rt.StdHostType]bool{}
	for _, h := range rt.StdHandleTypes() {
		handles[h] = true
	}
	values := map[rt.StdHostType]bool{}
	for _, v := range rt.StdHostValueTypes() {
		values[v] = true
	}
	for _, spec := range stdHostSpecs {
		if spec.origin == "" {
			// A prim (Byte, Bytes, Decimal): a value with std's equality.
			continue
		}
		key := rt.StdHostType{Origin: spec.origin, Name: spec.nomi}
		if handles[key] == values[key] {
			t.Errorf("%s.%s: is it a handle (rt.StdHandleTypes) or a value? It is in %s",
				spec.origin, spec.nomi, map[bool]string{true: "both lists", false: "neither list"}[handles[key]])
		}
	}
	for h := range handles {
		found := false
		for _, spec := range stdHostSpecs {
			found = found || (spec.origin == h.Origin && spec.nomi == h.Name)
		}
		if !found && !irEngineHandle(h) {
			t.Errorf("rt.StdHandleTypes names %s.%s, which no stdHostSpecs row declares", h.Origin, h.Name)
		}
	}
}

// irEngineHandle is a std handle the VM holds as its own value, with no
// stdHostSpecs row: a task, a channel half, a select arm.
func irEngineHandle(h rt.StdHostType) bool {
	switch h {
	case rt.StdHostType{Origin: "std/tasks", Name: "Task"}, rt.StdHostType{Origin: "std/channels", Name: "Sender"},
		rt.StdHostType{Origin: "std/channels", Name: "Receiver"}, rt.StdHostType{Origin: "std/channels", Name: "Arm"}:
		return true
	}
	return false
}

// TestStdHostTypesRenderByName holds rt's host-type name table
// (rt/hostnames.go) to stdHostSpecs: a value of every std `host type` whose
// Go type is rt's renders under Debug without an error, at the top and
// nested in a List, so no std host value can stop `Debug.inspect`. One with
// no Debug of its own (not a prim with its own rendering, not an rt.Opaque
// with its spec placeholder, not Dynamic, whose Debug is std's dispatched
// impl) renders as its bare type name (spec §12 *Display and Debug*).
func TestStdHostTypesRenderByName(t *testing.T) {
	for _, spec := range stdHostSpecs {
		if spec.goType == nil || spec.goType == reflect.TypeFor[rt.Dynamic]() {
			continue
		}
		v := reflect.Zero(spec.goType).Interface()
		got, err := rt.DebugText(v, nil)
		if err != nil {
			t.Errorf("%s: Debug refuses a value of it: %v", spec.nomi, err)
			continue
		}
		if nested, err := rt.DebugText(rt.ListFromGo([]any{v}, func(x any) any { return x }), nil); err != nil || nested != "["+got+"]" {
			t.Errorf("%s: Debug of [v] = %q, %v; want %q", spec.nomi, nested, err, "["+got+"]")
		}
		if _, opaque := v.(rt.Opaque); opaque || spec.prim != nil {
			continue
		}
		if got != spec.nomi {
			t.Errorf("%s: Debug = %q, want the bare type name %q", spec.nomi, got, spec.nomi)
		}
	}
}
