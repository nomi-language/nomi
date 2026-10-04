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
	"strings"

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
	userFiles := map[string]string{}
	for _, pkg := range packages {
		for _, e := range pkg.Exports {
			addNomiFiles(userFiles, e.SourceFile)
		}
		for _, t := range pkg.Types {
			addNomiFiles(userFiles, t.SourceFile)
		}
	}
	ms, err := hostgen.LoadModules(func(module string) ([]byte, bool) {
		if path, ok := userFiles[module]; ok {
			data, err := os.ReadFile(path)
			return data, err == nil
		}
		return stdSource(module)
	})
	if err != nil {
		return nil, err
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
			b, err := exportBinding(gt, ms, pkg, e, mainScope)
			if err != nil {
				out.Refused = append(out.Refused, refusedBinding{Key: e.Key, Reason: err.Error()})
				continue
			}
			bindings = append(bindings, b)
		}
	}
	g := hostgen.New("main", ms.Resolver(), handles)
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
func exportBinding(gt *goTypes, ms *hostgen.Modules, pkg DiscoveredPackage, e DiscoveredExport, mainScope goScope) (hostgen.Binding, error) {
	module := nomiModuleOfFile(e.SourceFile)
	hf, err := hostgen.FindHostFunc(ms, module, e.Key)
	if err != nil {
		var err2 error
		if hf, err2 = hostgen.FindHostFunc(ms, module, module+"."+e.Key); err2 != nil {
			return hostgen.Binding{}, err
		}
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
		return hostgen.Binding{Key: e.Key, Go: &hostgen.GoFunc{Pkg: "main", Name: e.WrapperName, Type: ty}, Decl: hf}, nil
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
	return hostgen.Binding{Key: e.Key, Go: &hostgen.GoFunc{Pkg: pkg.ImportPath, Name: e.FuncName, Type: ty}, Decl: hf}, nil
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
