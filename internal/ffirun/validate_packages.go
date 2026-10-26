package ffirun

import "fmt"

// The Go packages a binding's signature reaches beyond the bound package.
//
// A bound package's signature may name a type of another package it imports,
// directly (`model.User`) or through an alias it declares (`type Cell =
// core.Cell`). The validator reads that package's source the way it reads
// the bound package's: one of the project's own module, of a local
// `replace`, or of a required module in the Go module cache (goSourceOf). A
// type of a Go standard library package is not read; only the projection
// table's rows (time.Duration, time.Time) cross from one.

// goParsedPackage is one Go package's top-level declarations, parsed once
// per validation.
type goParsedPackage struct {
	dir     string
	std     bool
	symbols goPackageSymbols
}

// goPkgReader reads the packages the bindings of one bound package reach.
type goPkgReader struct {
	projectRoot string
	// bound is the import path of the package whose bindings are checked.
	bound string
	// explicit is each package's `opaque type ... go` bindings, by import
	// path, so a type another package binds as opaque stays opaque when the
	// bound package's signature names it.
	explicit map[string][]DiscoveredType
	// parsed is shared by every bound package's reader.
	parsed map[string]*goParsedPackage
	views  map[string]*goPkgTypes
}

// goPkgTypes is one Go package's types as the validator projects them.
type goPkgTypes struct {
	path     string
	name     string
	bindings goTypeBindings
	structs  map[string]goStructSymbol
	reader   *goPkgReader
}

// view is the types of the package at path, whose declarations are p.
// applyExplicit binds the package's `opaque type` declarations; the bound
// package's are bound by validateDiscoveredGoBindings, which reports one
// that names no type.
func (r *goPkgReader) view(path string, p *goParsedPackage, applyExplicit bool) *goPkgTypes {
	if v, ok := r.views[path]; ok {
		return v
	}
	v := &goPkgTypes{path: path, name: defaultImportName(path), bindings: makeGoTypeBindings(p.symbols),
		structs: p.symbols.structs, reader: r}
	if p.dir != "" {
		v.name = goPackageName(p.dir, path)
	}
	if applyExplicit {
		for _, typ := range r.explicit[path] {
			sym, ok := p.symbols.types[typ.TypeName]
			if !ok {
				continue
			}
			localName := typ.LocalName
			if localName == "" {
				localName = localTypeNameFromKey(typ.Key)
			}
			v.bindings[typ.TypeName] = goTypeBinding{localName: localName, explicit: true, symbol: sym}
		}
	}
	r.views[path] = v
	return v
}

// pkg is the types of the package at path, which a package the bindings
// reach imports, or nil for a Go standard library package, whose types
// cross only through the projection table.
func (r *goPkgReader) pkg(path string) (*goPkgTypes, error) {
	if v, ok := r.views[path]; ok {
		return v, nil
	}
	p, ok := r.parsed[path]
	if !ok {
		src, readable, err := goSourceOf(r.projectRoot, path)
		if err != nil {
			return nil, err
		}
		if src.std {
			return nil, nil
		}
		if !readable {
			if goModProvidesImportPath(r.projectRoot, path) {
				return nil, fmt.Errorf("Go package %q is in a module go.mod requires, which is not in the Go module cache; "+
					"run `nomi fetch` to download it", path)
			}
			return nil, fmt.Errorf("Go package %q is not in the project's own module, a local `replace` "+
				"or a module go.mod requires, so its source is not read", path)
		}
		symbols, err := src.symbols()
		if err != nil {
			return nil, err
		}
		p = &goParsedPackage{dir: src.dir, symbols: symbols}
		r.parsed[path] = p
	}
	if p.std {
		return nil, nil
	}
	return r.view(path, p, true), nil
}

// qualified is the Go type name declared in p as the bound package's author
// reads it: bare in the bound package, qualified by p's package name in
// another.
func (p *goPkgTypes) qualified(name string) string {
	if p.path == p.reader.bound {
		return name
	}
	return p.name + "." + name
}

// declaring names the package that declares the struct name, for an error
// about one of its fields, when that is not the bound package.
func (p *goPkgTypes) declaring(name string) string {
	if p.path == p.reader.bound {
		return ""
	}
	return fmt.Sprintf(" (Go %s, in package %q)", p.qualified(name), p.path)
}
