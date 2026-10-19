package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckRepeatedFields reports a field named twice in one struct literal or
// struct pattern, at the second name: `#Point{x: 1, x: 2}`, `#{x: 1, x: 2}`,
// `#.Rect{w: 1, w: 2}`, `#{..p, y: 1, y: 2}` (a spread's own fields; giving a
// field the spread already holds is the point of a spread),
// `Struct.update(p, #{y: 1, y: 2})`, `#Point{x: a, x: b}` in a pattern and
// `#{x: a, x: b} = p`. A literal has no last-wins rule and a pattern has no
// rule for matching one field twice, so each is an error.
//
// It also reports a name declared twice in one declaration, at the second
// name: a field of a struct or of a struct-shaped enum variant, a variant of
// an enum, a function of an interface, and a parameter of a function, host
// function, interface function or lambda. Nothing else rejects these: the
// checker keeps one of the two, and the IR builder declines what it meets
// later. (An anonymous record type's repeated field is the type resolver's
// error.)
//
// The check is syntactic: every checker path that builds or matches a struct
// (named, target-typed, variant, spread, patch) meets the same AST nodes, so
// one walk covers them all, block-local declarations included. It runs beside
// the unused-binding sweep.
func CheckRepeatedFields(nodes []ast.Node) []TypeError {
	var errs []TypeError
	report := func(name string, line, col int, what string) {
		errs = append(errs, TypeError{
			Line: line, Col: col, EndLine: line, EndCol: col + len(name),
			Message: fmt.Sprintf("field '%s' is given twice in this %s", name, what),
		})
	}
	// names reports each repeated name in one declaration's list.
	type named struct {
		name      string
		line, col int
	}
	names := func(kind, where string, items []named) {
		first := map[string]int{}
		for _, it := range items {
			if it.name == "" || it.name == "_" {
				continue
			}
			if line, ok := first[it.name]; ok {
				errs = append(errs, TypeError{
					Line: it.line, Col: it.col, EndLine: it.line, EndCol: it.col + len(it.name),
					Message: fmt.Sprintf("%s '%s' is declared twice in %s; the first is at line %d", kind, it.name, where, line),
				})
				continue
			}
			first[it.name] = it.line
		}
	}
	fieldNames := func(fields []ast.StructField) []named {
		out := make([]named, len(fields))
		for i, f := range fields {
			out[i] = named{f.Name, f.Line, f.Col}
		}
		return out
	}
	paramNames := func(params []ast.Param, where string) {
		items := make([]named, len(params))
		for i, p := range params {
			items[i] = named{p.Name, p.Line, p.Col}
		}
		names("parameter", where, items)
	}
	// The derive and Debug passes share type annotations among nodes, so a
	// node can be met twice; each one is checked once.
	seen := map[ast.Node]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if seen[n] {
				return false
			}
			seen[n] = true
			switch n := n.(type) {
			case *ast.StructDef:
				names("field", fmt.Sprintf("struct `%s`", n.Name), fieldNames(n.Fields))
			case *ast.EnumDef:
				variants := make([]named, len(n.Variants))
				for i, v := range n.Variants {
					variants[i] = named{v.Name, v.Line, v.Col}
					if v.Kind == "struct" {
						names("field", fmt.Sprintf("variant `%s.%s`", n.Name, v.Name), fieldNames(v.Fields))
					}
				}
				names("variant", fmt.Sprintf("enum `%s`", n.Name), variants)
			case *ast.InterfaceDef:
				fns := make([]named, len(n.Methods))
				for i, m := range n.Methods {
					fns[i] = named{m.Name, m.Line, m.Col}
					paramNames(m.Params, fmt.Sprintf("`fn %s`", m.Name))
				}
				names("function", fmt.Sprintf("interface `%s`", n.Name), fns)
			case *ast.FuncDef:
				paramNames(n.Params, fmt.Sprintf("`fn %s`", n.Name))
			case *ast.ExternFunc:
				paramNames(n.Params, fmt.Sprintf("`host fn %s`", n.Name))
			case *ast.Lambda:
				paramNames(n.Params, "this lambda")
			case *ast.StructLit:
				what := structSyntaxName(n.TypeName) + " literal"
				names := map[string]bool{}
				for _, f := range n.Fields {
					if names[f.Name] {
						report(f.Name, f.Line, f.Col, what)
					}
					names[f.Name] = true
				}
			case *ast.StructPattern:
				repeatedPatternFields(n.Fields, structSyntaxName(n.TypeName)+" pattern", n.Line, report)
			case *ast.StructDestructure:
				repeatedPatternFields(n.Fields, "struct pattern", n.Line, report)
			}
			return true
		})
	}
	return errs
}

func repeatedPatternFields(fields []ast.StructPatternField, what string, fallback int, report func(string, int, int, string)) {
	names := map[string]bool{}
	for _, f := range fields {
		if names[f.Name] {
			line := f.NameLine
			if line == 0 {
				line = fallback
			}
			report(f.Name, line, f.NameCol, what)
		}
		names[f.Name] = true
	}
}

// structSyntaxName names a struct literal or pattern by what its author
// wrote: the type (`Point`), the variant of a dot-leading form (`Rect`), or
// nothing for an anonymous one.
func structSyntaxName(t ast.TypeExpr) string {
	switch t := t.(type) {
	case nil:
		return "struct"
	case *ast.DotVariantType:
		return t.Name
	default:
		return t.TypeString()
	}
}
