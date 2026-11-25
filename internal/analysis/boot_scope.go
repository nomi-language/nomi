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
// The graph holds the program's own functions. A call into std carries
// its callee's depth and chain in std's own graph, which is closed, since
// std's functions call only std's (stdBootFact): so a call of std's
// Worker.new in a test body is reported whether or not any function of the
// program calls Worker.new.
//
// No body is read here. Each file's bodies are read once per body part
// into its summary (bootSummaryOf), and the program's graph is built once
// from the summaries and kept until one of them changes (programBootGraph),
// so a file's report reads its summary and the graph. A summary is read as
// BootFacts keyed by declaration here and by DeclRef in a file's stored
// facts (bootReading), and both graphs are built and reported by the same
// code (bootGraphOf, bootGraph.report): a check and a reduction over stored
// facts (reduceBoot) differ only in their keys.
//
// The runtime guard in rt.SupervisorNewExact stays as the backstop, with the
// same message.
func CheckBootScope(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	bootMu.Lock()
	defer bootMu.Unlock()
	g, std := programBootGraph(fa)
	s := fa.bootSummaryOf()
	return g.report(BootFacts[*ast.FuncDef]{Fns: s.facts, Visits: checkReading(fa, s, std).visits(nodes, s.facts)})
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
	// facts is fns as a check's graph reads them (checkReading), once read
	// says the graph has read them.
	facts []BootFnFact[*ast.FuncDef]
	read  bool
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

// bootReading reads a file's summary as BootFacts keyed by K. key names a
// function the file's bodies call or name, and says whether it is a
// function of the program's own files; a function key does not claim is
// std's when std declares it (std, nil when std's own graph is read), and
// is named by the key otherwise.
type bootReading[K comparable] struct {
	fa  *FileAnalysis
	s   *bootSummary
	key func(*ast.FuncDef) (K, bool)
	std func(*ast.FuncDef) *FileAnalysis
}

// target is the function sym names: a project function by its key, or a
// std function by its depth and chain in std's own graph.
func (r bootReading[K]) target(sym *Symbol) BootTarget[K] {
	fd := funcDefOf(sym)
	t := BootTarget[K]{Display: bootName(sym, fd)}
	k, own := r.key(fd)
	if !own && r.std != nil && r.std(fd) != nil {
		t.Std = true
		t.StdDepth, t.StdSteps = stdBootFact(fd, r.std)
		return t
	}
	t.Fn = k
	return t
}

func (r bootReading[K]) calls(refs []bootRef) []BootCallFact[K] {
	out := make([]BootCallFact[K], 0, len(refs))
	for _, ref := range refs {
		c := BootCallFact[K]{Line: ref.call.Line, Col: ref.call.Col, Supervisor: ref.supervisor}
		if !ref.supervisor {
			c.Target = r.target(ref.sym)
		}
		out = append(out, c)
	}
	return out
}

func (r bootReading[K]) values(vs []bootValue) []BootValueFact[K] {
	out := make([]BootValueFact[K], 0, len(vs))
	for _, v := range vs {
		out = append(out, BootValueFact[K]{Line: v.pos.Line, Col: v.pos.Col, Target: r.target(v.sym)})
	}
	return out
}

// fns is each function the file defines with a body, with its calls
// outside lambdas and the project functions it calls.
func (r bootReading[K]) fns() []BootFnFact[K] {
	out := make([]BootFnFact[K], 0, len(r.s.fns))
	boots := r.fa.Program().BootFunctions
	for fd, fn := range r.s.fns {
		k, _ := r.key(fd)
		bf := BootFnFact[K]{Key: k, Entry: boots[fd], Calls: r.calls(fn.body.calls)}
		seen := map[K]bool{}
		for _, sym := range fn.called {
			if t := r.target(sym); !t.Std && !seen[t.Fn] {
				seen[t.Fn] = true
				bf.Called = append(bf.Called, t.Fn)
			}
		}
		out = append(out, bf)
	}
	return out
}

// visits is each function and test body of nodes, the file's, in the order
// its report visits them, with fns its functions.
func (r bootReading[K]) visits(nodes []ast.Node, fns []BootFnFact[K]) []BootVisit[K] {
	fnIndex := make(map[K]int, len(fns))
	for i, bf := range fns {
		fnIndex[bf.Key] = i + 1
	}
	covered := map[*ast.FuncDef]bool{}
	for _, node := range nodes {
		if td, ok := node.(*ast.TestDecl); ok {
			for _, sym := range r.s.testBootsOf(r.fa, td) {
				if fd := funcDefOf(sym); fd != nil {
					covered[fd] = true
				}
			}
		}
	}
	visit := func(body *bootBody) BootVisit[K] {
		return BootVisit[K]{Calls: r.calls(body.calls), LambdaCalls: r.calls(body.lambdaCalls), Values: r.values(body.values)}
	}
	var out []BootVisit[K]
	var walk func(items []ast.Node)
	walk = func(items []ast.Node) {
		for _, node := range items {
			switch n := node.(type) {
			case *ast.FuncDef:
				if n.Body == nil {
					continue
				}
				v := visit(r.s.bodyOf(r.fa, n, n.Body))
				v.Key, _ = r.key(n)
				if i := fnIndex[v.Key]; i > 0 && r.s.fns[n] != nil {
					v.Fn, v.Calls = i, nil
				}
				v.Main, v.Public = n.Name == "main", n.Visibility == ast.Public
				v.Boot = n.Name == "boot" || covered[n] || r.fa.Program().BootFunctions[n]
				out = append(out, v)
			case *ast.TestDecl:
				if n.Body != nil {
					v := visit(r.s.bodyOf(r.fa, n, n.Body))
					v.Test = true
					out = append(out, v)
				}
			case *ast.ImplBlock:
				walk(n.Items)
			}
		}
	}
	walk(nodes)
	return out
}

// checkReading is a check's reading of f's summary s: each function keyed
// by its declaration, f's own claimed as the program's.
func checkReading(f *FileAnalysis, s *bootSummary, std func(*ast.FuncDef) *FileAnalysis) bootReading[*ast.FuncDef] {
	return bootReading[*ast.FuncDef]{fa: f, s: s, std: std, key: func(fd *ast.FuncDef) (*ast.FuncDef, bool) {
		return fd, s.fns[fd] != nil
	}}
}

// bootFile is one file's functions as the boot-only graph reads them: its
// name, and each function it defines, keyed by K.
type bootFile[K comparable] struct {
	base string
	fns  []BootFnFact[K]
}

// bootGraph is a program's functions, keyed by K, which of them are
// boot-only and why, and which are called anywhere in the program's own
// files.
type bootGraph[K comparable] struct {
	// base is the name of each function's file, and entry holds the entry
	// boots.
	base  map[K]string
	entry map[K]bool
	// called holds every function some function or lambda of the program's
	// own files calls.
	called map[K]bool
	// depth is the length of the chain from each boot-only function to
	// Supervisor.new, and why its first call along a shortest one
	// (bootDepthsOf); a function that is not boot-only has neither.
	depth map[K]int
	why   map[K]BootCallFact[K]
}

// bootGraphOf builds the graph of files' functions, the first of each key.
// A call into std counts at its callee's depth in std's own graph.
func bootGraphOf[K comparable](files []bootFile[K]) *bootGraph[K] {
	g := &bootGraph[K]{base: map[K]string{}, entry: map[K]bool{}, called: map[K]bool{}}
	calls := map[K][]BootCallFact[K]{}
	for _, f := range files {
		for i := range f.fns {
			fn := &f.fns[i]
			if _, dup := g.base[fn.Key]; dup {
				continue
			}
			g.base[fn.Key] = f.base
			calls[fn.Key] = fn.Calls
			if fn.Entry {
				g.entry[fn.Key] = true
			}
			for _, c := range fn.Called {
				g.called[c] = true
			}
		}
	}
	g.depth, g.why = bootDepthsOf(calls, func(c BootCallFact[K], depth map[K]int) (int, bool) {
		switch {
		case c.Supervisor:
			return 0, true
		case c.Target.Std:
			return c.Target.StdDepth, c.Target.StdDepth > 0
		}
		d, ok := depth[c.Target.Fn]
		return d, ok
	})
	return g
}

// bootOnly reports whether t is a boot-only function.
func (g *bootGraph[K]) bootOnly(t BootTarget[K]) bool {
	if t.Std {
		return t.StdDepth > 0
	}
	_, ok := g.depth[t.Fn]
	return ok
}

// steps names the chain of calls from t down to its Supervisor.new, one
// step per boot-only function: "`g` at file.nomi:12", ending
// "`Supervisor.new` at file.nomi:40".
func (g *bootGraph[K]) steps(t BootTarget[K]) []string {
	if t.Std {
		return t.StdSteps
	}
	var out []string
	seen := map[K]bool{}
	for k := t.Fn; !seen[k]; {
		seen[k] = true
		c, ok := g.why[k]
		if !ok {
			break
		}
		at := fmt.Sprintf("%s:%d", g.base[k], c.Line)
		if c.Supervisor {
			out = append(out, "`Supervisor.new` at "+at)
			break
		}
		out = append(out, "`"+c.Target.Display+"` at "+at)
		if c.Target.Std {
			out = append(out, c.Target.StdSteps...)
			break
		}
		k = c.Target.Fn
	}
	return out
}

// report is the boot-only errors of the file f reads: each call a root
// makes to a boot-only function, its lambdas' included, each call any
// function's lambdas make, and each boot-only function named as a value.
// A function that is no root has its own calls reported where its callers
// make them.
func (g *bootGraph[K]) report(f BootFacts[K]) []TypeError {
	var errs []TypeError
	reported := map[Pos]bool{}
	add := func(line, col int, msg string) {
		pos := Pos{Line: line, Col: col}
		if reported[pos] {
			return
		}
		reported[pos] = true
		errs = append(errs, TypeError{Line: line, Col: col, Message: msg})
	}
	call := func(c BootCallFact[K]) {
		if c.Supervisor {
			add(c.Line, c.Col, supervisorHereMessage)
			return
		}
		if !g.bootOnly(c.Target) || !c.Target.Std && g.entry[c.Target.Fn] {
			// A test's call of an entry boot runs it as boot: the call is
			// where the app value is built (CheckAppRoots refuses one
			// outside a test).
			return
		}
		add(c.Line, c.Col, bootCallMessage(becauseText(c.Target.Display, g.steps(c.Target))))
	}
	for i := range f.Visits {
		v := &f.Visits[i]
		if v.Test || !v.Boot && (v.Main || !g.called[v.Key] && !v.Public) {
			calls := v.Calls
			if v.Fn > 0 {
				calls = f.Fns[v.Fn-1].Calls
			}
			for _, c := range calls {
				call(c)
			}
		}
		for _, c := range v.LambdaCalls {
			call(c)
		}
		for _, val := range v.Values {
			if g.bootOnly(val.Target) {
				add(val.Line, val.Col, bootValueMessage(becauseText(val.Target.Display, g.steps(val.Target))))
			}
		}
	}
	return errs
}

// bootGraphCache is a program's graph and what it was built from: the
// program's file list, which of those files are std's, the files whose
// functions it read from summaries, and those summaries.
type bootGraphCache struct {
	all   []*FileAnalysis
	isStd []bool
	std   map[*FileAnalysis]bool
	files []*FileAnalysis
	sums  []*bootSummary
	g     *bootGraph[*ast.FuncDef]
}

// programBootGraph is the graph of fa's program: its own files' functions,
// read from their summaries, with fa's among them even when fa is a std
// file (a std module checked on its own), and the program's std files, in
// which a call into std finds its callee's depth. It is built when the file
// list or a summary it was built from changes, and kept on the program
// otherwise, so the files' reports share one graph. Telling std's files
// from the program's is kept with it: every file's report asks for the
// graph, and reading each path again per report grows with the square of
// the file count.
func programBootGraph(fa *FileAnalysis) (*bootGraph[*ast.FuncDef], func(*ast.FuncDef) *FileAnalysis) {
	p := fa.Program()
	all := p.Files()
	c := p.bootGraph
	var isStd []bool
	var stdFiles map[*FileAnalysis]bool
	if c != nil && slices.Equal(c.all, all) {
		isStd, stdFiles = c.isStd, c.std
	} else {
		isStd = make([]bool, len(all))
		stdFiles = map[*FileAnalysis]bool{}
		for i, f := range all {
			if f != nil && isStdFile(f) {
				isStd[i] = true
				stdFiles[f] = true
			}
		}
	}
	std := stdFnsIn(stdFiles)
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
	sums := make([]*bootSummary, len(files))
	for i, f := range files {
		sums[i] = f.bootSummaryOf()
	}
	if c != nil && slices.Equal(c.all, all) && slices.Equal(c.files, files) && slices.Equal(c.sums, sums) {
		return c.g, std
	}
	noteBootGraph()
	read := make([]bootFile[*ast.FuncDef], len(files))
	for i, f := range files {
		s := sums[i]
		if !s.read {
			s.facts, s.read = checkReading(f, s, std).fns(), true
		}
		read[i] = bootFile[*ast.FuncDef]{base: filepath.Base(f.FilePath), fns: s.facts}
	}
	g := bootGraphOf(read)
	p.bootGraph = &bootGraphCache{all: slices.Clone(all), isStd: isStd, std: stdFiles, files: files, sums: sums, g: g}
	return g, std
}

// bootDepthsOf works out which of the functions calls holds, keyed by K,
// are boot-only. A function's depth is one more than the least depth among
// its calls' callees (calleeDepth, from the depths so far: Supervisor.new's
// is 0, and a callee not boot-only has none), to a fixed point, and its why
// is its first call in order to a callee of that least depth: the first step
// of a shortest chain to Supervisor.new. Neither depends on the order the
// functions are visited in, so a check, keyed by declaration, and the
// boot-only check over stored facts (reduceBoot), keyed by declaration name,
// report the same chain.
func bootDepthsOf[K comparable, C any](calls map[K][]C, calleeDepth func(C, map[K]int) (int, bool)) (map[K]int, map[K]C) {
	depth := map[K]int{}
	for changed := true; changed; {
		changed = false
		for k, cs := range calls {
			best := -1
			for _, c := range cs {
				if d, ok := calleeDepth(c, depth); ok && (best < 0 || d < best) {
					best = d
				}
			}
			if best < 0 {
				continue
			}
			if d, ok := depth[k]; !ok || best+1 < d {
				depth[k] = best + 1
				changed = true
			}
		}
	}
	why := make(map[K]C, len(depth))
	for k, d := range depth {
		for _, c := range calls[k] {
			if at, ok := calleeDepth(c, depth); ok && at == d-1 {
				why[k] = c
				break
			}
		}
	}
	return depth, why
}

// stdBootFns caches a std function's reading, by its declaration, read once
// per process: std's analysis is shared and never changes.
var stdBootFns sync.Map // *ast.FuncDef -> bootFile[*ast.FuncDef]

// stdBootFacts caches stdBootFact's answers: std's graph is closed, since
// std's functions call only std's, so a std function's depth and chain are
// the same in every program.
var stdBootFacts ownedCache[ast.FuncDef, stdBootAnswer]

type stdBootAnswer struct {
	depth int
	steps []string
}

// stdBootFact answers fd's depth from Supervisor.new (0 when it is not
// boot-only) and its chain's steps, in the graph of the std functions std
// declares that fd reaches.
func stdBootFact(fd *ast.FuncDef, std func(*ast.FuncDef) *FileAnalysis) (int, []string) {
	if ans, ok := stdBootFacts.load(fd); ok {
		return ans.depth, ans.steps
	}
	var files []bootFile[*ast.FuncDef]
	seen := map[*ast.FuncDef]bool{}
	queue := []*ast.FuncDef{fd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		f := std(next)
		if f == nil {
			continue
		}
		seen[next] = true
		file := stdBootFnOf(f, next)
		files = append(files, file)
		for _, c := range file.fns[0].Calls {
			if !c.Supervisor && !seen[c.Target.Fn] {
				queue = append(queue, c.Target.Fn)
			}
		}
	}
	g := bootGraphOf(files)
	ans := stdBootAnswer{depth: g.depth[fd], steps: g.steps(BootTarget[*ast.FuncDef]{Fn: fd})}
	ans = stdBootFacts.store(fd, ans)
	return ans.depth, ans.steps
}

// stdBootFnOf is the std function fd of f as std's own graph reads it: its
// first boot-only call when f takes its boot facts from the stdlib's facts
// (stdFactsBootCalls), else its calls, read from its body.
func stdBootFnOf(f *FileAnalysis, fd *ast.FuncDef) bootFile[*ast.FuncDef] {
	if cached, ok := stdBootFns.Load(fd); ok {
		return cached.(bootFile[*ast.FuncDef])
	}
	fn := BootFnFact[*ast.FuncDef]{Key: fd}
	if calls, ok := stdFactsBootCalls(f, fd); ok {
		fn.Calls = calls
	} else {
		fn.Calls = stdReading(f, nil).calls(readBootRefs(f, fd.Body, func(*Symbol) {}))
	}
	file := bootFile[*ast.FuncDef]{base: filepath.Base(f.FilePath), fns: []BootFnFact[*ast.FuncDef]{fn}}
	stdBootFns.Store(fd, file)
	return file
}

// stdReading is the reading of std's own graph, which is closed: every
// function f's bodies call is keyed by its declaration, std's included.
func stdReading(f *FileAnalysis, s *bootSummary) bootReading[*ast.FuncDef] {
	return bootReading[*ast.FuncDef]{fa: f, s: s, key: func(fd *ast.FuncDef) (*ast.FuncDef, bool) { return fd, true }}
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

// supervisorHereMessage is the error at a call of Supervisor.new a root
// makes.
const supervisorHereMessage = "a supervisor here would be a new one every time this runs, and each " +
	"is kept for the life of the program with a `max_running` of its " +
	"own — so the bound would be per call rather than per downstream. " +
	"Create it while `boot` builds the app value: in boot, or in " +
	"something boot calls"

// bootCallMessage is the error at a root's call of a boot-only function,
// because saying why it is one (becauseText).
func bootCallMessage(because string) string {
	return because + ", so it can only be called while `boot` builds " +
		"the app value, in boot or in something boot calls: a supervisor made here would be a new one every time " +
		"this runs, so the bound would be per call rather than per downstream"
}

// bootValueMessage is the error where a boot-only function is named as a
// value, because saying why it is one (becauseText).
func bootValueMessage(because string) string {
	return because + ", so it cannot be passed as a value: a function value may be called anywhere, " +
		"and a supervisor can only be created while `boot` builds the app value. Call it in boot instead"
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

// isSupervisorNewSymbol reports whether sym is std's `Supervisor.new`, by
// its declaration (StdFuncOf), and never a program type's `new`.
func isSupervisorNewSymbol(fa *FileAnalysis, sym *Symbol) bool {
	return sym != nil && sym.Kind == SymbolFunction &&
		fa.StdFuncOf(sym.Node) == StdFunc{Module: "std/supervisors", Owner: "Supervisor", Name: "new"}
}
