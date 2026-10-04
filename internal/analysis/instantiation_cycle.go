package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Polymorphic recursion: a generic function that reaches itself at a type
// built from its own type parameter (`fn grow<T>(x: T) { grow(Box{inner: x}) }`)
// needs an instance at T, Box<T>, Box<Box<T>>, … — infinitely many. Nomi
// instantiates every generic function per type-argument tuple (spec §13), so
// the program has no finite translation and is rejected here rather than at
// run time. Rust ("reached the recursion limit while instantiating"), C++
// (template instantiation depth) and Go ("instantiation cycle") reject it too;
// the rule is Go's, which decides it statically.
//
// THE GRAPH. A node is one type parameter of one generic `fn`. A call from a
// generic body to a generic `fn` adds, for each of the callee's parameters Q
// and each of the caller's parameters P its type argument mentions, an edge
// P -> Q. The edge GROWS when the argument is more than P itself (`Box<T>`,
// `(T, T)`, `List<T>`); a bare `T`, or a swap of parameters, does not. A cycle
// through a growing edge is an unbounded instantiation chain. Every edge of a
// strongly connected component lies on a cycle, so a growing edge whose two
// ends share a component is exactly such a cycle.

// instNode is one type parameter of one generic declaration.
type instNode struct {
	fn *ast.FuncDef
	tp string
}

// instEdge is one instantiation a generic body makes: the caller's parameter
// `from` flows into the callee's parameter `to` through arg.
type instEdge struct {
	from, to  instNode
	grows     bool
	arg       Type
	line, col int
}

// recordInstantiation adds the edges a generic call makes from the enclosing
// generic declaration: each callee type parameter's argument, from the
// solved substitution, or read off the call's arguments against the
// parameters they fill. A self call leaves its own parameters unsolved in
// subs (they are the caller's, which unification keeps rigid), so the
// argument pairs are what carry `grow(Box{inner: x})`'s T = Box<T>.
func (c *checker) recordInstantiation(n *ast.Call, subs map[*TypeParam_]Type, args [][2]Type) {
	caller := c.currentFnDecl
	if caller == nil || len(caller.TypeParams) == 0 || len(c.fnTypeParams) == 0 {
		return
	}
	callee := c.calleeFuncDecl(n.Func)
	if callee == nil || len(callee.TypeParams) == 0 {
		return
	}
	solved := map[*TypeParam_]Type{}
	for _, pa := range args {
		matchTypeParams(pa[0], pa[1], solved)
	}
	for q, arg := range subs {
		solved[q] = arg
	}
	line, col := nodeLineCol(n.Func)
	for q, arg := range solved {
		if q == nil || arg == nil {
			continue
		}
		mentioned := collectTypeParamsByName(&FuncType{Params: []Type{arg}})
		for name, p := range c.fnTypeParams {
			if mentioned[name] != p {
				continue
			}
			_, bare := resolveTypeVar(arg).(*TypeParam_)
			c.instEdges = append(c.instEdges, instEdge{
				from: instNode{caller, name}, to: instNode{callee, q.Name_},
				grows: !bare, arg: arg, line: line, col: col,
			})
		}
	}
}

// matchTypeParams binds each type parameter in param to the part of arg at
// the same position, walking the shapes both sides share. It solves nothing
// the checker relies on; it only reads what a call passes.
func matchTypeParams(param, arg Type, out map[*TypeParam_]Type) {
	arg = resolveTypeVar(arg)
	if param == nil || arg == nil {
		return
	}
	switch p := param.(type) {
	case *TypeParam_:
		if _, seen := out[p]; !seen {
			out[p] = arg
		}
	case *ListType:
		if a, ok := arg.(*ListType); ok {
			matchTypeParams(p.Elem, a.Elem, out)
		}
	case *MapType:
		if a, ok := arg.(*MapType); ok {
			matchTypeParams(p.Key, a.Key, out)
			matchTypeParams(p.Val, a.Val, out)
		}
	case *TupleType:
		if a, ok := arg.(*TupleType); ok && len(a.Elems) == len(p.Elems) {
			for i := range p.Elems {
				matchTypeParams(p.Elems[i], a.Elems[i], out)
			}
		}
	case *FuncType:
		if a, ok := arg.(*FuncType); ok && len(a.Params) == len(p.Params) {
			for i := range p.Params {
				matchTypeParams(p.Params[i], a.Params[i], out)
			}
			matchTypeParams(p.Return, a.Return, out)
		}
	case *EnumType:
		if a, ok := arg.(*EnumType); ok && a.Name == p.Name && len(a.TypeArgs) == len(p.TypeArgs) {
			for i := range p.TypeArgs {
				matchTypeParams(p.TypeArgs[i], a.TypeArgs[i], out)
			}
		}
	case *StructType:
		if a, ok := arg.(*StructType); ok && a.Name == p.Name && len(a.TypeArgs) == len(p.TypeArgs) {
			for i := range p.TypeArgs {
				matchTypeParams(p.TypeArgs[i], a.TypeArgs[i], out)
			}
		}
	}
}

// calleeFuncDecl is the `fn` declaration a call's callee names, or nil.
func (c *checker) calleeFuncDecl(fn ast.Node) *ast.FuncDef {
	var line, col int
	switch f := fn.(type) {
	case *ast.Ident:
		line, col = f.Line, f.Col
	case *ast.FieldAccess:
		if f.Field == nil {
			return nil
		}
		line, col = f.Field.Line, f.Field.Col
	default:
		return nil
	}
	sym := c.fa.References[Pos{Line: line, Col: col}]
	for sym != nil && sym.Resolved != nil && sym.Resolved != sym {
		sym = sym.Resolved
	}
	if sym == nil {
		return nil
	}
	fd, _ := sym.Node.(*ast.FuncDef)
	return fd
}

// checkInstantiationCycles reports each growing edge that lies on a cycle,
// once per call site.
func (c *checker) checkInstantiationCycles() {
	if len(c.instEdges) == 0 {
		return
	}
	adj := map[instNode][]instNode{}
	for _, e := range c.instEdges {
		adj[e.from] = append(adj[e.from], e.to)
	}
	comp := sccs(c.instEdges, adj)
	reported := map[[2]int]bool{}
	for _, e := range c.instEdges {
		if !e.grows || comp[e.from] != comp[e.to] {
			continue
		}
		at := [2]int{e.line, e.col}
		if reported[at] {
			continue
		}
		reported[at] = true
		c.addError(e.line, e.col, fmt.Sprintf(
			"`%s` instantiates `%s` at `%s = %s`, a type that grows on every recursive call, so it needs "+
				"infinitely many instances (polymorphic recursion); each generic function is instantiated "+
				"per type, so this is rejected: recurse at the same type parameters, or through a "+
				"non-generic helper or an interface-typed parameter",
			e.from.fn.Name, e.to.fn.Name, e.to.tp, e.arg))
	}
}

// sccs labels each node with its strongly connected component (Tarjan).
func sccs(edges []instEdge, adj map[instNode][]instNode) map[instNode]int {
	index := map[instNode]int{}
	low := map[instNode]int{}
	onStack := map[instNode]bool{}
	comp := map[instNode]int{}
	var stack []instNode
	next, label := 0, 0
	var visit func(v instNode)
	visit = func(v instNode) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = label
				if w == v {
					break
				}
			}
			label++
		}
	}
	for _, e := range edges {
		for _, v := range []instNode{e.from, e.to} {
			if _, seen := index[v]; !seen {
				visit(v)
			}
		}
	}
	return comp
}
