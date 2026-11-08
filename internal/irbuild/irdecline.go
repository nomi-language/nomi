package irbuild

// The first decline of each build attempt: the reason a body the IR did not
// lower names.
//
// # The hook fires at most once per attempt, and keeps the first observation
//
// `lower` recurses, so the innermost failing node answers first and the first
// observation is the reason the body STOPPED rather than the outermost frame
// that noticed it. `irDeclineOpen` is called at exactly two sites —
// `irScalarBuild` and `irTestBodyBuild` — and the census's `seen` is the guard.
// A hook that fired twice for one attempt, read by an observer that keeps the
// last observation, would report the wrong reason.
//
// # `irScalarBody` reports before any line check
//
// The statement-form check in `irScalarBody` runs before the lead loop's
// `emittableLine` and before the tail's, so a class reported from there is
// credited with bodies whose real blocker is the synthesized line behind it,
// most often a derive-synthesized body that can never retain.
// `irDeclineSynthMask` marks those.
//
// # Why a hook and a classifier rather than a call at each decline site
//
// `lower` has ten decline arms and `bl.call` eight, and a hand-placed call at
// each would miss the arms nobody remembered. So the funnel is wrapped once
// and the reason is RE-DERIVED from the node by `declineReason`, which re-asks
// the same cheap tests the arms make. The statement-level sites, which `lower`
// cannot see, set the census's `bodyWhy` and `irScalarBuild` reports it.
//
// PRODUCTION READS ONLY the census's `why`, the reason a refused `nomi build`
// names, and the Declines a host's DeclineHooks are handed. The census is the
// lowering's own (declineCensus, gen.dc), never package state, so lowerings on
// two goroutines keep two.

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// DeclineHooks observe one lowering's declines: Program.Declines for
// GenerateIR, and an argument for the stdlib case lowerings. Each lowering
// carries its own (a declineCensus), so two lowerings on two goroutines never
// share one. Both are nil unless a host or a test installs them.
type DeclineHooks struct {
	// Observed is called with the attempt's name and the FIRST decline
	// reason of that attempt.
	Observed func(fn, reason string)
	// At is called with each attempt's first decline, as Observed is, for a
	// host that reports declines at their source (`nomi check`). The
	// Decline's Line and Col are filled in after the call, when the failing
	// expression's frame unwinds, so the host keeps the pointer and reads
	// them once lowering is done.
	At func(*Decline)
}

// Decline is one body the builder declined: the attempt's name, the file it
// was lowering, the builder's first reason, and the position of the innermost
// expression that failed, or 0 when the decline was taken at a statement or
// the body as a whole.
type Decline struct {
	Fn, Path  string
	Line, Col int
	Reason    string
	// Once is the attempt name ("once n") of the `once` this body declined
	// for: a read of a `once` whose initializer did not lower, or that has no
	// representable type. Empty for any other decline. A host reports such a
	// decline at the `once`, whose own attempt says where it stopped.
	Once string
	// Pattern reports that Line and Col are a pattern's: the innermost
	// pattern the builder could not test, which is no expression.
	Pattern bool
}

// declineCensus is one lowering's decline bookkeeping: the open attempt's
// first decline, the expressions being lowered, and the hooks that observe
// it. Every gen a lowering builds through holds the same one (gen.dc), and
// nothing in it is package state, so concurrent lowerings stay apart.
type declineCensus struct {
	hooks DeclineHooks
	// watch records every attempt's decline in watched, by the file the
	// attempt lowered: GenerateIR's census does, which a kept lowering
	// records and a lowering of one file is compared with (perfile.go).
	watch   bool
	watched []*Decline
	// muted counts the nested builds (an instance closure, an isolated
	// program-std body) whose declines no hook and no watch sees.
	muted int

	fn   string
	seen bool
	// path is the file the open attempt is lowering, and cur the Decline
	// handed to the hooks for it, whose position `lower` fills.
	path string
	cur  *Decline
	// bodyWhy is `irScalarBody`'s reason, which it cannot report itself
	// because it answers nil rather than declining.
	bodyWhy string
	// why is the first decline of the open attempt, recorded whether or not
	// a hook is installed: it is the reason a refused `nomi build` names for
	// a body the IR did not lower. See irnative.go.
	why string
	// line and col are the source position of the node the first
	// expression decline was taken at, or 0.
	line, col int
	// failures counts the expressions that declined, and frames holds its
	// value as each expression being lowered began (`lowerExact`,
	// `lowerTypedOperand`). An expression one of whose operands declined
	// declines without lowering anything more (abandoned): the operand may
	// have left a partial region behind, a terminated block among it, and a
	// second route to the expression would build on it.
	failures int
	frames   []int
}

// newDeclineCensus is a lowering's census, observed by hooks.
func newDeclineCensus(hooks DeclineHooks) *declineCensus {
	return &declineCensus{hooks: hooks}
}

// observed is the hook told each attempt's first reason, or nil.
func (dc *declineCensus) observed() func(fn, reason string) {
	if dc.muted > 0 {
		return nil
	}
	return dc.hooks.Observed
}

// placing reports whether an attempt's first decline is kept as a Decline
// whose position `lower` fills: a host's At hook or the watch wants it.
func (dc *declineCensus) placing() bool {
	return dc.muted == 0 && (dc.hooks.At != nil || dc.watch)
}

// report hands d to the watch and the At hook.
func (dc *declineCensus) report(d *Decline) {
	if dc.watch {
		dc.watched = append(dc.watched, d)
	}
	if dc.hooks.At != nil {
		dc.hooks.At(d)
	}
}

// irDeclineOpen starts one build attempt in g's file.
func (g *gen) irDeclineOpen(name string) {
	path := ""
	if g.fa != nil {
		path = g.fa.FilePath
	}
	g.dc.begin(name, path)
}

// begin starts one build attempt named name in the file at path.
func (dc *declineCensus) begin(name, path string) {
	dc.fn, dc.seen, dc.why = name, false, ""
	dc.line, dc.col = 0, 0
	dc.path, dc.cur = path, nil
}

// note records the first decline of the open attempt.
func (dc *declineCensus) note(reason string) {
	if dc.why == "" {
		dc.why = reason
	}
	observed, placing := dc.observed(), dc.placing()
	if dc.seen || (observed == nil && !placing) {
		return
	}
	dc.seen = true
	if observed != nil {
		observed(dc.fn, reason)
	}
	if placing {
		dc.cur = &Decline{Fn: dc.fn, Path: dc.path, Reason: reason}
		dc.report(dc.cur)
	}
}

// noteOnce records the first decline of the open attempt as a read of the
// `once` named once ("once n"), whose own failure is the cause.
func (dc *declineCensus) noteOnce(reason, once string) {
	dc.note(reason)
	if dc.cur != nil && dc.cur.Reason == reason {
		dc.cur.Once = once
	}
}

// mute keeps the hooks and the watch from seeing what is noted until the
// function it answers is called.
func (dc *declineCensus) mute() (unmute func()) {
	dc.muted++
	return func() { dc.muted-- }
}

// declineAttempt is the open attempt's record, which a nested build sets
// aside and puts back.
type declineAttempt struct {
	fn, path, bodyWhy, why string
	seen                   bool
	cur                    *Decline
	line, col              int
}

// suspend sets the open attempt's record aside and mutes the hooks and the
// watch for a build nested in it that is no part of it (an instance
// closure, an isolated program-std body); the function it answers puts both
// back.
func (dc *declineCensus) suspend() (resume func()) {
	a := declineAttempt{dc.fn, dc.path, dc.bodyWhy, dc.why, dc.seen, dc.cur, dc.line, dc.col}
	dc.muted++
	return func() {
		dc.muted--
		dc.fn, dc.path, dc.bodyWhy, dc.why, dc.seen, dc.cur, dc.line, dc.col = a.fn, a.path, a.bodyWhy, a.why, a.seen, a.cur, a.line, a.col
	}
}

// aside runs build, a lowering nested in the open attempt that is no part
// of it, with the attempt's census set aside: what build notes reaches no
// hook, and the attempt's own record is as it was afterwards.
func (dc *declineCensus) aside(build func()) {
	resume := dc.suspend()
	// What build declines abandons none of the attempt's expressions.
	failures, frames := dc.failures, dc.frames
	dc.frames = nil
	defer func() {
		resume()
		dc.failures, dc.frames = failures, frames
	}()
	build()
}

// withdrawn records that fn, a body the builder retained, was withdrawn
// because it reads a `once` whose cell no module retained
// (irWithdrawUnforceableReads). It is outside any attempt: fn's own attempt
// lowered, so this is its only decline.
func (dc *declineCensus) withdrawn(fn string, read *ir.Ref) {
	once := "once " + read.Sym().Name()
	reason := "reads `" + once + "`, whose initializer did not lower"
	if observed := dc.observed(); observed != nil {
		observed(fn, reason)
	}
	if dc.placing() {
		pos := read.Pos()
		d := &Decline{Fn: fn, Path: pos.File(), Reason: reason, Once: once}
		if emittableLine(pos.Line()) {
			d.Line, d.Col = pos.Line(), pos.Col()
		}
		dc.report(d)
	}
}

// byFile is each watched decline's function and reason by its file, each
// file's sorted.
func (dc *declineCensus) byFile() map[string][]string {
	if len(dc.watched) == 0 {
		return nil
	}
	out := map[string][]string{}
	for _, d := range dc.watched {
		out[d.Path] = append(out[d.Path], d.Fn+": "+d.Reason)
	}
	for _, list := range out {
		slices.Sort(list)
	}
	return out
}

// irDeclineUnnamedReason is the reason an attempt records when it declined
// and no site named why. It is a compiler bug wherever it appears; the
// irbuild tests fail on it (irDeclineUnnamed).
const irDeclineUnnamedReason = "the builder declined without naming a reason (a compiler bug; please report it)"

// irDeclineUnnamed is a test-only hook, nil in production, called with the
// attempt's name whenever close finds a decline no site named. TestMain
// fails the irbuild package on any call. Only TestMain and tests that run
// one lowering at a time write it.
var irDeclineUnnamed func(fn string)

// close ends an attempt that declined. Every decline should have called
// note; one that did not would surface as a BLOCKED line reading "no decline
// reason recorded", so it is recorded here with a reason that says the
// builder is at fault, and the test hook is told.
func (dc *declineCensus) close() {
	if dc.why != "" {
		return
	}
	if irDeclineUnnamed != nil {
		irDeclineUnnamed(dc.fn)
	}
	dc.note(irDeclineUnnamedReason)
}

// irDeclineSynthMask marks a reason `irScalarBody` reported for a body whose
// LINES ARE SYNTHESIZED.
//
// THE STATEMENT-FORM CHECK RUNS BEFORE ANY LINE CHECK, so a class reported
// from `irScalarBody` is credited with bodies whose real blocker is the
// synthesized line behind it — the lead loop's `emittableLine` and the tail's
// both run later. Without this mark, derive-synthesized bodies that can never
// retain would be counted under a statement-form reason.
func irDeclineSynthMask(fd *ast.FuncDef) string {
	if fd == nil || fd.Body == nil {
		return ""
	}
	for _, s := range fd.Body.Stmts {
		if line, _ := nodePos(s); !emittableLine(line) {
			return " [MASKS a synthesized line]"
		}
	}
	return ""
}

// lower types the value it lowered and classifies every expression decline,
// whatever arm produced it. A hand-placed hook at each `return ir.NoTemp,
// kindInvalid` would miss the arms nobody remembered.
func (bl *irScalarBuilder) lower(n ast.Node) (ir.Temp, kind, bool, bool) {
	t, k, m, ok := bl.lowerExact(n)
	if ok && k == kindNever {
		// A value of Infallible takes the kind its position settled, as a
		// `todo` does (irnever.go).
		// kindInvalid: lookup — no settled kind leaves the value Infallible.
		if want := bl.g.neverSettledKind(n); want != kindInvalid {
			if v, retyped := bl.neverAs(n, t, k, want); retyped {
				t, k = v, want
			}
		}
	}
	return t, k, m, ok
}

// lowerExact is lower without retyping a value of Infallible: the operand of
// a construct that never runs because of it (irnever.go's neverOperand).
func (bl *irScalarBuilder) lowerExact(n ast.Node) (ir.Temp, kind, bool, bool) {
	n = bl.g.holeRead(n)
	if bl.g.dc.abandoned() {
		// An operand of an enclosing expression already declined, so that
		// expression declines too; another route to it (a constructor
		// retried as a call of its function value) would append to the
		// blocks the abandoned operand left, which it may have terminated.
		return ir.NoTemp, kindInvalid, false, false
	}
	defer bl.g.dc.frame()()
	planted := plantedDeclineAt(n)
	t, k := ir.NoTemp, kindInvalid
	var m, ok, handled bool
	if planted == "" {
		t, k, m, ok, handled = bl.neverOperand(n)
		if !handled {
			t, k, m, ok = bl.lowerNode(n)
		}
	}
	if ok && bl.g.dc.failures != bl.g.dc.frames[len(bl.g.dc.frames)-1] {
		// Something inside n declined, and n answered anyway by another
		// route: the declined operand's IR is still in n's blocks.
		ok = false
	}
	if !ok {
		bl.g.dc.failures++
	}
	if ok {
		bl.g.irTypeTemp(bl.f, t, k)
		bl.pipeValue(n, t)
		bl.stageReached(n)
	}
	if !ok && bl.g.dc.why == "" {
		if line, col := nodePos(n); emittableLine(line) {
			bl.g.dc.line, bl.g.dc.col = line, col
		}
		reason := planted
		if reason == "" {
			reason = bl.declineReason(n)
		}
		bl.g.dc.note(reason)
	}
	if cur := bl.g.dc.cur; !ok && cur != nil && cur.Line == 0 {
		// The innermost failing expression: where the decline was noted, or
		// the nearest enclosing expression of a decline noted at a node
		// `lower` does not see.
		if line, col := nodePos(n); emittableLine(line) {
			cur.Line, cur.Col = line, col
		}
	}
	return t, k, m, ok
}

// frame opens an expression whose routes abandoned answers for; the caller
// defers the function it returns.
func (dc *declineCensus) frame() (end func()) {
	dc.frames = append(dc.frames, dc.failures)
	return func() { dc.frames = dc.frames[:len(dc.frames)-1] }
}

// abandoned reports whether the innermost expression being lowered has had
// an operand decline.
func (dc *declineCensus) abandoned() bool {
	return len(dc.frames) > 0 && dc.failures != dc.frames[len(dc.frames)-1]
}

// atNode places the open attempt's decline at n when nothing inside it gave
// the decline a position: a statement-level construct (a block or branch
// bound to a name) that declined where `lower` did not see it is reported at
// the construct, not at the enclosing function.
func (dc *declineCensus) atNode(n ast.Node) {
	if dc.cur == nil || dc.cur.Line != 0 {
		return
	}
	if line, col := nodePos(n); emittableLine(line) {
		dc.cur.Line, dc.cur.Col = line, col
	}
}

// atPattern records reason as the open attempt's decline, unless one is
// already recorded, and places it at the pattern p unless something inside
// p already placed it. Every site that gives up on a pattern calls it, so the
// innermost pattern that failed is where the decline is reported: a pattern is
// tested outside `lower`, which places only expressions.
func (dc *declineCensus) atPattern(p ast.Node, reason string) {
	first := dc.why == ""
	dc.note(reason)
	line, col := nodePos(p)
	if !emittableLine(line) {
		return
	}
	if first {
		dc.line, dc.col = line, col
	}
	if dc.cur == nil || dc.cur.Line != 0 {
		return
	}
	dc.cur.Line, dc.cur.Col, dc.cur.Pattern = line, col, true
}

// declineReason names why n did not lower, re-deriving the cheap tests the
// arms make so the reason says the OPERATOR or the KIND rather than the node
// type.
func (bl *irScalarBuilder) declineReason(n ast.Node) string {
	if isNilNode(n) {
		return "a nil node"
	}
	if line, _ := nodePos(n); !bl.positionOK(line) {
		// The same test `lowerNode` makes (`bl.positionOK`), so the two cannot
		// drift: a stale copy here would attribute declines for unrelated
		// reasons to this row, because this function re-derives the reason
		// from the innermost node.
		return "a node on a synthesized line"
	}
	switch t := n.(type) {
	case *ast.Binary:
		if _, arithmetic := irArithOps[t.Op]; !arithmetic {
			if t.Op == "and" || t.Op == "or" {
				return "the short-circuit operator `" + t.Op + "`"
			}
			if _, isCompare := irCompareOps[t.Op]; isCompare {
				return "the comparison operator `" + t.Op + "`"
			}
			if t.Op == "|>" {
				return "the pipe operator `|>`"
			}
			return "the operator `" + t.Op + "`"
		}
		return "an arithmetic operand or its kind"
	case *ast.Ident:
		if _, held := bl.bound[t.Name]; held {
			return "a bound name (unreachable)"
		}
		l, bound := bl.g.lookup(t.Name)
		if !bound {
			return "an ident bound nowhere this builder can see"
		}
		return "an ident kind outside the domain: " + l.k.nomi()
	case *ast.TypeIdent:
		return "a type name in value position: " + t.Name
	case *ast.Is:
		return "an `is` test's value or pattern"
	case *ast.Ascription:
		return "an `as` ascription's value or type"
	case *ast.Unary:
		if t.Op != "-" && t.Op != "not" {
			return "the unary operator `" + t.Op + "`"
		}
		return "a unary operand or its kind"
	case *ast.StringLit:
		return "a String literal (unreachable)"
	case *ast.Call:
		return bl.declineCallReason(t)
	case *ast.StructLit:
		return "a struct literal: " + typeText(t.TypeName)
	case *ast.FieldAccess:
		name := "?"
		if t.Field != nil {
			name = t.Field.Name
		}
		switch o := t.Object.(type) {
		case *ast.Ident:
			if l, bound := bl.g.lookup(o.Name); bound {
				return "a field access on a local of kind " + l.k.nomi() + ": ." + name
			}
			return "a field access on an unbound name " + o.Name + ": ." + name
		case *ast.TypeIdent:
			return "a field access on the type name " + o.Name + ": ." + name
		}
		return "a field access on " + irNodeWord(t.Object) + ": ." + name
	}
	return irNodeWord(n)
}

// declineCallReason splits the call arm, which is the widest one.
func (bl *irScalarBuilder) declineCallReason(t *ast.Call) string {
	for _, a := range t.Args {
		if _, named := a.(*ast.NamedArg); named {
			return "a named argument"
		}
	}
	if fa, qualified := t.Func.(*ast.FieldAccess); qualified {
		if fa.Field == nil {
			return "a qualified callee with no field"
		}
		if bl.qualCalleeIsValue(fa) {
			return "a call through a value read by a field chain: ." + fa.Field.Name
		}
		switch obj := fa.Object.(type) {
		case *ast.TypeIdent:
			return "qualified call, Type.method: " + obj.Name + "." + fa.Field.Name
		case *ast.Ident:
			if irQualIsLocal(bl, obj.Name) {
				return "qualified call through a local: " + obj.Name + "." + fa.Field.Name
			}
			return "qualified call, file.fn: " + obj.Name + "." + fa.Field.Name
		}
		return "qualified call with a 3-segment qualifier"
	}
	callee, direct := t.Func.(*ast.Ident)
	if !direct {
		if isType2, isType := t.Func.(*ast.TypeIdent); isType {
			return "a constructor call the distinct arms declined: " + isType2.Name
		}
		return "a callee that is not a name: " + irNodeWord(t.Func)
	}
	if _, shadowed := bl.g.lookup(callee.Name); shadowed {
		return "a call through a value: " + callee.Name
	}
	if bl.g.stdlibSibling(callee.Name) != nil {
		return "a direct callee that is also a stdlib sibling: " + callee.Name
	}
	sig := bl.g.funcs[callee.Name]
	switch {
	case sig == nil:
		return "a direct callee outside this module's fn table: " + callee.Name
	case !sig.lowerable || sig.decl == nil:
		return "a direct callee that is not lowerable: " + callee.Name
	case sig.dict != nil || sig.tps != nil:
		return "a generic direct callee: " + callee.Name
	case len(sig.params) != len(t.Args):
		return "a direct callee with an omitted default: " + callee.Name
	case !irRetainedValueKind(sig.result) && sig.result != kindUnit:
		return "a direct callee's result kind outside the domain: " + sig.result.nomi()
	}
	return "a direct call's operand: " + callee.Name
}

// irNodeWord names a syntax node's kind in a decline reason, as the parser
// names it (`Break`, `Lambda`), without the Go type that holds it.
func irNodeWord(n ast.Node) string {
	if isNilNode(n) {
		return "nothing"
	}
	return "a " + n.NodeType()
}
