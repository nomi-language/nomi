// Package tourgo is the Go the tour's examples bind, linked into the tour's
// wasm (cmd/nomi-wasm) and into every test that runs the tour's blocks.
//
// A browser cannot compile Go, so a tour block's `go` bindings cannot get
// the wrapper `nomi run` builds for them (internal/ffirun). Instead, every
// Go package a tour block binds is compiled in ahead of time, with the
// adapters ffirun generates for the block's declarations
// (ffirun.LinkedAdapters). `go generate ./internal/tourgo` reads the tour's
// chapters, stages each ```nomi-run block that declares a `gopkg` as a
// project, and writes linked_gen.go: the adapters, and `linked`, the one
// list of the Go functions the tour links, each with the Nomi declaration
// its adapter was generated for. TestGeneratedFileIsCurrent fails while it is
// stale.
//
// A block's Go file (`// FILE: urltools.go`) is shown read-only in its
// editor, and is this package's urltools/urltools.go, byte for byte
// (TestTourGoFilesAreTheLinkedSources): the generator compiles the file in
// this tree, and the page shows the same text.
//
// When a visitor edits a block, Hosts matches each `go` binding the edited
// program declares to a linked function by its Go package and name, and
// rekeys the adapter to the key the edited program crosses under, so a
// renamed or moved declaration still runs. A binding the tour did not link,
// or one whose declaration no longer has the types its adapter converts, is
// an error at the declaration (Problems).
package tourgo

//go:generate go run ./gen

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hostgen"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// linkedFunc is one Go function the tour links: the Go package's import
// path as a `gopkg` names it, the function, the Nomi declaration its adapter
// was generated for, and where to find the adapter.
type linkedFunc struct {
	importPath, fn string
	// decl is the declaration as the tour writes it, `fn upper(s: String)
	// -> String`, and shape its types as the adapter converts them
	// (Binding.Shape), and file the tab that declares it.
	decl, shape, file string
	// bind answers the adapters of the block the function was generated
	// from, numbered block, and key is this one's key there.
	block int
	bind  func(*hostadapt.Env) (map[string]hostadapt.Func, error)
	key   string
}

// Binding is one `go` binding a program declares.
type Binding struct {
	// File is the declaring file's name in the block (`urls.nomi`), and
	// Line and Col the declaration's position in it.
	File      string
	Line, Col int
	// Keys are the keys the program crosses under for it.
	Keys []string
	// ImportPath is the Go package its `gopkg` names, Alias the name the
	// binding writes it with, and Func the Go function.
	ImportPath, Alias, Func string
	// Decl is the declaration, `fn upper(s: String) -> String`.
	Decl string
	// Shape is its parameter and result types resolved to what an adapter
	// converts (hostgen.Shape.Structure): two declarations with one shape
	// share an adapter.
	Shape string
}

// GoBindings is every `go` binding the program declares: the entry
// entrySrc, whose module is entryName, and the other files, by module name
// (vmhost.SplitMultiFile's answer). A file that does not parse declares
// none; the front end reports it.
func GoBindings(entryName, entrySrc string, files map[string]string) []Binding {
	sources := map[string]string{}
	var modules []string
	add := func(name, src string) {
		base := path.Base(name)
		if _, taken := sources[base]; taken {
			return
		}
		sources[base] = src
		modules = append(modules, base)
	}
	add(entryName, entrySrc)
	names := make([]string, 0, len(files))
	for name := range files {
		if !strings.Contains(path.Base(name), ".") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		add(name, files[name])
	}
	ms := hostgen.NewModules(func(module string) ([]byte, bool) {
		src, ok := sources[module]
		return []byte(src), ok
	})
	res := ms.Resolver()
	res.Unloaded = true
	var out []Binding
	for i, module := range modules {
		if !strings.Contains(sources[module], "gopkg") {
			continue
		}
		m, err := ms.Load(module)
		if err != nil {
			continue
		}
		packages := map[string]string{}
		for _, n := range m.Nodes {
			if p, ok := n.(*ast.ExternPackage); ok {
				packages[p.Alias] = p.ImportPath
			}
		}
		for _, hf := range hostgen.HostFuncs(m) {
			d := hf.Decl
			if d.ForeignName == "" {
				continue
			}
			// A problem is reported at the Go name the binding writes.
			line, col := d.ForeignAliasLine, d.ForeignAliasCol
			if line == 0 {
				line, col = d.Line, d.Col
			}
			b := Binding{
				File: module + ".nomi", Line: line, Col: col,
				ImportPath: packages[d.ForeignAlias], Alias: d.ForeignAlias, Func: d.ForeignName,
				Decl: declText(d), Shape: shapeOf(res, hf),
			}
			// The entry's top-level declarations cross under their bare
			// names, every other one under the keys hostgen spells.
			b.Keys = hf.Keys
			if hf.Receiver == "" && i == 0 {
				b.Keys = []string{d.Name}
			}
			out = append(out, b)
		}
	}
	return out
}

// shapeOf is a declaration's parameter and result shapes, as
// internal/ffirun's Shapes hash renders them.
func shapeOf(res *hostgen.Resolver, hf hostgen.HostFunc) string {
	shape := func(t ast.TypeExpr) string {
		s, err := res.Shape(hf.Module, t)
		if err != nil {
			return "!" + err.Error()
		}
		return s.Structure()
	}
	var parts []string
	for _, p := range hf.Decl.Params {
		parts = append(parts, shape(p.TypeAnnotation))
	}
	return strings.Join(parts, "\t") + "\t->" + shape(hf.Decl.ReturnTypeExpr)
}

// declText is a declaration as a message quotes it.
func declText(d *ast.ExternFunc) string {
	params := make([]string, len(d.Params))
	for i, p := range d.Params {
		params[i] = p.Name + ": " + p.TypeAnnotation.TypeString()
	}
	text := "fn " + d.Name + "(" + strings.Join(params, ", ") + ")"
	if d.ReturnTypeExpr != nil {
		text += " -> " + d.ReturnTypeExpr.TypeString()
	}
	return text
}

// Problems is the bindings Hosts cannot answer, one line each, at the
// declaration.
type Problems []string

func (p Problems) Error() string { return strings.Join(p, "\n") }

// fixed opens every problem's message.
const fixed = "this example's Go code is fixed in the tour: "

// MarkedHosts is Hosts for a tour block as Source answers it, `// FILE:`
// sections and all, with the bindings of the modules Source attached to it:
// what every runner of tour blocks passes to vmhost.LoadMarked as
// vmhost.WithHosts.
func MarkedHosts(src string) (func(*hostadapt.Env) (map[string]hostadapt.Func, error), error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, err
	}
	hosts, err := Hosts(project.EntryName, project.EntrySource, project.VirtualFiles)
	if err != nil {
		return nil, err
	}
	if mods := carried(project); len(mods) > 0 {
		return withModules(hosts, mods), nil
	}
	return hosts, nil
}

// Hosts is the host table that answers the program's `go` bindings with the
// adapters the tour links (vmhost.WithHosts takes it), nil when the program
// declares none, or Problems when it declares one the tour cannot answer.
func Hosts(entryName, entrySrc string, files map[string]string) (func(*hostadapt.Env) (map[string]hostadapt.Func, error), error) {
	bindings := GoBindings(entryName, entrySrc, files)
	if len(bindings) == 0 {
		return nil, nil
	}
	type use struct {
		keys []string
		l    *linkedFunc
	}
	var uses []use
	var problems Problems
	for _, b := range bindings {
		at := fmt.Sprintf("%s:%d:%d: ", b.File, b.Line, b.Col)
		written := b.Alias + "." + b.Func
		l := lookup(b.ImportPath, b.Func)
		switch {
		case l == nil:
			problems = append(problems, at+fixed+"`"+written+"` is not available"+available(b.ImportPath, b.Alias))
		case l.shape != b.Shape && l.decl != b.Decl:
			problems = append(problems, fmt.Sprintf("%s%s`%s` is linked for `%s`, so a declaration that binds it takes and returns those types",
				at, fixed, written, l.decl))
		case l.shape != b.Shape:
			// The declaration reads the same, and a type it names differs.
			problems = append(problems, fmt.Sprintf("%s%s`%s` is linked for `%s` with the types %s declares, "+
				"so the types this declaration names must be those, declared there", at, fixed, written, l.decl, l.file))
		default:
			uses = append(uses, use{keys: b.Keys, l: l})
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		bound := map[int]map[string]hostadapt.Func{}
		out := map[string]hostadapt.Func{}
		for _, u := range uses {
			funcs, ok := bound[u.l.block]
			if !ok {
				var err error
				if funcs, err = u.l.bind(env); err != nil {
					return nil, err
				}
				bound[u.l.block] = funcs
			}
			f, ok := funcs[u.l.key]
			if !ok {
				return nil, fmt.Errorf("tourgo: the linked adapters have no %s", u.l.key)
			}
			for _, k := range u.keys {
				out[k] = f
			}
		}
		return out, nil
	}, nil
}

// lookup is the linked function fn of the Go package importPath, or nil.
func lookup(importPath, fn string) *linkedFunc {
	for i := range linked {
		if linked[i].importPath == importPath && linked[i].fn == fn {
			return &linked[i]
		}
	}
	return nil
}

// available says what the tour links of the Go package importPath, which a
// binding writes as alias.
func available(importPath, alias string) string {
	var fns []string
	for _, l := range linked {
		if l.importPath == importPath && !slices.Contains(fns, l.fn) {
			fns = append(fns, l.fn)
		}
	}
	if len(fns) == 0 {
		return fmt.Sprintf(" (the tour links no Go package %q)", importPath)
	}
	sort.Strings(fns)
	list := fns[0]
	if len(fns) > 1 {
		list = strings.Join(fns[:len(fns)-1], ", ") + " and " + fns[len(fns)-1]
	}
	return fmt.Sprintf(" (from %s the tour links %s)", alias, list)
}
