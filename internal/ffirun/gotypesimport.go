package ffirun

// A named type from a Go package outside the project, for the adapter
// generator.
//
// A binding's signature, or a type it reaches, may name a type from the
// standard library or a module-cache dependency (`sqlite.Conn` holds a
// `*sql.DB`). The project's own packages are read as source by gotypes.go;
// every other package is type-checked from source by go/types' source
// importer, which resolves the import path the way `go list` does from the
// importing package's directory, so a module-cache dependency resolves
// against the importing module's go.mod. Its types.Type is then read as a
// hostgen.Type, spelled as reflect spells it.

import (
	"fmt"
	"go/importer"
	gotoken "go/token"
	"go/types"
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/hostgen"
)

// importedNamed is the named type name of the non-local package at path,
// imported from srcDir.
func (gt *goTypes) importedNamed(path, name, srcDir string) (hostgen.Type, error) {
	pkg, err := gt.importPkg(path, srcDir)
	if err != nil {
		return nil, err
	}
	obj, ok := pkg.Scope().Lookup(name).(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("Go package %q declares no type %s", path, name)
	}
	return gt.fromTypes(obj.Type())
}

// importPkg type-checks the package at path from source, once.
func (gt *goTypes) importPkg(path, srcDir string) (*types.Package, error) {
	if p, ok := gt.imported[path]; ok {
		return p, nil
	}
	if gt.importer == nil {
		imp, ok := importer.ForCompiler(gotoken.NewFileSet(), "source", nil).(types.ImporterFrom)
		if !ok {
			return nil, fmt.Errorf("Go package %q: no source importer", path)
		}
		gt.importer = imp
	}
	if srcDir == "" {
		srcDir = gt.projectRoot
	}
	p, err := gt.importer.ImportFrom(path, srcDir, 0)
	if err != nil {
		return nil, fmt.Errorf("Go package %q: %w", path, err)
	}
	gt.imported[path] = p
	return p, nil
}

var basicKinds = map[types.BasicKind]reflect.Kind{
	types.Bool: reflect.Bool, types.String: reflect.String,
	types.Int: reflect.Int, types.Int8: reflect.Int8, types.Int16: reflect.Int16, types.Int32: reflect.Int32, types.Int64: reflect.Int64,
	types.Uint: reflect.Uint, types.Uint8: reflect.Uint8, types.Uint16: reflect.Uint16, types.Uint32: reflect.Uint32,
	types.Uint64: reflect.Uint64, types.Uintptr: reflect.Uintptr,
	types.Float32: reflect.Float32, types.Float64: reflect.Float64,
	types.Complex64: reflect.Complex64, types.Complex128: reflect.Complex128,
	types.UnsafePointer: reflect.UnsafePointer,
}

// fromTypes reads t as a hostgen.Type. A named type is cached before its
// underlying type is read, so a type that reaches itself terminates.
func (gt *goTypes) fromTypes(t types.Type) (hostgen.Type, error) {
	switch x := t.(type) {
	case *types.Alias:
		return gt.fromTypes(types.Unalias(x))
	case *types.Named:
		obj := x.Obj()
		if obj.Pkg() == nil {
			if obj.Name() == "error" {
				return &astType{kind: reflect.Interface, name: "error", str: "error"}, nil
			}
			return gt.fromTypes(x.Underlying())
		}
		path := obj.Pkg().Path()
		if path == "time" && (obj.Name() == "Duration" || obj.Name() == "Time") {
			kind := reflect.Int64
			if obj.Name() == "Time" {
				kind = reflect.Struct
			}
			return &astType{kind: kind, name: obj.Name(), pkg: "time", str: "time." + obj.Name()}, nil
		}
		key := types.TypeString(x, nil)
		if cached, ok := gt.named[key]; ok {
			return cached, nil
		}
		nt := &astType{name: obj.Name(), pkg: path, str: types.TypeString(x, func(p *types.Package) string { return p.Name() })}
		if x.TypeArgs().Len() > 0 {
			nt.name = strings.TrimPrefix(nt.str, obj.Pkg().Name()+".")
		}
		gt.named[key] = nt
		u, err := gt.fromTypes(x.Underlying())
		if err != nil {
			delete(gt.named, key)
			return nil, err
		}
		ut := u.(*astType)
		nt.kind, nt.elem, nt.key, nt.fields, nt.in, nt.out, nt.variadic = ut.kind, ut.elem, ut.key, ut.fields, ut.in, ut.out, ut.variadic
		return nt, nil
	case *types.Basic:
		switch x.Kind() {
		case types.UntypedNil, types.Invalid:
			return nil, fmt.Errorf("type %s has no Go type the generator reads", x)
		}
		k, ok := basicKinds[x.Kind()]
		if !ok {
			return nil, fmt.Errorf("type %s has no Go type the generator reads", x)
		}
		return &astType{kind: k, name: x.Name(), str: x.Name()}, nil
	case *types.Pointer:
		elem, err := gt.fromTypes(x.Elem())
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Pointer, elem: elem, str: "*" + elem.String()}, nil
	case *types.Slice:
		elem, err := gt.fromTypes(x.Elem())
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Slice, elem: elem, str: "[]" + elem.String()}, nil
	case *types.Array:
		elem, err := gt.fromTypes(x.Elem())
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Array, elem: elem, str: fmt.Sprintf("[%d]%s", x.Len(), elem.String())}, nil
	case *types.Map:
		key, err := gt.fromTypes(x.Key())
		if err != nil {
			return nil, err
		}
		elem, err := gt.fromTypes(x.Elem())
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Map, key: key, elem: elem, str: "map[" + key.String() + "]" + elem.String()}, nil
	case *types.Chan:
		return &astType{kind: reflect.Chan, str: types.TypeString(x, func(p *types.Package) string { return p.Name() })}, nil
	case *types.Interface:
		if x.Empty() {
			return &astType{kind: reflect.Interface, str: "interface {}"}, nil
		}
		return &astType{kind: reflect.Interface, str: "interface {...}"}, nil
	case *types.Signature:
		return gt.fromSignature(x)
	case *types.Struct:
		st := &astType{kind: reflect.Struct, str: "struct {...}"}
		for i := 0; i < x.NumFields(); i++ {
			f := x.Field(i)
			ft, err := gt.fromTypes(f.Type())
			if err != nil {
				return nil, err
			}
			st.fields = append(st.fields, hostgen.StructField{Name: f.Name(), Type: ft,
				Tag: reflect.StructTag(x.Tag(i)), Anonymous: f.Embedded(), Exported: f.Exported()})
		}
		return st, nil
	case *types.TypeParam:
		return nil, fmt.Errorf("type parameter %s has no Go type the generator reads", x)
	}
	return nil, fmt.Errorf("type %s has no Go type the generator reads", t)
}

func (gt *goTypes) fromSignature(sig *types.Signature) (*astType, error) {
	t := &astType{kind: reflect.Func, variadic: sig.Variadic()}
	var ins, outs []string
	for i := 0; i < sig.Params().Len(); i++ {
		p, err := gt.fromTypes(sig.Params().At(i).Type())
		if err != nil {
			return nil, err
		}
		t.in = append(t.in, p)
		ins = append(ins, p.String())
	}
	for i := 0; i < sig.Results().Len(); i++ {
		r, err := gt.fromTypes(sig.Results().At(i).Type())
		if err != nil {
			return nil, err
		}
		t.out = append(t.out, r)
		outs = append(outs, r.String())
	}
	t.str = "func(" + strings.Join(ins, ", ") + ")"
	switch len(outs) {
	case 0:
	case 1:
		t.str += " " + outs[0]
	default:
		t.str += " (" + strings.Join(outs, ", ") + ")"
	}
	return t, nil
}
