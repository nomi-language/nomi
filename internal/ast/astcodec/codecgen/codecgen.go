// Package codecgen writes internal/ast/astcodec's codec_gen.go: an encoder
// and a decoder for every type the ast package declares. It reads the ast
// package's Go source with go/types, so a node type or a field added to the
// AST changes its output, and astcodec's TestGeneratedCodecIsCurrent fails
// until `go generate ./internal/ast/astcodec` is rerun.
//
// Every exported field is written, in declaration order:
//
//   - a basic value (an integer, a float, a bool, a string, or a named type
//     over one) by its value;
//   - a struct held by value, embedded or not, by its fields in place;
//   - a pointer to a struct by the pointer table (Encoder.ptr), which writes
//     a pointer met before as its number, so a tree that shares a node
//     decodes to one that shares it;
//   - an interface by the form of its dynamic value: one of the package's
//     struct types, held by value or by pointer, numbered in name order;
//   - a slice by its length and then its elements, nil and empty told apart;
//   - *big.Int and *ast.Expansion by hand-written codecs (codec.go).
//
// Any other field type, or an unexported field outside Expansion, is an
// error, so no field is ever silently left out.
package codecgen

import (
	"bytes"
	"fmt"
	"go/ast"
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

// OutputFile is the generated file's name in astcodec's directory.
const OutputFile = "codec_gen.go"

// Generate reads the ast package in astDir, leaving out its tests and its
// generated walk, and answers OutputFile's contents.
func Generate(astDir string) ([]byte, error) {
	pkg, err := load(astDir)
	if err != nil {
		return nil, err
	}
	g := &gen{pkg: pkg, structs: map[string]*types.Struct{}}
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
		if strings.HasSuffix(base, "_test.go") || base == "children_gen.go" {
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
			// The walk the package calls is generated and left out.
			for _, name := range []string{"generatedChildren", "absent"} {
				if strings.HasSuffix(msg, "undefined: "+name) {
					return
				}
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
	// forms are the struct types an interface value can hold, by value and
	// by pointer, in name order; an interface writes its value's index.
	forms []form
	// structs are the struct types whose fields have a codec, by name.
	structs map[string]*types.Struct
	tmp     int
}

type form struct {
	name string
	ptr  bool
}

func (f form) goType() string {
	if f.ptr {
		return "*ast." + f.name
	}
	return "ast." + f.name
}

func (g *gen) newVar(prefix string) string {
	g.tmp++
	return fmt.Sprintf("%s%d", prefix, g.tmp)
}

// named answers the ast package's named type t refers to, if it is one.
func (g *gen) named(t types.Type) (*types.Named, bool) {
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() != g.pkg {
		return nil, false
	}
	return n, true
}

func isBig(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := p.Elem().(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "math/big" && n.Obj().Name() == "Int"
}

// basic answers the encoder method and Go conversion for a basic type.
func basic(t types.Type) (method string, ok bool) {
	b, isBasic := t.Underlying().(*types.Basic)
	if !isBasic {
		return "", false
	}
	switch b.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64:
		return "int", true
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return "uint", true
	case types.Float64, types.Float32:
		return "float", true
	case types.Bool:
		return "bool", true
	case types.String:
		return "string", true
	}
	return "", false
}

// encode writes the encoding of expression e, of type t, which is
// addressable.
func (g *gen) encode(b *bytes.Buffer, e string, t types.Type) error {
	if m, ok := basic(t); ok {
		switch m {
		case "int":
			fmt.Fprintf(b, "e.int(int64(%s))\n", e)
		case "uint":
			fmt.Fprintf(b, "e.uint(uint64(%s))\n", e)
		case "float":
			fmt.Fprintf(b, "e.float(float64(%s))\n", e)
		case "bool":
			fmt.Fprintf(b, "e.bool(%s)\n", e)
		case "string":
			fmt.Fprintf(b, "e.string(string(%s))\n", e)
		}
		return nil
	}
	if isBig(t) {
		fmt.Fprintf(b, "e.bigInt(%s)\n", e)
		return nil
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		fmt.Fprintf(b, "e.value(%s)\n", e)
		return nil
	case *types.Struct:
		n, ok := g.named(t)
		if !ok {
			return fmt.Errorf("an unnamed struct type %s", t)
		}
		if err := g.wantStruct(n, u); err != nil {
			return err
		}
		fmt.Fprintf(b, "e.struct%s(&%s)\n", n.Obj().Name(), e)
		return nil
	case *types.Pointer:
		n, ok := g.named(u.Elem())
		if !ok {
			return fmt.Errorf("a pointer to %s", u.Elem())
		}
		if n.Obj().Name() == "Expansion" {
			fmt.Fprintf(b, "e.expansion(%s)\n", e)
			return nil
		}
		s, ok := n.Underlying().(*types.Struct)
		if !ok {
			return fmt.Errorf("a pointer to %s, which is not a struct", n)
		}
		if err := g.wantStruct(n, s); err != nil {
			return err
		}
		fmt.Fprintf(b, "if %s == nil {\ne.uint(0)\n} else if e.ptr(%s) {\ne.struct%s(%s)\n}\n", e, e, n.Obj().Name(), e)
		return nil
	case *types.Slice:
		i := g.newVar("i")
		fmt.Fprintf(b, "e.len(%s == nil, len(%s))\nfor %s := range %s {\n", e, e, i, e)
		if err := g.encode(b, e+"["+i+"]", u.Elem()); err != nil {
			return err
		}
		b.WriteString("}\n")
		return nil
	}
	return fmt.Errorf("a field of type %s", t)
}

// decode writes the decoding into addressable expression e, of type t.
func (g *gen) decode(b *bytes.Buffer, e string, t types.Type) error {
	if m, ok := basic(t); ok {
		ty := types.TypeString(t, g.qualifier)
		switch m {
		case "int":
			fmt.Fprintf(b, "%s = %s(d.int())\n", e, ty)
		case "uint":
			fmt.Fprintf(b, "%s = %s(d.uint())\n", e, ty)
		case "float":
			fmt.Fprintf(b, "%s = %s(d.float())\n", e, ty)
		case "bool":
			fmt.Fprintf(b, "%s = %s(d.bool())\n", e, ty)
		case "string":
			fmt.Fprintf(b, "%s = %s(d.string())\n", e, ty)
		}
		return nil
	}
	if isBig(t) {
		fmt.Fprintf(b, "%s = d.bigInt()\n", e)
		return nil
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		v := g.newVar("v")
		fmt.Fprintf(b, "if %s := d.value(); %s != nil {\n%s = %s.(%s)\n}\n", v, v, e, v, types.TypeString(t, g.qualifier))
		return nil
	case *types.Struct:
		n, _ := g.named(t)
		fmt.Fprintf(b, "d.struct%s(&%s)\n", n.Obj().Name(), e)
		return nil
	case *types.Pointer:
		n, _ := g.named(u.Elem())
		name := n.Obj().Name()
		if name == "Expansion" {
			fmt.Fprintf(b, "%s = d.expansion()\n", e)
			return nil
		}
		p, fresh, nx := g.newVar("p"), g.newVar("fresh"), g.newVar("x")
		fmt.Fprintf(b, "if %s, %s := d.ptr(); %s != nil {\n%s = %s.(*ast.%s)\n} else if %s {\n%s := new(ast.%s)\nd.refs = append(d.refs, %s)\nd.struct%s(%s)\n%s = %s\n}\n",
			p, fresh, p, e, p, name, fresh, nx, name, nx, name, nx, e, nx)
		return nil
	case *types.Slice:
		n, i := g.newVar("n"), g.newVar("i")
		fmt.Fprintf(b, "if %s := d.len(); %s >= 0 {\n%s = make(%s, %s)\nfor %s := range %s {\n", n, n, e, types.TypeString(t, g.qualifier), n, i, e)
		if err := g.decode(b, e+"["+i+"]", u.Elem()); err != nil {
			return err
		}
		b.WriteString("}\n}\n")
		return nil
	}
	return fmt.Errorf("a field of type %s", t)
}

func (g *gen) qualifier(p *types.Package) string {
	if p == g.pkg {
		return "ast"
	}
	return p.Name()
}

// wantStruct checks that struct type n has a codec: every field exported.
func (g *gen) wantStruct(n *types.Named, s *types.Struct) error {
	name := n.Obj().Name()
	if _, ok := g.structs[name]; ok {
		return nil
	}
	for i := range s.NumFields() {
		if f := s.Field(i); !f.Exported() && !analysisSets[name+"."+f.Name()] {
			return fmt.Errorf("struct %s has an unexported field %s, which the codec cannot write", name, f.Name())
		}
	}
	g.structs[name] = s
	return nil
}

// analysisSets are the unexported fields the analysis sets on a tree it
// checks, which a tree as the parser and the expansions leave it, the tree
// an interface holds, never has set. The codec does not write them.
var analysisSets = map[string]bool{
	"TaggedString.expansion": true,
	"TaggedString.template":  true,
}

func (g *gen) file() ([]byte, error) {
	scope := g.pkg.Scope()
	var interfaces []*types.Interface
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		if i, ok := tn.Type().Underlying().(*types.Interface); ok {
			interfaces = append(interfaces, i)
		}
	}
	// A struct type that implements one of the package's interfaces, by
	// value or by pointer, is a form an interface field may hold.
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() || !tn.Exported() {
			continue
		}
		n, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		s, ok := n.Underlying().(*types.Struct)
		if !ok || name == "Expansion" {
			continue
		}
		var byValue, byPtr bool
		for _, i := range interfaces {
			if i.NumMethods() == 0 {
				continue
			}
			byValue = byValue || types.Implements(n, i)
			byPtr = byPtr || types.Implements(types.NewPointer(n), i)
		}
		if !byValue && !byPtr {
			continue
		}
		if err := g.wantStruct(n, s); err != nil {
			return nil, err
		}
		if byValue {
			g.forms = append(g.forms, form{name: name})
		}
		if byPtr {
			g.forms = append(g.forms, form{name: name, ptr: true})
		}
	}

	var enc, dec, structs bytes.Buffer
	// value and decValue: an interface's dynamic value by its form.
	enc.WriteString("func (e *Encoder) value(v any) {\nswitch v := v.(type) {\ncase nil:\ne.uint(0)\n")
	dec.WriteString("func (d *Decoder) value() any {\nswitch d.uint() {\ncase 0:\nreturn nil\n")
	for i, f := range g.forms {
		fmt.Fprintf(&enc, "case %s:\ne.uint(%d)\n", f.goType(), i+1)
		fmt.Fprintf(&dec, "case %d:\n", i+1)
		if f.ptr {
			fmt.Fprintf(&enc, "if v == nil {\ne.uint(0)\n} else if e.ptr(v) {\ne.struct%s(v)\n}\n", f.name)
			fmt.Fprintf(&dec, "p, fresh := d.ptr()\nif p != nil {\nreturn p.(*ast.%s)\n}\nif !fresh {\nreturn (*ast.%s)(nil)\n}\nx := new(ast.%s)\nd.refs = append(d.refs, x)\nd.struct%s(x)\nreturn x\n", f.name, f.name, f.name, f.name)
		} else {
			fmt.Fprintf(&enc, "e.struct%s(&v)\n", f.name)
			fmt.Fprintf(&dec, "var x ast.%s\nd.struct%s(&x)\nreturn x\n", f.name, f.name)
		}
	}
	enc.WriteString("default:\npanic(fmt.Sprintf(\"astcodec: no form for %T\", v))\n}\n}\n\n")
	dec.WriteString("default:\nd.fail(\"an unknown form\")\nreturn nil\n}\n}\n\n")

	// Each struct's fields. Writing one may ask for more.
	done := map[string]bool{}
	for {
		var names []string
		for name := range g.structs {
			if !done[name] {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			break
		}
		sort.Strings(names)
		for _, name := range names {
			done[name] = true
			s := g.structs[name]
			var eb, db bytes.Buffer
			g.tmp = 0
			for i := range s.NumFields() {
				f := s.Field(i)
				if !f.Exported() {
					continue
				}
				if err := g.encode(&eb, "x."+f.Name(), f.Type()); err != nil {
					return nil, fmt.Errorf("%s.%s: %v", name, f.Name(), err)
				}
				if err := g.decode(&db, "x."+f.Name(), f.Type()); err != nil {
					return nil, fmt.Errorf("%s.%s: %v", name, f.Name(), err)
				}
			}
			fmt.Fprintf(&structs, "func (e *Encoder) struct%s(x *ast.%s) {\n%s}\n\n", name, name, eb.String())
			fmt.Fprintf(&structs, "func (d *Decoder) struct%s(x *ast.%s) {\n%s}\n\n", name, name, db.String())
		}
	}

	var list bytes.Buffer
	for _, f := range g.forms {
		if f.ptr {
			fmt.Fprintf(&list, "(*ast.%s)(nil),\n", f.name)
		} else {
			fmt.Fprintf(&list, "ast.%s{},\n", f.name)
		}
	}

	var out bytes.Buffer
	out.WriteString(`// Code generated by internal/ast/astcodec/codecgen; DO NOT EDIT.
// Regenerate with ` + "`go generate ./internal/ast/astcodec`" + `.

package astcodec

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

`)
	out.Write(enc.Bytes())
	out.Write(dec.Bytes())
	out.Write(structs.Bytes())
	out.WriteString(`// forms holds a value of each form an interface field may hold, in tag
// order, for the test that encodes a tree of every shape.
var forms = []any{
`)
	out.Write(list.Bytes())
	out.WriteString("}\n")
	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting the generated file: %v\n%s", err, out.Bytes())
	}
	return src, nil
}
