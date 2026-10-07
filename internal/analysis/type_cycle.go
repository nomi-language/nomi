package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// distinctCycle reports whether inner, about to become dt's inner type,
// contains dt itself, directly or through the inner types of other distinct
// types. It returns the names along the way, starting and ending with dt's,
// or nil when there is no cycle.
//
// A distinct type's inner type is its representation, held by pointer
// (DistinctType.Inner). `type L List<L>` would make that a cyclic graph, and
// every walk over types that follows Inner would recurse until the stack
// overflows. A struct's fields and an enum's variants are looked up through
// the declaration instead, which is why `struct T { kids: List<T> }` is the
// way to write a recursive type. Inner is assigned only in buildTypeDef, so
// rejecting the cycle there keeps every type graph finite.
//
// The walk follows every position a Type holds by pointer except struct
// fields and enum variants, and visits each distinct type once.
func distinctCycle(dt *DistinctType, inner Type) []string {
	seen := map[*DistinctType]bool{}
	var walk func(t Type, path []string) []string
	walkAll := func(ts []Type, path []string) []string {
		for _, t := range ts {
			if p := walk(t, path); p != nil {
				return p
			}
		}
		return nil
	}
	walk = func(t Type, path []string) []string {
		switch tt := t.(type) {
		case *DistinctType:
			if tt == dt {
				return append(append([]string(nil), path...), dt.Name)
			}
			if seen[tt] {
				return nil
			}
			seen[tt] = true
			if p := walkAll(tt.TypeArgs, path); p != nil {
				return p
			}
			if tt.Inner != nil {
				return walk(tt.Inner, append(path, tt.Name))
			}
		case *ListType:
			return walk(tt.Elem, path)
		case *MapType:
			if p := walk(tt.Key, path); p != nil {
				return p
			}
			return walk(tt.Val, path)
		case *TupleType:
			return walkAll(tt.Elems, path)
		case *FuncType:
			if p := walkAll(tt.Params, path); p != nil {
				return p
			}
			return walk(tt.Return, path)
		case *StructType:
			return walkAll(tt.TypeArgs, path)
		case *EnumType:
			return walkAll(tt.TypeArgs, path)
		case *InterfaceType:
			return walkAll(tt.TypeArgs, path)
		case *AnonStructType:
			for _, f := range tt.Fields {
				if p := walk(f.Type, path); p != nil {
					return p
				}
			}
		case *PartialType:
			return walk(tt.Inner, path)
		}
		return nil
	}
	return walk(inner, []string{dt.Name})
}

// distinctCycleError is the error at a distinct type declaration whose inner
// type contains the type itself.
func distinctCycleError(n *ast.TypeDef, path []string) TypeError {
	msg := fmt.Sprintf("type '%s' is defined in terms of itself", n.Name)
	if len(path) > 2 {
		msg += " (" + strings.Join(path, " → ") + ")"
	}
	return TypeError{
		Line: n.Line, Col: n.Col,
		Message: msg,
	}.WithHint(fmt.Sprintf("a distinct type's inner type is its representation and cannot contain '%s' itself; declare a struct or an enum to make a recursive type", n.Name))
}

// aliasBuilder builds a file's or a block's typealiases. An alias resolves
// to its target when it is built, so a declaration that names an alias
// declared below it needs that alias built first. Each alias is registered
// as a lazy entry under its name (TypeRegistry.RegisterLazy) and built at
// whichever comes first, its declaration in source order or the first
// lookup of its name; building in source order alone made `typealias C D`
// above `typealias D Int` an "unknown type" error.
//
// An alias that names itself, directly or through other aliases, is never
// built: it gets "typealias 'X' refers to itself" at its declaration
// instead, which is also what keeps the lazy builds from recursing.
type aliasBuilder struct {
	fa      *FileAnalysis
	regFor  func(*ast.TypeAlias) (*TypeRegistry, bool)
	pending map[*ast.TypeAlias]bool
	errs    []TypeError
}

// newAliasBuilder prepares the aliases among nodes. regFor answers the
// registry an alias is built against and whether the alias owns its name
// there; only an alias that owns its name is built on lookup.
func newAliasBuilder(fa *FileAnalysis, nodes []ast.Node, regFor func(*ast.TypeAlias) (*TypeRegistry, bool)) *aliasBuilder {
	ab := &aliasBuilder{fa: fa, regFor: regFor, pending: map[*ast.TypeAlias]bool{}}
	inCycle, errs := typeAliasCycles(nodes)
	ab.errs = errs
	for _, node := range nodes {
		n, ok := node.(*ast.TypeAlias)
		if !ok || inCycle[n] {
			continue
		}
		ab.pending[n] = true
		if reg, owns := regFor(n); owns {
			reg.RegisterLazy(n.Name, func() { ab.build(n) })
		}
	}
	return ab
}

// build builds n unless it is built already or is on a cycle.
func (ab *aliasBuilder) build(n *ast.TypeAlias) {
	if !ab.pending[n] {
		return
	}
	delete(ab.pending, n)
	reg, _ := ab.regFor(n)
	ab.errs = append(ab.errs, buildTypeAlias(ab.fa, reg, n)...)
}

// typeAliasCycles returns the aliases among nodes whose target names the
// alias itself, directly or through other aliases among nodes, and an error
// at each.
func typeAliasCycles(nodes []ast.Node) (map[*ast.TypeAlias]bool, []TypeError) {
	byName := map[string]*ast.TypeAlias{}
	var decls []*ast.TypeAlias
	for _, node := range nodes {
		if n, ok := node.(*ast.TypeAlias); ok {
			decls = append(decls, n)
			if _, dup := byName[n.Name]; !dup {
				byName[n.Name] = n
			}
		}
	}
	if len(decls) == 0 {
		return nil, nil
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[*ast.TypeAlias]int{}
	inCycle := map[*ast.TypeAlias]bool{}
	var errs []TypeError
	var stack []*ast.TypeAlias
	var visit func(n *ast.TypeAlias)
	visit = func(n *ast.TypeAlias) {
		switch state[n] {
		case done:
			return
		case visiting:
			start := len(stack) - 1
			for stack[start] != n {
				start--
			}
			cycle := stack[start:]
			names := make([]string, 0, len(cycle)+1)
			for _, m := range cycle {
				names = append(names, m.Name)
			}
			names = append(names, n.Name)
			for _, m := range cycle {
				if inCycle[m] {
					continue
				}
				inCycle[m] = true
				msg := fmt.Sprintf("typealias '%s' refers to itself", m.Name)
				if len(cycle) > 1 {
					msg += " (" + strings.Join(rotateCycle(names, m.Name), " → ") + ")"
				}
				errs = append(errs, TypeError{Line: m.Line, Col: m.Col, Message: msg}.
					WithHint("a typealias is another name for its target, so the target cannot name the alias; a recursive type is a struct or an enum"))
			}
			return
		}
		state[n] = visiting
		stack = append(stack, n)
		forEachTypeAliasTarget(n, func(te ast.TypeExpr) {
			typeExprNames(te, func(name string) {
				if dep := byName[name]; dep != nil {
					visit(dep)
				}
			})
		})
		stack = stack[:len(stack)-1]
		state[n] = done
	}
	for _, n := range decls {
		visit(n)
	}
	return inCycle, errs
}

func forEachTypeAliasTarget(n *ast.TypeAlias, f func(ast.TypeExpr)) {
	if len(n.Bounds) > 0 {
		for _, b := range n.Bounds {
			f(b)
		}
		return
	}
	if n.TargetTypeExpr != nil {
		f(n.TargetTypeExpr)
	}
}

// typeExprNames calls f with every unqualified type name te writes.
func typeExprNames(te ast.TypeExpr, f func(string)) {
	switch t := te.(type) {
	case *ast.SimpleType:
		f(t.Name)
	case *ast.GenericType:
		f(t.Name)
		for _, p := range t.Params {
			typeExprNames(p, f)
		}
	case *ast.FuncType:
		for _, p := range t.Params {
			typeExprNames(p, f)
		}
		if t.Return != nil {
			typeExprNames(t.Return, f)
		}
	case *ast.AnonStructType:
		for _, fld := range t.Fields {
			if fld.TypeAnnotation != nil {
				typeExprNames(fld.TypeAnnotation, f)
			}
		}
	}
}
