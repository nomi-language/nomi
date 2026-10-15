// Package walkgen writes internal/ast's children_gen.go: a type switch that
// answers, for every node type the ast package declares, the nodes directly
// beneath a node, in the order and by the rules of ast's reflective walk
// (ast.ReflectChildren). It reads the package's Go source with go/types and
// nothing else, so a node type or field added to the AST changes its output,
// and ast's TestGeneratedChildrenAreCurrent fails until
// `go generate ./internal/ast` is rerun.
//
// The rules it follows are the reflective walk's, type by type:
//
//   - a pointer whose type implements Node is a child when it is not nil;
//   - any other pointer is followed when it is not nil;
//   - an interface is a child when its dynamic value is a Node and not a
//     typed nil pointer, and is descended by its dynamic type otherwise;
//   - a struct whose pointer implements Node is a child, passed as its
//     address, when it is addressable (a field of a node, an element of a
//     slice), and its fields are walked in its place when it is not (a copy
//     held in an interface);
//   - any other struct's exported fields are walked in declaration order;
//   - a slice's or array's elements are walked in index order;
//   - nothing else holds a node.
package walkgen

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

// OutputFile is the generated file's name in the ast package's directory.
const OutputFile = "children_gen.go"

// Generate reads the Go package in dir (internal/ast), leaving out its tests
// and OutputFile, and answers OutputFile's contents.
func Generate(dir string) ([]byte, error) {
	pkg, err := load(dir)
	if err != nil {
		return nil, err
	}
	g, err := newGen(pkg)
	if err != nil {
		return nil, err
	}
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
	// The package calls what OutputFile declares, and OutputFile is left
	// out (it may be stale or missing), so those names are undefined here.
	// Any other error is the package's own.
	var errs []string
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error: func(err error) {
			msg := err.Error()
			for _, name := range generatedNames {
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

// generatedNames are the functions OutputFile declares that the rest of
// the package calls.
var generatedNames = []string{"generatedChildren", "absent"}

type gen struct {
	pkg  *types.Package
	node *types.Interface
	// named lists the package's named types in name order.
	named []*types.Named
	// holdsMemo caches holds for types asked about with nothing in progress.
	holdsMemo map[types.Type]bool
	// helpers records the field walks to emit: a struct type and whether its
	// value is addressable. Emitted in name order once the switch is built.
	helpers map[helperKey]bool
	tmp     int
}

type helperKey struct {
	name string
	addr bool
}

func newGen(pkg *types.Package) (*gen, error) {
	obj := pkg.Scope().Lookup("Node")
	if obj == nil {
		return nil, fmt.Errorf("package %s declares no Node", pkg.Name())
	}
	node, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("Node is not an interface")
	}
	g := &gen{pkg: pkg, node: node, holdsMemo: map[types.Type]bool{}, helpers: map[helperKey]bool{}}
	for _, name := range pkg.Scope().Names() {
		tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		n, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		if n.TypeParams().Len() > 0 {
			return nil, fmt.Errorf("type %s is generic; the generator walks no generic type", name)
		}
		g.named = append(g.named, n)
	}
	return g, nil
}

func (g *gen) isNode(t types.Type) bool { return types.Implements(t, g.node) }

// holds reports whether a value of type t can hold a node, as the
// reflective walk decides whether a type has a walk at all.
func (g *gen) holds(t types.Type) bool {
	if v, ok := g.holdsMemo[t]; ok {
		return v
	}
	v := g.holdsIn(t, map[types.Type]bool{})
	g.holdsMemo[t] = v
	return v
}

func (g *gen) holdsIn(t types.Type, visiting map[types.Type]bool) bool {
	if visiting[t] {
		// A path back to a type being asked about adds nothing the other
		// paths do not.
		return false
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return g.isNode(t) || g.holdsIn(u.Elem(), visiting)
	case *types.Interface:
		return true
	case *types.Struct:
		for i := range u.NumFields() {
			if f := u.Field(i); f.Exported() && g.holdsIn(f.Type(), visiting) {
				return true
			}
		}
		return false
	case *types.Slice:
		return g.holdsIn(u.Elem(), visiting)
	case *types.Array:
		return g.holdsIn(u.Elem(), visiting)
	}
	return false
}

func (g *gen) typeName(t types.Type) string {
	return types.TypeString(t, types.RelativeTo(g.pkg))
}

func (g *gen) newVar(prefix string) string {
	g.tmp++
	return fmt.Sprintf("%s%d", prefix, g.tmp)
}

// helperName names the walk over struct type n's fields: over a pointer to
// an addressable value, or over a copy.
func helperName(name string, addr bool) string {
	if addr {
		return "childrenOf" + name
	}
	return "childrenOf" + name + "Value"
}

// addrOf spells the address of the addressable expression e.
func addrOf(e string) string {
	if strings.HasPrefix(e, "(*") && strings.HasSuffix(e, ")") {
		return e[2 : len(e)-1]
	}
	return "&" + e
}

// value writes the walk of expression e of type t, as the reflective walk's
// value step does: a node there is reported, anything else is descended.
// addr says whether e is addressable.
func (g *gen) value(b *bytes.Buffer, e string, t types.Type, addr bool, depth int) error {
	if depth > 32 {
		return fmt.Errorf("type %s nests too deeply to walk inline", g.typeName(t))
	}
	if !g.holds(t) {
		return nil
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		if g.isNode(t) {
			fmt.Fprintf(b, "if %s != nil {\nfn(%s)\n}\n", e, e)
			return nil
		}
		p := g.newVar("p")
		fmt.Fprintf(b, "if %s := %s; %s != nil {\n", p, e, p)
		if err := g.value(b, "(*"+p+")", u.Elem(), true, depth+1); err != nil {
			return err
		}
		b.WriteString("}\n")
	case *types.Interface:
		if g.isNode(t) {
			fmt.Fprintf(b, "if !absent(%s) {\nfn(%s)\n}\n", e, e)
		} else {
			fmt.Fprintf(b, "childrenOfDynamic(%s, fn)\n", e)
		}
	case *types.Struct:
		named, isNamed := t.(*types.Named)
		if isNamed && g.isNode(types.NewPointer(t)) && addr {
			fmt.Fprintf(b, "fn(%s)\n", addrOf(e))
			return nil
		}
		if isNamed {
			name := named.Obj().Name()
			g.helpers[helperKey{name, addr}] = true
			if addr {
				fmt.Fprintf(b, "%s(%s, fn)\n", helperName(name, true), addrOf(e))
			} else {
				fmt.Fprintf(b, "%s(%s, fn)\n", helperName(name, false), e)
			}
			return nil
		}
		return g.fields(b, e, u, addr, depth+1)
	case *types.Slice:
		return g.elements(b, e, u.Elem(), true, depth)
	case *types.Array:
		return g.elements(b, e, u.Elem(), addr, depth)
	}
	return nil
}

func (g *gen) elements(b *bytes.Buffer, e string, elem types.Type, addr bool, depth int) error {
	switch elem.Underlying().(type) {
	case *types.Pointer, *types.Interface:
		// Element values are copied out; they need no address.
		v := g.newVar("e")
		fmt.Fprintf(b, "for _, %s := range %s {\n", v, e)
		if err := g.value(b, v, elem, addr, depth+1); err != nil {
			return err
		}
	default:
		i := g.newVar("i")
		fmt.Fprintf(b, "for %s := range %s {\n", i, e)
		if err := g.value(b, e+"["+i+"]", elem, addr, depth+1); err != nil {
			return err
		}
	}
	b.WriteString("}\n")
	return nil
}

// fields writes the walk over struct s's exported fields, in declaration
// order, of the value e.
func (g *gen) fields(b *bytes.Buffer, e string, s *types.Struct, addr bool, depth int) error {
	for i := range s.NumFields() {
		f := s.Field(i)
		if !f.Exported() {
			continue
		}
		if err := g.value(b, e+"."+f.Name(), f.Type(), addr, depth); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) file() ([]byte, error) {
	var sw, absentSw, dyn, helpers, list bytes.Buffer

	// The switch over every node type: a pointer to a struct walks its
	// fields; anything else (a node held by value, which the reflective
	// walk gives no children) has none.
	for _, n := range g.named {
		ptr := types.NewPointer(n)
		if _, isIface := n.Underlying().(*types.Interface); isIface {
			continue
		}
		name := n.Obj().Name()
		if g.isNode(ptr) {
			s, isStruct := n.Underlying().(*types.Struct)
			switch {
			case isStruct && g.holds(s):
				g.helpers[helperKey{name, true}] = true
				fmt.Fprintf(&sw, "case *%s:\nif n != nil {\n%s(n, fn)\n}\n", name, helperName(name, true))
			default:
				fmt.Fprintf(&sw, "case *%s:\n", name)
			}
			fmt.Fprintf(&absentSw, "case *%s:\nreturn n == nil\n", name)
			fmt.Fprintf(&list, "(*%s)(nil),\n", name)
		}
		if g.isNode(n) {
			fmt.Fprintf(&sw, "case %s:\n", name)
			fmt.Fprintf(&absentSw, "case %s:\nreturn false\n", name)
			fmt.Fprintf(&list, "*new(%s),\n", name)
		}
	}

	g.tmp = 0
	// The dynamic walk of a non-node interface: each of the package's own
	// types that is not a node is descended by its own walk; a node is
	// reported; any other type falls back to reflection.
	for _, n := range g.named {
		if _, isIface := n.Underlying().(*types.Interface); isIface {
			continue
		}
		name := n.Obj().Name()
		ptr := types.NewPointer(n)
		if !g.isNode(ptr) && n.Obj().Exported() {
			fmt.Fprintf(&list, "*new(%s), (*%s)(nil),\n", name, name)
			fmt.Fprintf(&dyn, "case %s:\n", name)
			if err := g.value(&dyn, "v", n, false, 0); err != nil {
				return nil, err
			}
			fmt.Fprintf(&dyn, "case *%s:\n", name)
			if g.holds(n) {
				fmt.Fprintf(&dyn, "if v != nil {\n")
				if err := g.value(&dyn, "(*v)", n, true, 0); err != nil {
					return nil, err
				}
				dyn.WriteString("}\n")
			}
		}
	}

	// The field walks the code above asks for. Emitting one may ask for
	// more, so this loops until the set is closed.
	keys := make([]helperKey, 0, len(g.helpers))
	for k := range g.helpers {
		keys = append(keys, k)
	}
	emitted := map[helperKey]bool{}
	for len(keys) > 0 {
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].name != keys[j].name {
				return keys[i].name < keys[j].name
			}
			return keys[i].addr && !keys[j].addr
		})
		var bodies []string
		for _, k := range keys {
			if emitted[k] {
				continue
			}
			emitted[k] = true
			s := g.pkg.Scope().Lookup(k.name).Type().Underlying().(*types.Struct)
			var body bytes.Buffer
			param := "x " + k.name
			if k.addr {
				param = "x *" + k.name
			}
			g.tmp = 0
			fmt.Fprintf(&body, "func %s(%s, fn func(Node)) {\n", helperName(k.name, k.addr), param)
			if err := g.fields(&body, "x", s, k.addr, 0); err != nil {
				return nil, err
			}
			body.WriteString("}\n\n")
			bodies = append(bodies, body.String())
		}
		for _, s := range bodies {
			helpers.WriteString(s)
		}
		keys = keys[:0]
		for k := range g.helpers {
			if !emitted[k] {
				keys = append(keys, k)
			}
		}
	}

	var out bytes.Buffer
	out.WriteString(`// Code generated by internal/ast/walkgen; DO NOT EDIT.
// Regenerate with ` + "`go generate ./internal/ast`" + `.

package ast

// generatedChildren calls fn on each node directly beneath n, as
// ReflectChildren does, and reports whether it knows n's type. Every type
// this package declares is known; only a type declared elsewhere falls to
// reflection.
func generatedChildren(n Node, fn func(Node)) bool {
	switch n := n.(type) {
`)
	out.Write(sw.Bytes())
	out.WriteString(`default:
	return false
	}
	return true
}

// absent reports whether n is nil or a typed nil pointer: a child the walk
// does not report.
func absent(n Node) bool {
	switch n := n.(type) {
	case nil:
		return true
`)
	out.Write(absentSw.Bytes())
	out.WriteString(`}
	return reflectAbsent(n)
}

// childrenOfDynamic walks the value of an interface that is not a Node by
// its dynamic type: a node is reported unless it is a typed nil pointer,
// and anything else is descended by its own type's walk.
func childrenOfDynamic(v any, fn func(Node)) {
	switch v := v.(type) {
	case nil:
		return
`)
	out.Write(dyn.Bytes())
	out.WriteString(`case Node:
	if !absent(v) {
		fn(v)
	}
	default:
		reflectDynamic(v, fn)
	}
}

`)
	out.Write(helpers.Bytes())
	out.WriteString(`// generatedTypes holds a value of each type the switches above name, for
// the tests that build a tree of every shape and compare the generated walk
// of it with ReflectChildren.
var generatedTypes = []any{
`)
	out.Write(list.Bytes())
	out.WriteString("}\n")
	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting the generated file: %v\n%s", err, out.Bytes())
	}
	return src, nil
}
