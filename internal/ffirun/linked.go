package ffirun

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Linked is the host adapters of one program's Go bindings, generated for a
// binary that links them itself rather than through a wrapper: the tour's
// wasm links its examples' (internal/tourgo). It is what the wrapper's
// nomiHostTable is made of, without the wrapper.
type Linked struct {
	// Decls is the adapters' Go declarations, named with the prefix
	// LinkedAdapters was given: <prefix>Bind(env) answers the adapters by
	// binding key.
	Decls string
	// Imports is every package Decls names, by import path, with the alias
	// Decls spells it with.
	Imports map[string]string
	// Bindings is each `go` binding the adapters answer.
	Bindings []LinkedBinding
}

// LinkedBinding is one `go` binding: its key in the map <prefix>Bind
// answers, and the Go function it calls. A binding in the entry file is
// keyed by its module there, as a wrapper keys it before it knows the
// entry; the binary rekeys it.
type LinkedBinding struct {
	Key        string
	ImportPath string
	Func       string
	// File, Line and Col are the declaration's position.
	File      string
	Line, Col int
}

// LinkedAdapters generates the adapters of every `go` binding the program at
// entry declares, as Prepare would for its wrapper, named with prefix. A
// binding the generator cannot adapt is an error: a binary that links the
// adapters has no wrapper to report it when the program runs.
func LinkedAdapters(entry, prefix string) (*Linked, error) {
	absEntry, err := filepath.Abs(entry)
	if err != nil {
		return nil, err
	}
	projectRoot, ok := findGoModRoot(filepath.Dir(absEntry))
	sourceRoot := filepath.Dir(absEntry)
	if !ok {
		projectRoot = sourceRoot
	}
	discovered, err := discoverForEntry(projectRoot, sourceRoot, absEntry)
	if err != nil {
		return nil, err
	}
	if err := validateDiscoveredGoBindings(projectRoot, discovered); err != nil {
		return nil, err
	}
	adapters, err := generateAdaptersNamed(projectRoot, discovered, prefix)
	if err != nil {
		return nil, err
	}
	if len(adapters.Refused) > 0 {
		var reasons []string
		for _, r := range adapters.Refused {
			reasons = append(reasons, r.Message())
		}
		return nil, fmt.Errorf("ffirun: %s", strings.Join(reasons, "; "))
	}
	out := &Linked{Decls: adapters.Decls, Imports: map[string]string{}}
	for _, imp := range adapters.Imports {
		out.Imports[imp.Path] = imp.Alias
	}
	for _, pkg := range discovered {
		if pkg.Alias != "" {
			out.Imports[pkg.ImportPath] = pkg.Alias
		}
		for _, e := range pkg.Exports {
			out.Bindings = append(out.Bindings, LinkedBinding{
				Key: e.Key, ImportPath: pkg.ImportPath, Func: e.FuncName,
				File: e.SourceFile, Line: e.SourceLine, Col: e.SourceCol,
			})
		}
	}
	return out, nil
}
