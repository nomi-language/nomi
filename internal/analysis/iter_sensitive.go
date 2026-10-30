package analysis

import (
	"fmt"
	"slices"
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
	"Iter.reduce":     {1: true},
	"Iter.map_reduce": {1: true},
	"Iter.each":       {1: true},
	"Iter.find":       {1: true},
	"Iter.any":        {1: true},
	"Iter.all":        {1: true},

	// Lazy adapters: Iter owner functions, separate from concrete
	// collection-specific methods.
	"Iter.iterate":    {1: true},
	"Iter.map":        {1: true},
	"Iter.scan":       {1: true},
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
		scope := s.fa.Program().StdlibModuleScopes[modName]
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
	for modName, scope := range s.fa.Program().StdlibModuleScopes {
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

// iterSensitivity checks where a file places `break`, `continue` and the
// iter-sensitive functions (iter_signal.go) it uses, its own or another
// file's.
type iterSensitivity struct {
	fa *FileAnalysis

	// funcSyms lists the file's top-level functions, in definition order.
	funcSyms []*Symbol

	// funcBody maps each of them to its declaration.
	funcBody map[*Symbol]*ast.FuncDef

	// sensitive marks the ones that are iter-sensitive.
	sensitive map[*Symbol]bool

	// signals memoizes FuncIterSignals by declaration, this file's or
	// another's.
	signals map[*ast.FuncDef]IterSignals

	// errors collects the diagnostics.
	errors []TypeError
}

// AnalyzeIterSensitivity validates the placement of `break`, `continue`
// and every use of an iter-sensitive function across the file. It runs
// after BuildTypes so that call-site reference symbols are already wired up.
func AnalyzeIterSensitivity(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	s := &iterSensitivity{
		fa:        fa,
		funcBody:  make(map[*Symbol]*ast.FuncDef),
		sensitive: make(map[*Symbol]bool),
		signals:   make(map[*ast.FuncDef]IterSignals),
	}
	for _, node := range nodes {
		if fn, ok := node.(*ast.FuncDef); ok {
			s.collectFunc(fn)
		}
	}
	s.validate(nodes)

	// A terminal that must drain a source it can see never ends.
	s.checkInfiniteIters(nodes)

	return s.errors
}

func (s *iterSensitivity) collectFunc(fn *ast.FuncDef) {
	sym := s.fa.Definitions[Pos{Line: fn.Line, Col: fn.Col}]
	if sym == nil {
		return
	}
	s.funcSyms = append(s.funcSyms, sym)
	s.funcBody[sym] = fn
	s.sensitive[sym] = s.signalsOf(fn).Any()
}

func (s *iterSensitivity) signalsOf(fd *ast.FuncDef) IterSignals {
	if fd == nil {
		return IterSignals{}
	}
	sig, seen := s.signals[fd]
	if !seen {
		sig = FuncIterSignals(fd)
		s.signals[fd] = sig
	}
	return sig
}

// sensitiveFunc is the declaration of the iter-sensitive function an Ident
// or a file-qualified FieldAccess names, in this file or another, or nil.
func (s *iterSensitivity) sensitiveFunc(fn ast.Node) *ast.FuncDef {
	fd := s.fa.topLevelFunc(fn)
	if !s.signalsOf(fd).Any() {
		return nil
	}
	return fd
}

// iterSensitiveUseError is the error at a use of the iter-sensitive
// function fd, named name, that is not an iteration callback itself: a call
// (called) or a value use. in is the iteration whose callback holds the use
// ("Iter.reduce"), or "" outside every callback.
func (s *iterSensitivity) iterSensitiveUseError(at ast.Node, name string, fd *ast.FuncDef, called bool, in string) TypeError {
	words := s.signalsOf(fd).Words()
	shape := callbackShape(fd, name, in)
	var msg string
	switch {
	case called && shape != "":
		msg = fmt.Sprintf("`%s` uses %s, so it cannot be called: pass it as an iteration callback itself, as in `%s`", name, words, shape)
	case called:
		msg = fmt.Sprintf("`%s` uses %s, so it cannot be called: pass it as an iteration callback itself", name, words)
	case shape != "":
		msg = fmt.Sprintf("`%s` uses %s, so it can only be passed as an iteration callback itself, as in `%s`", name, words, shape)
	default:
		msg = fmt.Sprintf("`%s` uses %s, so it can only be passed as an iteration callback itself", name, words)
	}
	e := errAt(at, msg)
	if in != "" {
		e.Hints = []string{fmt.Sprintf(
			"to decide in this callback instead, return a `Maybe` or an enum from `%s` and write %s here", name, words)}
	}
	return e
}

// iterCallbackArity is the number of parameters an iteration function's
// callback takes: two for a fold, one for every other.
func iterCallbackArity(iteration string) int {
	if _, fn, ok := strings.Cut(iteration, "."); ok && slices.Contains(seededIterFuncs, fn) {
		return 2
	}
	return 1
}

// callbackShape is a call that passes fd, named name, as an iteration's
// callback and that its signature fits: the iteration in that holds the use
// when fd fits its callback, else `Iter.map` for a one-parameter function and
// `Iter.reduce` for a two-parameter one whose accumulator is its result, or
// "" when none fits. A fold and `Iter.loop` take their starting value by
// name.
func callbackShape(fd *ast.FuncDef, name, in string) string {
	fits := func(iteration string) bool {
		if len(fd.Params) != iterCallbackArity(iteration) {
			return false
		}
		ret := ""
		if fd.ReturnTypeExpr != nil {
			ret = typeExprHoverString(fd.ReturnTypeExpr)
		}
		switch iteration {
		case "Iter.reduce", "Iter.scan", "Iter.loop":
			// The callback answers its next state.
			return fd.Params[0].TypeAnnotation != nil && ret != "" &&
				typeExprHoverString(fd.Params[0].TypeAnnotation) == ret
		case "Iter.filter", "Iter.find", "Iter.any", "Iter.all", "Iter.take_while", "Iter.drop_while":
			return ret == "Bool"
		}
		return true
	}
	write := func(iteration string) string {
		switch {
		case iteration == "Iter.loop":
			return fmt.Sprintf("Iter.loop(%s, from: <start>)", name)
		case iterCallbackArity(iteration) == 2:
			return fmt.Sprintf("%s(xs, %s, from: <start>)", iteration, name)
		case iteration == "Iter.iterate":
			return fmt.Sprintf("Iter.iterate(seed, %s)", name)
		case strings.HasPrefix(iteration, "Map."):
			return fmt.Sprintf("%s(m, %s)", iteration, name)
		}
		return fmt.Sprintf("%s(xs, %s)", iteration, name)
	}
	if in != "" && fits(in) {
		return write(in)
	}
	for _, iteration := range []string{"Iter.map", "Iter.reduce"} {
		if fits(iteration) {
			return write(iteration)
		}
	}
	return ""
}

// iterationName is the canonical name of the iteration function call calls
// with a callback slot ("Iter.reduce", "Map.map_values"), or "".
func (s *iterSensitivity) iterationName(call *ast.Call) string {
	for _, key := range []string{callSiteKey(call), s.canonicalQualifiedOwnerKey(call), s.canonicalStdlibKey(call)} {
		if _, ok := iterCallbackSlots[key]; ok {
			return key
		}
	}
	if sym := s.resolveIterCallbackSymbol(call.Func); sym != nil && sym.OwningType != "" {
		if key := sym.OwningType + "." + sym.Name; iterCallbackSlots[key] != nil {
			return key
		}
	}
	return ""
}

// validate reports every use of an iter-sensitive function that is not an
// iteration callback itself, an iter-sensitive function of this file that
// nothing uses, and every `break` or `continue` with no iteration to act on.
func (s *iterSensitivity) validate(nodes []ast.Node) {
	used := s.checkUses(nodes)
	for _, fsym := range s.funcSyms {
		fd := s.funcBody[fsym]
		if !s.sensitive[fsym] || fd.Public || used[fd] {
			continue
		}
		// Nothing can run this function's break or continue where an
		// iteration catches it. A `pub` one's uses may be in other files,
		// each of which checks its own.
		s.reportNoCallSites(fsym)
	}
	s.reportStrayBreaks(nodes)
}

// checkUses walks the file, reports each use of an iter-sensitive function
// in a place it may not stand, and answers the iter-sensitive functions it
// saw used at all.
//
// An iter-sensitive function may stand in one place: as an iteration
// function's callback argument (`Iter.map(xs, keep)`, `Iter.find(xs, f:
// keep)`, `xs |> Iter.map(keep)`, `Iter.reduce(xs, keep, from: 0)`). Nomi
// has no non-local control flow, and its `break` and `continue` leave only
// the function they are written in, as a signal to the iteration function
// that called it. A call anywhere, the whole body of a callback lambda or
// a capture (`&keep(&1)`) included, would carry that signal across the
// caller's boundary too, and a value use is a call nobody can see.
func (s *iterSensitivity) checkUses(nodes []ast.Node) map[*ast.FuncDef]bool {
	used := map[*ast.FuncDef]bool{}
	// in is the iteration whose callback the walk is inside, or "".
	in := ""
	var walk func(n ast.Node)
	walkCall := func(call *ast.Call, pipeLHS ast.Node) {
		s.checkReduceSeed(call, pipeLHS)
		s.checkLoopSeed(call, pipeLHS)
		if fd := s.sensitiveFunc(call.Func); fd != nil {
			used[fd] = true
			s.errors = append(s.errors, s.iterSensitiveUseError(call.Func, calleeText(call.Func), fd, true, in))
		} else if _, named := call.Func.(*ast.Ident); !named {
			walk(call.Func)
		}
		slots := s.slotsForCall(call)
		iteration := ""
		if slots != nil {
			iteration = s.iterationName(call)
		}
		args := call.Args
		if pipeLHS != nil {
			args = append([]ast.Node{pipeLHS}, call.Args...)
		}
		for i, arg := range args {
			if slots == nil || !isCallbackArg(slots, args, i) {
				walk(arg)
				continue
			}
			cb := ungroupExpr(unwrapNamedArg(arg))
			if fd := s.sensitiveFunc(cb); fd != nil {
				// The function itself is the callback.
				used[fd] = true
				continue
			}
			outer := in
			in = iteration
			walk(arg)
			in = outer
		}
	}
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			// Import names are no use of a function.
		case *ast.Call:
			walkCall(node, nil)
		case *ast.Binary:
			if node.Op == "|>" {
				if call, ok := node.Right.(*ast.Call); ok {
					walkCall(call, node.Left)
					break
				}
			}
			walk(node.Left)
			walk(node.Right)
		case *ast.FuncDef:
			// A function's body is no callback's, whatever holds it.
			outer := in
			in = ""
			ast.Children(n, walk)
			in = outer
		case *ast.Ident, *ast.FieldAccess:
			// An iter-sensitive function named anywhere but directly as an
			// iteration function's callback is a value whose calls nothing
			// here can see.
			if fd := s.sensitiveFunc(node); fd != nil {
				used[fd] = true
				s.errors = append(s.errors, s.iterSensitiveUseError(node, calleeText(node), fd, false, in))
				break
			}
			if fa, ok := node.(*ast.FieldAccess); ok {
				walk(fa.Object)
			}
		default:
			ast.Children(n, walk)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return used
}

// isCallbackArg reports whether args[i], an argument of a call whose
// callback slots are slots (counting a piped source in args), is the
// callback: the one named for the callback parameter (`f`, `pred`, `step`),
// or the positional that fills a callback slot. Positionals fill slots in
// order around the named ones, so in `Iter.reduce(xs, from: 0, keep)` the
// callback is `keep`, and in `Iter.map(source: xs, keep)` it is too.
func isCallbackArg(slots map[int]bool, args []ast.Node, i int) bool {
	if na, ok := args[i].(*ast.NamedArg); ok {
		return callbackParamNames[na.Name]
	}
	slot := 0
	for _, a := range args[:i] {
		if _, named := a.(*ast.NamedArg); !named {
			slot++
		}
	}
	for _, a := range args {
		if na, ok := a.(*ast.NamedArg); ok && sourceParamNames[na.Name] {
			slot++
		}
	}
	return slots[slot]
}

// callbackParamNames are the names iterCallbackSlots' functions give their
// callback parameter, and sourceParamNames the names they give the
// parameter before it.
var (
	callbackParamNames = map[string]bool{"f": true, "pred": true, "step": true}
	sourceParamNames   = map[string]bool{"source": true, "seed": true, "m": true}
)

// reportNoCallSites emits an error for an iter-sensitive function of this
// file that nothing uses: its break/continue would always be uncaught.
func (s *iterSensitivity) reportNoCallSites(sym *Symbol) {
	// Prefer pointing at the first direct break/continue inside the body.
	fn := s.funcBody[sym]
	if fn == nil {
		return
	}
	if at := firstOwnSignal(fn.Body); at != nil {
		line, col := signalPos(at)
		s.errors = append(s.errors, TypeError{
			Line: line, Col: col,
			Message: fmt.Sprintf(
				"'%s' uses break/continue but has no iter-callback call site — break/continue can only be used where an iter-callback (Iter.map/Iter.filter/Iter.loop/etc.) can catch them",
				sym.Name,
			),
		})
		return
	}
	s.errors = append(s.errors, TypeError{
		Line: sym.Pos.Line, Col: sym.Pos.Col,
		Message: fmt.Sprintf(
			"'%s' propagates break/continue but has no iter-callback call site that could catch them",
			sym.Name,
		),
	})
}

// firstOwnSignal is the first `break` or `continue` in n's own scope
// (OwnScopeSignals), or nil.
func firstOwnSignal(n ast.Node) ast.Node {
	var found ast.Node
	if n == nil {
		return nil
	}
	ast.Inspect(n, func(m ast.Node) bool {
		if found != nil {
			return false
		}
		switch m.(type) {
		case *ast.Break, *ast.Continue:
			found = m
			return false
		case *ast.Lambda, *ast.FuncDef, *ast.Capture, *ast.ConcurrentBlock:
			return false
		}
		return true
	})
	return found
}

// reportStrayBreaks flags break/continue statements that no iteration can
// catch: one outside every function and callback, one in a lambda or
// capture that is not itself an iteration function's callback, one in a
// function other than a top-level one (an impl's, a nested `fn`), and one
// in a `concurrent` block, which runs as an activation of its own. A `break`
// or `continue` in a top-level function's own scope makes it iter-sensitive,
// and checkUses checks where it is used.
func (s *iterSensitivity) reportStrayBreaks(nodes []ast.Node) {
	// A place says whether a signal at the current position is caught, by
	// a callback's iteration or as an iter-sensitive function's own, and
	// whether it stands in a `concurrent` block.
	type place struct {
		caught     bool
		concurrent bool
	}
	callbacks := map[ast.Node]bool{}
	var walk func(n ast.Node, in place)
	walkCall := func(call *ast.Call, pipeLHS ast.Node, in place) {
		walk(call.Func, in)
		slots := s.slotsForCall(call)
		args := call.Args
		if pipeLHS != nil {
			args = append([]ast.Node{pipeLHS}, call.Args...)
		}
		for i, arg := range args {
			if slots != nil && isCallbackArg(slots, args, i) {
				callbacks[unwrapNamedArg(arg)] = true
			}
			walk(arg, in)
		}
	}
	walk = func(n ast.Node, in place) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.FuncDef:
			for _, p := range node.Params {
				walk(p.Default, place{})
			}
			if node.Body != nil {
				sym := s.fa.Definitions[Pos{Line: node.Line, Col: node.Col}]
				walk(node.Body, place{caught: sym != nil && s.sensitive[sym]})
			}
		case *ast.Lambda:
			// The body of an iteration function's callback catches its
			// `break` and `continue`. Any other lambda is an activation of
			// its own that nothing catches them in.
			for _, p := range node.Params {
				walk(p.Default, in)
			}
			if node.Body != nil {
				walk(node.Body, place{caught: callbacks[node]})
			}
		case *ast.Capture:
			walk(node.Body, place{caught: callbacks[node]})
		case *ast.ConcurrentBlock:
			if node.Body != nil {
				walk(node.Body, place{concurrent: true})
			}
		case *ast.TestDecl:
			// A `break` in a test body has no iteration to stop unless a
			// callback position catches it.
			ast.Children(n, func(c ast.Node) { walk(c, place{}) })
		case *ast.Break, *ast.Continue:
			if !in.caught {
				s.errors = append(s.errors, strayBreakError(node, in.concurrent))
			}
			if b, ok := node.(*ast.Break); ok && b.Value != nil {
				walk(b.Value, in)
			}
		case *ast.Call:
			walkCall(node, nil, in)
		case *ast.Binary:
			if node.Op == "|>" {
				if call, ok := node.Right.(*ast.Call); ok {
					walkCall(call, node.Left, in)
					break
				}
			}
			walk(node.Left, in)
			walk(node.Right, in)
		default:
			ast.Children(n, func(c ast.Node) { walk(c, in) })
		}
	}
	for _, node := range nodes {
		walk(node, place{})
	}
}

// strayBreakError is the error at a `break` or `continue` nothing catches.
func strayBreakError(n ast.Node, concurrent bool) TypeError {
	name := "break"
	if _, ok := n.(*ast.Continue); ok {
		name = "continue"
	}
	line, col := signalPos(n)
	msg := fmt.Sprintf(
		"'%s' can only be used inside an iter-callback (e.g. a lambda passed to Iter.map/Iter.filter/Iter.loop/etc.)",
		name)
	if concurrent {
		msg = fmt.Sprintf(
			"'%s' cannot leave a `concurrent` block: the block runs as an activation of its own, so no iteration around it sees the signal",
			name)
	}
	return TypeError{Line: line, Col: col, Message: msg}
}

// signalPos is a `break` or `continue` statement's position.
func signalPos(n ast.Node) (int, int) {
	line, col := 0, 0
	switch t := n.(type) {
	case *ast.Break:
		line, col = t.Line, t.Col
	case *ast.Continue:
		line, col = t.Line, t.Col
	}
	if col == 0 {
		col = 1
	}
	return line, col
}

// seededCallArgs splits a seeded iteration call (`Iter.reduce`, `scan`,
// `map_reduce` with withSource, `Iter.loop` without) into what the seed rule
// reads: the callback (the last positional after the source, or `f:`), the
// `from:` argument's value, and a starting value written positionally
// (`Iter.reduce(xs, 0, add)`), each nil when absent. pipeLHS is the piped
// source, or nil.
func seededCallArgs(call *ast.Call, pipeLHS ast.Node, withSource bool) (cb, from, positionalSeed ast.Node) {
	var positional []ast.Node
	for _, arg := range call.Args {
		if na, ok := arg.(*ast.NamedArg); ok {
			switch na.Name {
			case seedParam:
				from = na.Value
			case "f":
				cb = na.Value
			}
			continue
		}
		positional = append(positional, arg)
	}
	if withSource && pipeLHS == nil && len(positional) > 0 {
		positional = positional[1:]
	}
	if len(positional) > 0 && cb == nil {
		cb = positional[len(positional)-1]
		positional = positional[:len(positional)-1]
	}
	if len(positional) > 0 {
		positionalSeed = positional[0]
	}
	return cb, from, positionalSeed
}

// checkReduceSeed reports an `Iter.reduce`, `Iter.scan` or `Iter.map_reduce`
// whose starting value is not given exactly once. It is given by name,
// `from: 0`, with any callback (a named function, a capture, a lambda), or
// as the callback lambda's first-parameter default (`fn acc = 0, x -> acc +
// x`). Neither is an error, since there is no first-element fallback and an
// empty source would have no answer; both is an error, since the call would
// have two. A callback slot left open (`Iter.reduce(xs, &1)`) is rejected
// too. It runs in this pass because the pass already visits every call.
func (s *iterSensitivity) checkReduceSeed(call *ast.Call, pipeLHS ast.Node) {
	name := ""
	for _, seeded := range seededIterFuncs {
		if s.isIterOwnerCall(call, seeded) {
			name = seeded
			break
		}
	}
	if name == "" {
		return
	}
	cb, from, positionalSeed := seededCallArgs(call, pipeLHS, true)
	if positionalSeed != nil {
		e := errAt(positionalSeed, fmt.Sprintf("`Iter.%s` takes its starting value by name", name))
		e.Hints = []string{fmt.Sprintf("write it as `from:` after the callback: `Iter.%s(xs, f, from: <start>)`", name)}
		s.errors = append(s.errors, e)
		return
	}
	if cb == nil {
		return
	}
	if _, open := cb.(*ast.Placeholder); open {
		s.errors = append(s.errors, reduceSeedError(name, cb, fmt.Sprintf("pass the callback here: `Iter.%s(xs, f, from: <start>)`", name)))
		return
	}
	lam, isLambda := cb.(*ast.Lambda)
	defaulted := isLambda && len(lam.Params) > 0 && lam.Params[0].Default != nil
	switch {
	case from != nil && defaulted:
		e := errAt(from, fmt.Sprintf("`Iter.%s` has two starting values: `from:` and the default of the callback's first parameter", name))
		e.Hints = []string{fmt.Sprintf("keep one: remove `from:`, or remove the default from `%s`", paramText(lam.Params[0]))}
		s.errors = append(s.errors, e)
	case from == nil && !defaulted:
		s.errors = append(s.errors, reduceSeedError(name, cb, reduceSeedHints(name, cb)...))
	}
}

// paramText is a lambda parameter's name, or "the first parameter" for a
// pattern one.
func paramText(p ast.Param) string {
	if p.Destructure != nil || p.Name == "" {
		return "the first parameter"
	}
	return p.Name
}

// checkLoopSeed reports an `Iter.loop` whose starting state is not given
// exactly once. A loop with state starts from `from` (`Iter.loop(step, from:
// 0)`, any callback) or from the callback lambda's parameter default (`fn n
// = 0 -> ...`); a stateless loop is a lambda with no parameter (`fn -> ...`)
// and takes neither. A callback with state and no starting state would have
// no first state to call the callback with, and one with both would have
// two.
func (s *iterSensitivity) checkLoopSeed(call *ast.Call, pipeLHS ast.Node) {
	if !s.isIterOwnerCall(call, "loop") {
		return
	}
	cb, from, positionalSeed := seededCallArgs(call, pipeLHS, false)
	if pipeLHS != nil && cb == nil {
		cb = pipeLHS
	}
	if positionalSeed != nil {
		e := errAt(positionalSeed, "`Iter.loop` takes its starting state by name")
		e.Hints = []string{"write it as `from:` after the callback: `Iter.loop(step, from: <start>)`"}
		s.errors = append(s.errors, e)
		return
	}
	if cb == nil {
		return
	}
	if _, open := cb.(*ast.Placeholder); open {
		return
	}
	lam, isLambda := cb.(*ast.Lambda)
	stateless := isLambda && len(lam.Params) == 0
	defaulted := isLambda && len(lam.Params) > 0 && lam.Params[0].Default != nil
	switch {
	case from != nil && stateless:
		e := errAt(from, "a stateless `Iter.loop` takes no starting state")
		e.Hints = []string{"remove `from:`, or give the callback a parameter for the state: `fn state -> ...`"}
		s.errors = append(s.errors, e)
	case from != nil && defaulted:
		e := errAt(from, "`Iter.loop` has two starting states: `from:` and the default of the callback's parameter")
		e.Hints = []string{fmt.Sprintf("keep one: remove `from:`, or remove the default from `%s`", paramText(lam.Params[0]))}
		s.errors = append(s.errors, e)
	case from == nil && !stateless && !defaulted:
		e := errAt(cb, loopSeedMessage)
		switch n := cb.(type) {
		case *ast.Lambda:
			name := "state"
			if n.Params[0].Destructure == nil && n.Params[0].Name != "" {
				name = n.Params[0].Name
			}
			e.Hints = []string{fmt.Sprintf("give it with `from:`, `Iter.loop(fn %s -> ..., from: <start>)`, or as the parameter's default, `fn %s = <start> -> ...`; or take no parameter for a loop with no state: `fn -> ...`", name, name)}
		case *ast.Ident, *ast.FieldAccess:
			e.Hints = []string{fmt.Sprintf("give it with `from:`: `Iter.loop(%s, from: <start>)`", calleeText(n))}
		default:
			e.Hints = []string{"give it with `from:`: `Iter.loop(step, from: <start>)`, or pass a lambda whose state has a default: `fn state = <start> -> ...`"}
		}
		s.errors = append(s.errors, e)
	}
}

// loopSeedMessage is the error every unseeded `Iter.loop` reports.
const loopSeedMessage = "`Iter.loop` needs a starting state"

// isIterOwnerCall reports whether call calls std's `Iter.<name>`, however it
// is spelled: owner-qualified, through an owner alias, or bare inside std's
// own `impl Iter`. A program cannot declare its own `Iter`.
func (s *iterSensitivity) isIterOwnerCall(call *ast.Call, name string) bool {
	if callSiteKey(call) == "Iter."+name || s.canonicalQualifiedOwnerKey(call) == "Iter."+name {
		return true
	}
	sym := s.resolveIterCallbackSymbol(call.Func)
	return sym != nil && sym.OwningType == "Iter" && sym.Name == name
}

// seededIterFuncs are the `Iter` owner functions that fold with a starting
// value: `from`, or the callback lambda's first-parameter default.
var seededIterFuncs = []string{"reduce", "scan", "map_reduce"}

// seedParam is the parameter a seeded iteration function (seededIterFuncs and
// `Iter.loop`) takes its starting value in, when the callback lambda does not
// carry it.
const seedParam = "from"

// routeAroundSeed moves a positional argument the slot routing put in a
// seeded iteration call's `from` slot to the callback slot, the last, when no
// other positional argument fills that: the starting value is given by name
// or by the callback lambda's default, so `Iter.reduce(xs, Int.add, from: 0)`
// passes `Int.add` as the callback, as a bare name routes there
// (routesAsTrailingFunc). A starting value written positionally keeps its
// slot, and checkReduceSeed asks for it by name. slots is the routing of
// args, -1 for a named argument.
func (c *checker) routeAroundSeed(call *ast.Call, args []ast.Node, slots []int, nParams int) {
	seed := c.optionalSeedSlot(call)
	last := nParams - 1
	if seed < 0 || seed == last {
		return
	}
	at := -1
	for i, slot := range slots {
		if i >= len(args) {
			break
		}
		if _, named := args[i].(*ast.NamedArg); named {
			continue
		}
		switch slot {
		case last:
			return
		case seed:
			at = i
		}
	}
	if at >= 0 {
		slots[at] = last
	}
}

// seedingIterFunc is the name of the std `Iter` function call calls when it
// takes a starting value (`reduce`, `scan`, `map_reduce` or `loop`), or "".
func (fa *FileAnalysis) seedingIterFunc(call *ast.Call) string {
	if fa == nil || call == nil {
		return ""
	}
	s := &iterSensitivity{fa: fa}
	for _, name := range append([]string{"loop"}, seededIterFuncs...) {
		if s.isIterOwnerCall(call, name) {
			return name
		}
	}
	return ""
}

// optionalSeedSlot is the index of a seeded iteration call's `from`
// parameter, or -1 for any other call. The parameter has no default, yet a
// call may leave it out: the callback lambda's first-parameter default is
// then the starting value. Which of the two gives it, and that exactly one
// does, is checkReduceSeed's and checkLoopSeed's to report, so the arity
// checks count `from` as optional.
func (c *checker) optionalSeedSlot(call *ast.Call) int {
	if c.fa.seedingIterFunc(call) == "" {
		return -1
	}
	for i, name := range c.callDefParams(call.Func) {
		if name == seedParam {
			return i
		}
	}
	return -1
}

// reduceSeedError is the error an unseeded call to the seeded `Iter`
// function name reports: "`Iter.reduce` needs a starting value".
func reduceSeedError(name string, at ast.Node, hints ...string) TypeError {
	e := errAt(at, fmt.Sprintf("`Iter.%s` needs a starting value", name))
	e.Hints = hints
	return e
}

// reduceSeedHints shows the two spellings of cb's starting value: `from:`,
// and for a lambda its accumulator's default. A function named `min` or `max`
// also points at `Iter.min` / `Iter.max`.
func reduceSeedHints(fn string, cb ast.Node) []string {
	switch n := cb.(type) {
	case *ast.Lambda:
		acc, item := "acc", "x"
		if len(n.Params) == 2 && n.Params[0].Destructure == nil && n.Params[1].Destructure == nil {
			acc, item = n.Params[0].Name, n.Params[1].Name
		}
		return []string{fmt.Sprintf("give it with `from:`, `Iter.%s(xs, fn %s, %s -> ..., from: <start>)`, or as the accumulator's default, `fn %s = <start>, %s -> ...`", fn, acc, item, acc, item)}
	case *ast.Ident, *ast.FieldAccess:
		callee := calleeText(n)
		if callee == "?" || strings.HasPrefix(callee, "?.") {
			break
		}
		hints := []string{fmt.Sprintf("give it with `from:`: `Iter.%s(xs, %s, from: <start>)`", fn, callee)}
		if fn != "reduce" {
			return hints
		}
		switch callee[strings.LastIndexByte(callee, '.')+1:] {
		case "min":
			hints = append(hints, "for the least element, `Iter.min(xs)` answers a `Maybe`, with `None` for an empty source")
		case "max":
			hints = append(hints, "for the greatest element, `Iter.max(xs)` answers a `Maybe`, with `None` for an empty source")
		}
		return hints
	}
	return []string{fmt.Sprintf("give it with `from:`: `Iter.%s(xs, f, from: <start>)`, or pass a lambda whose accumulator has a default: `fn acc = <start>, x -> ...`", fn)}
}

// unwrapNamedArg is the value a call argument passes: a named argument's
// value, or the argument itself.
func unwrapNamedArg(arg ast.Node) ast.Node {
	if na, ok := arg.(*ast.NamedArg); ok {
		return na.Value
	}
	return arg
}
