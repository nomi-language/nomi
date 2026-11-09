// Package stategen writes internal/analysis's state_codec_gen.go: an encoder
// and a decoder for every type the stdlib's built analysis holds (the
// analysis package doc, "The stdlib's state"). It reads the analysis
// package's Go source with go/types, starting from stdState, and follows
// every field, exported or not, so a field added to any type the state
// reaches changes its output, and TestGeneratedStateCodecIsCurrent fails
// until `go generate ./internal/analysis` is rerun.
//
// How each field type is written:
//
//   - a basic value (an integer, a float, a bool, or a named type over one)
//     by its value, and a string by its number in the state's string table;
//   - a struct held by value by its fields in place;
//   - a pointer to a struct, and a map, by the pointer table: the first
//     time by what it holds, after that by its number, and a package-level
//     object (stateGlobals) by its place in that table, so a decoded state
//     shares what the encoded one shares and points at the process's own
//     singletons;
//   - an interface by the form of its dynamic value: one of the package's
//     types implementing it, numbered in name order; an internal/ast
//     interface, and a pointer to an AST node, through internal/ast/astcodec,
//     whose pointer table the nodes share;
//   - a slice and an array by its length and then its elements, a nil slice
//     told apart from an empty one;
//   - an atomic value by what it holds, and a sync.Mutex not at all.
//
// A function value, a channel, a sync.Once, or a field of another package's
// struct outside these is an error unless hooks names it, so no field is
// ever silently left out.
package stategen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OutputFile is the generated file's name in the analysis package's
// directory.
const OutputFile = "state_codec_gen.go"

// root is the type the generated codec starts from.
const root = "stdState"

// hooks are the fields the generated code hands to a hand-written method of
// the encoder and the decoder (state_codec.go), given the struct holding
// the field: `e.hook<Name>(x)` and `d.hook<Name>(x)`, with Name the type's
// and the field's names joined.
var hooks = map[string]bool{
	"builder.loader":        true,
	"defaultSite.set":       true,
	"Program.programErrors": true,
	"OnceTable.inferOnce":   true,
	"FileDecls.kept":        true,
}

// extraRoots are types written apart from the root, after it: a file's
// kept declarations (hookFileDeclsKept).
var extraRoots = []string{"keptDecls"}

// empty are the fields of functions, a slice or a map of them, that a
// finished build leaves empty: the encoder fails on one that holds any,
// and the decoder makes it nil or empty as it was.
var empty = map[string]bool{
	"TypeRegistry.lazy":   true,
	"unbuiltDecl.onBuilt": true,
}

// absent are the fields the stdlib's build never sets: the encoder fails
// on one that is not nil, and the decoder leaves it nil.
var absent = map[string]bool{
	"Program.sources":    true,
	"FileDecls.keptRest": true,
}

// Generate reads the analysis package in dir, leaving out its tests and the
// generated file, and answers OutputFile's contents.
func Generate(dir string) ([]byte, error) {
	pkg, err := load(dir)
	if err != nil {
		return nil, err
	}
	g := &gen{pkg: pkg, helpers: map[string]string{}, done: map[string]bool{}}
	return g.file()
}

func load(dir string) (*types.Package, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		base := filepath.Base(p)
		if strings.HasSuffix(base, "_test.go") || base == OutputFile {
			continue
		}
		if ok, err := build.Default.MatchFile(dir, base); err != nil || !ok {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(fset, base, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files in %s", dir)
	}
	var errs []string
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error: func(err error) {
			msg := err.Error()
			// What the generated file declares is left out.
			if i := strings.Index(msg, "undefined: "); i >= 0 {
				name := msg[i+len("undefined: "):]
				if strings.HasPrefix(name, "st") || strings.Contains(name, ".st") {
					return
				}
			}
			if strings.Contains(msg, "has no field or method st") {
				return
			}
			errs = append(errs, msg)
		},
	}
	pkg, _ := conf.Check(files[0].Name.Name, fset, files, nil)
	if len(errs) > 0 {
		return nil, fmt.Errorf("type-checking %s: %s", dir, strings.Join(errs, "; "))
	}
	return pkg, nil
}

type gen struct {
	pkg *types.Package
	// helpers maps a helper's name to the code of its encoder and decoder,
	// and done marks the struct types whose field codecs are written.
	helpers map[string]string
	done    map[string]bool
	// queue holds struct types still to write.
	queue []*types.Named
	// kinds numbers the types the pointer table keys on.
	kinds []string
	tmp   int
}

func (g *gen) q(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == g.pkg {
			return ""
		}
		return p.Name()
	})
}

// mangle turns a Go type into a fragment of an identifier.
func (g *gen) mangle(t types.Type) string {
	s := g.q(t)
	r := strings.NewReplacer("*", "P", "[]", "S", "[", "A", "]", "_", "map", "M", ".", "_", " ", "", "{", "", "}", "", ",", "_")
	return r.Replace(s)
}

func (g *gen) newVar(prefix string) string {
	g.tmp++
	return fmt.Sprintf("%s%d", prefix, g.tmp)
}

func (g *gen) kind(name string) int {
	for i, k := range g.kinds {
		if k == name {
			return i
		}
	}
	g.kinds = append(g.kinds, name)
	return len(g.kinds) - 1
}

func (g *gen) own(t types.Type) (*types.Named, bool) {
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() != g.pkg {
		return nil, false
	}
	return n, true
}

func pkgPath(t types.Type) (string, string) {
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return "", ""
	}
	return n.Obj().Pkg().Path(), n.Obj().Name()
}

const astPath = "github.com/nomi-language/nomi/internal/ast"

func basic(t types.Type) (string, bool) {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return "", false
	}
	switch b.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		return "int", true
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
		return "uint", true
	case types.Float32, types.Float64:
		return "float", true
	case types.Bool:
		return "bool", true
	case types.String:
		return "string", true
	}
	return "", false
}

// enc writes code encoding addressable expression e of type t; dec writes
// code decoding into it.
func (g *gen) code(t types.Type, e string) (enc, dec string, err error) {
	if m, ok := basic(t); ok {
		ty := g.q(t)
		switch m {
		case "int":
			return fmt.Sprintf("e.w.Int(int64(%s))\n", e), fmt.Sprintf("%s = %s(d.w.Int())\n", e, ty), nil
		case "uint":
			return fmt.Sprintf("e.w.Uint(uint64(%s))\n", e), fmt.Sprintf("%s = %s(d.w.Uint())\n", e, ty), nil
		case "float":
			return fmt.Sprintf("e.float(float64(%s))\n", e), fmt.Sprintf("%s = %s(d.float())\n", e, ty), nil
		case "bool":
			return fmt.Sprintf("e.w.Bool(%s)\n", e), fmt.Sprintf("%s = %s(d.w.Bool())\n", e, ty), nil
		case "string":
			return fmt.Sprintf("e.str(string(%s))\n", e), fmt.Sprintf("%s = %s(d.str())\n", e, ty), nil
		}
	}
	h, err := g.helper(t)
	if err != nil {
		return "", "", err
	}
	if _, ok := t.Underlying().(*types.Struct); ok {
		if _, isAtomic := atomicKind(t); !isAtomic {
			return fmt.Sprintf("e.%s(&%s)\n", h, e), fmt.Sprintf("d.%s(&%s)\n", h, e), nil
		}
		return fmt.Sprintf("e.%s(&%s)\n", h, e), fmt.Sprintf("d.%s(&%s)\n", h, e), nil
	}
	if _, ok := t.Underlying().(*types.Array); ok {
		return fmt.Sprintf("e.%s(&%s)\n", h, e), fmt.Sprintf("d.%s(&%s)\n", h, e), nil
	}
	return fmt.Sprintf("e.%s(%s)\n", h, e), fmt.Sprintf("%s = d.%s()\n", e, h), nil
}

// atomicKind reports whether t is one of sync/atomic's types, and which.
func atomicKind(t types.Type) (string, bool) {
	path, name := pkgPath(t)
	if path != "sync/atomic" {
		return "", false
	}
	return name, true
}

// helper answers the name of the helper pair for t, writing it on first
// use. A struct's helpers take a pointer to it; any other type's encoder
// takes the value and its decoder answers one.
func (g *gen) helper(t types.Type) (string, error) {
	name := "st" + g.mangle(t)
	if _, ok := g.helpers[name]; ok {
		return name, nil
	}
	g.helpers[name] = "" // reserved while being written
	code, err := g.writeHelper(name, t)
	if err != nil {
		delete(g.helpers, name)
		return "", err
	}
	g.helpers[name] = code
	return name, nil
}

func (g *gen) writeHelper(name string, t types.Type) (string, error) {
	ty := g.q(t)
	var b strings.Builder
	path, tname := pkgPath(t)
	switch {
	case path == "sync" && (tname == "Mutex" || tname == "RWMutex"):
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x *%s) {}\n\n", name, ty)
		fmt.Fprintf(&b, "func (d *stateDecoder) %s(x *%s) {}\n\n", name, ty)
		return b.String(), nil
	case path == "sync/atomic":
		if n, ok := t.(*types.Named); ok && n.TypeArgs().Len() == 1 {
			// atomic.Pointer[T]
			elem := types.NewPointer(n.TypeArgs().At(0))
			enc, dec, err := g.code(elem, "v")
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "func (e *stateEncoder) %s(x *%s) {\nv := x.Load()\n%s}\n\n", name, ty, enc)
			fmt.Fprintf(&b, "func (d *stateDecoder) %s(x *%s) {\nvar v %s\n%sx.Store(v)\n}\n\n", name, ty, g.q(elem), dec)
			return b.String(), nil
		}
		var vt string
		switch tname {
		case "Bool":
			vt = "bool"
		case "Int32":
			vt = "int32"
		case "Int64":
			vt = "int64"
		case "Uint32":
			vt = "uint32"
		case "Uint64":
			vt = "uint64"
		default:
			return "", fmt.Errorf("atomic.%s", tname)
		}
		enc, dec, err := g.code(types.Universe.Lookup(vt).Type(), "v")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x *%s) {\nv := x.Load()\n%s}\n\n", name, ty, enc)
		fmt.Fprintf(&b, "func (d *stateDecoder) %s(x *%s) {\nvar v %s\n%sx.Store(v)\n}\n\n", name, ty, vt, dec)
		return b.String(), nil
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		if path == astPath {
			fmt.Fprintf(&b, "func (e *stateEncoder) %s(x *%s) { e.w.Struct%s(x) }\n\n", name, ty, tname)
			fmt.Fprintf(&b, "func (d *stateDecoder) %s(x *%s) { d.w.Struct%s(x) }\n\n", name, ty, tname)
			return b.String(), nil
		}
		n, ok := t.(*types.Named)
		if !ok {
			return "", fmt.Errorf("an unnamed struct %s", ty)
		}
		if n.Obj().Pkg() != g.pkg {
			// Another package's struct is written field by field when
			// every field is exported.
			for i := range u.NumFields() {
				if !u.Field(i).Exported() {
					return "", fmt.Errorf("a struct %s of another package with an unexported field %s", ty, u.Field(i).Name())
				}
			}
		}
		var eb, db strings.Builder
		for i := range u.NumFields() {
			f := u.Field(i)
			key := n.Obj().Name() + "." + f.Name()
			if hooks[key] {
				hook := "hook" + upper(n.Obj().Name()) + upper(f.Name())
				fmt.Fprintf(&eb, "e.%s(x)\n", hook)
				fmt.Fprintf(&db, "d.%s(x)\n", hook)
				continue
			}
			if absent[key] {
				fmt.Fprintf(&eb, "if x.%s != nil {\ne.failf(\"%s is set\")\n}\n", f.Name(), key)
				continue
			}
			if empty[key] {
				fmt.Fprintf(&eb, "if len(x.%s) != 0 {\ne.failf(\"%s holds %%d functions\", len(x.%s))\n}\ne.w.Bool(x.%s == nil)\n", f.Name(), key, f.Name(), f.Name())
				fmt.Fprintf(&db, "if !d.w.Bool() {\nx.%s = %s{}\n}\n", f.Name(), g.q(f.Type()))
				continue
			}
			if f.Name() == "_" {
				continue
			}
			enc, dec, err := g.code(f.Type(), "x."+f.Name())
			if err != nil {
				return "", fmt.Errorf("%s: %v", key, err)
			}
			eb.WriteString(enc)
			db.WriteString(dec)
		}
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x *%s) {\n%s}\n\n", name, ty, eb.String())
		fmt.Fprintf(&b, "func (d *stateDecoder) %s(x *%s) {\n%s}\n\n", name, ty, db.String())
		return b.String(), nil
	case *types.Pointer:
		elem := u.Elem()
		if p, _ := pkgPath(elem); p == astPath {
			fmt.Fprintf(&b, "func (e *stateEncoder) %s(x %s) {\nif x == nil {\ne.w.Value(nil)\nreturn\n}\ne.w.Value(x)\n}\n\n", name, ty)
			fmt.Fprintf(&b, "func (d *stateDecoder) %s() %s {\nv := d.w.Value()\nif v == nil {\nreturn nil\n}\nreturn v.(%s)\n}\n\n", name, ty, ty)
			return b.String(), nil
		}
		if p, n := pkgPath(elem); p == "math/big" && n == "Int" {
			fmt.Fprintf(&b, "func (e *stateEncoder) %s(x %s) { e.bigInt(x) }\n\n", name, ty)
			fmt.Fprintf(&b, "func (d *stateDecoder) %s() %s { return d.bigInt() }\n\n", name, ty)
			return b.String(), nil
		}
		if _, ok := elem.Underlying().(*types.Struct); !ok {
			return "", fmt.Errorf("a pointer to %s", g.q(elem))
		}
		sh, err := g.helper(elem)
		if err != nil {
			return "", err
		}
		k := g.kind(ty)
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x %s) {\nif x == nil {\ne.w.Uint(0)\nreturn\n}\nif e.ref(unsafe.Pointer(x), %d) {\ne.%s(x)\n}\n}\n\n", name, ty, k, sh)
		fmt.Fprintf(&b, "func (d *stateDecoder) %s() %s {\nv, fresh := d.ref()\nif fresh {\nx := new(%s)\nd.add(x)\nd.%s(x)\nreturn x\n}\nif v == nil {\nreturn nil\n}\nreturn v.(%s)\n}\n\n", name, ty, g.q(elem), sh, ty)
		return b.String(), nil
	case *types.Slice:
		enc, dec, err := g.code(u.Elem(), "x[i]")
		if err != nil {
			return "", err
		}
		// A slice that is not empty is written once and then by its
		// number, keyed by its elements' address and its length, so
		// slices that share their elements read back sharing them.
		kind := g.kind(ty)
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x %s) {\ne.w.Len(x == nil, len(x))\nif len(x) == 0 || !e.refSlice(unsafe.Pointer(unsafe.SliceData(x)), len(x), %d) {\nreturn\n}\nfor i := range x {\n%s}\n}\n\n", name, ty, kind, enc)
		fmt.Fprintf(&b, "func (d *stateDecoder) %s() %s {\nn := d.w.SliceLen()\nif n < 0 {\nreturn nil\n}\nif n == 0 {\nreturn %s{}\n}\nif r, fresh := d.ref(); !fresh {\nx, _ := r.(%s)\nif len(x) != n {\nd.fail(\"a shared slice of another length\")\n}\nreturn x\n}\nd.w.Fits(n)\nx := make(%s, n)\nd.add(x)\nfor i := range x {\n%s}\nreturn x\n}\n\n", name, ty, ty, ty, ty, dec)
		return b.String(), nil
	case *types.Array:
		enc, dec, err := g.code(u.Elem(), "x[i]")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x *%s) {\nfor i := range x {\n%s}\n}\n\n", name, ty, enc)
		fmt.Fprintf(&b, "func (d *stateDecoder) %s(x *%s) {\nfor i := range x {\n%s}\n}\n\n", name, ty, dec)
		return b.String(), nil
	case *types.Map:
		if _, ok := u.Elem().Underlying().(*types.Signature); ok {
			return "", fmt.Errorf("a map of functions %s", ty)
		}
		kenc, kdec, err := g.code(u.Key(), "k")
		if err != nil {
			return "", err
		}
		venc, vdec, err := g.code(u.Elem(), "v")
		if err != nil {
			return "", err
		}
		kind := g.kind(ty)
		fmt.Fprintf(&b, "func (e *stateEncoder) %s(x %s) {\nif x == nil {\ne.w.Uint(0)\nreturn\n}\nif !e.ref(mapPointer(x), %d) {\nreturn\n}\ne.w.Uint(uint64(len(x)))\nfor k, v := range x {\n%s%s}\n}\n\n", name, ty, kind, kenc, venc)
		fmt.Fprintf(&b, "func (d *stateDecoder) %s() %s {\nr, fresh := d.ref()\nif !fresh {\nif r == nil {\nreturn nil\n}\nreturn r.(%s)\n}\nn := d.count()\nx := make(%s, n)\nd.add(x)\nfor range n {\nvar k %s\nvar v %s\n%s%sx[k] = v\n}\nreturn x\n}\n\n",
			name, ty, ty, ty, g.q(u.Key()), g.q(u.Elem()), kdec, vdec)
		return b.String(), nil
	case *types.Interface:
		if p, _ := pkgPath(t); p == astPath {
			fmt.Fprintf(&b, "func (e *stateEncoder) %s(x %s) {\nif x == nil {\ne.w.Value(nil)\nreturn\n}\ne.w.Value(x)\n}\n\n", name, ty)
			fmt.Fprintf(&b, "func (d *stateDecoder) %s() %s {\nv := d.w.Value()\nif v == nil {\nreturn nil\n}\nreturn v.(%s)\n}\n\n", name, ty, ty)
			return b.String(), nil
		}
		if _, ok := g.own(t); !ok {
			return "", fmt.Errorf("an interface %s", ty)
		}
		forms := g.forms(u)
		if len(forms) == 0 {
			return "", fmt.Errorf("no form of %s", ty)
		}
		var eb, db strings.Builder
		fmt.Fprintf(&eb, "func (e *stateEncoder) %s(x %s) {\nswitch v := x.(type) {\ncase nil:\ne.w.Uint(0)\n", name, ty)
		fmt.Fprintf(&db, "func (d *stateDecoder) %s() %s {\nswitch d.w.Uint() {\ncase 0:\nreturn nil\n", name, ty)
		for i, f := range forms {
			enc, dec, err := g.code(f, "v")
			if err != nil {
				return "", fmt.Errorf("%s as %s: %v", g.q(f), ty, err)
			}
			fmt.Fprintf(&eb, "case %s:\ne.w.Uint(%d)\n%s", g.q(f), i+1, enc)
			fmt.Fprintf(&db, "case %d:\nvar v %s\n%sreturn v\n", i+1, g.q(f), dec)
		}
		fmt.Fprintf(&eb, "default:\npanic(fmt.Sprintf(\"analysis: no state form for %%T\", x))\n}\n}\n\n")
		fmt.Fprintf(&db, "default:\nd.fail(\"an unknown form of %s\")\nreturn nil\n}\n}\n\n", ty)
		b.WriteString(eb.String())
		b.WriteString(db.String())
		return b.String(), nil
	case *types.Signature:
		return "", fmt.Errorf("a function %s", ty)
	case *types.Chan:
		return "", fmt.Errorf("a channel %s", ty)
	}
	return "", fmt.Errorf("a field of type %s", ty)
}

// forms are the package's named types whose values or pointers implement
// iface, in name order: a pointer form when the pointer implements it and
// the value does not.
func (g *gen) forms(iface *types.Interface) []types.Type {
	scope := g.pkg.Scope()
	var out []types.Type
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		n, ok := tn.Type().(*types.Named)
		if !ok || n.TypeParams().Len() > 0 {
			continue
		}
		if _, isIface := n.Underlying().(*types.Interface); isIface {
			continue
		}
		switch {
		case types.Implements(n, iface):
			out = append(out, n)
		case types.Implements(types.NewPointer(n), iface):
			out = append(out, types.NewPointer(n))
		}
	}
	return out
}

func (g *gen) file() ([]byte, error) {
	obj := g.pkg.Scope().Lookup(root)
	if obj == nil {
		return nil, fmt.Errorf("no type %s", root)
	}
	if _, err := g.helper(obj.Type()); err != nil {
		return nil, err
	}
	for _, name := range extraRoots {
		o := g.pkg.Scope().Lookup(name)
		if o == nil {
			return nil, fmt.Errorf("no type %s", name)
		}
		if _, err := g.helper(types.NewPointer(o.Type())); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(g.helpers))
	for name := range g.helpers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	out.WriteString(`// Code generated by internal/analysis/stategen; DO NOT EDIT.
// Regenerate with ` + "`go generate ./internal/analysis`" + `.

package analysis

import (
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

var (
	_ = fmt.Sprintf
	_ big.Int
	_ sync.Mutex
	_ atomic.Bool
	_ unsafe.Pointer
	_ ast.Node
	_ parser.ParseError
)

`)
	fmt.Fprintf(&out, "// stateKinds is the number of types the pointer table keys on.\nconst stateKinds = %d\n\n", len(g.kinds))
	// Every package-level variable that holds a pointer, a map or a Type,
	// as the package starts: a state that points at one is written as its
	// place here (stateGlobals).
	out.WriteString("// stateGlobalValues answers every package-level pointer, map and Type,\n// in name order.\nfunc stateGlobalValues() []any {\nreturn []any{\n")
	scope := g.pkg.Scope()
	for _, name := range scope.Names() {
		v, ok := scope.Lookup(name).(*types.Var)
		if !ok || name == "_" || strings.HasPrefix(name, "state") {
			continue
		}
		switch u := v.Type().Underlying().(type) {
		case *types.Pointer:
			if _, isStruct := u.Elem().Underlying().(*types.Struct); !isStruct {
				continue
			}
			if p, _ := pkgPath(u.Elem()); p == "sync" {
				continue
			}
		case *types.Map:
		case *types.Interface:
			if _, ok := g.own(v.Type()); !ok {
				continue
			}
		default:
			continue
		}
		fmt.Fprintf(&out, "%s,\n", name)
	}
	out.WriteString("}\n}\n\n")
	for _, name := range names {
		out.WriteString(g.helpers[name])
	}
	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting the generated file: %v\n%s", err, out.Bytes())
	}
	// A state is read only by code that writes it the same way: the digest
	// of this file's code keys an embedded one (StdStateFormat).
	sum := sha256.Sum256(src)
	return fmt.Appendf(src, "\n// stateCodecDigest is a digest of the code above it (StdStateFormat).\nconst stateCodecDigest = %q\n", hex.EncodeToString(sum[:8])), nil
}

// upper is s with its first letter upper case.
func upper(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
