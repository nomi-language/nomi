package analysis

import (
	"path/filepath"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckConcurrentScope enforces Rule 1 of structured concurrency (spec §20):
//
//	A `Task.spawn(...)` call is legal only inside the dynamic extent of a
//	`concurrent { }` block. Reaches transitively — `Task.spawn(|| helper())`
//	where `helper` itself calls `Task.spawn` is legal as long as every chain
//	from `Task.spawn` up through the call graph bottoms out inside a
//	`concurrent` block.
//
// Mechanically the same shape as analysis/iter_sensitive.go: a per-
// function flag ("calls spawn, directly or transitively, from a position
// that is NOT inside one of this function's own `concurrent { }`
// blocks") propagates through the in-file call graph. A function
// carrying that flag must be called only from inside a `concurrent`
// block, OR from inside another such function — otherwise its `spawn`
// call cannot reach a `concurrent` ancestor and the original call site
// is rejected with diagnostic `"spawn outside concurrent block"`.
//
// Cross-file call-graph propagation is not in scope. The check is
// pessimistic about stdlib / cross-module callees: if a needs-concurrent
// function is reachable only via cross-file calls, the error fires at
// the function-internal `spawn` site (where the user can see it),
// because no in-file site demonstrably puts it inside a `concurrent`
// block. This mirrors iter_sensitive.go's reportNoCallSites path.
func CheckConcurrentScope(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	s := &concurrentScope{
		fa:              fa,
		funcBody:        make(map[*Symbol]*ast.FuncDef),
		directlyNeeds:   make(map[*Symbol]bool),
		directCalls:     make(map[*Symbol]map[*Symbol]bool),
		needsConcurrent: make(map[*Symbol]bool),
		spawnCallSites:  make(map[*Symbol][]*ast.Call),
	}

	s.collectTopLevelFuncBodies(nodes)

	s.computeClosure()
	s.validate(nodes)
	return s.errors
}

func (s *concurrentScope) collectTopLevelFuncBodies(nodes []ast.Node) {
	for _, node := range nodes {
		s.collectFuncBodies(node)
	}
}

func (s *concurrentScope) collectFuncBodies(node ast.Node) {
	switch n := node.(type) {
	case *ast.FuncDef:
		s.collectFunc(n)
	case *ast.ImplBlock:
		for _, item := range n.Items {
			s.collectFuncBodies(item)
		}
	}
}

// concurrentScope tracks per-function "needs concurrent context" state
// for Rule 1 enforcement. Mirrors iterSensitivity in shape so the two
// passes can be read side-by-side.
type concurrentScope struct {
	fa *FileAnalysis

	// funcBody maps each top-level FuncDef Symbol to its AST body.
	funcBody map[*Symbol]*ast.FuncDef

	// directlyNeeds[sym] = true if sym's body calls `spawn` at a site
	// that is NOT lexically inside one of sym's own `concurrent { }`
	// blocks. Such a call relies on sym being called from a concurrent
	// context.
	directlyNeeds map[*Symbol]bool

	// directCalls[sym] = the set of in-file function symbols that sym's
	// body calls from a position that is NOT inside sym's own
	// `concurrent { }` block. Drives the transitive-closure propagation
	// of needsConcurrent: if a callee at such a site needs concurrent
	// context, sym does too (the call would propagate the requirement
	// outward).
	directCalls map[*Symbol]map[*Symbol]bool

	// needsConcurrent is the closure: every function transitively reached
	// from an `spawn` call site that is not inside a `concurrent` block.
	needsConcurrent map[*Symbol]bool

	// spawnCallSites[sym] records every `Task.spawn(...)` call inside sym's
	// body that sits OUTSIDE any local `concurrent { }` block — these
	// are the diagnostic anchors when sym ends up reachable from a
	// non-concurrent call site.
	spawnCallSites map[*Symbol][]*ast.Call

	errors []TypeError
}

func isTaskFunctionSymbol(fa *FileAnalysis, sym *Symbol, name string) bool {
	if sym == nil || sym.Kind != SymbolFunction || sym.Name != name {
		return false
	}
	if sym.OwningType == "Task" {
		if sym.SourceFile != "" {
			return isStdTasksFile(sym.SourceFile)
		}
		if declaredOutsideStd(fa, sym, "std/tasks") {
			return false
		}
		if fa != nil {
			if byName := fa.TypeMethods["Task"]; sameSymbolDefinition(sym, byName[name]) {
				return true
			}
			if fa.ProjectImpls != nil {
				if byName := fa.ProjectImpls.TypeMethods["Task"]; sameSymbolDefinition(sym, byName[name]) {
					return true
				}
			}
		}
	}
	if sym.OwningType != "" {
		return false
	}
	if fa != nil {
		if scope := fa.StdlibModuleScopes["tasks"]; scope != nil {
			if sameSymbolDefinition(sym, scope.LookupLocal(name)) {
				return true
			}
		}
		if isStdTasksFile(fa.FilePath) && fa.ModuleScope != nil && isTasksModuleScope(fa.ModuleScope) {
			return fa.ModuleScope.LookupLocal(name) == sym
		}
	}
	if sym.SourceFile != "" {
		return isStdTasksFile(sym.SourceFile)
	}
	return false
}

// declaredOutsideStd reports that sym is declared by the file fa analyses and
// that file is not the stdlib module stdOrigin. A type's identity is
// (declaring file, name), so a user file's own `Task` or `Supervisor` and
// its functions are not the stdlib's, whatever they are called. The
// file-local tables (fa.TypeMethods) are keyed by name alone and hold the
// user's declaration there, so they cannot answer this.
func declaredOutsideStd(fa *FileAnalysis, sym *Symbol, stdOrigin string) bool {
	if fa == nil || sym == nil || fa.Definitions[sym.Pos] != sym {
		return false
	}
	origin := declaredOrigin(fa)
	return origin != OriginUnresolved && origin != stdOrigin
}

func sameSymbolDefinition(a, b *Symbol) bool {
	if a == nil || b == nil {
		return false
	}
	if a == b || a.Resolved == b {
		return true
	}
	return a.Name == b.Name &&
		a.Kind == b.Kind &&
		a.OwningType == b.OwningType &&
		a.Pos == b.Pos &&
		a.Doc == b.Doc
}

var tasksModuleFingerprint = []string{
	"spawn",
	"await",
	"spawn_all",
	"await_all",
	"outcome",
	"cancel",
}

func isTasksModuleScope(scope *Scope) bool {
	return hasAllLocal(scope, tasksModuleFingerprint)
}

func isStdTasksFile(path string) bool {
	return filepath.Base(path) == "tasks.nomi" && filepath.Base(filepath.Dir(path)) == "std"
}

// isTaskSpawnSymbol reports whether sym is one of the block-owned spawn
// functions, `Task.spawn` or `Task.spawn_all`.
//
// The batch form has to satisfy the same three rules as the single one:
// it creates block-owned tasks, so it needs an owner (Rule 1), the
// handles it returns must be consumed (Rule 2), and they must not escape
// the block (Rule 3). Treating it as a recognised producer is also what
// makes it the fix for Gap 6 — the alternative spelling,
// `Iter.map(items, |i| Task.spawn(…))`, is a shape the syntactic trace
// cannot see at all.
//
// `Supervisor.spawn_all` is deliberately absent: group-owned tasks have
// an owner that outlives the block, so none of the three rules apply.
func isTaskSpawnSymbol(fa *FileAnalysis, sym *Symbol) bool {
	return isTaskFunctionSymbol(fa, sym, "spawn") || isTaskFunctionSymbol(fa, sym, "spawn_all")
}

func resolvedSymbol(sym *Symbol) *Symbol {
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym
}

// collectFunc walks one top-level FuncDef body to populate
// directlyNeeds, directCalls, and spawnCallSites. The walk threads a
// `concurrentDepth` counter through the descent so a `ConcurrentBlock`
// boundary is recognised lexically (no extra Pos lookups).
func (s *concurrentScope) collectFunc(fn *ast.FuncDef) {
	sym := s.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil || fn.Body == nil {
		return
	}
	s.funcBody[sym] = fn
	s.walkBody(sym, fn.Body, 0)
}

// walkBody descends an AST subtree, tracking `inConcurrent` — the depth
// of `concurrent { }` blocks nested around the current position.
// `spawn` calls at depth > 0 are "covered" (legal here); at depth == 0
// they're recorded as directlyNeeds + spawnCallSites entries. Calls to
// other in-file functions at depth == 0 contribute to directCalls so
// the transitive closure can propagate needsConcurrent outward.
func (s *concurrentScope) walkBody(owner *Symbol, n ast.Node, inConcurrent int) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *ast.FuncDef:
		// A nested fn definition is its own scope — handled separately
		// when we walk the top-level declarations. Don't descend.

	case *ast.Lambda:
		// A lambda's body inherits the inConcurrent depth from its
		// surrounding context. `Task.spawn(|| body)` is a Call whose arg is
		// this Lambda; the Call case below bumps depth around the
		// callback's body when the callee resolves to `spawn`.
		if node.Body != nil {
			for _, stmt := range node.Body.Stmts {
				s.walkBody(owner, stmt, inConcurrent)
			}
		}

	case *ast.ConcurrentBlock:
		if node.Body != nil {
			for _, stmt := range node.Body.Stmts {
				s.walkBody(owner, stmt, inConcurrent+1)
			}
		}

	case *ast.Call:
		s.walkCall(owner, node, inConcurrent)

	case *ast.Block:
		for _, stmt := range node.Stmts {
			s.walkBody(owner, stmt, inConcurrent)
		}
	case *ast.ExprStmt:
		s.walkBody(owner, node.Expr, inConcurrent)
	case *ast.GroupedExpr:
		s.walkBody(owner, node.Expr, inConcurrent)
	case *ast.If:
		s.walkBody(owner, node.Cond, inConcurrent)
		s.walkBody(owner, node.Then, inConcurrent)
		s.walkBody(owner, node.Else, inConcurrent)
	case *ast.Binary:
		s.walkBody(owner, node.Left, inConcurrent)
		s.walkBody(owner, node.Right, inConcurrent)
	case *ast.Unary:
		s.walkBody(owner, node.Right, inConcurrent)
	case *ast.Binding:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.TupleDestructure:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.StructDestructure:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.MapDestructure:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.DistinctDestructure:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.PatternBinding:
		s.walkBody(owner, node.Value, inConcurrent)
		for _, e := range node.ElseNodes() {
			s.walkBody(owner, e, inConcurrent)
		}
	case *ast.FieldAccess:
		s.walkBody(owner, node.Object, inConcurrent)
	case *ast.Return:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.Break:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.ListLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, inConcurrent)
		}
	case *ast.VectorLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, inConcurrent)
		}
	case *ast.SetLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, inConcurrent)
		}
	case *ast.TupleLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, inConcurrent)
		}
	case *ast.MapLit:
		for _, entry := range node.Entries {
			s.walkBody(owner, entry.Key, inConcurrent)
			s.walkBody(owner, entry.Value, inConcurrent)
		}
	case *ast.StructLit:
		if node.Spread != nil {
			s.walkBody(owner, node.Spread, inConcurrent)
		}
		for _, f := range node.Fields {
			s.walkBody(owner, f.Value, inConcurrent)
		}
	case *ast.Case:
		s.walkBody(owner, node.Value, inConcurrent)
		for _, br := range node.Branches {
			s.walkBody(owner, br.Guard, inConcurrent)
			s.walkBody(owner, br.Body, inConcurrent)
		}
	case *ast.TryOp:
		s.walkBody(owner, node.Expr, inConcurrent)
	case *ast.Dbg:
		s.walkBody(owner, node.Expr, inConcurrent)
	case *ast.StringInterp:
		for _, part := range node.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				s.walkBody(owner, se.Expr, inConcurrent)
			}
		}
	case *ast.With:
		s.walkBody(owner, node.Value, inConcurrent)
	case *ast.Defer:
		s.walkBody(owner, node.Call, inConcurrent)
	}
}

// walkCall is the Call-specific arm of walkBody. The callee is
// resolved; if it's `spawn`, the call is either covered (inConcurrent
// > 0) or recorded as a needs-concurrent demand on owner. Either way,
// the call's arguments are descended into — `Task.spawn(|| body)`'s lambda
// body is walked at +1 depth so the body's own nested calls also count
// as inside-concurrent.
func (s *concurrentScope) walkCall(owner *Symbol, call *ast.Call, inConcurrent int) {
	callee := s.resolveSym(call.Func)
	isSpawnCall := isTaskSpawnSymbol(s.fa, callee)

	if isSpawnCall && inConcurrent == 0 {
		s.directlyNeeds[owner] = true
		s.spawnCallSites[owner] = append(s.spawnCallSites[owner], call)
	}

	// Walk callee expression (e.g., a FieldAccess `mod.fn`) for nested
	// references / nested calls.
	s.walkBody(owner, call.Func, inConcurrent)

	// Track in-file callee edge for transitive closure — at depth == 0
	// only, because deeper calls are already inside concurrent.
	if !isSpawnCall && inConcurrent == 0 {
		if callee != nil {
			if _, ok := s.funcBody[callee]; ok {
				if s.directCalls[owner] == nil {
					s.directCalls[owner] = make(map[*Symbol]bool)
				}
				s.directCalls[owner][callee] = true
			}
		}
	}

	// Walk arguments. For `Task.spawn(|| body)` the lambda body counts as
	// inside-concurrent (the goroutine runs there), so descend its
	// arguments at +1 depth when the callee is `spawn`.
	argDepth := inConcurrent
	if isSpawnCall {
		argDepth = inConcurrent + 1
	}
	for _, arg := range call.Args {
		s.walkBody(owner, arg, argDepth)
	}
}

// resolveSym mirrors the pattern from iter_sensitive.go's
// resolveIterCallbackSymbol — looks up the call's callee in
// fa.References and follows the Resolved chain.
func (s *concurrentScope) resolveSym(fn ast.Node) *Symbol {
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

// computeClosure propagates needsConcurrent: any function that calls (at
// depth-0) another needsConcurrent function is itself needsConcurrent.
// Seed from directlyNeeds.
func (s *concurrentScope) computeClosure() {
	for sym, v := range s.directlyNeeds {
		if v {
			s.needsConcurrent[sym] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for caller, callees := range s.directCalls {
			if s.needsConcurrent[caller] {
				continue
			}
			for callee := range callees {
				if s.needsConcurrent[callee] {
					s.needsConcurrent[caller] = true
					changed = true
					break
				}
			}
		}
	}
}

// validate emits one diagnostic per recorded `spawn` call site whose
// owner cannot be reached only from concurrent contexts. We classify
// each owner with recorded spawn calls as either:
//
//   - "covered": every observed in-file call site of the owner is
//     either lexically inside a `concurrent { }` block OR lexically
//     inside another needsConcurrent function's body (the chain
//     bottoms out further up); AND at least one such call site exists.
//   - "uncovered": at least one call site is in plain function-body
//     position, OR no in-file call sites exist at all (entry points
//     like `fn main` have no in-file callers).
//
// Uncovered owners surface the diagnostic at each recorded spawn call
// site. Covered owners are quiet.
func (s *concurrentScope) validate(nodes []ast.Node) {
	uncovered := s.classifyOwners(nodes)
	for owner, sites := range s.spawnCallSites {
		if !uncovered[owner] {
			continue
		}
		for _, call := range sites {
			// Name the spawn the user actually wrote — a message about
			// `spawn` for a `Task.spawn_all(...)` call reads like the
			// analyzer is describing some other line.
			spawn := "spawn"
			if callee := s.resolveSym(call.Func); isTaskFunctionSymbol(s.fa, callee, "spawn_all") {
				spawn = "spawn_all"
			}
			s.errors = append(s.errors, TypeError{
				Line:    call.Line,
				Col:     call.Col,
				Message: spawn + " outside concurrent block",
			})
		}
	}
}

// classifyOwners returns a map from owner Symbol (one with recorded
// spawn calls) to a bool: true ⇒ the owner is "uncovered" and its
// spawn calls must surface a diagnostic.
//
// Coverage is computed as a fixed point over the call graph:
//
//   - A needsConcurrent function `g` is "covered" iff at least one
//     in-file call site of `g` exists AND every in-file call site is
//     either (a) lexically inside a `concurrent { }` block, OR (b)
//     lexically inside the body of another covered needsConcurrent
//     function.
//
//   - The fixed point starts optimistically (all needsConcurrent
//     functions tentatively covered if they have ≥1 observed call
//     site) and iterates: any function that has an uncovered call
//     site through a function we've now classified as uncovered also
//     becomes uncovered.
//
// The two-pass shape — first record all call sites, then iterate
// classification — is needed because a call site through `caller`
// counts as "covered" only when `caller` itself is covered, and
// `caller`'s coverage depends on ITS call sites, possibly through
// other functions in the same cycle.
func (s *concurrentScope) classifyOwners(nodes []ast.Node) map[*Symbol]bool {
	// callSite records one in-file invocation of a needsConcurrent
	// function, classified by the immediate (enclosing, inConcurrent)
	// pair. The enclosing function's coverage status is consulted at
	// fixed-point time.
	type callSite struct {
		enclosing    *Symbol // owner of the call site's enclosing FuncDef body
		inConcurrent bool    // true if lexically inside a `concurrent { }` block
	}
	callSites := make(map[*Symbol][]callSite, len(s.needsConcurrent))
	for sym := range s.needsConcurrent {
		callSites[sym] = nil
	}

	var walk func(n ast.Node, enclosing *Symbol, inConcurrent int)
	walk = func(n ast.Node, enclosing *Symbol, inConcurrent int) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.FuncDef:
			sym := s.fa.Definitions[Pos{Line: node.Line, Col: node.Col}]
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, sym, 0)
				}
			}
		case *ast.Lambda:
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, enclosing, inConcurrent)
				}
			}
		case *ast.ConcurrentBlock:
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, enclosing, inConcurrent+1)
				}
			}
		case *ast.Call:
			if callee := s.resolveSym(node.Func); callee != nil {
				if _, tracked := callSites[callee]; tracked {
					callSites[callee] = append(callSites[callee], callSite{
						enclosing:    enclosing,
						inConcurrent: inConcurrent > 0,
					})
				}
			}
			walk(node.Func, enclosing, inConcurrent)
			argDepth := inConcurrent
			if callee := s.resolveSym(node.Func); isTaskSpawnSymbol(s.fa, callee) {
				argDepth = inConcurrent + 1
			}
			for _, arg := range node.Args {
				walk(arg, enclosing, argDepth)
			}
		case *ast.Block:
			for _, stmt := range node.Stmts {
				walk(stmt, enclosing, inConcurrent)
			}
		case *ast.ExprStmt:
			walk(node.Expr, enclosing, inConcurrent)
		case *ast.GroupedExpr:
			walk(node.Expr, enclosing, inConcurrent)
		case *ast.If:
			walk(node.Cond, enclosing, inConcurrent)
			walk(node.Then, enclosing, inConcurrent)
			walk(node.Else, enclosing, inConcurrent)
		case *ast.Binary:
			walk(node.Left, enclosing, inConcurrent)
			walk(node.Right, enclosing, inConcurrent)
		case *ast.Unary:
			walk(node.Right, enclosing, inConcurrent)
		case *ast.Binding:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.TupleDestructure:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.StructDestructure:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.MapDestructure:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.DistinctDestructure:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.PatternBinding:
			walk(node.Value, enclosing, inConcurrent)
			for _, e := range node.ElseNodes() {
				walk(e, enclosing, inConcurrent)
			}
		case *ast.FieldAccess:
			walk(node.Object, enclosing, inConcurrent)
		case *ast.Return:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.Break:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.ListLit:
			for _, item := range node.Items {
				walk(item, enclosing, inConcurrent)
			}
		case *ast.VectorLit:
			for _, item := range node.Items {
				walk(item, enclosing, inConcurrent)
			}
		case *ast.SetLit:
			for _, item := range node.Items {
				walk(item, enclosing, inConcurrent)
			}
		case *ast.TupleLit:
			for _, item := range node.Items {
				walk(item, enclosing, inConcurrent)
			}
		case *ast.MapLit:
			for _, entry := range node.Entries {
				walk(entry.Key, enclosing, inConcurrent)
				walk(entry.Value, enclosing, inConcurrent)
			}
		case *ast.StructLit:
			if node.Spread != nil {
				walk(node.Spread, enclosing, inConcurrent)
			}
			for _, f := range node.Fields {
				walk(f.Value, enclosing, inConcurrent)
			}
		case *ast.Case:
			walk(node.Value, enclosing, inConcurrent)
			for _, br := range node.Branches {
				walk(br.Guard, enclosing, inConcurrent)
				walk(br.Body, enclosing, inConcurrent)
			}
		case *ast.TryOp:
			walk(node.Expr, enclosing, inConcurrent)
		case *ast.Dbg:
			walk(node.Expr, enclosing, inConcurrent)
		case *ast.StringInterp:
			for _, part := range node.Parts {
				if se, ok := part.(ast.StringExpr); ok {
					walk(se.Expr, enclosing, inConcurrent)
				}
			}
		case *ast.With:
			walk(node.Value, enclosing, inConcurrent)
		case *ast.Defer:
			walk(node.Call, enclosing, inConcurrent)
		}
	}
	for _, node := range nodes {
		walk(node, nil, 0)
	}

	// Fixed-point coverage. Start with the optimistic assumption:
	// every needsConcurrent function with ≥1 observed call site is
	// covered. Then propagate uncoveredness: if `f` has any call site
	// whose enclosing function isn't covered AND the site isn't in a
	// concurrent block, mark `f` uncovered. Repeat until stable.
	covered := make(map[*Symbol]bool, len(callSites))
	for sym, sites := range callSites {
		covered[sym] = len(sites) > 0
	}
	changed := true
	for changed {
		changed = false
		for sym, sites := range callSites {
			if !covered[sym] {
				continue
			}
			for _, cs := range sites {
				if cs.inConcurrent {
					continue
				}
				// Site is outside concurrent — only covered if the
				// enclosing function is itself covered and is a
				// needsConcurrent function (a covered plain function
				// doesn't carry concurrent context to its own callees,
				// because Rule 1 enforcement is exactly about NOT
				// being inside a concurrent block at the leaf spawn
				// site).
				if cs.enclosing != nil && s.needsConcurrent[cs.enclosing] && covered[cs.enclosing] {
					continue
				}
				covered[sym] = false
				changed = true
				break
			}
		}
	}

	result := make(map[*Symbol]bool, len(s.spawnCallSites))
	for owner := range s.spawnCallSites {
		result[owner] = !covered[owner]
	}
	return result
}
