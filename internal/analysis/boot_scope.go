package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// CheckBootScope enforces that named task groups are created while the
// app value is being constructed in `boot`.
//
//	A `Supervisor.new(...)` call is legal only inside the dynamic extent of
//	`boot`. Reaches transitively — a constructor like `Counter.start()`
//	that creates its own group is legal exactly as long as every chain
//	from that call up through the call graph bottoms out in `boot`.
//
// The requirement concerns the dynamic execution scope. A server
// type owning its own group may start itself:
//
//	pub struct Counter {
//	  fn start(): Counter {
//	    inbox = Channel.buffered<Request>(16)
//	    _ = Supervisor.new(drain: …, restart: Restart.Permanent)
//	      |> Supervisor.spawn(|| Counter.serve(inbox))
//	    Counter{inbox}
//	  }
//	}
//
// which satisfies the requirement (boot calls it) but fails the proxy
// (it is not lexically boot). Forcing the group to be built in `boot` and
// passed in cost a second app-struct field per server and made
// `Restart.Permanent` unusable, since one shared group cannot be
// permanent for some of its work and not the rest.
//
// Mechanically this is analysis/concurrent_scope.go's shape with `boot`
// as the covering context instead of a `concurrent { }` block: a
// per-function flag ("creates a group, directly or transitively")
// propagates through the in-file call graph, and every call site of a
// flagged function must sit inside `boot` or inside another flagged
// function.
//
// Reported at the offending **call site**, which is where the mistake is.
// A constructor called from both `boot` and a request handler is an error
// at the handler alone; the constructor itself is fine.
//
// Cross-file call-graph propagation is not in scope, matching
// concurrent_scope. A flagged function reachable only through cross-module
// calls is not provably outside boot here, so it is left to the runtime
// guard in `rt.SupervisorNewExact` — the same division of labour Rule 1
// already uses for `Task.spawn`.
func CheckBootScope(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	s := &bootScope{
		fa:           fa,
		directCalls:  make(map[*Symbol]map[*Symbol]bool),
		createsGroup: make(map[*Symbol]bool),
		newSites:     make(map[*Symbol][]*ast.Call),
	}
	for _, node := range nodes {
		s.collect(node)
	}
	s.computeClosure()
	s.validate(nodes)
	return s.errors
}

type bootScope struct {
	fa *FileAnalysis

	// directCalls is the in-file call graph: caller -> callees.
	directCalls map[*Symbol]map[*Symbol]bool

	// createsGroup marks functions that call Supervisor.new directly or
	// transitively.
	createsGroup map[*Symbol]bool

	// newSites are the Supervisor.new calls written in each function, used
	// as the diagnostic anchor when a flagged function has no in-file
	// call site to blame.
	newSites map[*Symbol][]*ast.Call

	// bootSym is `boot` itself — the one function allowed to create groups
	// with no further justification.
	bootSym *Symbol

	errors []TypeError
}

func (s *bootScope) collect(node ast.Node) {
	switch n := node.(type) {
	case *ast.FuncDef:
		sym := s.fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
		if sym == nil {
			return
		}
		if n.Name == "boot" {
			s.bootSym = sym
		}
		s.walkBody(sym, n.Body)
	case *ast.StructDef:
		for _, item := range n.Items {
			s.collect(item)
		}
	case *ast.EnumDef:
		for _, item := range n.Items {
			s.collect(item)
		}
	case *ast.TypeDef:
		for _, item := range n.Items {
			s.collect(item)
		}
	case *ast.ExternType:
		for _, item := range n.Items {
			s.collect(item)
		}
	case *ast.ImplBlock:
		for _, item := range n.Items {
			s.collect(item)
		}
	}
}

// walkBody records this function's Supervisor.new sites and its callees.
func (s *bootScope) walkBody(owner *Symbol, body ast.Node) {
	WalkNodes(body, func(n ast.Node) {
		call, ok := n.(*ast.Call)
		if !ok {
			return
		}
		callee := s.resolveSym(call.Func)
		if isSupervisorNewSymbol(s.fa, callee) {
			s.createsGroup[owner] = true
			s.newSites[owner] = append(s.newSites[owner], call)
			return
		}
		if callee != nil {
			if s.directCalls[owner] == nil {
				s.directCalls[owner] = make(map[*Symbol]bool)
			}
			s.directCalls[owner][callee] = true
		}
	})
}

func (s *bootScope) computeClosure() {
	changed := true
	for changed {
		changed = false
		for caller, callees := range s.directCalls {
			if s.createsGroup[caller] {
				continue
			}
			for callee := range callees {
				if s.createsGroup[callee] {
					s.createsGroup[caller] = true
					changed = true
					break
				}
			}
		}
	}
}

// validate reports every call to a group-creating function from somewhere
// that is not itself inside boot's extent.
func (s *bootScope) validate(nodes []ast.Node) {
	// Which functions are legitimately allowed to create groups: boot, and
	// anything reachable from it. Computed by fixed point so a chain
	// boot -> a -> b is covered at every step.
	covered := make(map[*Symbol]bool)
	if s.bootSym != nil {
		covered[s.bootSym] = true
	}
	// A `tests "…" { boot … }` expression constructs the app value for the
	// tests below it, so it is boot for this rule. Anything it calls is
	// covered, exactly as if `fn boot` had called it — otherwise a server
	// that starts itself would be untestable.
	for _, node := range nodes {
		s.seedFromTestBoot(node, covered)
	}
	changed := true
	for changed {
		changed = false
		for caller, callees := range s.directCalls {
			if !covered[caller] {
				continue
			}
			for callee := range callees {
				if s.createsGroup[callee] && !covered[callee] {
					covered[callee] = true
					changed = true
				}
			}
		}
	}

	// Any call to a group-creating function from an uncovered function is
	// the error, anchored where it was written.
	reported := make(map[Pos]bool)
	for _, node := range nodes {
		s.reportBadCalls(node, nil, covered, reported)
	}

	// A group-creating function with no in-file call site at all cannot be
	// shown to run under boot. Anchor at its own Supervisor.new so the user
	// has a line to look at, matching concurrent_scope's fallback.
	for owner, sites := range s.newSites {
		if covered[owner] || owner == s.bootSym {
			continue
		}
		if s.hasInFileCaller(owner) {
			continue
		}
		for _, call := range sites {
			s.addError(call, reported)
		}
	}
}

// seedFromTestBoot marks every function a test boot expression calls as
// allowed to create groups.
func (s *bootScope) seedFromTestBoot(node ast.Node, covered map[*Symbol]bool) {
	switch n := node.(type) {
	case *ast.TestDecl:
		if n.Boot != nil {
			WalkNodes(n.Boot, func(inner ast.Node) {
				if call, ok := inner.(*ast.Call); ok {
					if callee := s.resolveSym(call.Func); callee != nil {
						covered[callee] = true
					}
				}
			})
		}
		if n.Body != nil {
			for _, stmt := range n.Body.Stmts {
				s.seedFromTestBoot(stmt, covered)
			}
		}
	}
}

func (s *bootScope) hasInFileCaller(target *Symbol) bool {
	for _, callees := range s.directCalls {
		if callees[target] {
			return true
		}
	}
	return false
}

func (s *bootScope) reportBadCalls(node ast.Node, enclosing *Symbol, covered map[*Symbol]bool, reported map[Pos]bool) {
	switch n := node.(type) {
	case *ast.FuncDef:
		sym := s.fa.Definitions[Pos{Line: n.Line, Col: n.Col}]
		WalkNodes(n.Body, func(inner ast.Node) {
			call, ok := inner.(*ast.Call)
			if !ok {
				return
			}
			if covered[sym] || sym == s.bootSym {
				return
			}
			callee := s.resolveSym(call.Func)
			switch {
			case isSupervisorNewSymbol(s.fa, callee):
				// A group created here, in a function boot never reaches.
				s.addError(call, reported)
			case callee != nil && s.createsGroup[callee] && covered[callee]:
				// Calling a constructor that IS legitimately used from
				// boot, from somewhere that is not. `Counter.start()` in a
				// request handler is the case this exists for.
				//
				// Only when the callee is covered: if it is not, its own
				// `Supervisor.new` is reported instead, so a chain
				// a -> b -> new yields one error at the creation rather
				// than one per link.
				s.addError(call, reported)
			}
		})
	case *ast.StructDef:
		for _, item := range n.Items {
			s.reportBadCalls(item, enclosing, covered, reported)
		}
	case *ast.EnumDef:
		for _, item := range n.Items {
			s.reportBadCalls(item, enclosing, covered, reported)
		}
	case *ast.TypeDef:
		for _, item := range n.Items {
			s.reportBadCalls(item, enclosing, covered, reported)
		}
	case *ast.ExternType:
		for _, item := range n.Items {
			s.reportBadCalls(item, enclosing, covered, reported)
		}
	case *ast.ImplBlock:
		for _, item := range n.Items {
			s.reportBadCalls(item, enclosing, covered, reported)
		}
	}
}

func (s *bootScope) addError(call *ast.Call, reported map[Pos]bool) {
	pos := Pos{Line: call.Line, Col: call.Col}
	if reported[pos] {
		return
	}
	reported[pos] = true
	s.errors = append(s.errors, TypeError{
		Line: call.Line,
		Col:  call.Col,
		Message: "a supervisor here would be a new one every time this runs, and each " +
			"is kept for the life of the program with a `max_running` of its " +
			"own — so the bound would be per call rather than per downstream. " +
			"Create it while `boot` builds the app value: in boot, or in " +
			"something boot calls",
	})
}

func (s *bootScope) resolveSym(fn ast.Node) *Symbol {
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
	sym, ok := s.fa.References[pos]
	if !ok {
		return nil
	}
	return resolvedSymbol(sym)
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
		if fa.ProjectImpls != nil {
			if byName := fa.ProjectImpls.TypeMethods["Supervisor"]; sameSymbolDefinition(sym, byName["new"]) {
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
