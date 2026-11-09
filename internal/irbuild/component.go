package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The unit partition is described in the package doc, "Unit packages".

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
		// A std module lowered with the program (programstd.go) names a
		// sibling bare (`import sql.Conn`), and the program knows it as
		// `std/sql`.
		inStd := strings.HasPrefix(p.Modules[i].Name, "std/")
		for _, node := range p.Modules[i].Nodes {
			// An `import` at the top of a block is an import too: a
			// block-local declaration may name a type it binds.
			ast.Inspect(node, func(m ast.Node) bool {
				for _, target := range importTargets(m) {
					// A file's own directory-relative key, matched against
					// the program's module keys. A stdlib or Go import
					// resolves to nothing here and contributes no edge, which
					// is right: only user files become unit packages.
					if j, ok := byKey[target]; ok && j != i {
						g[i][j] = true
					}
					if j, ok := byKey["std/"+target]; ok && inStd && j != i {
						g[i][j] = true
					}
				}
				_, isBlock := m.(*ast.ImportBlock)
				return !isBlock
			})
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
		// A directory import reaches the files its PathUses name: the
		// directory's path with each use after it, and, for a module's
		// root imported by the module's name (`import app`, then
		// `app::users`), the use alone. An edge too many is the safe
		// direction.
		for _, use := range t.PathUses {
			rest := strings.ReplaceAll(use, ast.PathSeparator, "/")
			out = append(out, strings.Join(segs, "/")+"/"+rest)
			if len(segs) == 1 {
				out = append(out, rest)
			}
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
