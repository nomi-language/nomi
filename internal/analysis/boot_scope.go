package analysis

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckBootScope enforces that supervisors are created only while `boot`
// builds the application value (spec §16 *Supervision*), across the whole
// program.
//
// A function is BOOT-ONLY when it calls `Supervisor.new`, or calls another
// boot-only function, directly or through any chain of calls, across files
// and modules, std included: std/jobs's `Worker.new` is boot-only, and so is
// an app's `make_worker` that calls it. A call to a boot-only function must
// run only while boot runs. The check finds every place code starts running
// outside boot, a ROOT, and reports each call a root makes to a boot-only
// function, at that call:
//
//   - `fn main`, and a test body;
//   - a function no function of the program calls, unless it is `pub`: a
//     private one is dead code that would create a supervisor if it ran, and
//     a public one is a library's API, which nothing here runs;
//   - a lambda or capture, which runs whenever whoever holds it calls it,
//     even one written in boot.
//
// A function a root calls is reported at the root's call, not at its own
// calls, so a chain main -> handler -> Worker.new -> Supervisor.new is one
// error, at main's call to handler, naming the chain. `boot`, and a function
// a test group's `boot` calls, is no root: what it calls runs inside boot.
//
// A boot-only function named as a value (`Supervisor.spawn(s, make_group)`,
// `f = Worker.new`) is refused where it is named: a function value may be
// called anywhere, which no check can follow.
//
// No body is read here. Each file's bodies are read once per body part
// into its summary (bootSummaryOf), and the program's graph is built once
// from the summaries and kept until one of them changes (programBootGraph),
// so a file's report reads its summary and the graph.
//
// The runtime guard in rt.SupervisorNewExact stays as the backstop, with the
// same message.
func CheckBootScope(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	bootMu.Lock()
	defer bootMu.Unlock()
	g := programBootGraph(fa)
	r := &bootReport{g: g, fa: fa, s: fa.bootSummaryOf(), reported: map[Pos]bool{}, covered: map[*ast.FuncDef]bool{}}
	for _, node := range nodes {
		r.seedTestBoots(node)
	}
	for _, node := range nodes {
		r.visit(node)
	}
	return r.errors
}

// bootMu guards the boot-only check's summaries and graphs, which the check
// fills on first use.
var bootMu sync.Mutex

// bootSummary is what one file's bodies tell the boot-only check, read
// once for its body part (FileBodies.generation): a recheck that rebuilds
// the bodies gives the file a new generation, and the summary is read
// again.
type bootSummary struct {
	generation uint64
	// fns is each function the file defines with a body, as the graph
	// reads them.
	fns map[*ast.FuncDef]*bootFnSummary
	// bodies is the calls and function values of each function or test
	// body the file's report has read, by its declaration.
	bodies map[ast.Node]*bootBody
	// testBoots is the functions each test group's boot calls, nested
	// groups' included.
	testBoots map[*ast.TestDecl][]*Symbol
}

// bootFnSummary is one function's share of its file's summary.
type bootFnSummary struct {
	sym  *Symbol
	body *bootBody
	// called is every function its body calls, its lambdas' included.
	called []*Symbol
}

// bootBody is one body's calls and function values.
type bootBody struct {
	// calls are its calls outside any lambda, in order, each to
	// Supervisor.new or to a function the program declares.
	calls []bootRef
	// lambdaCalls are the calls of each lambda and capture inside it,
	// lambda by lambda in walk order, each outside the lambdas it holds.
	lambdaCalls []bootRef
	// values are the functions it names other than as a callee, in walk
	// order.
	values []bootValue
}

// bootRef is one call, to Supervisor.new or to the function sym names.
type bootRef struct {
	call       *ast.Call
	sym        *Symbol
	supervisor bool
}

// bootValue is a function named as a value, at pos.
type bootValue struct {
	pos Pos
	sym *Symbol
}

// bootSummaryOf is fa's summary for its current body part, read now when
// it has none. It reads every function the file defines; a report reads
// the bodies of its tests on demand (bodyOf).
func (fa *FileAnalysis) bootSummaryOf() *bootSummary {
	if s := fa.boot; s != nil && s.generation == fa.generation {
		return s
	}
	s := &bootSummary{
		generation: fa.generation,
		fns:        map[*ast.FuncDef]*bootFnSummary{},
		bodies:     map[ast.Node]*bootBody{},
		testBoots:  map[*ast.TestDecl][]*Symbol{},
	}
	for _, sym := range fa.Definitions {
		fd, ok := sym.Node.(*ast.FuncDef)
		if !ok || sym.Kind != SymbolFunction || fd.Body == nil || s.fns[fd] != nil {
			continue
		}
		fn := &bootFnSummary{sym: sym}
		fn.body = readBootBody(fa, fd.Body, func(callee *Symbol) { fn.called = append(fn.called, callee) })
		s.fns[fd] = fn
		s.bodies[fd] = fn.body
	}
	fa.boot = s
	return s
}

// bodyOf is the calls and values of decl's body, a function's or a test's.
func (s *bootSummary) bodyOf(fa *FileAnalysis, decl ast.Node, body ast.Node) *bootBody {
	if b := s.bodies[decl]; b != nil {
		return b
	}
	b := readBootBody(fa, body, func(*Symbol) {})
	s.bodies[decl] = b
	return b
}

// readBootBody reads body's calls and function values in f, and passes
// every function it calls, its lambdas' calls included, to called.
func readBootBody(f *FileAnalysis, body ast.Node, called func(*Symbol)) *bootBody {
	noteBootWalk(f, body)
	b := &bootBody{calls: readBootRefs(f, body, called)}
	callees := map[Pos]bool{}
	WalkNodes(body, func(n ast.Node) {
		switch x := n.(type) {
		case *ast.Call:
			switch fn := x.Func.(type) {
			case *ast.Ident:
				callees[Pos{Line: fn.Line, Col: fn.Col}] = true
			case *ast.TypeIdent:
				callees[Pos{Line: fn.Line, Col: fn.Col}] = true
			case *ast.FieldAccess:
				if fn.Field != nil {
					callees[Pos{Line: fn.Field.Line, Col: fn.Field.Col}] = true
				}
			}
		case *ast.Lambda:
			if x.Body != nil {
				b.lambdaCalls = append(b.lambdaCalls, readBootRefs(f, x.Body, func(*Symbol) {})...)
			}
		case *ast.Capture:
			b.lambdaCalls = append(b.lambdaCalls, readBootRefs(f, x.Body, func(*Symbol) {})...)
		}
	})
	WalkNodes(body, func(n ast.Node) {
		var pos Pos
		switch x := n.(type) {
		case *ast.Ident:
			pos = Pos{Line: x.Line, Col: x.Col}
		case *ast.FieldAccess:
			if x.Field == nil {
				return
			}
			pos = Pos{Line: x.Field.Line, Col: x.Field.Col}
		default:
			return
		}
		if callees[pos] {
			return
		}
		ref, ok := f.References[pos]
		if !ok {
			return
		}
		if sym := resolvedSymbol(ref); funcDefOf(sym) != nil {
			b.values = append(b.values, bootValue{pos: pos, sym: sym})
		}
	})
	return b
}

// testBootsOf is the functions td's boot calls, and those of the test
// groups nested in it.
func (s *bootSummary) testBootsOf(fa *FileAnalysis, td *ast.TestDecl) []*Symbol {
	if syms, ok := s.testBoots[td]; ok {
		return syms
	}
	var syms []*Symbol
	if td.Boot != nil {
		WalkNodes(td.Boot, func(inner ast.Node) {
			if call, isCall := inner.(*ast.Call); isCall {
				if sym := resolveCalleeSym(fa, call.Func); funcDefOf(sym) != nil {
					syms = append(syms, sym)
				}
			}
		})
	}
	if td.Body != nil {
		for _, stmt := range td.Body.Stmts {
			if nested, ok := stmt.(*ast.TestDecl); ok {
				syms = append(syms, s.testBootsOf(fa, nested)...)
			}
		}
	}
	s.testBoots[td] = syms
	return syms
}

// bootFn is one function's calls, read once.
type bootFn struct {
	fd  *ast.FuncDef
	fa  *FileAnalysis
	sym *Symbol
	// calls are the calls in its body outside any lambda, each to a
	// function the program declares or to Supervisor.new.
	calls []bootCall
}

type bootCall struct {
	call   *ast.Call
	callee *ast.FuncDef // nil for Supervisor.new
	sym    *Symbol
}

// bootCallsOf is refs as the graph reads them: each call's callee is the
// function its symbol declares now.
func bootCallsOf(refs []bootRef) []bootCall {
	calls := make([]bootCall, 0, len(refs))
	for _, ref := range refs {
		if ref.supervisor {
			calls = append(calls, bootCall{call: ref.call, sym: ref.sym})
		} else if fd := funcDefOf(ref.sym); fd != nil {
			calls = append(calls, bootCall{call: ref.call, callee: fd, sym: ref.sym})
		}
	}
	return calls
}

// bootGraph is the program's functions, which of them are boot-only and
// why, and which are called or named as values anywhere in the program's
// own files.
type bootGraph struct {
	fns map[*ast.FuncDef]*bootFn
	// why is each boot-only function's boot-only call, and depth the length
	// of the chain from it to Supervisor.new (bootDepths); absent for a
	// function that is not boot-only.
	why   map[*ast.FuncDef]bootCall
	depth map[*ast.FuncDef]int
	// called holds every function some function or lambda of the program's
	// own files calls.
	called map[*ast.FuncDef]bool
}

// bootGraphCache is a program's graph and what it was built from: the
// program's file list, which of those files are std's, the files whose
// functions it read from summaries, and those summaries.
type bootGraphCache struct {
	all   []*FileAnalysis
	isStd []bool
	files []*FileAnalysis
	sums  []*bootSummary
	g     *bootGraph
}

// stdBootFns caches a std function's calls, read once per process: std's
// analysis is shared and never changes.
var stdBootFns sync.Map // *ast.FuncDef -> *bootFn

// programBootGraph is the graph of fa's program: its own files' functions,
// read from their summaries, with fa's among them even when fa is a std
// file (a std module checked on its own), and each std function they reach.
// It is built when the file list or a summary it was built from changes,
// and kept on the program otherwise, so the files' reports share one graph.
// Telling std's files from the program's is kept with it: every file's
// report asks for the graph, and reading each path again per report grows
// with the square of the file count.
func programBootGraph(fa *FileAnalysis) *bootGraph {
	p := fa.Program()
	all := p.Files()
	c := p.bootGraph
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
	var files, std []*FileAnalysis
	own := false
	for i, f := range all {
		if f == nil {
			continue
		}
		if f == fa {
			own = true
		} else if isStd[i] {
			std = append(std, f)
			continue
		}
		files = append(files, f)
	}
	if !own {
		files = append([]*FileAnalysis{fa}, files...)
	}
	sums := make([]*bootSummary, len(files))
	for i, f := range files {
		sums[i] = f.bootSummaryOf()
	}
	if c != nil && slices.Equal(c.all, all) && slices.Equal(c.files, files) && slices.Equal(c.sums, sums) {
		return c.g
	}
	g := newBootGraph(files, sums, std)
	p.bootGraph = &bootGraphCache{all: slices.Clone(all), isStd: isStd, files: files, sums: sums, g: g}
	return g
}

// newBootGraph builds the graph of the functions files define, from their
// summaries sums, and of each function of the std files std they reach.
func newBootGraph(files []*FileAnalysis, sums []*bootSummary, std []*FileAnalysis) *bootGraph {
	noteBootGraph()
	g := &bootGraph{fns: map[*ast.FuncDef]*bootFn{}, called: map[*ast.FuncDef]bool{}}
	for i, f := range files {
		for fd, s := range sums[i].fns {
			if g.fns[fd] != nil {
				continue
			}
			g.fns[fd] = &bootFn{fd: fd, fa: f, sym: s.sym, calls: bootCallsOf(s.body.calls)}
			for _, sym := range s.called {
				if callee := funcDefOf(sym); callee != nil {
					g.called[callee] = true
				}
			}
		}
	}
	// A std function enters the graph when the program's code reaches it,
	// with what it reaches in turn.
	stdIndex := map[*ast.FuncDef]*FileAnalysis{}
	for _, f := range std {
		for _, sym := range f.Definitions {
			if fd, ok := sym.Node.(*ast.FuncDef); ok && sym.Kind == SymbolFunction && fd.Body != nil {
				stdIndex[fd] = f
			}
		}
	}
	var queue []*ast.FuncDef
	for _, fn := range g.fns {
		for _, c := range fn.calls {
			if c.callee != nil && g.fns[c.callee] == nil {
				queue = append(queue, c.callee)
			}
		}
	}
	for len(queue) > 0 {
		fd := queue[0]
		queue = queue[1:]
		if g.fns[fd] != nil {
			continue
		}
		f := stdIndex[fd]
		if f == nil {
			continue
		}
		var fn *bootFn
		if cached, ok := stdBootFns.Load(fd); ok {
			fn = cached.(*bootFn)
		} else {
			fn = &bootFn{fd: fd, fa: f, sym: f.Definitions[Pos{Line: fd.Line, Col: fd.Col}]}
			fn.calls = bootCallsOf(readBootRefs(f, fd.Body, func(*Symbol) {}))
			stdBootFns.Store(fd, fn)
		}
		g.fns[fd] = fn
		for _, c := range fn.calls {
			if c.callee != nil && g.fns[c.callee] == nil {
				queue = append(queue, c.callee)
			}
		}
	}
	g.depth, g.why = bootDepths(g.fns)
	return g
}

// bootDepths works out which of fns are boot-only. A function's depth is
// one more than the least depth among its calls' callees, Supervisor.new's
// being 0, to a fixed point, and its why is its first call in order to a
// callee of that least depth: the first step of a shortest chain to
// Supervisor.new. Neither depends on the order the functions are visited
// in, so the boot-only check over stored facts (stored_facts.go), which
// keys functions by declaration name, reports the same chain.
func bootDepths(fns map[*ast.FuncDef]*bootFn) (map[*ast.FuncDef]int, map[*ast.FuncDef]bootCall) {
	depth := map[*ast.FuncDef]int{}
	calleeDepth := func(c bootCall) (int, bool) {
		if c.callee == nil {
			return 0, true
		}
		d, ok := depth[c.callee]
		return d, ok
	}
	for changed := true; changed; {
		changed = false
		for fd, fn := range fns {
			best := -1
			for _, c := range fn.calls {
				if d, ok := calleeDepth(c); ok && (best < 0 || d < best) {
					best = d
				}
			}
			if best < 0 {
				continue
			}
			if d, ok := depth[fd]; !ok || best+1 < d {
				depth[fd] = best + 1
				changed = true
			}
		}
	}
	why := make(map[*ast.FuncDef]bootCall, len(depth))
	for fd, d := range depth {
		for _, c := range fns[fd].calls {
			if at, ok := calleeDepth(c); ok && at == d-1 {
				why[fd] = c
				break
			}
		}
	}
	return depth, why
}

// readBootRefs is body's calls outside lambdas, each to a function the
// program declares or to Supervisor.new, resolved in f. Every function it
// sees called, lambdas' calls included, is passed to called.
func readBootRefs(f *FileAnalysis, body ast.Node, called func(*Symbol)) []bootRef {
	var refs []bootRef
	var walk func(n ast.Node, inLambda bool)
	walk = func(n ast.Node, inLambda bool) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(inner ast.Node) bool {
			switch x := inner.(type) {
			case *ast.Lambda:
				if x.Body != nil {
					walk(x.Body, true)
				}
				return false
			case *ast.Capture:
				walk(x.Body, true)
				return false
			case *ast.Call:
				sym := resolveCalleeSym(f, x.Func)
				if isSupervisorNewSymbol(f, sym) {
					if !inLambda {
						refs = append(refs, bootRef{call: x, sym: sym, supervisor: true})
					}
				} else if funcDefOf(sym) != nil {
					called(sym)
					if !inLambda {
						refs = append(refs, bootRef{call: x, sym: sym})
					}
				}
			}
			return true
		})
	}
	walk(body, false)
	return refs
}

func funcDefOf(sym *Symbol) *ast.FuncDef {
	if sym == nil || sym.Kind != SymbolFunction {
		return nil
	}
	fd, _ := sym.Node.(*ast.FuncDef)
	return fd
}

// resolveCalleeSym is the function a callee expression names in f, through
// an import.
func resolveCalleeSym(f *FileAnalysis, fn ast.Node) *Symbol {
	var pos Pos
	switch n := fn.(type) {
	case *ast.Ident:
		pos = Pos{Line: n.Line, Col: n.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: n.Line, Col: n.Col}
	case *ast.FieldAccess:
		if n.Field == nil {
			return nil
		}
		pos = Pos{Line: n.Field.Line, Col: n.Field.Col}
	default:
		return nil
	}
	sym, ok := f.References[pos]
	if !ok {
		return nil
	}
	return resolvedSymbol(sym)
}

// bootReport reports one file's roots' calls to boot-only functions and its
// boot-only function values, from its summary.
type bootReport struct {
	g        *bootGraph
	fa       *FileAnalysis
	s        *bootSummary
	covered  map[*ast.FuncDef]bool
	reported map[Pos]bool
	errors   []TypeError
}

// seedTestBoots marks each function a test group's boot calls as running
// inside boot.
func (r *bootReport) seedTestBoots(node ast.Node) {
	td, ok := node.(*ast.TestDecl)
	if !ok {
		return
	}
	for _, sym := range r.s.testBootsOf(r.fa, td) {
		if fd := funcDefOf(sym); fd != nil {
			r.covered[fd] = true
		}
	}
}

func (r *bootReport) visit(node ast.Node) {
	switch n := node.(type) {
	case *ast.FuncDef:
		r.function(n)
	case *ast.TestDecl:
		if n.Body != nil {
			r.root(r.s.bodyOf(r.fa, n, n.Body))
		}
	case *ast.StructDef:
		r.visitAll(n.Items)
	case *ast.EnumDef:
		r.visitAll(n.Items)
	case *ast.TypeDef:
		r.visitAll(n.Items)
	case *ast.ExternType:
		r.visitAll(n.Items)
	case *ast.ImplBlock:
		r.visitAll(n.Items)
	}
}

func (r *bootReport) visitAll(items []ast.Node) {
	for _, item := range items {
		r.visit(item)
	}
}

// function reports fd's calls when it is a root, and in any case its
// lambdas' calls and the boot-only functions it names as values.
func (r *bootReport) function(fd *ast.FuncDef) {
	if fd.Body == nil {
		return
	}
	body := r.s.bodyOf(r.fa, fd, fd.Body)
	boot := fd.Name == "boot" || r.covered[fd] || r.fa.Program().BootFunctions[fd]
	isRoot := !boot && (fd.Name == "main" || (!r.g.called[fd] && !fd.Public))
	if isRoot {
		r.root(body)
		return
	}
	// Not a root: its own calls run where its callers do. Its lambdas
	// run wherever they are called, and its function values likewise.
	r.lambdasAndValues(body)
}

// root reports every call in body to a boot-only function, its lambdas'
// included, and the boot-only functions it names as values.
func (r *bootReport) root(body *bootBody) {
	for _, c := range bootCallsOf(body.calls) {
		r.reportCall(c)
	}
	r.lambdasAndValues(body)
}

// lambdasAndValues reports the calls to boot-only functions in body's
// lambdas, which run whenever they are called, and the boot-only functions
// body names as values.
func (r *bootReport) lambdasAndValues(body *bootBody) {
	for _, c := range bootCallsOf(body.lambdaCalls) {
		r.reportCall(c)
	}
	for _, v := range body.values {
		fd := funcDefOf(v.sym)
		if fd == nil {
			continue
		}
		if _, bootOnly := r.g.why[fd]; !bootOnly {
			continue
		}
		r.add(v.pos, fmt.Sprintf("%s, so it cannot be passed as a value: a function value may be called anywhere, "+
			"and a supervisor can only be created while `boot` builds the app value. Call it in boot instead",
			r.because(bootCall{callee: fd, sym: v.sym})))
	}
}
func (r *bootReport) reportCall(c bootCall) {
	if c.callee == nil {
		r.add(Pos{Line: c.call.Line, Col: c.call.Col}, "a supervisor here would be a new one every time this runs, and each "+
			"is kept for the life of the program with a `max_running` of its "+
			"own — so the bound would be per call rather than per downstream. "+
			"Create it while `boot` builds the app value: in boot, or in "+
			"something boot calls")
		return
	}
	if _, bootOnly := r.g.why[c.callee]; !bootOnly {
		return
	}
	r.add(Pos{Line: c.call.Line, Col: c.call.Col}, fmt.Sprintf("%s, so it can only be called while `boot` builds "+
		"the app value, in boot or in something boot calls: a supervisor made here would be a new one every time "+
		"this runs, so the bound would be per call rather than per downstream", r.because(c)))
}

// because is "`f` creates a supervisor (...)", naming the chain of calls
// from c's callee down to its Supervisor.new.
func (r *bootReport) because(c bootCall) string {
	return becauseText(bootName(c.sym, c.callee), bootSteps(r.g, c.callee))
}

// bootSteps names the chain of calls from fd down to its Supervisor.new,
// one step per boot-only function: "`g` at file.nomi:12", ending
// "`Supervisor.new` at file.nomi:40".
func bootSteps(g *bootGraph, fd *ast.FuncDef) []string {
	var steps []string
	seen := map[*ast.FuncDef]bool{}
	for fd != nil && !seen[fd] {
		seen[fd] = true
		why, ok := g.why[fd]
		fn := g.fns[fd]
		if !ok || fn == nil {
			break
		}
		at := fmt.Sprintf("%s:%d", filepath.Base(fn.fa.FilePath), why.call.Line)
		if why.callee == nil {
			steps = append(steps, "`Supervisor.new` at "+at)
			break
		}
		steps = append(steps, "`"+bootName(why.sym, why.callee)+"` at "+at)
		fd = why.callee
	}
	return steps
}

// becauseText is the reason a call to the function named name, whose chain
// to Supervisor.new is steps, is boot-only.
func becauseText(name string, steps []string) string {
	head := "`" + name + "` creates a supervisor"
	if len(steps) == 0 {
		return head
	}
	if len(steps) == 1 {
		return head + " (" + steps[0] + ")"
	}
	return head + " (through " + strings.Join(steps[:len(steps)-1], ", ") + ", then " + steps[len(steps)-1] + ")"
}

func bootName(sym *Symbol, fd *ast.FuncDef) string {
	name := fd.Name
	if sym != nil && sym.OwningType != "" {
		return sym.OwningType + "." + name
	}
	return name
}

func (r *bootReport) add(pos Pos, msg string) {
	if r.reported[pos] {
		return
	}
	r.reported[pos] = true
	r.errors = append(r.errors, TypeError{Line: pos.Line, Col: pos.Col, Message: msg})
}

func isSupervisorNewSymbol(fa *FileAnalysis, sym *Symbol) bool {
	if sym == nil || sym.Kind != SymbolFunction || sym.Name != "new" || sym.OwningType != "Supervisor" {
		return false
	}
	if sym.SourceFile != "" {
		return isStdSupervisorsFile(sym.SourceFile)
	}
	if declaredOutsideStd(fa, sym, "std/supervisors") {
		return false
	}
	if fa != nil {
		if byName := fa.TypeMethods["Supervisor"]; sameSymbolDefinition(sym, byName["new"]) {
			return true
		}
		if fa.Program().ImplIndex != nil {
			if byName := fa.Program().ImplIndex.TypeMethods["Supervisor"]; sameSymbolDefinition(sym, byName["new"]) {
				return true
			}
		}
	}
	return false
}

func isStdSupervisorsFile(path string) bool {
	name, ok := stdlibModuleForPath(path)
	return ok && name == "supervisors"
}
