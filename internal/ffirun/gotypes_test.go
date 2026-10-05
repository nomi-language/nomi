package ffirun

import (
	"bytes"
	"reflect"
	gruntime "runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/hostgen"
	"github.com/nomi-language/nomi/internal/hostgen/fixture"
)

// TestGoSourceTypesGenerateTheReflectedAdapters: the adapters generated from
// the ffx fixture's Go SOURCE, through this package's go/ast front end, are
// byte for byte the ones generated from its linked function values through
// reflect. The fixture reaches every row of the FFI projection a project
// binding can: narrow and unsigned integers, float32, time.Time, embedded and
// tagged struct fields, pointer-to-struct Maybe, a named byte element, tuples
// with and without a trailing error, Unit, a handle, four callback shapes, and
// Go maps with slice values. ffxadapters' own suite holds those adapters to
// recorded outcomes.
func TestGoSourceTypesGenerateTheReflectedAdapters(t *testing.T) {
	root := repoRoot(t)
	const fixturePkg = "github.com/nomi-language/nomi/internal/hostgen/fixture"
	want, err := hostgen.Generate(fixture.Table())
	if err != nil {
		t.Fatal(err)
	}

	gt := newGoTypes(root)
	p, err := gt.pkg(fixturePkg)
	if err != nil {
		t.Fatal(err)
	}
	tb := fixture.Table()
	for i, row := range tb.Funcs {
		full := gruntime.FuncForPC(reflect.ValueOf(row.Fn).Pointer()).Name()
		name := full[strings.LastIndex(full, ".")+1:]
		fn, ok := p.funcs[name]
		if !ok {
			t.Fatalf("%s: the fixture's source declares no function %s", row.Name, name)
		}
		ty, err := gt.resolve(fn.typ, goScope{pkg: p, imports: fn.imports})
		if err != nil {
			t.Fatalf("%s: %v", row.Name, err)
		}
		tb.Funcs[i] = hostgen.FuncRow{Name: row.Name, Go: &hostgen.GoFunc{Pkg: fixturePkg, Name: name, Type: ty}}
	}
	for i, tr := range tb.Types {
		proto := reflect.TypeOf(tr.Prototype)
		if proto.Kind() != reflect.Pointer {
			t.Fatalf("%s: prototype is not a pointer", tr.Name)
		}
		elem, err := gt.namedIn(p, proto.Elem().Name())
		if err != nil {
			t.Fatal(err)
		}
		tb.Types[i] = hostgen.TypeRow{Name: tr.Name, Go: &astType{kind: reflect.Pointer, elem: elem, str: "*" + elem.String()}}
	}
	got, err := hostgen.Generate(tb)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		for i := range min(len(gl), len(wl)) {
			if gl[i] != wl[i] {
				t.Fatalf("the source front end's adapters differ from reflect's at line %d:\n  source  %s\n  reflect %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("the source front end's adapters differ from reflect's in length: %d lines against %d", len(gl), len(wl))
	}
	if len(tb.Funcs) != 25 {
		t.Fatalf("%d fixture bindings, want 25", len(tb.Funcs))
	}
}
