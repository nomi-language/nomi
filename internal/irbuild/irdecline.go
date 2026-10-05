package irbuild

// The first decline of each build attempt: the reason a body the IR did not
// lower names.
//
// # The hook fires at most once per attempt, and keeps the first observation
//
// `lower` recurses, so the innermost failing node answers first and the first
// observation is the reason the body STOPPED rather than the outermost frame
// that noticed it. `irDeclineOpen` is called at exactly two sites —
// `irScalarBuild` and `irTestBodyBuild` — and `irDeclineSeen` is the guard.
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
// cannot see, set `irDeclineBodyWhy` and `irScalarBuild` reports it.
//
// PRODUCTION READS ONLY `irDeclineWhy`, the reason a refused `nomi build`
// names. `IRDeclineObserved` is nil unless a test installs it, and
// `irDeclineOpen`/`irDeclineNote` are two assignments and a branch.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// IRDeclineObserved is called with the attempt's name and the FIRST decline
// reason of that attempt. Nil in production.
//
// EXPORTED, unlike the package's three other observation hooks, and the reason
// is the tour. A tour block's id is `chapter:L<line>` and only the markdown
// holds the code, so the staging lives in `internal/vm`'s test package —
// `vmPathResolver` — so the tour's decline reasons are taken there and the
// hook has to cross the package boundary. `irbuild` is an internal package
// and `Analyze`/`Generate` are exported for that same consumer.
var IRDeclineObserved func(fn, reason string)

// Decline is one body the builder declined: the attempt's name, the file it
// was lowering, the builder's first reason, and the position of the innermost
// expression that failed, or 0 when the decline was taken at a statement or
// the body as a whole.
type Decline struct {
	Fn, Path  string
	Line, Col int
	Reason    string
}

// IRDeclineAt is called with each attempt's first decline, as
// IRDeclineObserved is, for a host that reports declines at their source
// (`nomi check`). The Decline's Line and Col are filled in after the call,
// when the failing expression's frame unwinds, so the host keeps the
// pointer and reads them once lowering is done. Nil unless a host installs it.
var IRDeclineAt func(*Decline)

var (
	irDeclineFn   string
	irDeclineSeen bool
	// irDeclinePath is the file the open attempt is lowering, and irDeclineCur
	// the Decline handed to IRDeclineAt for it, whose position `lower` fills.
	irDeclinePath string
	irDeclineCur  *Decline
	// irDeclineBodyWhy is `irScalarBody`'s reason, which it cannot report
	// itself because it answers nil rather than declining.
	irDeclineBodyWhy string
	// irDeclineWhy is the first decline of the open attempt, recorded whether
	// or not a hook is installed: it is the reason a refused `nomi build`
	// names for a body the IR did not lower. See irnative.go.
	irDeclineWhy string
	// irDeclineLine and irDeclineCol are the source position of the node the
	// first expression decline was taken at, or 0.
	irDeclineLine, irDeclineCol int
)

// irDeclineOpen starts one build attempt in g's file.
func (g *gen) irDeclineOpen(name string) {
	irDeclineFn, irDeclineSeen, irDeclineWhy = name, false, ""
	irDeclineLine, irDeclineCol = 0, 0
	irDeclinePath, irDeclineCur = "", nil
	if g.fa != nil {
		irDeclinePath = g.fa.FilePath
	}
}

// irDeclineNote records the first decline of the open attempt.
func irDeclineNote(reason string) {
	if irDeclineWhy == "" {
		irDeclineWhy = reason
	}
	if irDeclineSeen || (IRDeclineObserved == nil && IRDeclineAt == nil) {
		return
	}
	irDeclineSeen = true
	if IRDeclineObserved != nil {
		IRDeclineObserved(irDeclineFn, reason)
	}
	if IRDeclineAt != nil {
		irDeclineCur = &Decline{Fn: irDeclineFn, Path: irDeclinePath, Reason: reason}
		IRDeclineAt(irDeclineCur)
	}
}

// irDeclineUnnamedReason is the reason an attempt records when it declined
// and no site named why. It is a compiler bug wherever it appears; the
// irbuild tests fail on it (irDeclineUnnamed).
const irDeclineUnnamedReason = "the builder declined without naming a reason (a compiler bug; please report it)"

// irDeclineUnnamed is a test-only hook, nil in production, called with the
// attempt's name whenever irDeclineClose finds a decline no site named.
// TestMain fails the irbuild package on any call.
var irDeclineUnnamed func(fn string)

// irDeclineClose ends an attempt that declined. Every decline should have
// called irDeclineNote; one that did not would surface as a BLOCKED line
// reading "no decline reason recorded", so it is recorded here with a reason
// that says the builder is at fault, and the test hook is told.
func irDeclineClose() {
	if irDeclineWhy != "" {
		return
	}
	if irDeclineUnnamed != nil {
		irDeclineUnnamed(irDeclineFn)
	}
	irDeclineNote(irDeclineUnnamedReason)
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
	t, k, m, ok := bl.lowerNode(n)
	if ok {
		bl.g.irTypeTemp(bl.f, t, k)
	}
	if !ok && irDeclineWhy == "" {
		if line, col := nodePos(n); emittableLine(line) {
			irDeclineLine, irDeclineCol = line, col
		}
		irDeclineNote(bl.declineReason(n))
	}
	if !ok && irDeclineCur != nil && irDeclineCur.Line == 0 {
		// The innermost failing expression: where the decline was noted, or
		// the nearest enclosing expression of a decline noted at a node
		// `lower` does not see.
		if line, col := nodePos(n); emittableLine(line) {
			irDeclineCur.Line, irDeclineCur.Col = line, col
		}
	}
	return t, k, m, ok
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
	case *ast.Unary:
		if t.Op != "-" && t.Op != "!" {
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
	if len(t.TypeArgs) != 0 {
		return "a turbofish on a call"
	}
	for _, a := range t.Args {
		if _, named := a.(*ast.NamedArg); named {
			return "a named argument"
		}
	}
	if fa, qualified := t.Func.(*ast.FieldAccess); qualified {
		if fa.Field == nil {
			return "a qualified callee with no field"
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
