package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Polymorphic recursion: a generic function that reaches itself at a type
// built from its own type parameter (`fn grow<T>(x: T) { grow(Box{inner: x}) }`)
// needs an instance at T, Box<T>, Box<Box<T>>, … — infinitely many. Nomi
// instantiates every generic function per type-argument tuple (spec §12), so
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

// selfCallAtOwnParams reports whether a generic call whose unification
// solved nothing is a call at the enclosing declaration's own type
// parameters: every type parameter of ft is the caller's own, and each
// argument passes it at itself (`walk(rest)` with `rest: List<T>` inside
// `fn walk<T>`). A parameter an argument passes at anything else
// (`f((x, x))`) or nothing passes answers false.
func (c *checker) selfCallAtOwnParams(ft *FuncType, args [][2]Type) bool {
	own := collectTypeParamsByName(ft)
	if len(own) == 0 || c.containsForeignTypeParam(ft) {
		return false
	}
	solved := map[*TypeParam_]Type{}
	for _, pa := range args {
		matchTypeParams(pa[0], pa[1], solved)
	}
	for _, p := range own {
		if arg, seen := solved[p]; !seen || resolveTypeVar(arg) != Type(p) {
			return false
		}
	}
	return true
}

// matchTypeParams binds each type parameter in param to the part of arg at
// the same position, walking the shapes both sides share
// (pairedTypeChildren). It solves nothing the checker relies on; it only
// reads what a call passes, or what an instance put where its declaration
// has a parameter.
func matchTypeParams(param, arg Type, out map[*TypeParam_]Type) {
	matchTypeParamsDepth(param, arg, out, 0)
}

func matchTypeParamsDepth(param, arg Type, out map[*TypeParam_]Type, depth int) {
	param, arg = resolveTypeVar(param), resolveTypeVar(arg)
	if param == nil || arg == nil || depth > maxTypeWalkDepth {
		return
	}
	if p, ok := param.(*TypeParam_); ok {
		if _, seen := out[p]; !seen {
			out[p] = arg
		}
		return
	}
	ps, as, _ := pairedTypeChildren(param, arg)
	for i := range ps {
		matchTypeParamsDepth(ps[i], as[i], out, depth+1)
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
