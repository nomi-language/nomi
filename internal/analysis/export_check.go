package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An `export fn` is called by a host by name, with its arguments decoded
// from JSON and its result encoded to JSON. The parser keeps `export` to
// top-level functions outside impl blocks; the checks here need types.

var (
	toJSONIface   = &InterfaceType{Name: "ToJson", Origin: "std/json"}
	fromJSONIface = &InterfaceType{Name: "FromJson", Origin: "std/json"}
)

// checkExport reports an exported function whose signature cannot cross as
// JSON: a generic one, a parameter or result whose type is not concrete, a
// parameter whose type does not implement FromJson (the host's JSON is
// decoded into it), or a result whose type does not implement ToJson (it is
// encoded for the host). A Unit result crosses as nothing.
func (c *checker) checkExport(fn *ast.FuncDef) {
	if !fn.Export {
		return
	}
	if len(fn.TypeParams) > 0 {
		tp := fn.TypeParams[0]
		e := TypeError{Line: tp.Line, Col: tp.Col,
			Message: fmt.Sprintf("exported function `%s` cannot be generic: a host calls it with JSON values of one known type", fn.Name)}
		c.report(e.WithHint("declare it with concrete types, as a wrapper over the generic function"))
		return
	}
	sym := c.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil || sym.Node != fn {
		return
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || len(ft.Params) != len(fn.Params) {
		return
	}
	for i, p := range fn.Params {
		var at ast.Node
		if p.TypeAnnotation != nil {
			at = p.TypeAnnotation
		}
		what := fmt.Sprintf("parameter `%s` of exported function `%s`", p.Name, fn.Name)
		if p.Destructure != nil || p.Name == "" {
			what = fmt.Sprintf("parameter %d of exported function `%s`", i+1, fn.Name)
		}
		c.checkCrossesAsJSON(ft.Params[i], fromJSONIface, what, at, Pos{Line: p.Line, Col: p.Col})
	}
	ret := resolveTypeVar(ft.Return)
	if ret == nil || isUnitLike(ret) {
		return
	}
	var at ast.Node
	if fn.ReturnTypeExpr != nil {
		at = fn.ReturnTypeExpr
	}
	c.checkCrossesAsJSON(ret, toJSONIface, fmt.Sprintf("the result of exported function `%s`", fn.Name), at, Pos{Line: fn.Line, Col: fn.Col})
}

// checkCrossesAsJSON reports t, the type of what, when it cannot cross as
// JSON in the direction iface converts. at is the type annotation to
// underline, or nil to point at pos.
func (c *checker) checkCrossesAsJSON(t Type, iface *InterfaceType, what string, at ast.Node, pos Pos) {
	report := func(msg string, hints ...string) {
		var e TypeError
		if at != nil {
			e = errAt(at, msg)
		} else {
			e = TypeError{Line: pos.Line, Col: pos.Col, Message: msg}
		}
		for _, h := range hints {
			e = e.WithHint(h)
		}
		c.report(e)
	}
	t = resolveTypeVar(t)
	if t == nil {
		return
	}
	if !jsonConcrete(t) {
		report(fmt.Sprintf("%s has type `%s`, which is not concrete: a host passes JSON values of one known type", what, c.typef("%s", t)))
		return
	}
	bad := c.firstNonJSON(t, iface)
	if bad == nil {
		return
	}
	name := c.typef("%s", bad)
	full := c.typef("%s", t)
	subject := fmt.Sprintf("`%s`", name)
	if name != full {
		subject = fmt.Sprintf("`%s`, in `%s`,", name, full)
	}
	report(fmt.Sprintf("%s cannot cross as JSON: %s does not implement `%s`", what, subject, iface.Name), jsonHint(bad, name, iface.Name))
}

// firstNonJSON is the innermost part of t that does not implement iface, or
// nil when t does. A type argument is checked before the type that holds
// it, so `List<Game>` names `Game` rather than the list.
func (c *checker) firstNonJSON(t Type, iface *InterfaceType) Type {
	t = resolveTypeVar(t)
	for _, a := range jsonTypeArgs(t) {
		if bad := c.firstNonJSON(a, iface); bad != nil {
			return bad
		}
	}
	if typeSatisfiesInterfaceForImplMatch(t, iface, c.implsContext(), c.implTypeArgsContext(), nil) {
		return nil
	}
	return t
}

// jsonTypeArgs are the type arguments of a generic struct, enum or distinct
// type: the parts an impl over it converts too.
func jsonTypeArgs(t Type) []Type {
	switch v := t.(type) {
	case *ListType:
		return []Type{v.Elem}
	case *MapType:
		return []Type{v.Key, v.Val}
	case *StructType:
		return v.TypeArgs
	case *EnumType:
		return v.TypeArgs
	case *DistinctType:
		return v.TypeArgs
	}
	return nil
}

// jsonConcrete reports whether t names one known type: no type parameter,
// unsolved variable, interface value or Any anywhere in it.
func jsonConcrete(t Type) bool {
	t = resolveTypeVar(t)
	switch v := t.(type) {
	case nil:
		return true
	case *TypeParam_, *TypeVar, *InterfaceType:
		return false
	case *PrimitiveType:
		return v != TypeAny
	}
	if ContainsTypeParam(t) || containsTypeVar(t) {
		return false
	}
	for _, a := range jsonTypeArgs(t) {
		if !jsonConcrete(a) {
			return false
		}
	}
	return true
}

// jsonHint says how bad, which does not implement iface, can.
func jsonHint(bad Type, name, iface string) string {
	if !programDeclares(bad) {
		return "a parameter's type must implement `FromJson` and a result's `ToJson`: std implements both for Int, Float, String, Bool, List, Vector, Set, Maybe and Map with String keys, and a struct or enum the program declares can derive them"
	}
	return fmt.Sprintf("add `derive %s for %s`", iface, name)
}

// CheckExportNames reports each exported function whose name another file
// of the program already exports: a host calls them by name. files are the
// program's files in build order, each with its path and nodes; the error
// for a repeat is added to the analysis of the file that repeats it.
func CheckExportNames(files []ExportFile) {
	type first struct {
		path string
		fn   *ast.FuncDef
	}
	seen := map[string]first{}
	done := map[*ast.FuncDef]bool{}
	for _, f := range files {
		for _, n := range f.Nodes {
			fn, ok := n.(*ast.FuncDef)
			if !ok || !fn.Export || done[fn] {
				continue
			}
			done[fn] = true
			prev, dup := seen[fn.Name]
			if !dup {
				seen[fn.Name] = first{f.Path, fn}
				continue
			}
			if prev.path == f.Path {
				// The file declares the name twice, which the file's own
				// duplicate-declaration check reports.
				continue
			}
			e := TypeError{Line: fn.Line, Col: fn.Col, EndLine: fn.Line, EndCol: fn.Col + len(fn.Name),
				Message: fmt.Sprintf("exported function `%s` is already exported by %s: a host calls exported functions by name, so each name is exported once",
					fn.Name, displayExportPath(prev.path))}
			e = e.WithRelated(nameRelated(prev.path, Pos{Line: prev.fn.Line, Col: prev.fn.Col}, fn.Name, "`"+fn.Name+"` is exported here"))
			f.FA.TypeErrors = append(f.FA.TypeErrors, e)
		}
	}
}

// ExportFile is one file CheckExportNames reads.
type ExportFile struct {
	Path  string
	Nodes []ast.Node
	FA    *FileAnalysis
}

// displayExportPath is the base name of the file at path.
func displayExportPath(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}
