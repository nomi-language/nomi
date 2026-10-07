package ffirun

import (
	"errors"
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
// or replaces.
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
		return ""
	}
	return "outside the project's own Go module " + f.Module.Mod.Path +
		", the Go standard library, and every module its go.mod requires or replaces"
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
