package ffirun

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/mod/module"
)

// GoImportProblem says what is wrong with a `gopkg` declaration's
// importPath, declared in the .nomi file nomiPath, in words that follow the
// declaration (`gopkg "x": <problem>`), or "" when nothing is. The checker
// reports it at the declaration, by the rule the wrapper build validates
// against (goModProvidesImportPath), so `nomi check` and the language server
// say what a run's FFI validation would.
//
// The import path must be a valid Go import path. A Go standard library
// package needs no go.mod (gostd.go), unless it is internal to the standard
// library. Any other package must be provided by the nearest go.mod above
// nomiPath (found as findGoModRoot finds the wrapper's project root): the
// project's own module or a package in it, or a module that go.mod requires
// or replaces. And it must build without cgo, as every Go binding is built
// (cgoProblem): a package that imports "C", or imports one that does, is
// refused here.
func GoImportProblem(nomiPath, importPath string) string {
	if why := GoImportPathProblem(importPath); why != "" {
		return why
	}
	if IsStdPackage(importPath) {
		if stdInternal(importPath) {
			return "internal to the Go standard library, which no other module may import"
		}
		return ""
	}
	root, ok := findGoModRoot(filepath.Dir(nomiPath))
	if !ok {
		return "no go.mod above the declaring file, so no Go module provides it"
	}
	f, err := parseProjectGoMod(root)
	if err != nil || f.Module == nil || f.Module.Mod.Path == "" {
		return "the go.mod at " + root + " cannot be read or names no module"
	}
	if goModProvidesImportPath(root, importPath) {
		return cgoProblem(root, importPath)
	}
	return "outside the project's own Go module " + f.Module.Mod.Path +
		", the Go standard library, and every module its go.mod requires or replaces"
}

// InMemoryFile reports whether nomiPath names a file the front end holds in
// memory rather than one on disk: a relative path (a virtual sibling of a
// source string, such as a tour block's `urls.nomi`) with no file there. No
// Go module provides such a file's `gopkg`; the host table its program is
// loaded with answers its bindings, or the front end reports them unanswered
// (frontend.ValidateHosts).
func InMemoryFile(nomiPath string) bool {
	if filepath.IsAbs(nomiPath) {
		return false
	}
	_, err := os.Stat(nomiPath)
	return err != nil
}

// GoImportPathProblem is GoImportProblem's first half, which needs no file:
// what is wrong with importPath as a Go import path, or "".
func GoImportPathProblem(importPath string) string {
	err := module.CheckImportPath(importPath)
	if err == nil {
		return ""
	}
	var invalid *module.InvalidPathError
	if errors.As(err, &invalid) && invalid.Err != nil {
		err = invalid.Err
	}
	return "not a valid Go import path: " + err.Error()
}
