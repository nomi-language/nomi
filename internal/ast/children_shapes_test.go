package ast

import (
	"reflect"
	"testing"
)

// TestChildrenMatchTheReflectiveWalkOnEveryShape builds, for each type the
// generated walk names, a value with every exported field filled (each
// pointer set, each slice two long, each interface holding a different
// type that fits it in turn, now and then a typed nil), a few levels deep,
// and holds the generated walk of every node in it, and the dynamic walk
// of every value an interface can hold, to ReflectChildren's answer. The
// parsed sources TestChildrenMatchTheReflectiveWalk walks leave many fields
// empty; this fills them all.
func TestChildrenMatchTheReflectiveWalkOnEveryShape(t *testing.T) {
	f := &filler{}
	nodes := 0
	for _, proto := range generatedTypes {
		typ := reflect.TypeOf(proto)
		var root reflect.Value
		if typ.Kind() == reflect.Pointer {
			root = reflect.New(typ.Elem())
			f.fill(root.Elem(), 0)
		} else {
			root = reflect.New(typ).Elem()
			f.fill(root, 0)
		}
		v := root.Interface()
		if n, ok := v.(Node); ok {
			var visit func(n Node)
			visit = func(n Node) {
				nodes++
				var gen, ref []Node
				if !generatedChildren(n, func(c Node) { gen = append(gen, c) }) {
					t.Fatalf("the generated walk does not know %T", n)
				}
				ReflectChildren(n, func(c Node) { ref = append(ref, c) })
				if !sameChildren(gen, ref) {
					t.Fatalf("the walks of a %T differ:\ngenerated  %v\nreflective %v", n, names(gen), names(ref))
				}
				for _, c := range ref {
					visit(c)
				}
			}
			visit(n)
			continue
		}
		var gen, ref []Node
		childrenOfDynamic(v, func(c Node) { gen = append(gen, c) })
		reflectDynamic(v, func(c Node) { ref = append(ref, c) })
		if !sameChildren(gen, ref) {
			t.Fatalf("the dynamic walks of a %T differ:\ngenerated  %v\nreflective %v", v, names(gen), names(ref))
		}
	}
	if nodes < 8_500 {
		t.Fatalf("only %d nodes were built", nodes)
	}
	t.Logf("%d types, %d nodes", len(generatedTypes), nodes)
}

func sameChildren(a, b []Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if reflect.ValueOf(a[i]).Kind() == reflect.Pointer {
			if a[i] != b[i] {
				return false
			}
		} else if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// filler fills values for TestChildrenMatchTheReflectiveWalkOnEveryShape.
// turn rotates the type an interface is given, so each fitting type is
// chosen in turn.
type filler struct{ turn int }

const fillDepth = 4

func (f *filler) fill(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.Pointer:
		if depth >= fillDepth {
			return
		}
		p := reflect.New(v.Type().Elem())
		f.fill(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Interface:
		if depth >= fillDepth {
			return
		}
		var fits []reflect.Type
		for _, proto := range generatedTypes {
			if typ := reflect.TypeOf(proto); typ.Implements(v.Type()) {
				fits = append(fits, typ)
			}
		}
		if len(fits) == 0 {
			return
		}
		f.turn++
		typ := fits[f.turn%len(fits)]
		switch {
		case typ.Kind() == reflect.Pointer && f.turn%7 == 0:
			v.Set(reflect.Zero(typ)) // a typed nil
		case typ.Kind() == reflect.Pointer:
			p := reflect.New(typ.Elem())
			f.fill(p.Elem(), depth+1)
			v.Set(p)
		default:
			x := reflect.New(typ).Elem()
			f.fill(x, depth+1)
			v.Set(x)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				f.fill(v.Field(i), depth)
			}
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := range 2 {
			f.fill(s.Index(i), depth)
		}
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			f.fill(v.Index(i), depth)
		}
	}
}
