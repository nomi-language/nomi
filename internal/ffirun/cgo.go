package ffirun

import (
	"go/build"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/modcache"
)

// Go bindings build with CGO_ENABLED=0: every wrapper and runner does, which
// is what lets one toolchain cross-compile them to every target, and the
// managed toolchain has no C compiler. A package that calls C through cgo
// cannot be bound, so a `gopkg` naming one, or a package that imports one, is
// an error at the `gopkg` (cgoProblem), not a failure deep in a wrapper
// build.
//
// A package needs cgo when, built for this platform with cgo enabled, one of
// its files imports "C". The check follows imports through the project's own
// module, its local replaces and the modules in the module cache, and skips
// the Go standard library, whose cgo files all have pure-Go alternatives
// (net, os/user). A package whose source is not on disk is not checked.

// cgoProblem says why importPath cannot be bound for want of cgo, in words
// that follow the `gopkg` declaration, or "".
func cgoProblem(projectRoot, importPath string) string {
	chain := cgoChain(projectRoot, importPath)
	switch len(chain) {
	case 0:
		return ""
	case 1:
		return "needs cgo: its files import \"C\", and Go bindings build with CGO_ENABLED=0"
	}
	return "needs cgo: it imports " + chain[len(chain)-1] + " (" + strings.Join(chain, " → ") +
		"), whose files import \"C\", and Go bindings build with CGO_ENABLED=0"
}

// cgoChain is the import chain from importPath to the first package on it
// that needs cgo, or nil.
func cgoChain(projectRoot, importPath string) []string {
	parent := map[string]string{importPath: ""}
	queue := []string{importPath}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		dir, ok, err := resolveLocalImportDir(projectRoot, path)
		if err != nil || !ok {
			continue
		}
		cgo, imports := packageCgo(dir)
		if cgo {
			var chain []string
			for p := path; p != ""; p = parent[p] {
				chain = append([]string{p}, chain...)
			}
			return chain
		}
		for _, imp := range imports {
			if _, seen := parent[imp]; seen || imp == "C" || stdLooking(imp) {
				continue
			}
			parent[imp] = path
			queue = append(queue, imp)
		}
	}
	return nil
}

// stdLooking reports whether an import path is spelled as a standard library
// package's: no dot in its first element. A module path without a dot is
// resolved first (resolveLocalImportDir), so this only skips what is left.
func stdLooking(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

type cgoResult struct {
	cgo     bool
	imports []string
}

// cgoCache holds the answer for directories in the module cache, which never
// change.
var cgoCache sync.Map

// packageCgo reports whether the package in dir has a file that imports "C"
// when built with cgo for this platform, and what it imports.
func packageCgo(dir string) (bool, []string) {
	immutable := underModCache(dir)
	if immutable {
		if r, ok := cgoCache.Load(dir); ok {
			res := r.(cgoResult)
			return res.cgo, res.imports
		}
	}
	ctxt := build.Default
	ctxt.CgoEnabled = true
	// A partial package (a file that does not parse, no Go files) still
	// answers what was read.
	pkg, _ := ctxt.ImportDir(dir, build.ImportComment)
	res := cgoResult{}
	if pkg != nil {
		res.cgo = len(pkg.CgoFiles) > 0
		res.imports = pkg.Imports
	}
	if immutable {
		cgoCache.Store(dir, res)
	}
	return res.cgo, res.imports
}

func underModCache(dir string) bool {
	cache, err := modcache.Root()
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(cache, dir)
	return err == nil && !strings.HasPrefix(rel, "..")
}
