// Package stdtable is the stdlib's adapter table: internal/stdlibbindings'
// rows paired with the std facades, as internal/hostgen generates
// internal/stdlibadapters from them. It is its own package so internal/hostgen
// does not import nomi/std, which imports the front end, which imports
// internal/ffirun, which generates a project's adapters with internal/hostgen.
package stdtable

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"

	"github.com/nomi-language/nomi/internal/hostgen"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/std"
)

// Package is the package the stdlib adapters are generated into.
const Package = "stdlibadapters"

// Generate renders internal/stdlibadapters/adapters_gen.go: one adapter
// per row of internal/stdlibbindings' two lists, Funcs and RtFuncs, the table
// that names each Go symbol. The declarations come from the embedded std
// facades. Nothing else is read, so the file is a function of those two inputs
// and the staleness test can regenerate it and compare.
func Generate() ([]byte, error) { return hostgen.Generate(Table()) }

// Table is Generate's input.
func Table() hostgen.Table {
	tb := hostgen.Table{
		Package: Package,
		Header: "// Source: internal/stdlibbindings (the Go symbols) and std/*.nomi (the\n" +
			"// declarations). Regenerate with `go generate ./internal/stdlibadapters`.\n",
		Source:   std.ReadFile,
		ModuleOf: stdRowModule,
	}
	for _, b := range stdlibbindings.Types() {
		tb.Types = append(tb.Types, hostgen.TypeRow{Name: b.Name, Prototype: b.Prototype})
	}
	for _, list := range [][]stdlibbindings.Binding{stdlibbindings.Funcs(), stdlibbindings.RtFuncs()} {
		for _, b := range list {
			tb.Funcs = append(tb.Funcs, hostgen.FuncRow{Name: b.Name, Fn: b.Fn, PanicsPropagate: b.PanicsPropagate})
		}
	}
	return tb
}

// stdRowModule is the std module a stdlib row's `host fn` is declared in. An
// rt function implements a declaration whose key is module-qualified
// (`strings.String.trim`), so the module is the key's first segment; any other
// function lives in a first-party adapter package, which names its module.
func stdRowModule(row hostgen.FuncRow) (string, error) {
	if goPackageOf(row.Fn) == rtPackage {
		module, _, found := strings.Cut(row.Name, ".")
		if !found {
			return "", fmt.Errorf("the key of an rt row is not module-qualified")
		}
		return module, nil
	}
	return stdModuleOf(row.Fn)
}

const rtPackage = "github.com/nomi-language/nomi/rt"

// goPackageOf is the import path of the package a function value is declared
// in, or "" when it has no symbol.
func goPackageOf(fn any) string {
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return ""
	}
	full := f.Name()
	slash := strings.LastIndex(full, "/")
	dot := strings.Index(full[slash+1:], ".")
	if dot < 0 {
		return ""
	}
	return full[:slash+1+dot]
}

// stdModuleOf is the std module a first-party adapter function implements,
// read off its Go package: `internal/std<name>` implements `std/<name>`. Every
// first-party adapter package follows that layout, and a binding outside it is
// refused rather than guessed at.
func stdModuleOf(fn any) (string, error) {
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return "", fmt.Errorf("the function value has no symbol")
	}
	full := f.Name()
	slash := strings.LastIndex(full, "/")
	dot := strings.Index(full[slash+1:], ".")
	if dot < 0 {
		return "", fmt.Errorf("symbol %q has no package", full)
	}
	pkg := full[:slash+1+dot]
	name, ok := strings.CutPrefix(pkg, "github.com/nomi-language/nomi/internal/std")
	if !ok || name == "" {
		return "", fmt.Errorf("Go package %s is not a first-party adapter package (internal/std<module>)", pkg)
	}
	return name, nil
}
