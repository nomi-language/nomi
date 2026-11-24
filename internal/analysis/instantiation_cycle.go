package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// PolymorphicRecursionCode tags a generic declaration that reaches itself
// at a type argument built from its own type parameter: a function that
// calls itself so, or a type that holds itself so. Either needs infinitely
// many instances.
const PolymorphicRecursionCode = "polymorphic-recursion"

// Polymorphic recursion: a generic declaration that reaches itself at a
// type built from its own type parameter needs an instance at T, Box<T>,
// Box<Box<T>>, … — infinitely many. Nomi instantiates every generic
// function per type-argument tuple and lays out every generic type per
// instance (spec §4 *Generics*), so the program has no finite translation
// and is rejected here rather than when it is lowered. A function does it
// by a call (`fn grow<T>(x: T) { grow(Box{inner: x}) }`), a type by a part
// (`struct Nested<T>` holding `Option<Nested<List<T>>>`). Rust ("reached
// the recursion limit while instantiating"), C++ (template instantiation
// depth) and Go ("instantiation cycle") reject it too; the rule is Go's,
// which decides it statically.
//
// THE GRAPH. A node is one type parameter of one generic declaration. A
// call from a generic body to a generic `fn` adds, for each of the
// callee's parameters Q and each of the caller's parameters P its type
// argument mentions, an edge P -> Q. A part of a generic type declaration
// (a field, a payload, an embedded type, a distinct type's inner type)
// adds the same edge for each instance of a generic type the part's type
// holds, at any depth: `Option<Nested<List<T>>>` in Nested adds T ->
// Option's T and T -> Nested's T. The edge GROWS when the argument is more
// than P itself (`Box<T>`, `(T, T)`, `List<T>`); a bare `T`, or a swap of
// parameters, does not. A cycle through a growing edge is an unbounded
// instantiation chain. Every edge of a strongly connected component lies
// on a cycle, so a growing edge whose two ends share a component is
// exactly such a cycle.
//
// A type declaration's edges are added when the checker checks the
// declaration, at the top level or in a body, and each declaration of
// the program an instance names is walked in turn, once, so a cycle
// through a type another file declares is seen whole. Only the edges of
// this file's declarations are reported here; another file's are reported
// when that file is checked. A type the walk cannot find the declaration
// of (one declared in another body, or read from an interface without its
// parts) adds no edges, and neither does a field whose type is its
// default's, so a cycle through either is accepted, and the IR builder's
// bound on nested instances (typeInstCap) refuses it instead.

// instNode is one type parameter of one generic declaration. decl is the
// declaration's identity: the `fn` of a function, and for a type the first
// of its TypeParamDefs, which the declaration shares with every instance
// of it. name is the declaration's name.
type instNode struct {
	decl any
	name string
	tp   string
}

// instEdge is one instantiation a generic declaration makes: its
// parameter `from` flows into the parameter `to` through arg. A call's
// edge is at the call's callee; a type's at at, the type of the part
// whose type holds the instance, which part names. elsewhere is set for
// the edges of a type another file declares, which this file does not
// report.
type instEdge struct {
	from, to  instNode
	grows     bool
	arg       Type
	line, col int
	at        ast.Node
	part      string
	elsewhere bool
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
				from: instNode{caller, caller.Name, name}, to: instNode{callee, callee.Name, q.Name_},
				grows: !bare, arg: arg, line: line, col: col,
			})
		}
	}
}

// recordTypeInstantiations adds the edges the generic type declaration n
// of this file makes, and those of each declaration its parts' instances
// name, transitively, each once.
func (c *checker) recordTypeInstantiations(n ast.Node) {
	name, ok := typeDeclName(n)
	if !ok {
		return
	}
	line, col := typeDeclPos(n)
	pending := []typeDeclToWalk{{node: n, decl: c.declaredStructType(Pos{Line: line, Col: col}, name)}}
	for len(pending) > 0 {
		d := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		pending = c.walkTypeDeclParts(d, pending)
	}
}

// typeDeclToWalk is a type declaration whose parts' edges are to be added:
// its node, its declared type, and whether another file declares it.
type typeDeclToWalk struct {
	node      ast.Node
	decl      Type
	elsewhere bool
}

// walkTypeDeclParts adds d's edges, and answers pending with each
// declaration they reach that is not walked yet.
func (c *checker) walkTypeDeclParts(d typeDeclToWalk, pending []typeDeclToWalk) []typeDeclToWalk {
	_, name, defs, _ := nominalInstance(d.decl)
	if len(defs) == 0 || defs[0] == nil || c.typeDeclsWalked[defs[0]] {
		return pending
	}
	if c.typeDeclsWalked == nil {
		c.typeDeclsWalked = map[*TypeParam_]bool{}
	}
	c.typeDeclsWalked[defs[0]] = true
	eachDeclPart(d.node, func(part declPart) {
		// A field whose type comes from its default writes no type for the
		// error to stand at, and is not followed.
		pt, _ := declaredPartType(d.decl, part.name)
		if pt == nil || part.te == nil {
			return
		}
		at := part.te
		walkType(pt, func(t Type) bool {
			c.typeInstSteps++
			_, toName, qs, args := nominalInstance(resolveTypeVar(t))
			if len(qs) == 0 || qs[0] == nil || len(args) != len(qs) {
				return true
			}
			for i, arg := range args {
				for _, p := range defs {
					if p == nil || !typeHoldsOutsideVars(arg, func(x Type) bool { return x == Type(p) }) {
						continue
					}
					c.instEdges = append(c.instEdges, instEdge{
						from: instNode{defs[0], name, p.Name_}, to: instNode{qs[0], toName, qs[i].Name_},
						grows: resolveTypeVar(arg) != Type(p), arg: arg, at: at, part: part.name,
						elsewhere: d.elsewhere,
					})
				}
			}
			if !c.typeDeclsWalked[qs[0]] {
				if next, ok := c.typeDeclOfInstance(t); ok {
					pending = append(pending, next)
				}
			}
			return true
		})
	})
	return pending
}

// typeDeclOfInstance is the declaration of the generic type t is an
// instance of, from its file's top level, where the program holds that
// file and the declaration is the one t's parameters come from.
func (c *checker) typeDeclOfInstance(t Type) (typeDeclToWalk, bool) {
	origin, name, defs, _ := nominalInstance(t)
	if IsBodyOrigin(origin) || origin == OriginUnresolved {
		return typeDeclToWalk{}, false
	}
	home := c.fa.fileOfOrigin(origin)
	if home == nil || home.ModuleScope == nil {
		return typeDeclToWalk{}, false
	}
	sym := home.ModuleScope.Lookup(name)
	if sym == nil {
		return typeDeclToWalk{}, false
	}
	sym = resolveRef(sym)
	if _, ok := typeDeclName(sym.Node); !ok {
		return typeDeclToWalk{}, false
	}
	if _, _, own, _ := nominalInstance(sym.Type); len(own) == 0 || own[0] != defs[0] {
		return typeDeclToWalk{}, false
	}
	return typeDeclToWalk{node: sym.Node, decl: sym.Type, elsewhere: home != c.fa}, true
}

// nominalInstance answers a struct's, enum's or distinct type's origin,
// name, type parameters and type arguments, which are the declaration's
// own parameters on the declaration itself.
func nominalInstance(t Type) (origin, name string, defs []*TypeParam_, args []Type) {
	switch n := t.(type) {
	case *StructType:
		return n.Origin, n.Name, n.TypeParamDefs, n.TypeArgs
	case *EnumType:
		return n.Origin, n.Name, n.TypeParamDefs, n.TypeArgs
	case *DistinctType:
		return n.Origin, n.Name, n.TypeParamDefs, n.TypeArgs
	}
	return "", "", nil, nil
}

// typeDeclPos is where the type declaration n names its type.
func typeDeclPos(n ast.Node) (int, int) {
	switch v := n.(type) {
	case *ast.StructDef:
		return v.Line, v.Col
	case *ast.EnumDef:
		return v.Line, v.Col
	case *ast.TypeDef:
		return v.Line, v.Col
	case *ast.ExternType:
		return v.Line, v.Col
	}
	return 0, 0
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

// checkInstantiationCycles reports each growing edge of this file that
// lies on a cycle, once per site.
func (c *checker) checkInstantiationCycles() {
	if len(c.instEdges) == 0 {
		return
	}
	adj := map[instNode][]instNode{}
	for _, e := range c.instEdges {
		adj[e.from] = append(adj[e.from], e.to)
	}
	comp := sccs(c.instEdges, adj)
	reported := map[any]bool{}
	for _, e := range c.instEdges {
		if !e.grows || e.elsewhere || comp[e.from] != comp[e.to] {
			continue
		}
		var site any = [2]int{e.line, e.col}
		if e.at != nil {
			site = e.at
		}
		if reported[site] {
			continue
		}
		reported[site] = true
		if e.at == nil {
			c.report(TypeError{Line: e.line, Col: e.col, Code: PolymorphicRecursionCode, Message: fmt.Sprintf(
				"`%s` instantiates `%s` at `%s = %s`, a type that grows on every recursive call, so it needs "+
					"infinitely many instances (polymorphic recursion); each generic function is instantiated "+
					"per type, so this is rejected: recurse at the same type parameters, or through a "+
					"non-generic helper or an interface-typed parameter",
				e.from.name, e.to.name, e.to.tp, e.arg)})
			continue
		}
		held := "`" + e.to.name + "`"
		if e.to.decl == e.from.decl {
			held = "itself"
		}
		msg := fmt.Sprintf("`%s` holds %s at `%s = %s` in `%s`", e.from.name, held, e.to.tp, e.arg, e.part)
		if path := cyclePath(e, adj, comp); len(path) > 2 {
			msg += fmt.Sprintf(", and `%s` holds `%s` again (%s)", e.to.name, e.from.name, strings.Join(path, " → "))
		}
		err := errAt(e.at, msg+fmt.Sprintf(
			", so the argument grows at every level and `%s` needs infinitely many instances "+
				"(polymorphic recursion); each generic type is laid out per type argument, so this is "+
				"rejected: hold it at its own type parameters, or at arguments that do not grow",
			e.from.name))
		err.Code = PolymorphicRecursionCode
		c.report(err)
	}
}

// cyclePath names the declarations on a shortest path from e's
// declaration through e and back, the first and the last the same, with
// each declaration named once where the path stays in it.
func cyclePath(e instEdge, adj map[instNode][]instNode, comp map[instNode]int) []string {
	prev := map[instNode]instNode{e.to: e.to}
	queue := []instNode{e.to}
	for len(queue) > 0 && !hasNode(prev, e.from) {
		v := queue[0]
		queue = queue[1:]
		for _, w := range adj[v] {
			if _, seen := prev[w]; !seen && comp[w] == comp[e.from] {
				prev[w] = v
				queue = append(queue, w)
			}
		}
	}
	if !hasNode(prev, e.from) {
		return nil
	}
	var back []instNode
	for v := e.from; v != e.to; v = prev[v] {
		back = append(back, v)
	}
	names := []string{e.from.name}
	add := func(v instNode) {
		if names[len(names)-1] != v.name {
			names = append(names, v.name)
		}
	}
	add(e.to)
	for i := len(back) - 1; i >= 0; i-- {
		add(back[i])
	}
	return names
}

func hasNode(m map[instNode]instNode, v instNode) bool {
	_, ok := m[v]
	return ok
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
