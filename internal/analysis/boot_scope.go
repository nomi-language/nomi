package analysis

import (
	"fmt"
	"path/filepath"
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
// The runtime guard in rt.SupervisorNewExact stays as the backstop, with the
// same message.
func CheckBootScope(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	g := newBootGraph(fa)
	r := &bootReport{g: g, fa: fa, reported: map[Pos]bool{}, covered: map[*ast.FuncDef]bool{}}
	for _, node := range nodes {
		r.seedTestBoots(node)
	}
	for _, node := range nodes {
		r.visit(node)
	}
	return r.errors
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

// bootGraph is the program's functions, which of them are boot-only and
// why, and which are called or named as values anywhere in the program's
// own files.
type bootGraph struct {
	fns map[*ast.FuncDef]*bootFn
	// why is each boot-only function's first boot-only call; absent for a
	// function that is not boot-only.
	why map[*ast.FuncDef]bootCall
	// called holds every function some function or lambda of the program's
	// own files calls.
	called map[*ast.FuncDef]bool
}

// stdBootFns caches a std function's calls, read once per process: std's
// analysis is shared and never changes.
var stdBootFns sync.Map // *ast.FuncDef -> *bootFn

func newBootGraph(fa *FileAnalysis) *bootGraph {
	g := &bootGraph{fns: map[*ast.FuncDef]*bootFn{}, why: map[*ast.FuncDef]bootCall{}, called: map[*ast.FuncDef]bool{}}
	files := fa.Program().Files()
	seen := map[*FileAnalysis]bool{}
	var std []*FileAnalysis
	add := func(f *FileAnalysis) {
		if f == nil || seen[f] {
			return
		}
		seen[f] = true
		if _, isStd := stdlibModuleForPath(f.FilePath); isStd && f != fa {
			std = append(std, f)
			return
		}
		for _, sym := range f.Definitions {
			fd, ok := sym.Node.(*ast.FuncDef)
			if !ok || sym.Kind != SymbolFunction || fd.Body == nil || g.fns[fd] != nil {
				continue
			}
			fn := &bootFn{fd: fd, fa: f, sym: sym}
			fn.calls = readBootCalls(f, fd.Body, func(callee *ast.FuncDef) { g.called[callee] = true })
			g.fns[fd] = fn
		}
	}
	add(fa)
	for _, f := range files {
		add(f)
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
			fn.calls = readBootCalls(f, fd.Body, func(*ast.FuncDef) {})
			stdBootFns.Store(fd, fn)
		}
		g.fns[fd] = fn
		for _, c := range fn.calls {
			if c.callee != nil && g.fns[c.callee] == nil {
				queue = append(queue, c.callee)
			}
		}
	}
	// Boot-only, to a fixed point: a function is boot-only through its
	// first call to Supervisor.new or to a boot-only function.
	for changed := true; changed; {
		changed = false
		for fd, fn := range g.fns {
			if _, done := g.why[fd]; done {
				continue
			}
			for _, c := range fn.calls {
				if _, bootOnly := g.why[c.callee]; c.callee == nil || bootOnly {
					g.why[fd] = c
					changed = true
					break
				}
			}
		}
	}
	return g
}

// readBootCalls is body's calls outside lambdas, each to a function the
// program declares or to Supervisor.new, resolved in f. Every call it sees,
// lambdas' included, is passed to called.
func readBootCalls(f *FileAnalysis, body ast.Node, called func(*ast.FuncDef)) []bootCall {
	var calls []bootCall
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
						calls = append(calls, bootCall{call: x, sym: sym})
					}
				} else if fd := funcDefOf(sym); fd != nil {
					called(fd)
					if !inLambda {
						calls = append(calls, bootCall{call: x, callee: fd, sym: sym})
					}
				}
			}
			return true
		})
	}
	walk(body, false)
	return calls
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

// bootReport walks one file for its roots' calls to boot-only functions and
// its boot-only function values.
type bootReport struct {
	g        *bootGraph
	fa       *FileAnalysis
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
	if td.Boot != nil {
		WalkNodes(td.Boot, func(inner ast.Node) {
			if call, isCall := inner.(*ast.Call); isCall {
				if fd := funcDefOf(resolveCalleeSym(r.fa, call.Func)); fd != nil {
					r.covered[fd] = true
				}
			}
		})
	}
	if td.Body != nil {
		for _, stmt := range td.Body.Stmts {
			r.seedTestBoots(stmt)
		}
	}
}

func (r *bootReport) visit(node ast.Node) {
	switch n := node.(type) {
	case *ast.FuncDef:
		r.function(n)
	case *ast.TestDecl:
		if n.Body != nil {
			r.root(n.Body, "")
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
	boot := fd.Name == "boot" || r.covered[fd] || r.fa.Program().BootFunctions[fd]
	isRoot := !boot && (fd.Name == "main" || (!r.g.called[fd] && !fd.Public))
	if isRoot {
		r.root(fd.Body, "")
		return
	}
	// Not a root: its own calls run where its callers do. Its lambdas
	// run wherever they are called, and its function values likewise.
	r.lambdasAndValues(fd.Body)
}

// root reports every call in body to a boot-only function, its lambdas'
// included, and the boot-only functions it names as values.
func (r *bootReport) root(body ast.Node, _ string) {
	calls := readBootCalls(r.fa, body, func(*ast.FuncDef) {})
	for _, c := range calls {
		r.reportCall(c)
	}
	r.lambdasAndValues(body)
}

// lambdasAndValues reports the calls to boot-only functions in body's
// lambdas, which run whenever they are called, and the boot-only functions
// body names as values.
func (r *bootReport) lambdasAndValues(body ast.Node) {
	callees := map[Pos]bool{}
	WalkNodes(body, func(n ast.Node) {
		switch x := n.(type) {
		case *ast.Call:
			switch f := x.Func.(type) {
			case *ast.Ident:
				callees[Pos{Line: f.Line, Col: f.Col}] = true
			case *ast.TypeIdent:
				callees[Pos{Line: f.Line, Col: f.Col}] = true
			case *ast.FieldAccess:
				if f.Field != nil {
					callees[Pos{Line: f.Field.Line, Col: f.Field.Col}] = true
				}
			}
		case *ast.Lambda:
			if x.Body != nil {
				for _, c := range readBootCalls(r.fa, x.Body, func(*ast.FuncDef) {}) {
					r.reportCall(c)
				}
			}
		case *ast.Capture:
			for _, c := range readBootCalls(r.fa, x.Body, func(*ast.FuncDef) {}) {
				r.reportCall(c)
			}
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
		ref, ok := r.fa.References[pos]
		if !ok {
			return
		}
		sym := resolvedSymbol(ref)
		fd := funcDefOf(sym)
		if fd == nil {
			return
		}
		if _, bootOnly := r.g.why[fd]; !bootOnly {
			return
		}
		r.add(pos, fmt.Sprintf("%s, so it cannot be passed as a value: a function value may be called anywhere, "+
			"and a supervisor can only be created while `boot` builds the app value. Call it in boot instead",
			r.because(bootCall{callee: fd, sym: sym})))
	})
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
	var steps []string
	seen := map[*ast.FuncDef]bool{}
	fd := c.callee
	for fd != nil && !seen[fd] {
		seen[fd] = true
		why, ok := r.g.why[fd]
		fn := r.g.fns[fd]
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
	head := "`" + bootName(c.sym, c.callee) + "` creates a supervisor"
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
