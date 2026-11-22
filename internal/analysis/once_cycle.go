package analysis

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckOnceCycles reports each `once` whose initializer reaches the `once`
// itself (spec §6 *`once` Bindings*), across the whole program: an
// initializer that reads its own binding, directly, through other `once`
// initializers, or through the functions it calls, would read a value that
// is still being computed. The error stands at the declaration and names
// the chain.
//
// THE GRAPH. A vertex is a `once` binding or a function with a body, of the
// program's own files (std's are left out: std imports no module of the
// program, so no cycle through a program's `once` passes through std). Its
// arcs are what its initializer or body does while it runs:
//
//   - a read of a `once`, by name or as `Type.name`, a call of a
//     function-valued `once` included;
//   - a call whose callee the checker resolved to one function: a call by
//     name, a `Type.f` or method call on a receiver of a concrete type, an
//     interface call the checker resolved to one impl (DispatchImpl), and a
//     pipe stage or `tap` naming a function;
//   - what a lambda or capture applied where it is written runs: a `then`
//     or `tap` stage's, and `(fn -> ...)()`.
//
// A lambda, capture or nested `fn` written anywhere else runs when whoever
// holds it calls it, which may be never during initialization, so its body
// adds no arc: a `once` that stores a lambda reading the `once` is no
// cycle. A call through a function value or an interface value, a call
// whose receiver is a type parameter, an operator's implementation and a
// default argument add none either. Those cycles, and any other the graph
// cannot see, are left to the run-time check (rt.OnceCell), which traps at
// the read that re-enters.
//
// Control flow is not followed: a read on a branch the initializer never
// takes during initialization counts, as Go's initialization-cycle rule
// counts it.
//
// No body is read here. Each file's bodies are read once per body part into
// its summary (onceSummaryOf), and the program's graph is built from the
// summaries and kept until one of them changes (programOnceGraph), as the
// boot-only check keeps its own. A summary is read as OnceFacts keyed by
// declaration node here and by DeclRef in a file's stored facts
// (onceDeclsOf, onceReportsOf), and both graphs are built and reported by
// the same code (onceCycleErrors, onceReport): a check and a reduction over
// stored facts (reduceOnce) differ only in their keys.
//
// An unannotated `once` whose value reads its own type is reported by type
// inference already (onceCycleMessage); this check says nothing more there.
func CheckOnceCycles(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	onceMu.Lock()
	defer onceMu.Unlock()
	g := programOnceGraph(fa)
	return onceReport(g.errs, onceReportsOf(fa.onceSummaryOf(), nodes, func(n ast.Node) (*ast.OnceBinding, bool) {
		b, ok := n.(*ast.OnceBinding)
		return b, ok
	}))
}

// onceMu guards the once-cycle check's summaries and graphs, which the
// check fills on first use.
var onceMu sync.Mutex

// onceSummary is what one file's bodies tell the once-cycle check, read
// once for its body part (FileBodies.generation).
type onceSummary struct {
	generation uint64
	// decls is each `once` with a value and each function with a body the
	// file defines, by declaration.
	decls map[ast.Node]*onceDecl
}

// onceDecl is one `once`'s initializer or one function's body.
type onceDecl struct {
	sym *Symbol
	// typeCycle says type inference reports the `once` (its value reads its
	// own type).
	typeCycle bool
	refs      []onceRef
}

// onceRef is one read of a `once` or one call of a function, at a line of
// the declaring file.
type onceRef struct {
	sym  *Symbol
	read bool
	line int
}

// onceSummaryOf is fa's summary for its current body part, read now when it
// has none.
func (fa *FileAnalysis) onceSummaryOf() *onceSummary {
	if s := fa.onces; s != nil && s.generation == fa.generation {
		return s
	}
	s := &onceSummary{generation: fa.generation, decls: map[ast.Node]*onceDecl{}}
	t := fa.Program().Onces
	for _, sym := range fa.Definitions {
		if sym == nil || s.decls[sym.Node] != nil {
			continue
		}
		switch n := sym.Node.(type) {
		case *ast.FuncDef:
			if sym.Kind == SymbolFunction && n.Body != nil {
				s.decls[n] = &onceDecl{sym: sym, refs: readOnceRefs(fa, n.Body)}
			}
		case *ast.OnceBinding:
			if sym.Kind == SymbolOnce && n.Value != nil {
				d := &onceDecl{sym: sym, refs: readOnceRefs(fa, n.Value)}
				d.typeCycle = t != nil && t.cycles[n] != nil
				s.decls[n] = d
			}
		}
	}
	fa.onces = s
	return s
}

// readOnceRefs is what body does while it runs, as the graph reads it:
// its reads of `once` bindings and its calls of functions the checker
// resolved, in walk order.
func readOnceRefs(f *FileAnalysis, body ast.Node) []onceRef {
	var refs []onceRef
	callees := map[ast.Node]bool{}
	seen := map[ast.Node]bool{}
	callee := func(n ast.Node) {
		callees[n] = true
		if fa, ok := n.(*ast.FieldAccess); ok && fa.Field != nil {
			callees[fa.Field] = true
		}
	}
	at := func(line, col int, isCallee bool) {
		sym, ok := f.References[Pos{Line: line, Col: col}]
		if !ok || sym == nil {
			return
		}
		if isCallee {
			for s := sym; s != nil; s = s.Resolved {
				if fd, ok := s.DispatchImpl.(*ast.FuncDef); ok && fd.Body != nil {
					owner := s.OwningInterface
					if s.DispatchReceiver != nil {
						owner = s.DispatchReceiver.String()
					}
					refs = append(refs, onceRef{sym: &Symbol{Name: fd.Name, Kind: SymbolFunction, Node: fd, OwningType: owner}, line: line})
					return
				}
			}
		}
		r := resolvedSymbol(sym)
		switch n := r.Node.(type) {
		case *ast.OnceBinding:
			if r.Kind == SymbolOnce {
				refs = append(refs, onceRef{sym: r, read: true, line: line})
			}
		case *ast.FuncDef:
			if isCallee && r.Kind == SymbolFunction && n.Body != nil {
				refs = append(refs, onceRef{sym: r, line: line})
			}
		}
	}
	var walk func(n ast.Node)
	// applied walks the body of a lambda or capture that runs where it is
	// written.
	applied := func(fn ast.Node) {
		switch x := fn.(type) {
		case *ast.Lambda:
			if x.Body != nil {
				walk(x.Body)
			}
		case *ast.Capture:
			walk(x.Body)
		}
	}
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(m ast.Node) bool {
			if m == nil || seen[m] {
				return false
			}
			seen[m] = true
			switch x := m.(type) {
			case *ast.Lambda, *ast.Capture, *ast.FuncDef:
				return false
			case *ast.Then:
				applied(x.Fn())
				return false
			case *ast.Tap:
				if x.Name != nil {
					callee(x.Name)
					walk(x.Name)
				} else {
					applied(x.Fn())
				}
				return false
			case *ast.Call:
				if l, ok := x.Func.(*ast.Lambda); ok {
					applied(l)
					for _, a := range x.Args {
						walk(a)
					}
					return false
				}
				callee(x.Func)
			case *ast.Binary:
				if x.Op == "|>" {
					switch x.Right.(type) {
					case *ast.Ident, *ast.FieldAccess:
						callee(x.Right)
					}
				}
			case *ast.Ident:
				at(x.Line, x.Col, callees[x])
			case *ast.TypeIdent:
				at(x.Line, x.Col, callees[x])
			case *ast.FieldAccess:
				if x.Field != nil {
					at(x.Field.Line, x.Field.Col, callees[x])
					seen[x.Field] = true
				}
			}
			return true
		})
	}
	walk(body)
	return refs
}

// onceGraph is the program's `once` bindings and functions, and the cycle
// error of each `once` on a cycle.
type onceGraph struct {
	errs map[*ast.OnceBinding]TypeError
}

// onceGraphCache is a program's graph and what it was built from.
type onceGraphCache struct {
	all   []*FileAnalysis
	isStd []bool
	files []*FileAnalysis
	sums  []*onceSummary
	g     *onceGraph
}

// programOnceGraph is the graph of fa's program: its own files' `once`
// bindings and functions, read from their summaries, with fa's among them
// even when fa is a std file (a std module checked on its own). It is built
// when the file list or a summary it was built from changes, and kept on
// the program otherwise.
func programOnceGraph(fa *FileAnalysis) *onceGraph {
	p := fa.Program()
	all := p.Files()
	c := p.onceGraph
	var isStd []bool
	if c != nil && slices.Equal(c.all, all) {
		isStd = c.isStd
	} else {
		isStd = make([]bool, len(all))
		for i, f := range all {
			if f != nil {
				_, isStd[i] = stdlibModuleForPath(f.FilePath)
			}
		}
	}
	var files []*FileAnalysis
	own := false
	for i, f := range all {
		if f == nil {
			continue
		}
		if f == fa {
			own = true
		} else if isStd[i] {
			continue
		}
		files = append(files, f)
	}
	if !own {
		files = append([]*FileAnalysis{fa}, files...)
	}
	sums := make([]*onceSummary, len(files))
	for i, f := range files {
		sums[i] = f.onceSummaryOf()
	}
	if c != nil && slices.Equal(c.all, all) && slices.Equal(c.files, files) && slices.Equal(c.sums, sums) {
		return c.g
	}
	read := make([]onceFile[ast.Node], len(files))
	for i, f := range files {
		read[i] = onceFile[ast.Node]{base: filepath.Base(f.FilePath), decls: onceDeclsOf(sums[i], nodeKey)}
	}
	g := &onceGraph{errs: map[*ast.OnceBinding]TypeError{}}
	for k, e := range onceCycleErrors(read) {
		g.errs[k.(*ast.OnceBinding)] = e
	}
	p.onceGraph = &onceGraphCache{all: slices.Clone(all), isStd: isStd, files: files, sums: sums, g: g}
	return g
}

// nodeKey keys a declaration by its node, as a check reads the reductions'
// facts: every node is its own key.
func nodeKey(n ast.Node) (ast.Node, bool) { return n, true }

// onceDisplay is how the chain names a `once` or a function: `once rate`,
// `once Money.zero`, `load`, `Money.parse`.
func onceDisplay(sym *Symbol, decl ast.Node) string {
	name := sym.Name
	switch n := decl.(type) {
	case *ast.OnceBinding:
		name = n.Name
	case *ast.FuncDef:
		name = n.Name
	}
	if sym.OwningType != "" {
		name = sym.OwningType + "." + name
	}
	if _, ok := decl.(*ast.OnceBinding); ok {
		return "once " + name
	}
	return name
}

// onceVertex is one `once` or function of the graph, keyed by K: its
// declaration in a check, its DeclRef over stored facts.
type onceVertex[K comparable] struct {
	once bool
	// name is how a chain names it, base the name of its file, and line
	// and col where a `once` is declared, where its error stands.
	name      string
	base      string
	line, col int
	// skip says type inference reports the `once` already.
	skip bool
	arcs []onceArc[K]
}

// onceArc is one read or call, at line of the source vertex's file, of
// the vertex keyed to, which a chain names name.
type onceArc[K comparable] struct {
	to   K
	read bool
	line int
	name string
}

// onceCycles answers the error of each `once` of vs that reaches itself:
// the shortest chain from it back to it, in arc order. A vertex an arc
// names that vs does not hold (a std function) ends that path.
func onceCycles[K comparable](vs map[K]*onceVertex[K]) map[K]TypeError {
	out := map[K]TypeError{}
	onCycle := onceCyclic(vs)
	for k, v := range vs {
		if !v.once || v.skip || !onCycle[k] {
			continue
		}
		chain := onceChain(vs, k)
		if chain == nil {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "cyclic 'once' binding: `%s` depends on itself: ", strings.TrimPrefix(v.name, "once "))
		from := v
		for i, arc := range chain {
			verb := "calls"
			if arc.read {
				verb = "reads"
			}
			if i == 0 {
				b.WriteString("it ")
			} else {
				b.WriteString(", which ")
			}
			fmt.Fprintf(&b, "%s `%s` (%s:%d)", verb, arc.name, from.base, arc.line)
			from = vs[arc.to]
		}
		out[k] = TypeError{Line: v.line, Col: v.col, Message: b.String()}
	}
	return out
}

// onceChain is the arcs of the shortest path from k back to k, nil when
// there is none.
func onceChain[K comparable](vs map[K]*onceVertex[K], k K) []onceArc[K] {
	type step struct {
		from K
		arc  onceArc[K]
	}
	prev := map[K]step{}
	queue := []K{k}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, arc := range vs[at].arcs {
			if vs[arc.to] == nil {
				continue
			}
			if arc.to == k {
				chain := []onceArc[K]{arc}
				for cur := at; cur != k; cur = prev[cur].from {
					chain = append(chain, prev[cur].arc)
				}
				slices.Reverse(chain)
				return chain
			}
			if _, seen := prev[arc.to]; seen {
				continue
			}
			prev[arc.to] = step{from: at, arc: arc}
			queue = append(queue, arc.to)
		}
	}
	return nil
}

// onceCyclic answers the vertices that lie on a cycle: those of a strongly
// connected component of more than one vertex, and those with an arc to
// themselves (Tarjan).
func onceCyclic[K comparable](vs map[K]*onceVertex[K]) map[K]bool {
	index := map[K]int{}
	low := map[K]int{}
	onStack := map[K]bool{}
	var stack []K
	next := 0
	out := map[K]bool{}
	var visit func(v K)
	visit = func(v K) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, arc := range vs[v].arcs {
			w := arc.to
			if vs[w] == nil {
				continue
			}
			if w == v {
				out[v] = true
			}
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []K
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				for _, w := range comp {
					out[w] = true
				}
			}
		}
	}
	// A cycle through a `once` lies in the component of that `once`, which
	// a walk from it finds.
	for k, v := range vs {
		if _, seen := index[k]; !seen && v.once {
			visit(k)
		}
	}
	return out
}

// onceFile is one file's declarations as the once-cycle graph reads them:
// its name, and each `once` and function it defines, keyed by K.
type onceFile[K comparable] struct {
	base  string
	decls []OnceDeclFact[K]
}

// onceDeclsOf is the once-cycle check's reading of the declarations s
// summarizes, each keyed by key, and each read or call of a declaration key
// names. A target key does not name (a std function, which no file of the
// program declares) is left out, as the graph leaves it out.
func onceDeclsOf[K comparable](s *onceSummary, key func(ast.Node) (K, bool)) []OnceDeclFact[K] {
	out := make([]OnceDeclFact[K], 0, len(s.decls))
	for decl, d := range s.decls {
		k, _ := key(decl)
		df := OnceDeclFact[K]{Key: k, Display: onceDisplay(d.sym, decl), TypeCycle: d.typeCycle}
		if n, ok := decl.(*ast.OnceBinding); ok {
			df.Once, df.Line, df.Col = true, n.Line, n.Col
		}
		for _, r := range d.refs {
			if r.sym == nil || r.sym.Node == nil {
				continue
			}
			to, ok := key(r.sym.Node)
			if !ok {
				continue
			}
			df.Refs = append(df.Refs, OnceRefFact[K]{Target: to, Read: r.read, Line: r.line, Display: onceDisplay(r.sym, r.sym.Node)})
		}
		out = append(out, df)
	}
	return out
}

// onceReportsOf is the `once` bindings of the file s summarizes, whose
// nodes are nodes, in the order its report visits them, keyed by key.
func onceReportsOf[K comparable](s *onceSummary, nodes []ast.Node, key func(ast.Node) (K, bool)) []K {
	var out []K
	for _, node := range nodes {
		for _, n := range moduleOnces(node) {
			if s.decls[n] != nil {
				k, _ := key(n)
				out = append(out, k)
			}
		}
	}
	return out
}

// onceCycleErrors builds the graph of files' declarations, the first of
// each key, and answers the error of each `once` on a cycle (onceCycles).
func onceCycleErrors[K comparable](files []onceFile[K]) map[K]TypeError {
	vs := map[K]*onceVertex[K]{}
	for _, f := range files {
		for _, d := range f.decls {
			if vs[d.Key] != nil {
				continue
			}
			v := &onceVertex[K]{once: d.Once, name: d.Display, base: f.base, line: d.Line, col: d.Col, skip: d.TypeCycle}
			for _, rf := range d.Refs {
				v.arcs = append(v.arcs, onceArc[K]{to: rf.Target, read: rf.Read, line: rf.Line, name: rf.Display})
			}
			vs[d.Key] = v
		}
	}
	return onceCycles(vs)
}

// onceReport is a file's once-cycle errors: those of errs at its `once`
// bindings reports, in order.
func onceReport[K comparable](errs map[K]TypeError, reports []K) []TypeError {
	var out []TypeError
	for _, k := range reports {
		if e, ok := errs[k]; ok {
			out = append(out, e)
		}
	}
	return out
}

// onceFactsOf is the once-cycle check's reading of the file with key,
// analyzed as fa with nodes, its declarations named as FileFactsOf names
// them (ownRef) and other files' by namer.
func onceFactsOf(fa *FileAnalysis, nodes []ast.Node, namer *FactsNamer, ownRef func(ast.Node) DeclRef) OnceFacts[DeclRef] {
	onceMu.Lock()
	defer onceMu.Unlock()
	s := fa.onceSummaryOf()
	key := func(node ast.Node) (DeclRef, bool) {
		if s.decls[node] != nil {
			return ownRef(node), true
		}
		return namer.ref(node)
	}
	out := OnceFacts[DeclRef]{Decls: onceDeclsOf(s, key), Reports: onceReportsOf(s, nodes, key)}
	slices.SortFunc(out.Decls, func(a, b OnceDeclFact[DeclRef]) int { return strings.Compare(a.Key.Name, b.Key.Name) })
	return out
}

// reduceOnce is CheckOnceCycles over facts.
func reduceOnce(files map[string]*FileFacts, keys []string, r *Reductions) {
	read := make([]onceFile[DeclRef], len(keys))
	for i, key := range keys {
		read[i] = onceFile[DeclRef]{base: files[key].Base, decls: files[key].Once.Decls}
	}
	errs := onceCycleErrors(read)
	for _, key := range keys {
		if out := onceReport(errs, files[key].Once.Reports); len(out) > 0 {
			r.Once[key] = out
		}
	}
}
