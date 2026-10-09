package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Unit granularity: one unit package per strongly-connected component of the
// Nomi IMPORT graph, not per Nomi file.
//
// The unit package is a name, not a compiled artifact. The builder still spells
// each expression's `code` as Go text qualified by the unit's package
// (typeRegistry's pkgOf), and nothing compiles that text. The partition below
// decides which declarations share one namespace for that spelling.
//
// # Why not one package per file
//
// Nomi permits a cyclic file import graph deliberately. The analyzer resolves
// names whole-program in two phases, so import ORDER never matters and adding
// an `import` can never produce a circular-dependency error. Go forbids an
// import cycle between packages absolutely.
//
// So one Go-spelled package per Nomi FILE cannot represent a legal Nomi
// program. The shape is ordinary: an app-root file declaring the app struct
// plus a dispatching `load`, and N deployment files each declaring one builder
// that names the root's type. The mutual reference is inherent — the root must
// call the builders and the builders must name the root's type.
//
// # Why the IMPORT graph rather than the reference graph
//
// Merging on fileIndex.reaches, the closure of the REFERENCE graph, would be
// circular: buildFileIndex needs each unit's package name to build the units it
// then closes over. The import graph is a pure
// function of the parsed program and needs nothing from the builder.
//
// It is also SAFE in the one direction that matters. Every reference edge
// implies an import edge — a qualified `owner.member`, a bare name from a
// selective import, and a type reference all need the name in scope, and a
// user file's name reaches another user file's scope only through an `import`.
// So reference ⊆ import, mutual-in-reference implies mutual-in-import, and
// merging by import-graph component merges at least as much as the reference
// graph needs. TestReferenceEdgesAreImportEdges holds that containment over the
// whole corpus, and it is the soundness argument for this whole file: the
// reference graph is a subset of the Nomi import graph, whose components are
// exactly what collapse to one unit package here, so the unit graph is
// acyclic.
//
// # The `sibling file import cycle` refusal is a fail-safe
//
// It is unreachable through this partition. `importTargets` completeness over
// Nomi's import SYNTAX is an empirical claim about a front-end surface this
// package does not own: a new import form `importTargets` does not recognise
// would make the partition under-merge silently. With the refusal in place that
// degrades to a correct, named refusal; without it, it degrades to a cycle
// between unit packages that nothing reports.
//
// # What a shared package costs, and what it does not
//
// Nothing about VISIBILITY. `pub` is enforced by the front end rather than
// borrowed from Go's exported-identifier rule, so two files sharing a unit
// package changes no visibility decision.
//
// Package-level IDENTIFIER collisions are the real cost, and they are the
// normal case rather than a corner: files that share a package typically
// collide on a `pub` function name, because a same-named builder in each
// deployment file IS the idiom (`configure`/`configure`,
// `build`/`build`/`build`). Mangling is collision-TRIGGERED at every minting
// site, so a program whose components are all singletons (every acyclic
// program) reserves no name against a non-empty predecessor and mints the same
// names it would alone.

func unitPackagesFrom(owner []int) []string {
	out := make([]string, len(owner))
	for i, o := range owner {
		out[i] = unitPackage(o)
	}
	return out
}

// componentOwners maps each unit to the lowest unit index it is mutually
// import-reachable with — a canonical representative per strongly-connected
// component.
//
// Mutual reachability over the transitive closure IS the component relation, so
// this needs no Tarjan: the closure is already computed for the containment
// argument above, module counts are small, and an equivalence relation makes
// the first lower-indexed member's own representative the component's minimum
// by induction.
func componentOwners(p *Program) []int {
	reach := nomiImportGraph(p)
	owner := make([]int, len(reach))
	for i := range owner {
		owner[i] = i
		for j := range i {
			if reach[i][j] && reach[j][i] {
				owner[i] = owner[j]
				break
			}
		}
	}
	return owner
}

// nomiImportGraph includes lexical imports and scoped fields' declared type
// dependencies. A helper can use a field without naming its schema; its
// Go-spelled read still names the unit package that declares the field's
// value type.
func nomiImportGraph(p *Program) [][]bool {
	n := len(p.Modules)
	byKey := make(map[string]int, n)
	for i := range p.Modules {
		byKey[p.Modules[i].Name] = i
	}
	g := make([][]bool, n)
	for i := range g {
		g[i] = make([]bool, n)
	}
	for i := range p.Modules {
		scopedTypeEdges(p, i, func(j int) { g[i][j] = true })
		for _, node := range p.Modules[i].Nodes {
			for _, target := range importTargets(node) {
				// A file's own directory-relative key, matched against the
				// program's module keys. A stdlib or Go import resolves to
				// nothing here and contributes no edge, which is right: only
				// user files become unit packages.
				if j, ok := byKey[target]; ok && j != i {
					g[i][j] = true
				}
			}
		}
	}
	for k := range n {
		for i := range n {
			if !g[i][k] {
				continue
			}
			for j := range n {
				if g[k][j] {
					g[i][j] = true
				}
			}
		}
	}
	return g
}

// importTargets is every user-file key an import node could name.
func importTargets(n ast.Node) []string {
	switch t := n.(type) {
	case *ast.ImportBlock:
		var out []string
		for _, e := range t.Entries {
			out = append(out, importTargets(e)...)
		}
		return out
	case *ast.ImportStmt:
		segs := make([]string, 0, len(t.ModulePath))
		for _, s := range t.ModulePath {
			segs = append(segs, ast.ImportNodeName(s))
		}
		if len(segs) == 0 {
			return nil
		}
		// `import api.{Widget, label}` names the file `api`; the trailing
		// segments of a DRILL-THROUGH (`api/http/header.{...}`) are path
		// steps, so every prefix is a candidate file key and the program's
		// own key set decides which one exists.
		out := make([]string, 0, len(segs))
		for i := range segs {
			out = append(out, strings.Join(segs[:i+1], "/"))
		}
		return out
	}
	return nil
}

// scopedTypeEdges excludes the root schema: field contracts are independent of
// its nominal identity and may be shared by unrelated execution schemas.
func scopedTypeEdges(p *Program, from int, edge func(int)) {
	fa := p.Modules[from].FA
	if fa == nil {
		return
	}
	seen := map[analysis.Type]bool{}
	var walk func(analysis.Type)
	walk = func(t analysis.Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		origin := ""
		var args []analysis.Type
		switch v := t.(type) {
		case *analysis.StructType:
			origin, args = v.Origin, v.TypeArgs
		case *analysis.EnumType:
			origin, args = v.Origin, v.TypeArgs
		case *analysis.DistinctType:
			origin = v.Origin
		case *analysis.InterfaceType:
			origin, args = v.Origin, v.TypeArgs
		case *analysis.TypeVar:
			walk(v.Resolved)
		case *analysis.ListType:
			walk(v.Elem)
		case *analysis.MapType:
			walk(v.Key)
			walk(v.Val)
		case *analysis.TupleType:
			args = v.Elems
		case *analysis.AnonStructType:
			for _, f := range v.Fields {
				walk(f.Type)
			}
		case *analysis.FuncType:
			args = v.Params
			walk(v.Return)
		}
		for _, a := range args {
			walk(a)
		}
		if origin != "" {
			for j, m := range p.Modules {
				if j != from && m.FA != nil && m.FA.Origin == origin {
					edge(j)
				}
			}
		}
	}
	for _, read := range fa.AppReadsByPos {
		walk(read.Field.Type)
	}
}
