package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// iterCallbackSlots maps "Owner.function" / "module.function" / "Type.method" to the set of
// 0-based argument indices that are designated iter-callback positions.
// Lambdas and iter-sensitive functions passed in these positions have their
// break/continue caught by the host HOF, so they are legal. Other call
// spellings (selective-import bare names, imported-owner aliases) resolve through
// the symbol path — see slotsForCall / resolveIterCallbackSyms.
//
// NOT included (explicit comparators, non-iter HOFs):
//   - Iter.sort_with: the comparator is NOT an iter-callback; `break` in a
//     comparator has no well-defined semantics. (Iter.sort / Iter.sort_by take
//     no callback or a key projection, not a comparator.)
//
// This list is hardcoded because parameter roles are not yet discoverable from
// signatures. Once they are, the list should be derived from the stdlib rather
// than maintained by hand.
var iterCallbackSlots = map[string]map[int]bool{
	// `loop` is an Iter type function that threads state rather than a
	// collection input.
	"Iter.loop": {0: true},

	// Terminals: Iter owner functions that consume an iterator to a value.
	// The callback-bearing ones are listed here.
	"Iter.reduce": {1: true},
	"Iter.each":   {1: true},
	"Iter.find":   {1: true},
	"Iter.any?":   {1: true},
	"Iter.all?":   {1: true},

	// Lazy adapters: Iter owner functions, separate from concrete
	// collection-specific methods.
	"Iter.iterate":    {1: true},
	"Iter.map":        {1: true},
	"Iter.filter":     {1: true},
	"Iter.take_while": {1: true},
	"Iter.drop_while": {1: true},
	"Iter.flat_map":   {1: true},

	// Container-specific callback-bearing methods that survive on the type
	// (Map's value/key transforms). Every *generic* terminal/adapter is now an
	// `iter.*` module function above — there are no `lists.reduce` / `List.find` /
	// `maps.reduce` callback spellings to list (those dissolved into `iter.*`
	// when the Iter became the pure protocol).
	"Map.map_values": {1: true},
	"Map.map_keys":   {1: true},
}

// callSiteKey identifies a Call by its callee syntax as one of:
//   - "module.name" for FieldAccess{Object=Ident("module"), Field=...}
//   - "name" for bare idents (never matches the table — every key is
//     qualified; bare calls like selective-import `loop(...)` are
//     recognised via the symbol path in slotsForCall instead)
//
// It mirrors what iterCallbackSlots indexes, so direct lookup just works.
func callSiteKey(call *ast.Call) string {
	switch callee := call.Func.(type) {
	case *ast.Ident:
		return callee.Name
	case *ast.FieldAccess:
		if callee.Field == nil {
			return ""
		}
		switch obj := callee.Object.(type) {
		case *ast.Ident:
			return obj.Name + "." + callee.Field.Name
		case *ast.TypeIdent:
			return obj.Name + "." + callee.Field.Name
		}
	}
	return ""
}

// slotsForCall returns the iter-callback slot set for the given Call, by:
//  1. Looking up the syntactic "module.name" key in iterCallbackSlots
//     (handles the normal user-code path);
//  2. Resolving the callee to a Symbol and matching its owner and name
//     against the table (handles selective-import bare names like
//     `import std/iter.Iter.{loop}` → `loop(...)`, and unqualified calls
//     inside the stdlib module that defines the iter-callback function);
//  3. Canonicalizing an imported owner alias (`import std/iter as it` →
//     `it.map(...)`) back to its canonical "Owner.name" key. Inherent
//     type-body methods (List.reduce et al.) have no module-scope symbol
//     and no Reference at the field position, so the symbol path in (2)
//     can't see them — but the alias's module symbol carries the
//     module's Scope, which pointer-matches StdlibModuleScopes.
//
// Returns nil when the call is not to any known iter-callback function.
func (s *iterSensitivity) slotsForCall(call *ast.Call) map[int]bool {
	if slots, ok := iterCallbackSlots[callSiteKey(call)]; ok {
		return slots
	}
	if sym := s.resolveIterCallbackSymbol(call.Func); sym != nil {
		if slots := s.slotsForStdlibCallbackSymbol(sym); slots != nil {
			return slots
		}
	}
	if key := s.canonicalQualifiedOwnerKey(call); key != "" {
		if slots, ok := iterCallbackSlots[key]; ok {
			return slots
		}
	}
	if key := s.canonicalStdlibKey(call); key != "" {
		if slots, ok := iterCallbackSlots[key]; ok {
			return slots
		}
	}
	return nil
}

func (s *iterSensitivity) slotsForStdlibCallbackSymbol(sym *Symbol) map[int]bool {
	if sym == nil || s.fa == nil {
		return nil
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	for key, slots := range iterCallbackSlots {
		dot := strings.IndexByte(key, '.')
		if dot < 0 {
			continue
		}
		modName, funcName := key[:dot], key[dot+1:]
		if real.Name != funcName {
			continue
		}
		if real.OwningType == modName {
			return slots
		}
		scope := s.fa.StdlibModuleScopes[modName]
		if scope == nil {
			continue
		}
		candidate := scope.LookupLocal(funcName)
		if candidate == nil {
			continue
		}
		if candidate.Resolved != nil {
			candidate = candidate.Resolved
		}
		if candidate == real {
			return slots
		}
	}
	return nil
}

// canonicalQualifiedOwnerKey rewrites an aliased module/interface owner
// (`import std/iter as it` → `it.map`) to its canonical owner key
// (`Iter.map`) by resolving the object ident and reading the real symbol name.
func (s *iterSensitivity) canonicalQualifiedOwnerKey(call *ast.Call) string {
	fieldAcc, ok := call.Func.(*ast.FieldAccess)
	if !ok || fieldAcc.Field == nil {
		return ""
	}
	var pos Pos
	switch obj := fieldAcc.Object.(type) {
	case *ast.Ident:
		pos = Pos{Line: obj.Line, Col: obj.Col}
	case *ast.TypeIdent:
		pos = Pos{Line: obj.Line, Col: obj.Col}
	default:
		return ""
	}
	sym, ok := s.fa.References[pos]
	if !ok {
		if s.fa.ModuleScope == nil {
			return ""
		}
		sym = s.fa.ModuleScope.Lookup(objNameFromFieldAccess(fieldAcc))
		if sym == nil {
			return ""
		}
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real.ModuleScope == nil || real.Name == "" {
		return ""
	}
	switch real.Kind {
	case SymbolModule, SymbolInterface:
		return real.Name + "." + fieldAcc.Field.Name
	default:
		return ""
	}
}

// canonicalStdlibKey rewrites an aliased owner-qualified callee
// (`it.map` under `import std/iter as it`) to its canonical "Owner.field"
// key by resolving the object ident to its module symbol and pointer-
// matching the symbol's ModuleScope against the file's stdlib module
// scopes. Returns "" when the callee isn't an owner-qualified field
// access whose object resolves to a stdlib owner.
func (s *iterSensitivity) canonicalStdlibKey(call *ast.Call) string {
	fieldAcc, ok := call.Func.(*ast.FieldAccess)
	if !ok || fieldAcc.Field == nil {
		return ""
	}
	objName := objNameFromFieldAccess(fieldAcc)
	if objName == "" {
		return ""
	}
	var objPos Pos
	switch obj := fieldAcc.Object.(type) {
	case *ast.Ident:
		objPos = Pos{Line: obj.Line, Col: obj.Col}
	case *ast.TypeIdent:
		objPos = Pos{Line: obj.Line, Col: obj.Col}
	}
	sym, ok := s.fa.References[objPos]
	if !ok {
		if s.fa.ModuleScope == nil {
			return ""
		}
		sym = s.fa.ModuleScope.Lookup(objName)
		if sym == nil {
			return ""
		}
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real.ModuleScope == nil && s.fa.ModuleScope != nil {
		if local := s.fa.ModuleScope.Lookup(objName); local != nil {
			localReal := local
			if localReal.Resolved != nil {
				localReal = localReal.Resolved
			}
			if localReal.ModuleScope != nil {
				real = localReal
			}
		}
	}
	if real.Kind != SymbolModule || real.ModuleScope == nil {
		return ""
	}
	for modName, scope := range s.fa.StdlibModuleScopes {
		if scope == real.ModuleScope {
			return modName + "." + fieldAcc.Field.Name
		}
	}
	return ""
}

func objNameFromFieldAccess(fieldAcc *ast.FieldAccess) string {
	switch obj := fieldAcc.Object.(type) {
	case *ast.Ident:
		return obj.Name
	case *ast.TypeIdent:
		return obj.Name
	default:
		return ""
	}
}

// resolveIterCallbackSymbol returns the real (Resolved-chain-followed) Symbol
// referenced by the call's callee expression, or nil if the expression is not
// a simple reference.
func (s *iterSensitivity) resolveIterCallbackSymbol(fn ast.Node) *Symbol {
	var pos Pos
	switch n := fn.(type) {
	case *ast.Ident:
		pos = Pos{Line: n.Line, Col: n.Col}
		if sym, ok := s.fa.References[pos]; ok {
			real := sym
			if real.Resolved != nil {
				real = real.Resolved
			}
			return real
		}
		if s.fa.ModuleScope != nil {
			return s.fa.ModuleScope.LookupLocal(n.Name)
		}
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
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	return real
}

// hasAllLocal reports whether scope defines (locally) every name in names as
// a function symbol.
func hasAllLocal(scope *Scope, names []string) bool {
	if scope == nil {
		return false
	}
	for _, n := range names {
		sym := scope.LookupLocal(n)
		if sym == nil {
			return false
		}
		real := sym
		if real.Resolved != nil {
			real = real.Resolved
		}
		if real.Kind != SymbolFunction {
			return false
		}
	}
	return true
}

// iterSensitivity tracks per-function iter-sensitivity state through the
// analysis. Keyed by the definition symbol (the FuncDef's Pos-indexed entry
// in FileAnalysis.Definitions).
type iterSensitivity struct {
	fa *FileAnalysis

	// funcSyms lists every top-level FuncDef/impl/extend method symbol we're
	// analyzing in this file, in definition order.
	funcSyms []*Symbol

	// funcBody maps each func symbol to its FuncDef node (the body source).
	funcBody map[*Symbol]*ast.FuncDef

	// directBC[sym] = true if sym's body contains break/continue at its own
	// function boundary (not buried inside a nested Lambda or nested FuncDef).
	directBC map[*Symbol]bool

	// directCalls[sym] = callee symbols called from sym's body, not buried
	// inside any Lambda or nested FuncDef. These edges drive the transitive
	// closure of iter-sensitivity.
	directCalls map[*Symbol]map[*Symbol]bool

	// sensitive[sym] = true after transitive closure.
	sensitive map[*Symbol]bool

	// errors collects diagnostics emitted by validateCallSites.
	errors []TypeError
}

// AnalyzeIterSensitivity collects iter-sensitivity facts and validates
// break/continue placement across the file. It runs after BuildTypes so that
// call-site reference symbols are already wired up.
func AnalyzeIterSensitivity(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	s := &iterSensitivity{
		fa:          fa,
		funcBody:    make(map[*Symbol]*ast.FuncDef),
		directBC:    make(map[*Symbol]bool),
		directCalls: make(map[*Symbol]map[*Symbol]bool),
		sensitive:   make(map[*Symbol]bool),
	}

	// Collect: every top-level FuncDef (and methods nested in impl/extend).
	for _, node := range nodes {
		if fn, ok := node.(*ast.FuncDef); ok {
			s.collectFunc(fn)
		}
	}

	// Transitive closure: a function is iter-sensitive if it has a direct
	// break/continue OR calls (directly, not via a caught lambda) another
	// iter-sensitive function.
	s.computeClosure()

	// Validate call sites and stray break/continue statements.
	s.validate(nodes)

	return s.errors
}

func (s *iterSensitivity) collectFunc(fn *ast.FuncDef) {
	sym := s.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil {
		return
	}
	s.funcSyms = append(s.funcSyms, sym)
	s.funcBody[sym] = fn
	if fn.Body == nil {
		return
	}
	// Walk the body at the function's own boundary. Break/continue encountered
	// here (and in nested Lambdas that are themselves NOT at iter-callback
	// positions) count as "direct" for owner — they unwind through owner's
	// frame. Lambdas directly passed at iter-callback slots are boundaries:
	// their inner break/continue are caught there and do not propagate.
	s.walkBody(sym, fn.Body, false)
}

// walkCallForSensitivity analyzes a Call's callee and args for iter-sensitivity.
// When pipeLHS is non-nil the call was reached via `lhs |> call(...)`, so the
// effective arg vector is [pipeLHS, ...call.Args] and iter-callback slot
// indices shift by +1 against call.Args.
func (s *iterSensitivity) walkCallForSensitivity(owner *Symbol, call *ast.Call, pipeLHS ast.Node, caughtHere bool) {
	if !caughtHere {
		if callee := s.resolveCalleeSym(call.Func); callee != nil {
			if s.directCalls[owner] == nil {
				s.directCalls[owner] = make(map[*Symbol]bool)
			}
			s.directCalls[owner][callee] = true
		}
	}
	s.walkBody(owner, call.Func, caughtHere)
	slots := s.slotsForCall(call)
	slotOffset := 0
	if pipeLHS != nil {
		lhsCaught := caughtHere
		if slots != nil && slots[0] {
			lhsCaught = true
		}
		s.walkBody(owner, pipeLHS, lhsCaught)
		slotOffset = 1
	}
	for i, arg := range call.Args {
		argCaught := caughtHere
		if slots != nil && slots[i+slotOffset] {
			argCaught = true
		}
		s.walkBody(owner, arg, argCaught)
	}
}

// walkBody walks `n` as part of the outer function's body. The boolean
// `caughtHere` indicates whether the current position is INSIDE an iter-
// callback boundary that would catch break/continue before they reach the
// owner's frame (e.g. inside a lambda passed at an iter-callback slot).
// When caughtHere is true we still descend to catch nested lambdas that
// sit at iter-callback positions themselves, but direct break/continue
// and edges are suppressed for the owner.
func (s *iterSensitivity) walkBody(owner *Symbol, n ast.Node, caughtHere bool) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *ast.FuncDef:
		// A nested fn definition is its own scope — handled separately when we
		// walk the top-level declarations. Don't descend.

	case *ast.Lambda:
		// A Lambda's body runs with the enclosing caughtHere flag intact: if
		// the surrounding caller placed this lambda at an iter-callback slot,
		// caughtHere will already be true (set by the Call case below).
		if node.Body != nil {
			for _, stmt := range node.Body.Stmts {
				s.walkBody(owner, stmt, caughtHere)
			}
		}

	case *ast.Break, *ast.Continue:
		if !caughtHere {
			s.directBC[owner] = true
		}
		if b, ok := node.(*ast.Break); ok && b.Value != nil {
			s.walkBody(owner, b.Value, caughtHere)
		}

	case *ast.Call:
		s.walkCallForSensitivity(owner, node, nil, caughtHere)

	case *ast.Block:
		for _, stmt := range node.Stmts {
			s.walkBody(owner, stmt, caughtHere)
		}
	case *ast.ExprStmt:
		s.walkBody(owner, node.Expr, caughtHere)
	case *ast.GroupedExpr:
		s.walkBody(owner, node.Expr, caughtHere)
	case *ast.If:
		s.walkBody(owner, node.Cond, caughtHere)
		s.walkBody(owner, node.Then, caughtHere)
		s.walkBody(owner, node.Else, caughtHere)
	case *ast.Binary:
		// `lhs |> Call(...)` desugars to Call(lhs, ...args). For iter-callback
		// slot matching to work, we analyze the Call with the LHS prepended
		// as arg 0 so slots indexed by the callee's arity line up.
		if node.Op == "|>" {
			if call, ok := node.Right.(*ast.Call); ok {
				s.walkCallForSensitivity(owner, call, node.Left, caughtHere)
				break
			}
		}
		s.walkBody(owner, node.Left, caughtHere)
		s.walkBody(owner, node.Right, caughtHere)
	case *ast.Unary:
		s.walkBody(owner, node.Right, caughtHere)
	case *ast.Binding:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.TupleDestructure:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.StructDestructure:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.MapDestructure:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.DistinctDestructure:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.PatternBinding:
		s.walkBody(owner, node.Value, caughtHere)
		for _, e := range node.ElseNodes() {
			s.walkBody(owner, e, caughtHere)
		}
	case *ast.With:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.FieldAccess:
		s.walkBody(owner, node.Object, caughtHere)
	case *ast.Return:
		s.walkBody(owner, node.Value, caughtHere)
	case *ast.ListLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, caughtHere)
		}
	case *ast.VectorLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, caughtHere)
		}
	case *ast.SetLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, caughtHere)
		}
	case *ast.TupleLit:
		for _, item := range node.Items {
			s.walkBody(owner, item, caughtHere)
		}
	case *ast.MapLit:
		for _, entry := range node.Entries {
			s.walkBody(owner, entry.Key, caughtHere)
			s.walkBody(owner, entry.Value, caughtHere)
		}
	case *ast.StructLit:
		if node.Spread != nil {
			s.walkBody(owner, node.Spread, caughtHere)
		}
		for _, f := range node.Fields {
			s.walkBody(owner, f.Value, caughtHere)
		}
	case *ast.Case:
		s.walkBody(owner, node.Value, caughtHere)
		for _, br := range node.Branches {
			s.walkBody(owner, br.Guard, caughtHere)
			s.walkBody(owner, br.Body, caughtHere)
		}
	case *ast.TryOp:
		s.walkBody(owner, node.Expr, caughtHere)
	case *ast.Dbg:
		s.walkBody(owner, node.Expr, caughtHere)
	case *ast.Assertion:
		s.walkBody(owner, node.Expr, caughtHere)
	case *ast.StringInterp:
		for _, part := range node.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				s.walkBody(owner, se.Expr, caughtHere)
			}
		}
	}
}

// resolveCalleeSym returns the top-level function Symbol that a call's Func
// node resolves to, or nil if it resolves to something else (parameter,
// binding, module, interface, primitive, stdlib, etc.).
func (s *iterSensitivity) resolveCalleeSym(fn ast.Node) *Symbol {
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
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if real.Kind != SymbolFunction {
		return nil
	}
	// Only care about functions defined in THIS file — cross-module call-graph
	// tracking would require loading all imports, which the current analysis
	// pipeline doesn't do here. Stdlib functions are treated as non-iter-
	// sensitive (their bodies' break/continue are always inside lambdas, and
	// their call-site roles are on the hardcoded iter-callback list).
	if _, ok := s.funcBody[real]; !ok {
		return nil
	}
	return real
}

func (s *iterSensitivity) computeClosure() {
	// Seed: directBC → sensitive.
	for sym, v := range s.directBC {
		if v {
			s.sensitive[sym] = true
		}
	}
	// Fixed point over directCalls edges.
	changed := true
	for changed {
		changed = false
		for caller, callees := range s.directCalls {
			if s.sensitive[caller] {
				continue
			}
			for callee := range callees {
				if s.sensitive[callee] {
					s.sensitive[caller] = true
					changed = true
					break
				}
			}
		}
	}
}

// validate walks the full AST looking for:
//  1. Break/Continue statements that have no iter-callback boundary catching
//     them (traced through lambdas and function call sites).
//  2. Calls to iter-sensitive functions that sit outside iter-callback
//     boundaries AND whose enclosing function cannot legitimately propagate
//     the break/continue (because its own call sites are not iter-callback).
//
// The validation hinges on the iter-sensitive set computed in Pass 1 plus a
// scan of every call site — if an iter-sensitive function has any non-iter-
// callback call site (or zero call sites at all), we surface an error at the
// first such spot so the user sees a concrete location.
func (s *iterSensitivity) validate(nodes []ast.Node) {
	// For each iter-sensitive function sym, check its call sites.
	callSites := s.collectCallSites(nodes)

	for _, fsym := range s.funcSyms {
		if !s.sensitive[fsym] {
			continue
		}
		sites := callSites[fsym]
		if len(sites) == 0 {
			// No call sites for an iter-sensitive function — a break somewhere
			// inside can never be caught. Report at a representative break/
			// continue location (or at the function definition as a fallback).
			s.reportNoCallSites(fsym)
			continue
		}
		for _, site := range sites {
			if !site.inIterCallback {
				s.errors = append(s.errors, TypeError{
					Line: site.call.Line, Col: 1,
					Message: fmt.Sprintf(
						"'%s' uses break/continue (directly or transitively) and must only be called from an iter-callback position (e.g. as a lambda arg to Iter.map/Iter.filter/Iter.loop/etc.)",
						fsym.Name,
					),
				})
			}
		}
	}

	// Flag stray break/continue statements that aren't inside any function at
	// all (top-level expressions), and flag break/continue inside lambdas at
	// non-iter-callback positions whose enclosing function cannot carry
	// them either (it's not iter-sensitive, or validation would have flagged
	// its bad call sites above — but a standalone lambda with break at top
	// level needs its own explicit check).
	//
	// The straight-forward case: a break outside any enclosing FuncDef.
	// (A break inside a FuncDef is handled via the function's own iter-
	// sensitivity + call-site chain.)
	s.reportStrayBreaks(nodes)
}

// callSite records one call to an iter-sensitive function along with the
// judgement of whether that call sits in an iter-callback position.
type callSite struct {
	call           *ast.Call
	inIterCallback bool
}

// collectCallSites walks the AST and returns all call sites that reference
// in-file FuncDef symbols, grouped by the callee.
func (s *iterSensitivity) collectCallSites(nodes []ast.Node) map[*Symbol][]callSite {
	result := make(map[*Symbol][]callSite)

	// Walker state: true iff the current position is inside an iter-callback
	// boundary (a lambda argument at an iter-callback slot, or the body of an
	// iter-sensitive function whose own call sites are iter-callback — which
	// we enforce recursively). For the purpose of this pass we only mark
	// "immediate" iter-callback positions; an iter-sensitive function's body
	// inherits the flag from its own call sites, so we treat every iter-
	// sensitive function's body as "in iter" here — its call sites are
	// validated separately against their own iter-callback context.

	var walk func(n ast.Node, inIter bool)
	// walkCall analyzes a Call, optionally with a pipe LHS prepended. When
	// pipeLHS != nil, iter-callback slot indices shift by +1 against call.Args.
	walkCall := func(call *ast.Call, pipeLHS ast.Node, inIter bool) {
		s.checkReduceSeed(call, pipeLHS)
		if callee := s.resolveCalleeSym(call.Func); callee != nil {
			result[callee] = append(result[callee], callSite{
				call:           call,
				inIterCallback: inIter,
			})
		}
		walk(call.Func, inIter)
		slots := s.slotsForCall(call)
		slotOffset := 0
		if pipeLHS != nil {
			lhsInIter := inIter
			if slots != nil && slots[0] {
				lhsInIter = true
			}
			walk(pipeLHS, lhsInIter)
			slotOffset = 1
		}
		for i, arg := range call.Args {
			argInIter := inIter
			isIterCbArg := slots != nil && slots[i+slotOffset]
			if isIterCbArg {
				argInIter = true
			}
			// When an iter-sensitive in-file function is passed as a
			// direct argument at an iter-callback slot, treat that as
			// a "call site" for the purpose of call-site validation.
			if isIterCbArg {
				if refSym := s.resolveCalleeSym(arg); refSym != nil {
					result[refSym] = append(result[refSym], callSite{
						call:           call,
						inIterCallback: true,
					})
				}
			}
			walk(arg, argInIter)
		}
	}
	walk = func(n ast.Node, inIter bool) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.FuncDef:
			sym := s.fa.Definitions[Pos{Line: node.Line, Col: node.Col}]
			// Inside an iter-sensitive function body, calls to other
			// iter-sensitive functions are allowed to propagate — the body's
			// iter-sensitivity is itself validated at the function's call
			// sites. So walking the body with inIter=true lets the call-site
			// check skip calls made here (they're propagating through a
			// validated chain).
			bodyInIter := false
			if sym != nil && s.sensitive[sym] {
				bodyInIter = true
			}
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, bodyInIter)
				}
			}

		case *ast.Lambda:
			// A lambda's body inherits the inIter flag as set by the caller
			// that placed this lambda (the Call case below sets inIter=true
			// when the lambda occupies an iter-callback slot).
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, inIter)
				}
			}

		case *ast.Call:
			walkCall(node, nil, inIter)

		case *ast.Block:
			for _, stmt := range node.Stmts {
				walk(stmt, inIter)
			}
		case *ast.ExprStmt:
			walk(node.Expr, inIter)
		case *ast.GroupedExpr:
			walk(node.Expr, inIter)
		case *ast.If:
			walk(node.Cond, inIter)
			walk(node.Then, inIter)
			walk(node.Else, inIter)
		case *ast.Binary:
			if node.Op == "|>" {
				if call, ok := node.Right.(*ast.Call); ok {
					walkCall(call, node.Left, inIter)
					break
				}
			}
			walk(node.Left, inIter)
			walk(node.Right, inIter)
		case *ast.Unary:
			walk(node.Right, inIter)
		case *ast.Binding:
			walk(node.Value, inIter)
		case *ast.TupleDestructure:
			walk(node.Value, inIter)
		case *ast.StructDestructure:
			walk(node.Value, inIter)
		case *ast.MapDestructure:
			walk(node.Value, inIter)
		case *ast.DistinctDestructure:
			walk(node.Value, inIter)
		case *ast.PatternBinding:
			walk(node.Value, inIter)
			for _, e := range node.ElseNodes() {
				walk(e, inIter)
			}
		case *ast.With:
			walk(node.Value, inIter)
		case *ast.FieldAccess:
			walk(node.Object, inIter)
		case *ast.Return:
			walk(node.Value, inIter)
		case *ast.Break:
			walk(node.Value, inIter)
		case *ast.ListLit:
			for _, item := range node.Items {
				walk(item, inIter)
			}
		case *ast.VectorLit:
			for _, item := range node.Items {
				walk(item, inIter)
			}
		case *ast.SetLit:
			for _, item := range node.Items {
				walk(item, inIter)
			}
		case *ast.TupleLit:
			for _, item := range node.Items {
				walk(item, inIter)
			}
		case *ast.MapLit:
			for _, entry := range node.Entries {
				walk(entry.Key, inIter)
				walk(entry.Value, inIter)
			}
		case *ast.StructLit:
			if node.Spread != nil {
				walk(node.Spread, inIter)
			}
			for _, f := range node.Fields {
				walk(f.Value, inIter)
			}
		case *ast.Case:
			walk(node.Value, inIter)
			for _, br := range node.Branches {
				walk(br.Guard, inIter)
				walk(br.Body, inIter)
			}
		case *ast.TryOp:
			walk(node.Expr, inIter)
		case *ast.Dbg:
			walk(node.Expr, inIter)
		case *ast.Assertion:
			walk(node.Expr, inIter)
		case *ast.TestDecl:
			// A test body, its group's boot and setup are call sites like
			// any function body's.
			walk(node.Boot, false)
			walk(node.Setup, false)
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, false)
				}
			}
		case *ast.StringInterp:
			for _, part := range node.Parts {
				if se, ok := part.(ast.StringExpr); ok {
					walk(se.Expr, inIter)
				}
			}
		}
	}

	for _, node := range nodes {
		walk(node, false)
	}
	return result
}

// reportNoCallSites emits an error for an iter-sensitive function that has no
// recorded call sites — its break/continue would always be uncaught.
func (s *iterSensitivity) reportNoCallSites(sym *Symbol) {
	// Prefer pointing at the first direct break/continue inside the body.
	fn := s.funcBody[sym]
	if fn == nil {
		return
	}
	if line, col, ok := s.findFirstDirectBC(fn.Body); ok {
		s.errors = append(s.errors, TypeError{
			Line: line, Col: col,
			Message: fmt.Sprintf(
				"'%s' uses break/continue but has no iter-callback call site — break/continue can only be used where an iter-callback (Iter.map/Iter.filter/Iter.loop/etc.) can catch them",
				sym.Name,
			),
		})
		return
	}
	// Fallback: point at the function definition itself.
	s.errors = append(s.errors, TypeError{
		Line: sym.Pos.Line, Col: sym.Pos.Col,
		Message: fmt.Sprintf(
			"'%s' propagates break/continue but has no iter-callback call site that could catch them",
			sym.Name,
		),
	})
}

// findFirstDirectBC searches a function body for the first break/continue
// that is NOT caught by an intervening iter-callback slot, returning its
// source position.
func (s *iterSensitivity) findFirstDirectBC(n ast.Node) (int, int, bool) {
	var found struct {
		line int
		col  int
		set  bool
	}
	var walk func(n ast.Node, caught bool)
	walk = func(n ast.Node, caught bool) {
		if found.set || n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.Break:
			if !caught {
				found.line, found.col, found.set = node.Line, node.Col, true
			}
		case *ast.Continue:
			if !caught {
				found.line, found.col, found.set = node.Line, node.Col, true
			}
		case *ast.Lambda:
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, caught)
				}
			}
		case *ast.FuncDef:
			// Nested function definition: its own scope — not ours.
		case *ast.Call:
			walk(node.Func, caught)
			slots := s.slotsForCall(node)
			for i, arg := range node.Args {
				argCaught := caught
				if slots != nil && slots[i] {
					argCaught = true
				}
				walk(arg, argCaught)
			}
		case *ast.Block:
			for _, stmt := range node.Stmts {
				walk(stmt, caught)
			}
		case *ast.ExprStmt:
			walk(node.Expr, caught)
		case *ast.GroupedExpr:
			walk(node.Expr, caught)
		case *ast.If:
			walk(node.Cond, caught)
			walk(node.Then, caught)
			walk(node.Else, caught)
		case *ast.Binary:
			if node.Op == "|>" {
				if call, ok := node.Right.(*ast.Call); ok {
					walk(call.Func, caught)
					slots := s.slotsForCall(call)
					lhsCaught := caught
					if slots != nil && slots[0] {
						lhsCaught = true
					}
					walk(node.Left, lhsCaught)
					for i, arg := range call.Args {
						argCaught := caught
						if slots != nil && slots[i+1] {
							argCaught = true
						}
						walk(arg, argCaught)
					}
					break
				}
			}
			walk(node.Left, caught)
			walk(node.Right, caught)
		case *ast.Unary:
			walk(node.Right, caught)
		case *ast.Binding:
			walk(node.Value, caught)
		case *ast.TupleDestructure:
			walk(node.Value, caught)
		case *ast.StructDestructure:
			walk(node.Value, caught)
		case *ast.MapDestructure:
			walk(node.Value, caught)
		case *ast.DistinctDestructure:
			walk(node.Value, caught)
		case *ast.PatternBinding:
			walk(node.Value, caught)
			for _, e := range node.ElseNodes() {
				walk(e, caught)
			}
		case *ast.With:
			walk(node.Value, caught)
		case *ast.FieldAccess:
			walk(node.Object, caught)
		case *ast.Return:
			walk(node.Value, caught)
		case *ast.ListLit:
			for _, item := range node.Items {
				walk(item, caught)
			}
		case *ast.VectorLit:
			for _, item := range node.Items {
				walk(item, caught)
			}
		case *ast.SetLit:
			for _, item := range node.Items {
				walk(item, caught)
			}
		case *ast.TupleLit:
			for _, item := range node.Items {
				walk(item, caught)
			}
		case *ast.MapLit:
			for _, entry := range node.Entries {
				walk(entry.Key, caught)
				walk(entry.Value, caught)
			}
		case *ast.StructLit:
			if node.Spread != nil {
				walk(node.Spread, caught)
			}
			for _, f := range node.Fields {
				walk(f.Value, caught)
			}
		case *ast.Case:
			walk(node.Value, caught)
			for _, br := range node.Branches {
				walk(br.Guard, caught)
				walk(br.Body, caught)
			}
		case *ast.TryOp:
			walk(node.Expr, caught)
		case *ast.Dbg:
			walk(node.Expr, caught)
		case *ast.Assertion:
			walk(node.Expr, caught)
		case *ast.StringInterp:
			for _, part := range node.Parts {
				if se, ok := part.(ast.StringExpr); ok {
					walk(se.Expr, caught)
				}
			}
		}
	}
	walk(n, false)
	if found.set {
		return found.line, found.col, true
	}
	return 0, 0, false
}

// reportStrayBreaks flags break/continue statements that cannot possibly be
// caught:
//   - at top level (no enclosing function at all);
//   - inside a lambda that lives at a non-iter-callback position AND whose
//     enclosing function is not iter-sensitive (and therefore does not itself
//     propagate through a validated chain).
//
// For break/continue inside an iter-sensitive function, validation is
// delegated to that function's call-site check — if every call site is
// iter-callback, the break is fine; otherwise the call-site error already
// fires.
func (s *iterSensitivity) reportStrayBreaks(nodes []ast.Node) {
	// Track: current enclosing function (if any), and immediate-iter flag.
	var walk func(n ast.Node, enclosing *Symbol, immediateIter bool)
	// walkCallValidate analyzes a Call, optionally with a pipe LHS prepended.
	// When pipeLHS != nil, iter-callback slot indices shift by +1 against
	// call.Args.
	walkCallValidate := func(call *ast.Call, pipeLHS ast.Node, enclosing *Symbol, immediateIter bool) {
		walk(call.Func, enclosing, immediateIter)
		slots := s.slotsForCall(call)
		slotOffset := 0
		if pipeLHS != nil {
			lhsIter := immediateIter
			if slots != nil && slots[0] {
				lhsIter = true
			}
			walk(pipeLHS, enclosing, lhsIter)
			slotOffset = 1
		}
		for i, arg := range call.Args {
			argIter := immediateIter
			if slots != nil && slots[i+slotOffset] {
				argIter = true
			}
			walk(arg, enclosing, argIter)
		}
	}
	walk = func(n ast.Node, enclosing *Symbol, immediateIter bool) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.FuncDef:
			sym := s.fa.Definitions[Pos{Line: node.Line, Col: node.Col}]
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, sym, false)
				}
			}
		case *ast.Lambda:
			// immediateIter for the body is inherited from what the caller
			// passed in (the Call case below sets it true for iter-callback
			// slot args).
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, enclosing, immediateIter)
				}
			}
		case *ast.Break, *ast.Continue:
			// Covered if (a) we're in an immediate iter-callback boundary,
			// OR (b) the enclosing function is iter-sensitive (its call
			// sites carry the validation). If neither: stray break.
			if immediateIter {
				break
			}
			if enclosing != nil && s.sensitive[enclosing] {
				break
			}
			line, col := 0, 0
			if b, ok := node.(*ast.Break); ok {
				line, col = b.Line, b.Col
			} else if c, ok := node.(*ast.Continue); ok {
				line, col = c.Line, c.Col
			}
			if col == 0 {
				col = 1
			}
			name := "break"
			if _, ok := node.(*ast.Continue); ok {
				name = "continue"
			}
			s.errors = append(s.errors, TypeError{
				Line: line, Col: col,
				Message: fmt.Sprintf(
					"'%s' can only be used inside an iter-callback (e.g. a lambda passed to Iter.map/Iter.filter/Iter.loop/etc.)",
					name,
				),
			})
			if b, ok := node.(*ast.Break); ok && b.Value != nil {
				walk(b.Value, enclosing, immediateIter)
			}
		case *ast.Call:
			walkCallValidate(node, nil, enclosing, immediateIter)
		case *ast.Block:
			for _, stmt := range node.Stmts {
				walk(stmt, enclosing, immediateIter)
			}
		case *ast.ExprStmt:
			walk(node.Expr, enclosing, immediateIter)
		case *ast.GroupedExpr:
			walk(node.Expr, enclosing, immediateIter)
		case *ast.If:
			walk(node.Cond, enclosing, immediateIter)
			walk(node.Then, enclosing, immediateIter)
			walk(node.Else, enclosing, immediateIter)
		case *ast.Binary:
			if node.Op == "|>" {
				if call, ok := node.Right.(*ast.Call); ok {
					walkCallValidate(call, node.Left, enclosing, immediateIter)
					break
				}
			}
			walk(node.Left, enclosing, immediateIter)
			walk(node.Right, enclosing, immediateIter)
		case *ast.Unary:
			walk(node.Right, enclosing, immediateIter)
		case *ast.Binding:
			walk(node.Value, enclosing, immediateIter)
		case *ast.TupleDestructure:
			walk(node.Value, enclosing, immediateIter)
		case *ast.StructDestructure:
			walk(node.Value, enclosing, immediateIter)
		case *ast.MapDestructure:
			walk(node.Value, enclosing, immediateIter)
		case *ast.DistinctDestructure:
			walk(node.Value, enclosing, immediateIter)
		case *ast.PatternBinding:
			walk(node.Value, enclosing, immediateIter)
			for _, e := range node.ElseNodes() {
				walk(e, enclosing, immediateIter)
			}
		case *ast.With:
			walk(node.Value, enclosing, immediateIter)
		case *ast.FieldAccess:
			walk(node.Object, enclosing, immediateIter)
		case *ast.Return:
			walk(node.Value, enclosing, immediateIter)
		case *ast.ListLit:
			for _, item := range node.Items {
				walk(item, enclosing, immediateIter)
			}
		case *ast.VectorLit:
			for _, item := range node.Items {
				walk(item, enclosing, immediateIter)
			}
		case *ast.SetLit:
			for _, item := range node.Items {
				walk(item, enclosing, immediateIter)
			}
		case *ast.TupleLit:
			for _, item := range node.Items {
				walk(item, enclosing, immediateIter)
			}
		case *ast.MapLit:
			for _, entry := range node.Entries {
				walk(entry.Key, enclosing, immediateIter)
				walk(entry.Value, enclosing, immediateIter)
			}
		case *ast.StructLit:
			if node.Spread != nil {
				walk(node.Spread, enclosing, immediateIter)
			}
			for _, f := range node.Fields {
				walk(f.Value, enclosing, immediateIter)
			}
		case *ast.Case:
			walk(node.Value, enclosing, immediateIter)
			for _, br := range node.Branches {
				walk(br.Guard, enclosing, immediateIter)
				walk(br.Body, enclosing, immediateIter)
			}
		case *ast.TryOp:
			walk(node.Expr, enclosing, immediateIter)
		case *ast.Dbg:
			walk(node.Expr, enclosing, immediateIter)
		case *ast.Assertion:
			walk(node.Expr, enclosing, immediateIter)
		case *ast.TestDecl:
			// A `break` in a test body has no iteration to stop unless a
			// callback position catches it.
			walk(node.Boot, nil, false)
			walk(node.Setup, nil, false)
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					walk(stmt, nil, false)
				}
			}
		case *ast.StringInterp:
			for _, part := range node.Parts {
				if se, ok := part.(ast.StringExpr); ok {
					walk(se.Expr, enclosing, immediateIter)
				}
			}
		}
	}
	for _, node := range nodes {
		walk(node, nil, false)
	}
}

// checkReduceSeed reports an `Iter.reduce` whose callback has no initial
// accumulator and whose accumulator type is not the element type. Without a
// seed the first element is the initial accumulator (std/iter.nomi), so
// `Iter.reduce(items, sum_valid)` with `fn sum_valid(total: Int, item: Item)`
// would hand an Item to an Int parameter. Only a lambda's first-parameter
// default supplies a seed. It runs in this pass because the pass already
// visits every call, after the checker recorded the callback's type.
func (s *iterSensitivity) checkReduceSeed(call *ast.Call, pipeLHS ast.Node) {
	if callSiteKey(call) != "Iter.reduce" && s.canonicalQualifiedOwnerKey(call) != "Iter.reduce" {
		return
	}
	at := 1
	if pipeLHS != nil {
		at = 0
	}
	if len(call.Args) != at+1 {
		return
	}
	cb := call.Args[at]
	if lam, ok := cb.(*ast.Lambda); ok && len(lam.Params) > 0 && lam.Params[0].Default != nil {
		return
	}
	ft, _ := resolveTypeVar(s.fa.ExprTypes[cb]).(*FuncType)
	if ft == nil || len(ft.Params) != 2 {
		return
	}
	acc, elem := resolveTypeVar(ft.Params[0]), resolveTypeVar(ft.Params[1])
	if acc == nil || elem == nil || TypesEqual(acc, elem) {
		return
	}
	line, col := call.Line, call.Col
	switch n := cb.(type) {
	case *ast.Ident:
		line, col = n.Line, n.Col
	case *ast.FieldAccess:
		line, col = n.Line, n.Col
	case *ast.Lambda:
		line, col = n.Line, n.Col
	}
	s.errors = append(s.errors, TypeError{
		Line: line, Col: col,
		Message: fmt.Sprintf(
			"Iter.reduce without an initial value starts from the first element, so the accumulator must have the element type; this callback's accumulator is %s and its element is %s. Give the accumulator a default in a lambda: `|acc = <initial>, item| ...`",
			acc.String(), elem.String()),
	})
}
