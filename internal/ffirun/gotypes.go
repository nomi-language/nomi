package ffirun

// The Go types of a project's FFI bindings, read from source, for the adapter
// generator (internal/hostgen).
//
// The generator reads a Go signature through hostgen.Type. For the stdlib it
// reflects over a linked function value. A project's bindings are generated
// into the wrapper before the project's Go is compiled, so there is no value to
// reflect over: this file answers the same questions from the go/ast symbols
// readGoPackageSymbols already reads for validation.
//
// It covers what the FFI projection covers: predeclared types, the
// package's own named types (struct, scalar-backed, pointer, slice, map,
// func), time.Duration and time.Time, and a named type from another LOCAL
// package of the project (one reached through a local `replace` or the
// project's own module), which is read the same way. A bound standard library
// package is read the same way from GOROOT (gostd.go). A named type from any
// other package, the standard library's or a module-cache dependency's, is
// type-checked from source by go/types (gotypesimport.go).

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/hostgen"
)

// goTypes resolves type expressions for one project, reading each Go package
// it reaches once.
type goTypes struct {
	projectRoot string
	pkgs        map[string]*goSourcePkg
	named       map[string]*astType
	// imported is each non-local package gotypesimport.go type-checked.
	imported map[string]*types.Package
	importer types.ImporterFrom
}

// goSourcePkg is one Go package's symbols as source.
type goSourcePkg struct {
	path  string // import path; "main" for the wrapper's own declarations
	name  string // package name, for reflect's spelling of its types
	dir   string // source directory; "" for the wrapper's own declarations
	types map[string]goTypeSymbol
	funcs map[string]goFuncSymbol
}

// goScope is where a type expression is written: the package declaring it and
// the imports of the file it is in.
type goScope struct {
	pkg     *goSourcePkg
	imports map[string]string
}

func newGoTypes(projectRoot string) *goTypes {
	return &goTypes{projectRoot: projectRoot, pkgs: map[string]*goSourcePkg{}, named: map[string]*astType{},
		imported: map[string]*types.Package{}}
}

// pkg reads the package at importPath, which must be local to the project.
func (gt *goTypes) pkg(importPath string) (*goSourcePkg, error) {
	if p, ok := gt.pkgs[importPath]; ok {
		return p, nil
	}
	src, readable, err := goSourceOf(gt.projectRoot, importPath)
	if err != nil {
		return nil, err
	}
	if !readable {
		return nil, fmt.Errorf("Go package %q is neither in the Go standard library nor local to the project (a local `replace` or the project's own module), so its source is not read", importPath)
	}
	syms, err := src.symbols()
	if err != nil {
		return nil, err
	}
	name := defaultImportName(importPath)
	if !src.std {
		name = goPackageName(src.dir, importPath)
	}
	p := &goSourcePkg{path: importPath, name: name, dir: src.dir, types: syms.types, funcs: syms.funcs}
	gt.pkgs[importPath] = p
	return p, nil
}

// goPackageName is the package clause of the first Go file in dir, or the
// import path's last element.
func goPackageName(dir, importPath string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		f, err := goparser.ParseFile(gotoken.NewFileSet(), m, nil, goparser.PackageClauseOnly)
		if err == nil && f.Name != nil {
			return f.Name.Name
		}
	}
	return defaultImportName(importPath)
}

// mainPkg is the wrapper's own package: the types its `go { }` declarations
// declare. Their spelling in the generated code is unqualified.
func (gt *goTypes) mainPkg(goDecls []string) (*goSourcePkg, error) {
	if p, ok := gt.pkgs["main"]; ok {
		return p, nil
	}
	p := &goSourcePkg{path: "main", name: "main", types: map[string]goTypeSymbol{}, funcs: map[string]goFuncSymbol{}}
	if len(goDecls) > 0 {
		src := "package main\n" + strings.Join(goDecls, "\n")
		f, err := goparser.ParseFile(gotoken.NewFileSet(), "nomi_go_decls.go", src, 0)
		if err != nil {
			return nil, fmt.Errorf("parsing the wrapper's Go declarations: %w", err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*goast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*goast.TypeSpec); ok && ts.Name != nil {
					p.types[ts.Name.Name] = goTypeSymbolFor(ts, nil)
				}
			}
		}
	}
	gt.pkgs["main"] = p
	return p, nil
}

// funcSig parses a function signature written as Go source text, `a int, b
// string` and `(int, error)`, as the wrapper declares an inline body.
func funcSig(params, results string) (*goast.FuncType, error) {
	src := "package p\nfunc f(" + params + ") " + results + " {}\n"
	f, err := goparser.ParseFile(gotoken.NewFileSet(), "sig.go", src, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing the signature (%s) %s: %w", params, results, err)
	}
	return f.Decls[0].(*goast.FuncDecl).Type, nil
}

var predeclared = map[string]reflect.Kind{
	"bool": reflect.Bool, "string": reflect.String,
	"int": reflect.Int, "int8": reflect.Int8, "int16": reflect.Int16, "int32": reflect.Int32, "int64": reflect.Int64,
	"uint": reflect.Uint, "uint8": reflect.Uint8, "uint16": reflect.Uint16, "uint32": reflect.Uint32,
	"uint64": reflect.Uint64, "uintptr": reflect.Uintptr,
	"float32": reflect.Float32, "float64": reflect.Float64,
	"complex64": reflect.Complex64, "complex128": reflect.Complex128,
}

// resolve answers the Type of expr written in scope.
func (gt *goTypes) resolve(expr goast.Expr, scope goScope) (hostgen.Type, error) {
	switch x := expr.(type) {
	case *goast.ParenExpr:
		return gt.resolve(x.X, scope)
	case *goast.Ident:
		switch x.Name {
		case "byte":
			return &astType{kind: reflect.Uint8, name: "uint8", str: "uint8"}, nil
		case "rune":
			return &astType{kind: reflect.Int32, name: "int32", str: "int32"}, nil
		case "error":
			return &astType{kind: reflect.Interface, name: "error", str: "error"}, nil
		case "any":
			return &astType{kind: reflect.Interface, str: "interface {}"}, nil
		}
		if k, ok := predeclared[x.Name]; ok {
			return &astType{kind: k, name: x.Name, str: x.Name}, nil
		}
		if scope.pkg == nil {
			return nil, fmt.Errorf("type %s is not declared", x.Name)
		}
		return gt.namedIn(scope.pkg, x.Name)
	case *goast.SelectorExpr:
		alias, ok := x.X.(*goast.Ident)
		if !ok {
			return nil, fmt.Errorf("type %s is not a package-qualified name", goNodeString(x))
		}
		path, ok := scope.imports[alias.Name]
		if !ok {
			return nil, fmt.Errorf("type %s: no import is named %s", goNodeString(x), alias.Name)
		}
		if path == "time" {
			switch x.Sel.Name {
			case "Duration":
				return &astType{kind: reflect.Int64, name: "Duration", pkg: "time", str: "time.Duration"}, nil
			case "Time":
				return &astType{kind: reflect.Struct, name: "Time", pkg: "time", str: "time.Time"}, nil
			}
		}
		// A type from any package but the project's own is type-checked, the
		// standard library's included: a binding to `strings` reads its own
		// declarations as source, and `io.Reader` in one of them is
		// imported.
		if _, local, err := resolveLocalImportDir(gt.projectRoot, path); err != nil || !local {
			srcDir := gt.projectRoot
			if scope.pkg != nil && scope.pkg.dir != "" {
				srcDir = scope.pkg.dir
			}
			t, err := gt.importedNamed(path, x.Sel.Name, srcDir)
			if err != nil {
				return nil, fmt.Errorf("type %s: %w", goNodeString(x), err)
			}
			return t, nil
		}
		p, err := gt.pkg(path)
		if err != nil {
			return nil, fmt.Errorf("type %s: %w", goNodeString(x), err)
		}
		return gt.namedIn(p, x.Sel.Name)
	case *goast.StarExpr:
		elem, err := gt.resolve(x.X, scope)
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Pointer, elem: elem, str: "*" + elem.String()}, nil
	case *goast.ArrayType:
		elem, err := gt.resolve(x.Elt, scope)
		if err != nil {
			return nil, err
		}
		if x.Len != nil {
			return &astType{kind: reflect.Array, elem: elem, str: "[" + goNodeString(x.Len) + "]" + elem.String()}, nil
		}
		return &astType{kind: reflect.Slice, elem: elem, str: "[]" + elem.String()}, nil
	case *goast.Ellipsis:
		elem, err := gt.resolve(x.Elt, scope)
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Slice, elem: elem, str: "[]" + elem.String()}, nil
	case *goast.MapType:
		key, err := gt.resolve(x.Key, scope)
		if err != nil {
			return nil, err
		}
		elem, err := gt.resolve(x.Value, scope)
		if err != nil {
			return nil, err
		}
		return &astType{kind: reflect.Map, key: key, elem: elem, str: "map[" + key.String() + "]" + elem.String()}, nil
	case *goast.FuncType:
		return gt.funcType(x, scope)
	case *goast.StructType:
		t := &astType{kind: reflect.Struct, str: "struct {...}"}
		if err := gt.fillFields(t, x, scope); err != nil {
			return nil, err
		}
		return t, nil
	case *goast.InterfaceType:
		return &astType{kind: reflect.Interface, str: "interface {...}"}, nil
	case *goast.ChanType:
		return &astType{kind: reflect.Chan, str: goNodeString(x)}, nil
	}
	return nil, fmt.Errorf("type %s has no Go type the generator reads", goNodeString(expr))
}

// namedIn is the named type name declared in p.
func (gt *goTypes) namedIn(p *goSourcePkg, name string) (hostgen.Type, error) {
	key := p.path + "." + name
	if t, ok := gt.named[key]; ok {
		return t, nil
	}
	sym, ok := p.types[name]
	if !ok {
		return nil, fmt.Errorf("Go package %q declares no type %s", p.path, name)
	}
	scope := goScope{pkg: p, imports: sym.imports}
	if sym.alias {
		return gt.resolve(sym.expr, scope)
	}
	t := &astType{name: name, pkg: p.path, str: p.name + "." + name}
	gt.named[key] = t
	u, err := gt.resolve(sym.expr, scope)
	if err != nil {
		delete(gt.named, key)
		return nil, err
	}
	ut := u.(*astType)
	t.kind, t.elem, t.key, t.fields, t.in, t.out, t.variadic = ut.kind, ut.elem, ut.key, ut.fields, ut.in, ut.out, ut.variadic
	return t, nil
}

func (gt *goTypes) funcType(ft *goast.FuncType, scope goScope) (*astType, error) {
	t := &astType{kind: reflect.Func}
	var ins, outs []string
	if ft.Params != nil {
		for _, f := range ft.Params.List {
			ty, err := gt.resolve(f.Type, scope)
			if err != nil {
				return nil, err
			}
			if _, ok := f.Type.(*goast.Ellipsis); ok {
				t.variadic = true
			}
			for range max(1, len(f.Names)) {
				t.in = append(t.in, ty)
				ins = append(ins, ty.String())
			}
		}
	}
	if ft.Results != nil {
		for _, f := range ft.Results.List {
			ty, err := gt.resolve(f.Type, scope)
			if err != nil {
				return nil, err
			}
			for range max(1, len(f.Names)) {
				t.out = append(t.out, ty)
				outs = append(outs, ty.String())
			}
		}
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

func (gt *goTypes) fillFields(t *astType, st *goast.StructType, scope goScope) error {
	if st.Fields == nil {
		return nil
	}
	for _, f := range st.Fields.List {
		ty, err := gt.resolve(f.Type, scope)
		if err != nil {
			return err
		}
		var tag reflect.StructTag
		if f.Tag != nil {
			if raw, err := strconv.Unquote(f.Tag.Value); err == nil {
				tag = reflect.StructTag(raw)
			}
		}
		if len(f.Names) == 0 {
			name := embeddedName(f.Type)
			t.fields = append(t.fields, hostgen.StructField{Name: name, Type: ty, Tag: tag, Anonymous: true, Exported: goast.IsExported(name)})
			continue
		}
		for _, n := range f.Names {
			t.fields = append(t.fields, hostgen.StructField{Name: n.Name, Type: ty, Tag: tag, Exported: n.IsExported()})
		}
	}
	return nil
}

// embeddedName is an embedded field's name: its type's.
func embeddedName(expr goast.Expr) string {
	switch x := expr.(type) {
	case *goast.StarExpr:
		return embeddedName(x.X)
	case *goast.Ident:
		return x.Name
	case *goast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// astType is a hostgen.Type read from source.
type astType struct {
	kind      reflect.Kind
	name, pkg string
	str       string
	elem, key hostgen.Type
	fields    []hostgen.StructField
	in, out   []hostgen.Type
	variadic  bool
}

func (t *astType) Kind() reflect.Kind              { return t.kind }
func (t *astType) Name() string                    { return t.name }
func (t *astType) PkgPath() string                 { return t.pkg }
func (t *astType) String() string                  { return t.str }
func (t *astType) Elem() hostgen.Type              { return t.elem }
func (t *astType) Key() hostgen.Type               { return t.key }
func (t *astType) NumField() int                   { return len(t.fields) }
func (t *astType) Field(i int) hostgen.StructField { return t.fields[i] }
func (t *astType) NumIn() int                      { return len(t.in) }
func (t *astType) In(i int) hostgen.Type           { return t.in[i] }
func (t *astType) NumOut() int                     { return len(t.out) }
func (t *astType) Out(i int) hostgen.Type          { return t.out[i] }
func (t *astType) IsVariadic() bool                { return t.variadic }

func (t *astType) FieldByName(name string) (hostgen.StructField, bool) {
	for _, f := range t.fields {
		if f.Name == name {
			return f, true
		}
	}
	return hostgen.StructField{}, false
}
