package irbuild

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// resolvedCallOf is the checker's record of call t (analysis.ResolvedCall),
// nil when t has none. A call the builder made carries its record
// (carryCall). A copy of a written call (a pipe stage with its head spliced
// in first, a call requalified through its module's alias) keeps the
// written call's position and arguments, and finds the record by them; a
// call at that position with other arguments is another call, and has
// none.
func (g *gen) resolvedCallOf(t *ast.Call) *analysis.ResolvedCall {
	fa := g.srcFA()
	if fa == nil || t == nil {
		return nil
	}
	if rec := g.carriedCalls[t]; rec != nil {
		return rec
	}
	if rec := fa.Calls[t]; rec != nil {
		return rec
	}
	if len(fa.Calls) == 0 {
		return nil
	}
	if g.resolvedCallsAt == nil || g.resolvedCallsFA != fa {
		g.resolvedCallsFA = fa
		g.resolvedCallsAt = make(map[analysis.Pos]*ast.Call, len(fa.Calls))
		for n := range fa.Calls {
			if c, ok := n.(*ast.Call); ok {
				g.resolvedCallsAt[analysis.Pos{Line: c.Line, Col: c.Col}] = c
			}
		}
	}
	written := g.resolvedCallsAt[analysis.Pos{Line: t.Line, Col: t.Col}]
	if written == nil {
		return nil
	}
	rec := fa.Calls[written]
	args := t.Args
	if rec.Piped && len(args) == len(written.Args)+1 {
		args = args[1:]
	}
	if !slices.Equal(args, written.Args) {
		return nil
	}
	return rec
}

// argPlan is the checker's placement of a call's written arguments, over
// the arguments the builder lowers: the call's own, or a pipe stage's with
// the head spliced in first.
type argPlan struct {
	// slots maps each argument's index to the parameter slot it fills. A
	// slot absent from it is one no argument fills, which fillCallDefaults
	// completes.
	slots []int
	// order is the arguments' indices in the order they are evaluated.
	order []int
	// defaulted is the slots the call leaves to their defaults, ascending,
	// the order they are evaluated in after the written arguments (spec §7
	// *Default Arguments*): those the record marks analysis.SlotDefault,
	// and every slot of a callee with parameters past the record's.
	defaulted []int
}

// callPlan is where each of t's arguments goes and the order they are
// evaluated in, read from the checker's record of t
// (analysis.ResolvedCall): the builder decides neither. params is how many
// parameters the callee the builder is lowering t against has; ok is false
// when the record places an argument past them, so that is not the callee
// the checker placed the arguments for, which a route that tries one
// callee after another finds this way. A callee with parameters after the
// record's (a partial's entry, whose hidden slots follow the partial's
// own) leaves them to their defaults.
//
// Every call the checker accepted has a record (TestElaborationGate), and a
// call the builder makes carries one (carryCall), so a call with none, or
// with other arguments than its record's, is the builder's own error.
func (g *gen) callPlan(t *ast.Call, params int) (argPlan, bool) {
	rec := g.resolvedCallOf(t)
	if rec == nil {
		panic(fmt.Sprintf("irbuild: %s:%d:%d: a call with no placement record", g.srcFA().FilePath, t.Line, t.Col))
	}
	// A pipe stage is lowered as a copy with its head spliced in first.
	written, shift := len(t.Args), 0
	if rec.Piped && len(t.Args) == len(rec.Order)+1 {
		written, shift = len(t.Args)-1, 1
	}
	if written != len(rec.Order) {
		panic(fmt.Sprintf("irbuild: %s:%d:%d: a call of %d arguments whose placement record has %d", g.srcFA().FilePath, t.Line, t.Col, written, len(rec.Order)))
	}
	if len(rec.Slots) > params {
		return argPlan{}, false
	}
	plan := argPlan{slots: make([]int, len(t.Args)), order: make([]int, 0, len(t.Args))}
	for i, slot := range rec.ArgSlots(written) {
		if slot < 0 {
			return argPlan{}, false
		}
		plan.slots[i+shift] = slot
	}
	if shift == 1 {
		plan.slots[0] = -1
		for slot, s := range rec.Slots {
			if s.Fill == analysis.SlotPiped {
				plan.slots[0] = slot
			}
		}
		if plan.slots[0] < 0 {
			return argPlan{}, false
		}
		plan.order = append(plan.order, 0)
	}
	for _, i := range rec.Order {
		plan.order = append(plan.order, i+shift)
	}
	for slot, s := range rec.Slots {
		if s.Fill == analysis.SlotDefault {
			plan.defaulted = append(plan.defaulted, slot)
		}
	}
	for slot := len(rec.Slots); slot < params; slot++ {
		plan.defaulted = append(plan.defaulted, slot)
	}
	return plan, true
}

// leavesDefaults reports whether call t leaves a parameter of a callee
// with params parameters to its default (argPlan.defaulted), for a route
// that fills them only after it has chosen its callee. A call with no
// record is code the builder wrote with an argument for every parameter
// (markUnchecked), and leaves none; so does a call whose record is not of
// this callee.
func (g *gen) leavesDefaults(t *ast.Call, params int) bool {
	if g.resolvedCallOf(t) == nil {
		return false
	}
	plan, ok := g.callPlan(t, params)
	return ok && len(plan.defaulted) > 0
}

// carryCall makes rec the record of call, a call the builder made, until
// done is called: the record of the stage or function value it stands for,
// or the slots the builder wrote its arguments at. A record whose callee
// names its reference (Callee.Ref, which carries a generic callee's type
// arguments) is the reference at the call's callee too (calleeRef). A call
// that lives as long as the body it is in never calls done. A nil rec
// carries nothing.
func (g *gen) carryCall(call *ast.Call, rec *analysis.ResolvedCall) (done func()) {
	if rec == nil {
		return func() {}
	}
	if g.carriedCalls == nil {
		g.carriedCalls = map[*ast.Call]*analysis.ResolvedCall{}
	}
	g.carriedCalls[call] = rec
	if ref := rec.Callee.Ref; ref != nil && call.Func != nil {
		if g.carriedRefs == nil {
			g.carriedRefs = map[ast.Node]*analysis.Symbol{}
		}
		g.carriedRefs[call.Func] = ref
	}
	return func() {
		delete(g.carriedCalls, call)
		delete(g.carriedRefs, call.Func)
	}
}

// calleeRef is the reference at ref, a call's callee: the one the record
// of a call the builder made carries (carryCall), else the checker's.
func (g *gen) calleeRef(ref ast.Node) *analysis.Symbol {
	if sym := g.carriedRefs[ref]; sym != nil {
		return sym
	}
	line, col := calleeRefPos(ref)
	return g.srcFA().References[analysis.Pos{Line: line, Col: col}]
}

// callsProgramFunc reports whether the checker resolved call t to a `fn` one
// of the program's files declares (analysis.Callee.InProgram), which the
// routes for a std type's own functions do not lower: `Range.describe(r)`
// for a program's `impl Describe for Range<Int>`.
func (bl *irScalarBuilder) callsProgramFunc(t *ast.Call) bool {
	rec := bl.g.resolvedCallOf(t)
	return rec != nil && rec.Callee.InProgram
}

// calleeVerify compares, in a test binary, the function declaration each
// call route chose with the one the checker recorded for the call
// (analysis.Callee.Decl). NOMI_CALLEE_LOG=<file> compares in any binary and
// writes each difference there instead of failing.
var calleeVerify = testing.Testing() || os.Getenv("NOMI_CALLEE_LOG") != ""

// verifyCallee fails when a route lowers t as a call of a declaration
// other than the one the checker recorded for it (analysis.Callee.Decl):
// a `fn`, a `host fn`, an impl's function, or the interface function an
// impl inherits. A qualified call's first route reads the record
// (recordedCallPlan); the routes after it, and the routes of other call
// shapes, still choose the callee from its spelling, and this holds each
// choice to the record until they read it. A
// call the checker leaves to dispatch where it runs (CalleeInterface: a
// type parameter's or an interface value's receiver) is held only where
// the route calls the interface's function too: an instance reaches its
// own receiver's impl.
func (g *gen) verifyCallee(t *ast.Call, decl ast.Node) {
	g.verifyCalleeIn(t, decl, nil)
}

// verifyCalleeIn is verifyCallee for a route whose function is the default
// an impl block inherits: block is that block, which must be the record's
// (Callee.Impl) too.
func (g *gen) verifyCalleeIn(t *ast.Call, decl ast.Node, block *ast.ImplBlock) {
	if !calleeVerify || t == nil || isNilNode(decl) || g.srcFA() == nil {
		return
	}
	c := g.calleeOf(t)
	if isNilNode(c.Decl) {
		return
	}
	switch c.Kind {
	case analysis.CalleeFunction, analysis.CalleeHost, analysis.CalleeImpl:
	case analysis.CalleeInterface:
		if _, iface := decl.(*ast.InterfaceMethod); !iface {
			return
		}
	default:
		return
	}
	decl = shellSource(decl)
	want := c.Decl
	id := c.Std
	same := want == decl || sameDecl(want, decl, id, g.stdFuncIn(id, decl))
	if have, ok := c.Impl.(*ast.ImplBlock); same && ok && block != nil {
		same = sameImplBlock(have, block)
	}
	if !same {
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("a call the checker resolved to %s is lowered as a call of %s", declLabel(want), declLabel(decl)))
	}
}

// verifyDispatched fails when a route lowers t as a call dispatched where
// it runs although the checker recorded the impl block it reaches.
func (g *gen) verifyDispatched(t *ast.Call) {
	if !calleeVerify || t == nil || g.srcFA() == nil {
		return
	}
	if rec := g.resolvedCallOf(t); rec != nil && rec.Callee.Impl != nil {
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("a call the checker resolved to %s is lowered as a call dispatched where it runs", declLabel(rec.Callee.Decl)))
	}
}

// shellSource is the declaration n stands for: the `host fn` a
// shell is (hostShellOf), else n.
func shellSource(n ast.Node) ast.Node {
	if fd, ok := n.(*ast.FuncDef); ok {
		if ef, ok := hostExtOf(fd); ok {
			return ef
		}
	}
	return n
}

// declLabel names a function declaration in a verifyCallee failure.
func declLabel(n ast.Node) string {
	switch d := n.(type) {
	case *ast.FuncDef:
		return fmt.Sprintf("`fn %s` (line %d)", d.Name, d.Line)
	case *ast.ExternFunc:
		return fmt.Sprintf("`host fn %s` (line %d)", d.Name, d.Line)
	case *ast.InterfaceMethod:
		return fmt.Sprintf("the interface function `%s` (line %d)", d.Name, d.Line)
	}
	return fmt.Sprintf("%T", n)
}

// sameImplBlock reports whether a and b are one impl block: one node, or
// one block of two parses of a std module, at one position with one
// header.
func sameImplBlock(a, b *ast.ImplBlock) bool {
	if a == b {
		return true
	}
	return a.Line == b.Line && a.Col == b.Col && a.Interface != nil && b.Interface != nil && a.Receiver != nil && b.Receiver != nil &&
		a.Interface.TypeString() == b.Interface.TypeString() && a.Receiver.TypeString() == b.Receiver.TypeString()
}

// sitePos is line:col of the file being lowered, for a verifier's report:
// the file's path, or its module where it has none (a source loaded by
// name), and in place of a position no source has, what wrote the node:
// the front end, on a synthesized line (a derived body), or the checker,
// at a column below zero (a refinement literal's check call).
func (g *gen) sitePos(line, col int) string {
	fa := g.srcFA()
	path := fa.FilePath
	if path == "" {
		path = fa.Origin
	}
	switch {
	case analysis.IsSynthesizedLine(line):
		return path + ": in code the front end writes"
	case col < 0:
		return fmt.Sprintf("%s:%d: in a check the checker writes for that line", path, line)
	}
	return fmt.Sprintf("%s:%d:%d", path, line, col)
}

// calleeMismatch reports a route that lowers the call or operator at
// line:col against another function than the record's: into
// NOMI_CALLEE_LOG when it is set, else by panicking.
func (g *gen) calleeMismatch(line, col int, what string) {
	msg := fmt.Sprintf("irbuild: %s: %s", g.sitePos(line, col), what)
	if out := os.Getenv("NOMI_CALLEE_LOG"); out != "" {
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, msg)
			f.Close()
		}
		return
	}
	panic(msg)
}

// stdFuncIn is which function of the std module that declares want, the
// function a call's record names, decl is in a parse of that module: the
// shared analysis's, or the program's own parse of it, checked from its
// text (sameDecl). The zero StdFunc when want is no std function, or decl
// is not that module's.
func (g *gen) stdFuncIn(want analysis.StdFunc, decl ast.Node) analysis.StdFunc {
	name, inStd := strings.CutPrefix(want.Module, "std/")
	if !inStd {
		return analysis.StdFunc{}
	}
	parses := []*analysis.FileAnalysis{std.Shared().Files[name], g.srcFA()}
	if g.std != nil {
		// The parse the std index was built from, whose functions the
		// routes over it name.
		if v := g.std.views[name]; v != nil {
			parses = append(parses, v.fa)
		}
	}
	files := g.files
	if files == nil && g.stdProgCaller != nil {
		// A std instance's: the program's own parse of an edited module,
		// whose functions the instance lowers against.
		files = g.stdProgCaller.files
	}
	if files != nil {
		for _, u := range files.units {
			if u.key == want.Module {
				parses = append(parses, u.fa)
			}
		}
	}
	for _, fa := range parses {
		if fa == nil || fa.Origin != want.Module {
			continue
		}
		if s := fa.StdFuncOf(decl); !s.IsZero() {
			return s
		}
	}
	return analysis.StdFunc{}
}

// calleeOf is the callee of call t as the checker classified it
// (analysis.Callee): the record's, or the one a call the builder wrote
// carries, its Decl as this gen's module declares it (ownCallee). The zero
// Callee for a call with neither.
func (g *gen) calleeOf(t *ast.Call) analysis.Callee {
	if rec := g.resolvedCallOf(t); rec != nil {
		return g.ownCallee(rec.Callee)
	}
	return analysis.Callee{}
}

// ownCallee is c with its Decl read as this gen's module declares it
// (ownDecl): in a std module checked from edited source, a call through
// the module's std name is recorded against the shared analysis's copy of
// the function, and the edited twin is the one lowered here.
func (g *gen) ownCallee(c analysis.Callee) analysis.Callee {
	c.Decl = g.ownDecl(c.Decl)
	return c
}

// calleeAt is the callee of call t (calleeOf) in the instance being
// built: for a call the checker left to dispatch on a type parameter of
// the generic body (analysis.CalleeInterface, For naming the parameter),
// the record at the instance's type arguments, as the checker answers it
// (analysis.FileAnalysis.CalleeAt) from the checker's types the frames
// carry (genericSubstType), and in an abstract body the record at its type
// parameter's bound (recordTypes). A parameter with no checker type, and a
// call outside both, keeps its record. A std instance asks through the
// program file whose call made it (stdProgCaller), whose impls are the
// ones its type arguments have.
func (g *gen) calleeAt(t *ast.Call) analysis.Callee {
	c := g.calleeOf(t)
	if c.Kind != analysis.CalleeInterface || c.For == nil {
		return c
	}
	if by, at := g.recordTypes(); at {
		return g.ownCallee(g.instanceFA().CalleeAt(c, by))
	}
	return c
}

// instanceFA is the file whose program's impls an instance's type
// arguments have: this gen's; in a std instance, the program file whose
// call made it (stdProgCaller); in a std module listed in
// programStdModules and lowered with the program (programStd), whose
// analysis is std's and holds none of the program's impls, the program's
// first file.
func (g *gen) instanceFA() *analysis.FileAnalysis {
	if g.stdProgCaller != nil {
		return g.stdProgCaller.srcFA()
	}
	if g.programStd && g.stdInsts != nil {
		for _, og := range g.stdInsts.program {
			if og != nil && og.stdModule == "" && !og.programStd && og.fa != nil {
				return og.fa
			}
		}
	}
	return g.srcFA()
}

// inInstance reports whether the body being built is an instance whose
// type parameters the checker's records name: a generic instance (a
// frame), or an interface default lowered for one implementor, whose
// `self` the implementor binds (genericSubstType).
func (g *gen) inInstance() bool {
	return len(g.visibleSubst()) > 0 || g.defaultSelf != nil
}

// genericSubstType_ is genericSubstType in the form the checker's
// questions at type arguments take (analysis.ReplaceTypeParams).
func (g *gen) genericSubstType_(p *analysis.TypeParam_) (analysis.Type, bool) {
	return g.genericSubstType(p.Name_)
}

// operatorUseAt is the record of the operator t (operatorRecord) in the
// instance being built: for an operator over a type parameter
// (OperatorPerInstance), the record at the instance's type arguments as
// the checker answers it (analysis.FileAnalysis.OperatorUseAt), the
// record an operator written at those types has, and in an abstract body
// the record at its type parameter's bound (recordTypes). resolved is
// false, and the record t's own, outside both and where a parameter has
// no checker type.
func (g *gen) operatorUseAt(t ast.Node) (use *analysis.OperatorUse, resolved bool) {
	use = g.operatorRecord(t)
	if use == nil || use.Form != analysis.OperatorPerInstance {
		return use, false
	}
	if by, at := g.recordTypes(); at {
		return g.instanceFA().OperatorUseAt(g.srcFA(), t, by)
	}
	return use, false
}

// recordTypes is the checker's type each type parameter a per-instance
// record names stands for in the body being built (calleeAt,
// operatorUseAt): an instance's type argument (genericSubstType_), and in
// an abstract lowering, for a type parameter left open, the interface it
// is held as (abstractRecordType). False outside an instance.
func (g *gen) recordTypes() (func(*analysis.TypeParam_) (analysis.Type, bool), bool) {
	switch {
	case !g.inInstance():
		return nil, false
	case g.dc != nil && g.dc.abstract:
		return g.abstractRecordType, true
	}
	return g.genericSubstType_, true
}

// verifyCalleeAt fails, under substVerify, when a route lowers t, a call
// inside an instance whose record the instance's types resolve
// (calleeAt), as a call of another function than the one the record
// names there, or as a call dispatched where it runs.
func (g *gen) verifyCalleeAt(t *ast.Call, p *irQualPlan) {
	if !substVerify || t == nil || g.srcFA() == nil || !g.inInstance() {
		return
	}
	if rec := g.resolvedCallOf(t); rec == nil || rec.Callee.Kind != analysis.CalleeInterface {
		return
	}
	at := g.calleeAt(t)
	if at.Kind != analysis.CalleeImpl {
		return
	}
	if p.dispatch {
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("an instance's call the checker resolves to %s is lowered as a call dispatched where it runs", declLabel(at.Decl)))
		return
	}
	target, block := irQualPlanTarget(p)
	if target == nil {
		return
	}
	target = shellSource(target)
	same := at.Decl == target || sameDecl(at.Decl, target, at.Std, g.stdFuncIn(at.Std, target))
	if have, ok := at.Impl.(*ast.ImplBlock); same && ok && block != nil {
		same = sameImplBlock(have, block)
	}
	if !same {
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("an instance's call the checker resolves to %s is lowered as a call of %s", declLabel(at.Decl), declLabel(target)))
	}
}

// positionalCallOf is the record of a call the builder writes that passes
// its first args parameters of params in order, of the function ref names
// in the source (analysis.FileAnalysis.CalleeOf).
func (g *gen) positionalCallOf(ref ast.Node, args, params int) *analysis.ResolvedCall {
	rc := analysis.PositionalCall(args, params)
	rc.Callee = g.srcFA().CalleeOf(ref)
	return rc
}

// rewrittenCallOf is the record of a call the builder writes in place of
// t, with t's callee and its first args parameters of params passed in
// order.
func (g *gen) rewrittenCallOf(t *ast.Call, args, params int) *analysis.ResolvedCall {
	rc := analysis.PositionalCall(args, params)
	rc.Callee = g.calleeOf(t)
	return rc
}

// respelledCallOf is the record of a copy of t the builder writes with
// another owner, one a type parameter or a turbofish stands for (`Map` for
// `C` in `C.contains(c, x)`): t's record, qualified with q, the new
// owner's qualifier. The callee the call reaches and the placement stay
// the checker's. t's own record when q is the zero Qualifier (an owner no
// std module declares), and nil when t has none.
func (g *gen) respelledCallOf(t *ast.Call, q analysis.Qualifier) *analysis.ResolvedCall {
	rec := g.resolvedCallOf(t)
	if rec == nil || q.Kind == analysis.QualNone {
		return rec
	}
	out := *rec
	out.Callee.Qualifier = q
	return &out
}

// routeCallee is the callee of t as the checker classified it, for the
// routes that key on what a call is qualified with or found through
// (analysis.Callee.RouteOwner): the call's record, or for a call the
// builder writes with none, the classification of its callee.
func (g *gen) routeCallee(t *ast.Call) analysis.Callee {
	fa := g.srcFA()
	if fa == nil || t == nil {
		return analysis.Callee{}
	}
	rec := g.carriedCalls[t]
	if rec == nil {
		rec = fa.Calls[t]
	}
	if rec != nil && rec.Callee.RouteOwner().Kind != analysis.QualNone {
		return rec.Callee
	}
	// A copy of a written call (an instance's `Int.from_json` for
	// `T.from_json`) stands where the written call does, whose record
	// says what it is qualified with.
	if rec := g.resolvedCallOf(t); rec != nil && rec.Callee.Qualifier.Kind != analysis.QualNone {
		return rec.Callee
	}
	return fa.CalleeOf(t.Func)
}

// markUnchecked marks every node of n, code the builder writes and lowers
// without the checker having checked it: its calls have no record. Each
// caller is one such site, and
// TestUncheckedSynthesizedBodiesOnlyGoDown counts them (irbuild package
// doc, "Calls").
func (g *gen) markUnchecked(n ast.Node) {
	if g.unchecked == nil {
		g.unchecked = map[ast.Node]bool{}
	}
	ast.Inspect(n, func(m ast.Node) bool {
		if m != nil {
			g.unchecked[m] = true
		}
		return true
	})
}
