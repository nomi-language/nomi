package ffirun

// The VM's host adapters for a project's Go bindings, generated into the
// wrapper's main.go.
//
// The wrapper's modes call the project's Go functions through adapters
// internal/hostgen generates here, from the Nomi declaration and the Go
// signature read from source (gotypes.go), and bind them on the machine
// through nomivmhost.Load's host tables. A Go callback parameter reaches a VM
// function value through the machine's Invoker.
//
// A binding the generator refuses is not a failed build: a program that never
// reaches it still runs. It becomes an adapter that answers the refusal as an
// error, so a program that reaches it fails naming the binding and why.

import (
	"fmt"
	goparser "go/parser"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	nomiast "github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hostgen"
)

// adapterPrefix names the generated declarations in main.go.
const adapterPrefix = "nomiHost"

// wrapperAdapters is the generated code for a wrapper's VM host table.
type wrapperAdapters struct {
	Decls   string
	Imports []wrapperImport
	Refused []refusedBinding
	// Rekeyed are the adapted bindings whose key depends on the entry.
	Rekeyed []DiscoveredExport
}

type wrapperImport struct{ Alias, Path string }

type refusedBinding struct{ Key, Reason string }

// Message is the error the refused binding's adapter answers.
func (r refusedBinding) Message() string {
	return fmt.Sprintf("the FFI wrapper generated no VM host adapter for %s: %s", r.Key, r.Reason)
}

// wrapperImportAliases are the names the template imports or declares at
// package scope, which a generated import alias must not take.
var wrapperImportAliases = []string{
	"stdfmt", "stdos", "stdfilepath", "stdstrconv", "stdtime", "stdcontext",
	"nomivmhost", "main", "rt",
}

// generateAdapters generates the adapters for every discovered binding.
func generateAdapters(projectRoot string, packages []DiscoveredPackage) (*wrapperAdapters, error) {
	src, err := resolveCompilerSource()
	if err != nil {
		return nil, err
	}
	stdSource := hostgen.DirSource(filepath.Join(src.Dir, "std"))
	mods := newAdapterModules(packages, func(user func(string) ([]byte, bool)) (*hostgen.Modules, error) {
		return hostgen.LoadModules(func(module string) ([]byte, bool) {
			if data, ok := user(module); ok {
				return data, true
			}
			return stdSource(module)
		})
	})
	// A set over no declaring directory, for the generator: every binding
	// carries the resolver of its own declaring set.
	base := mods.set("")
	if base.err != nil {
		return nil, base.err
	}

	gt := newGoTypes(projectRoot)
	var goDecls []string
	mainImports := map[string]string{"stdtime": "time"}
	for _, pkg := range packages {
		goDecls = append(goDecls, pkg.GoDecls...)
		if pkg.ImportPath != "" && pkg.Alias != "" {
			mainImports[pkg.Alias] = pkg.ImportPath
		}
	}
	mainPkg, err := gt.mainPkg(goDecls)
	if err != nil {
		return nil, err
	}
	mainScope := goScope{pkg: mainPkg, imports: mainImports}

	out := &wrapperAdapters{}
	handles := map[string]string{}
	for _, pkg := range packages {
		for _, typ := range pkg.Types {
			name := nomiModuleOfFile(typ.SourceFile) + "." + nomiTypeName(typ)
			t, err := handleType(gt, pkg, typ, mainScope)
			if err != nil {
				continue // every binding naming the type is refused below
			}
			handles[hostgen.TypeKey(t)] = name
			handles[hostgen.TypeKey(t.Elem())] = name
		}
	}

	var bindings []hostgen.Binding
	for _, pkg := range packages {
		for _, e := range pkg.Exports {
			b, err := exportBinding(gt, mods, pkg, e, mainScope)
			if err != nil {
				out.Refused = append(out.Refused, refusedBinding{Key: e.Key, Reason: err.Error()})
				continue
			}
			bindings = append(bindings, b)
		}
	}
	g := hostgen.New("main", base.ms.Resolver(), handles)
	g.SetOwnPackage("main")
	g.SetPrefix(adapterPrefix)
	g.Reserve(wrapperImportAliases...)
	preset := map[string]string{}
	for _, pkg := range packages {
		if wrapperImports(pkg) && pkg.Alias != "" {
			g.UseImport(pkg.ImportPath, pkg.Alias)
			preset[pkg.ImportPath] = pkg.Alias
		}
	}
	byKey := map[string]DiscoveredExport{}
	for _, pkg := range packages {
		for _, e := range pkg.Exports {
			byKey[e.Key] = e
		}
	}
	for _, b := range bindings {
		if err := g.Add(b); err != nil {
			out.Refused = append(out.Refused, refusedBinding{Key: b.Key, Reason: strings.TrimPrefix(err.Error(), b.Key+": ")})
			continue
		}
		if e := byKey[b.Key]; e.EntryKey != "" {
			out.Rekeyed = append(out.Rekeyed, e)
		}
	}
	decls, imports := g.Fragment()
	out.Decls = string(decls)
	for path, alias := range imports {
		if preset[path] == alias {
			continue
		}
		out.Imports = append(out.Imports, wrapperImport{Alias: alias, Path: path})
	}
	sort.Slice(out.Imports, func(i, j int) bool { return out.Imports[i].Path < out.Imports[j].Path })
	return out, nil
}

// exportBinding pairs one discovered export with its `host fn` declaration and
// its Go signature.
func exportBinding(gt *goTypes, mods *adapterModules, pkg DiscoveredPackage, e DiscoveredExport, mainScope goScope) (hostgen.Binding, error) {
	hf, res, err := mods.hostFunc(e)
	if err != nil {
		return hostgen.Binding{}, err
	}
	if e.GoBody != "" {
		sig, err := funcSig(e.ParamDecls, e.ReturnDecl)
		if err != nil {
			return hostgen.Binding{}, err
		}
		ty, err := gt.resolve(sig, mainScope)
		if err != nil {
			return hostgen.Binding{}, err
		}
		return hostgen.Binding{Key: e.Key, Go: &hostgen.GoFunc{Pkg: "main", Name: e.WrapperName, Type: ty}, Decl: hf, Res: res}, nil
	}
	p, err := gt.pkg(pkg.ImportPath)
	if err != nil {
		return hostgen.Binding{}, err
	}
	fn, ok := p.funcs[e.FuncName]
	if !ok {
		return hostgen.Binding{}, fmt.Errorf("Go package %q has no top-level function %s", pkg.ImportPath, e.FuncName)
	}
	ty, err := gt.resolve(fn.typ, goScope{pkg: p, imports: fn.imports})
	if err != nil {
		return hostgen.Binding{}, err
	}
	return hostgen.Binding{Key: e.Key, Go: &hostgen.GoFunc{Pkg: pkg.ImportPath, Name: e.FuncName, Type: ty}, Decl: hf, Res: res}, nil
}

// adapterModules finds each export's `host fn` declaration and resolves the
// types it names, in the project's Nomi files: every .nomi file beside a file
// that declares a binding, by module name.
//
// hostgen names a module by its file's base name, which is also the qualifier
// its types carry at run time, so a/util.nomi and b/util.nomi cannot share one
// module set: one set would look b/util.nomi's bindings up in a/util.nomi,
// which declares none of them. Each directory that declares a
// binding gets its own set, which answers that directory's files first.
type adapterModules struct {
	files []string
	open  func(source func(module string) ([]byte, bool)) (*hostgen.Modules, error)
	sets  map[string]*adapterModuleSet
}

type adapterModuleSet struct {
	ms  *hostgen.Modules
	err error
}

// newAdapterModules indexes packages' declaring files. open starts a module
// set over a source of the project's files: generation adds std to it, and
// the Shapes hash does not.
func newAdapterModules(packages []DiscoveredPackage, open func(source func(module string) ([]byte, bool)) (*hostgen.Modules, error)) *adapterModules {
	a := &adapterModules{open: open, sets: map[string]*adapterModuleSet{}}
	for _, pkg := range packages {
		for _, e := range pkg.Exports {
			a.files = append(a.files, e.SourceFile)
		}
		for _, t := range pkg.Types {
			a.files = append(a.files, t.SourceFile)
		}
	}
	return a
}

// set is the module set file's declarations are found in.
func (a *adapterModules) set(file string) *adapterModuleSet {
	dir := filepath.Dir(file)
	if s, ok := a.sets[dir]; ok {
		return s
	}
	index := map[string]string{}
	addNomiFiles(index, file)
	for _, f := range a.files {
		addNomiFiles(index, f)
	}
	s := &adapterModuleSet{}
	s.ms, s.err = a.open(func(module string) ([]byte, bool) {
		path, ok := index[module]
		if !ok {
			return nil, false
		}
		data, err := os.ReadFile(path)
		return data, err == nil
	})
	a.sets[dir] = s
	return s
}

// hostFunc is the `host fn` declaration e's adapter is generated from, and a
// resolver over the modules it can name.
//
// e.Key's module part is BindingModule's ("a/util"), and hostgen keys a
// declaration by the file's base name ("util"), so the lookup swaps one for
// the other.
func (a *adapterModules) hostFunc(e DiscoveredExport) (hostgen.HostFunc, *hostgen.Resolver, error) {
	s := a.set(e.SourceFile)
	if s.err != nil {
		return hostgen.HostFunc{}, nil, s.err
	}
	module := nomiModuleOfFile(e.SourceFile)
	key := e.Key
	if rest, ok := strings.CutPrefix(key, BindingModule(e.SourceFile)+"."); ok {
		key = module + "." + rest
	}
	hf, err := hostgen.FindHostFunc(s.ms, module, key)
	if err != nil {
		var err2 error
		if hf, err2 = hostgen.FindHostFunc(s.ms, module, module+"."+key); err2 != nil {
			return hostgen.HostFunc{}, nil, err
		}
	}
	return hf, s.ms.Resolver(), nil
}

// adapterShapesHashInput is every type shape the adapters convert, as the
// Shapes hash field reads it: each export's parameter and result types,
// resolved through the project's own declarations to every struct field,
// enum variant and distinct's inner type they reach.
//
// The declaration text in the Discovered field names a struct and not its
// fields, and the adapter writes the fields. Without this a field renamed in
// a nested struct kept the cached adapter building the old one, and the
// program failed reading the new field at run time instead of the binding
// being refused at load. Only the project's files are loaded: a name they do
// not declare (std's Duration, say) is a leaf, since std's declarations are
// part of the compiler identity. A resolution error is recorded as its text,
// so the hash still changes when the error does.
func adapterShapesHashInput(packages []DiscoveredPackage) string {
	mods := newAdapterModules(packages, func(source func(string) ([]byte, bool)) (*hostgen.Modules, error) {
		return hostgen.NewModules(source), nil
	})
	var lines []string
	for _, pkg := range packages {
		for _, e := range pkg.Exports {
			parts := []string{strconv.Quote(e.SourceFile), strconv.Quote(e.Key)}
			hf, res, err := mods.hostFunc(e)
			if err != nil {
				parts = append(parts, "!"+strconv.Quote(err.Error()))
				lines = append(lines, strings.Join(parts, "\t"))
				continue
			}
			res.Unloaded = true
			shape := func(t nomiast.TypeExpr) string {
				s, err := res.Shape(hf.Module, t)
				if err != nil {
					return "!" + strconv.Quote(err.Error())
				}
				return s.Structure()
			}
			for _, p := range hf.Decl.Params {
				parts = append(parts, shape(p.TypeAnnotation))
			}
			parts = append(parts, "->"+shape(hf.Decl.ReturnTypeExpr))
			lines = append(lines, strings.Join(parts, "\t"))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// handleType is the Go type behind a registered host type: the wrapper's
// prototype expression, `(*alias.Name)(nil)` or the declared expression.
func handleType(gt *goTypes, pkg DiscoveredPackage, typ DiscoveredType, mainScope goScope) (hostgen.Type, error) {
	if typ.GoTypeExpr != "" {
		expr, err := goparser.ParseExpr(typ.GoTypeExpr)
		if err != nil {
			return nil, err
		}
		return gt.resolve(expr, mainScope)
	}
	p, err := gt.pkg(pkg.ImportPath)
	if err != nil {
		return nil, err
	}
	elem, err := gt.namedIn(p, typ.TypeName)
	if err != nil {
		return nil, err
	}
	return &astType{kind: reflect.Pointer, elem: elem, str: "*" + elem.String()}, nil
}

// nomiTypeName is a host type's Nomi name.
func nomiTypeName(typ DiscoveredType) string {
	if typ.LocalName != "" {
		return typ.LocalName
	}
	return localTypeNameFromKey(typ.Key)
}

// nomiModuleOfFile is the module name a Nomi file's declarations are
// qualified by at run time: its base name.
func nomiModuleOfFile(file string) string {
	return strings.TrimSuffix(filepath.Base(file), ".nomi")
}

// addNomiFiles indexes every .nomi file beside file by module name, so a
// module importing a sibling resolves it.
func addNomiFiles(index map[string]string, file string) {
	if file == "" {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(file), "*.nomi"))
	for _, m := range matches {
		name := nomiModuleOfFile(m)
		if _, taken := index[name]; !taken {
			index[name] = m
		}
	}
	if _, taken := index[nomiModuleOfFile(file)]; !taken {
		index[nomiModuleOfFile(file)] = file
	}
}
