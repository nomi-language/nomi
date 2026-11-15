package analysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// Checking values into refinements (refinement.go). A literal of the base
// written where a distinct refinement is expected is that refinement, and
// the compiler evaluates the rules on it when it builds the program; any
// other value of the base is an error that names `try_from`.

// RefinedLiteral is a literal checked as a refinement
// (FileAnalysis.RefinedLiterals).
type RefinedLiteral struct {
	// Type is the refinement the literal is.
	Type Type
	// Steps are the refinements whose rules the literal must satisfy, in
	// the order RefinementChecks gives, Type last: the order a diagnostic
	// finds the first rule that fails in. A struct refinement is its own
	// one step.
	Steps []RefinementStep
}

// RefinementStep is one refinement a RefinedLiteral is checked against and
// its generated `try_from`, whose Some or None says whether the literal
// satisfies its rule and every rule it refines.
type RefinementStep struct {
	Type    Type
	TryFrom ast.Node
	// Check is set for a generic refinement, whose `try_from` is
	// instantiated per type arguments: the call `Type.try_from(v)` at the
	// literal's type arguments, checked as written in the literal's file,
	// whose argument is the identifier RefinementLiteralName standing for
	// the literal's value (refinementCheckCall). Nil otherwise, and where
	// the refinement's name does not reach it from the literal's file.
	Check ast.Node
	// Via are the refinements between the literal's type and Type, along
	// the first path of parents that reaches it: for `Top` over `Mid` over
	// `Low`, the step Low has Via [Mid]. Empty for Type itself and its
	// parents.
	Via []Type
}

// refinementLiteral reports whether n, ungrouped, is a literal a refinement
// position checks at compile time: a number, a negated number, a string
// with no `${...}`, a codepoint, `True` or `False`, or a typed literal with
// no `${...}` (`Duration"15m"`), whose value the compiler builds already.
func refinementLiteral(n ast.Node) bool {
	switch v := ungroupExpr(n).(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.StringLit, *ast.CodepointLit:
		return true
	case *ast.TaggedString:
		for _, part := range v.Parts {
			if _, slot := part.(*ast.StringExpr); slot {
				return false
			}
		}
		return true
	case *ast.Unary:
		if v.Op != "-" {
			return false
		}
		switch v.Right.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
			return true
		}
	case *ast.TypeIdent:
		return v.Name == "True" || v.Name == "False"
	}
	return false
}

// checkRefinementLiteral checks node, a literal, where the refinement dt is
// expected: against dt's base, as the literal it is, and records it for the
// compile-time check of dt's rules (FileAnalysis.RefinedLiterals). The
// literal is a dt.
func (c *checker) checkRefinementLiteral(node ast.Node, dt *DistinctType) Type {
	base := refinementBaseOf(dt)
	if refinementOf(base) != nil {
		// The chain reaches no base: a parent that names no type, or a
		// declaration cycle, each reported where it is declared. Checking
		// the literal against the base would expect dt again, so it is
		// checked as itself, and no rule runs on it.
		c.checkNode(node)
		return dt
	}
	ty := c.checkNodeExpecting(node, base)
	if ty != nil && !c.argMatchesParam(ty, base, c.recPos(startOf(node)), RecordingKindCallSite) {
		line, col := startOf(node)
		c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(dt), formatTypeForError(ty)))
		return dt
	}
	c.recordRefinedLiteral(node, dt)
	return dt
}

// recordRefinedLiteral notes that the literal node is checked as the
// refinement t, and makes a hover on it say so.
func (c *checker) recordRefinedLiteral(node ast.Node, t Type) {
	if c.fa.RefinedLiterals == nil {
		c.fa.RefinedLiterals = map[ast.Node]*RefinedLiteral{}
	}
	rec := &RefinedLiteral{Type: t}
	// Each parent's rules before t's own (RefinementChecks), so the first
	// rule that fails is the one the diagnostic names.
	for i, check := range RefinementChecks(t) {
		step := RefinementStep{Type: check, Via: refinementVia(t, check)}
		dt := refinementOf(check)
		if dt == nil || len(dt.TypeArgs) == 0 {
			// A checked `type`'s `try_from` is its TryFrom impl's, which a
			// call written in this file reaches (refinementCheckCall).
			dt = checkedDistinctOf(check)
		} else if dt.Intersection {
			dt = nil
		}
		if dt != nil && (len(dt.TypeArgs) > 0 || !dt.Subtype) {
			step.Check = c.refinementCheckCall(node, dt, i)
			if step.Check == nil {
				line, col := startOf(node)
				c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
					"this %s literal cannot be checked when the program is compiled: its rule is checked through `%s.try_from`, and `%s` is not named in this file",
					formatTypeForError(t), dt.Name, dt.Name)}.WithHint(fmt.Sprintf(
					"import `%s` into this file, or build the value with `%s.try_from(...)`", dt.Name, dt.Name)))
				return
			}
		} else if origin, nominal := nominalOrigin(check); nominal {
			if sym := c.lookupTypeMethodSymbol(check.String(), "try_from", origin); sym != nil {
				step.TryFrom = sym.Node
			}
		}
		rec.Steps = append(rec.Steps, step)
	}
	c.fa.RefinedLiterals[node] = rec
	line, col := startOf(node)
	switch node.(type) {
	case *ast.StructLit, *ast.ListLit, *ast.MapLit:
		// A literal of many tokens has the hover of its type.
		return
	}
	if line <= 0 || IsSynthesizedLine(line) {
		return
	}
	c.fa.References[Pos{Line: line, Col: col}] = &Symbol{
		Name: refinementLiteralText(node),
		Kind: SymbolLiteral,
		Pos:  Pos{Line: line, Col: col},
		Type: t,
		Doc:  fmt.Sprintf("checked as %s %s when the program is compiled: `%s`", ArticleFor(typeName(t)), typeName(t), RefinementRule(t)),
		Span: len(refinementLiteralText(node)),
	}
}

// RefinementLiteralName is the identifier a generic refinement's check
// call (RefinementStep.Check) passes the literal's value through: a name
// no source can write.
const RefinementLiteralName = "refinement literal"

// refinementCheckCall is the call `Name.try_from(v)` that checks the
// literal node against the generic refinement dt, its step-th check, at
// dt's type arguments, typed as a call written in this file would be, with
// v (RefinementLiteralName) dt's base. Its nodes stand at the literal's
// line and at columns below zero, unique per literal and step, where no
// source token is, so its records collide with none. Nil when dt's name
// does not reach dt here, or the call does not check.
func (c *checker) refinementCheckCall(node ast.Node, dt *DistinctType, step int) ast.Node {
	if c.fa == nil || dt.Name == "" || dt.Intersection {
		return nil
	}
	if local, ok := c.reg.Lookup(dt.Name).(*DistinctType); !ok || !sameNominalIdentity(local.Origin, local.Name, dt.Origin, dt.Name) {
		return nil
	}
	line, col := startOf(node)
	if line <= 0 || IsSynthesizedLine(line) {
		return nil
	}
	at := -(col*64 + step*4)
	owner := &ast.TypeIdent{Name: dt.Name, Line: line, Col: at - 1}
	value := identExpr(RefinementLiteralName, line, at-3)
	call := &ast.Call{Func: fieldAccess(owner, "try_from", line, at-2), Args: []ast.Node{value}, Line: line, Col: at}
	typeSym := c.fa.ModuleScope.Lookup(dt.Name)
	if typeSym == nil {
		return nil
	}
	c.fa.References[Pos{Line: line, Col: at - 1}] = typeSym
	base := refinementBaseOf(dt)
	c.fa.References[Pos{Line: line, Col: at - 3}] = &Symbol{Name: RefinementLiteralName, Kind: SymbolBinding, Type: base, Pos: Pos{Line: line, Col: at - 3}}
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	got := c.checkNode(call)
	failed := len(c.errors) > mark
	c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
	if failed || got == nil {
		return nil
	}
	return call
}

// RefinementRuleCheck is a generic refinement's rule as its declaration
// checks it for effects (FileAnalysis.RefinementRuleChecks). Type is the
// refinement. Tries are the calls `Name.try_from(v)` its declaration
// typed, each at one set of type arguments, in order; the IR builder
// lowers each. Empty when none checks;
// vmhost still checks every impl of its bounds' interfaces and every
// instance of its `try_from` the program has.
type RefinementRuleCheck struct {
	Type  *DistinctType
	Tries []RefinementRuleTry
}

// RefinementRuleTry is one call of a RefinementRuleCheck, whose argument
// is the identifier RefinementLiteralName, of type Arg.
type RefinementRuleTry struct {
	Check ast.Node
	Arg   Type
}

// ruleWitnesses are the types tried, in order, as every type argument of a
// generic refinement whose rule its declaration checks for effects, after
// its bounds' interfaces: one of them meets most bounds a rule needs
// (Comparable, Hashable, Display).
func ruleWitnesses() []Type {
	return []Type{TypeInt, TypeString, TypeFloat, TypeBool, TypeUnit}
}

// boundWitness is the type that stands for tp at a generic refinement's
// declaration check: a value of its bound's interface, or of the
// conjunction of its bounds, which any implementor can be, or Int for a
// parameter with no bound.
func boundWitness(tp *TypeParam_) Type {
	switch len(tp.Bounds) {
	case 0:
		return TypeInt
	case 1:
		return tp.Bounds[0]
	}
	return NewConjunction(tp.Bounds)
}

// recordRefinementRuleCheck types, for the generic refinement n, the call
// `Name.try_from(v)` at the type arguments its declaration is checked at,
// and records them (FileAnalysis.RefinementRuleChecks). The IR builder
// lowers each and vmhost refuses the rule when one can reach an effect, so
// a rule is checked whether or not a literal of it is, as a non-generic
// one is through its `try_from`.
//
// The first try gives each parameter the interface of its bounds
// (boundWitness), which meets them whichever they are, so a rule whose
// bounds only the program's own types meet is checked too. A call of a
// bound's function on a value of interface type dispatches, and the VM's
// effect walk reaches every implementation of a dispatched function the
// program has. The second gives every parameter the first of
// ruleWitnesses that checks, for a rule whose first instance the machine
// cannot run (`a <= b` on two interface values). vmhost
// adds every impl of a bound's interface and every instance of the rule's
// `try_from` the program has (irbuild.RefinementRuleSite).
//
// Each try's nodes stand at the `when` keyword's line, at columns below
// every literal check's (refinementCheckCall).
func (c *checker) recordRefinementRuleCheck(n *ast.TypeDef) {
	if c.fa == nil || n.Rule == nil || len(n.TypeParams) == 0 || n.RuleLine <= 0 || IsSynthesizedLine(n.RuleLine) {
		return
	}
	dt := refinementOf(c.reg.Lookup(n.Name))
	if dt == nil {
		dt = checkedDistinctOf(c.reg.Lookup(n.Name))
	}
	if dt == nil || dt.Inner == nil || len(dt.TypeParamDefs) == 0 || refinementOf(refinementBaseOf(dt)) != nil {
		return
	}
	if strings.HasPrefix(dt.Origin, "std/") {
		// The IR builder checks the rules of a program's own files only.
		return
	}
	typeSym := c.fa.ModuleScope.Lookup(n.Name)
	if typeSym == nil {
		return
	}
	rc := &RefinementRuleCheck{Type: dt}
	if c.fa.RefinementRuleChecks == nil {
		c.fa.RefinementRuleChecks = map[*ast.TypeDef]*RefinementRuleCheck{}
	}
	c.fa.RefinementRuleChecks[n] = rc
	line := n.RuleLine
	try := func(i int, subs map[*TypeParam_]Type) bool {
		arg := Substitute(refinementBaseOf(dt), subs)
		at := -(1<<20 + n.RuleCol*64 + i*4)
		owner := &ast.TypeIdent{Name: n.Name, Line: line, Col: at - 1}
		value := identExpr(RefinementLiteralName, line, at-3)
		call := &ast.Call{Func: fieldAccess(owner, "try_from", line, at-2), Args: []ast.Node{value}, Line: line, Col: at}
		c.fa.References[Pos{Line: line, Col: at - 1}] = typeSym
		c.fa.References[Pos{Line: line, Col: at - 3}] = &Symbol{Name: RefinementLiteralName, Kind: SymbolBinding, Type: arg, Pos: Pos{Line: line, Col: at - 3}}
		mark, undeterminedMark := len(c.errors), len(c.undetermined)
		got := c.checkNode(call)
		failed := len(c.errors) > mark
		c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
		if failed || got == nil {
			return false
		}
		rc.Tries = append(rc.Tries, RefinementRuleTry{Check: call, Arg: arg})
		return true
	}
	at := func(each func(*TypeParam_) Type) map[*TypeParam_]Type {
		subs := map[*TypeParam_]Type{}
		for _, tp := range dt.TypeParamDefs {
			subs[tp] = each(tp)
		}
		// A bound may name another parameter (`T: Into<U>`).
		for tp, t := range subs {
			subs[tp] = Substitute(t, subs)
		}
		return subs
	}
	i := 0
	try(i, at(boundWitness))
	bounded := slices.ContainsFunc(dt.TypeParamDefs, func(tp *TypeParam_) bool { return len(tp.Bounds) > 0 })
	for _, witness := range ruleWitnesses() {
		if !bounded {
			// The first try was at Int already.
			break
		}
		i++
		if try(i, at(func(*TypeParam_) Type { return witness })) {
			break
		}
	}
}

// refinementVia answers the refinements strictly between from and its
// ancestor to, along the first path of parents that reaches it, parents
// taken in the order they are written.
func refinementVia(from, to Type) []Type {
	if checked := checkedDistinctOf(from); checked != nil {
		// A type over a subtype: the path starts at what it wraps.
		inner := DistinctInner(checked)
		if TypesIdentical(inner, to) || TypesIdentical(from, to) {
			return nil
		}
		return append([]Type{inner}, refinementVia(inner, to)...)
	}
	var path []Type
	var walk func(t Type, depth int) bool
	walk = func(t Type, depth int) bool {
		if depth > maxRefinementChain {
			return false
		}
		for _, p := range RefinementParents(t) {
			if TypesIdentical(p, to) {
				return true
			}
			path = append(path, p)
			if walk(p, depth+1) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	if TypesIdentical(from, to) || !walk(from, 0) {
		return nil
	}
	return path
}

// typeName is t's name as a diagnostic spells it.
func typeName(t Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}

// refinementLiteralText is how a diagnostic and a hover spell the literal.
func refinementLiteralText(n ast.Node) string {
	return RenderLiteral(n)
}

// RenderLiteral spells a literal as the source writes it.
func RenderLiteral(n ast.Node) string {
	return strings.Join(strings.Fields(format.RenderNode(n)), " ")
}

// refinementCtorArg checks the one argument of a distinct refinement's call
// form, `Port(8080)`: a literal is checked as a dt; any other value is an
// error naming `try_from`, since its rules would have to run when the
// program does.
func (c *checker) refinementCtorArg(arg ast.Node, dt *DistinctType, label string) {
	if refinementLiteral(arg) {
		c.checkRefinementLiteral(arg, dt)
		return
	}
	switch ungroupExpr(arg).(type) {
	case *ast.ListLit, *ast.MapLit:
		// `Pair([3, 4])`: a collection literal builds the refinement as
		// it does where one is expected, and checkCollectionRefinementLit
		// records it or reports its first part that is no literal.
		argTy := c.checkNodeExpecting(arg, dt)
		if argTy != nil && !TypeAssignable(dt, argTy) {
			line, col := startOf(arg)
			c.report(refinementNeedsCheck(line, col, dt, argTy, label+"(...)"))
		}
		return
	}
	argTy := c.checkNode(arg)
	if argTy != nil && TypeAssignable(dt, argTy) {
		// `Port(port)`: already a Port.
		return
	}
	line, col := startOf(arg)
	c.report(refinementNeedsCheck(line, col, dt, argTy, label+"(...)"))
}

// checkedKindText names what dt is in a diagnostic: "a subtype" or "a
// validated type".
func checkedKindText(dt *DistinctType) string {
	if dt.Subtype {
		return "a subtype"
	}
	return "a validated type"
}

// checkedCtorArg checks the one argument of the call form of a checked
// distinct type (checkedDistinctOf), `Email("a@b.c")` or `UserId(5)`: a
// literal is checked against the type's rules when the program is
// compiled, a value already of a `type`'s inner subtype passes when the
// type adds no rule, and any other value is an error naming `try_from`.
func (c *checker) checkedCtorArg(arg ast.Node, dt *DistinctType, label string) {
	inner := DistinctInner(dt)
	if refinementLiteral(arg) {
		c.checkRefinementLiteral(arg, dt)
		return
	}
	switch ungroupExpr(arg).(type) {
	case *ast.ListLit, *ast.MapLit:
		if dt.Rule == "" {
			// `Ids([1, 2])` over `NonEmptyList<Int>`: the literal is the
			// inner subtype's, checked as one.
			if argTy := c.checkNodeExpecting(arg, inner); argTy != nil && !TypeAssignable(inner, argTy) {
				line, col := startOf(arg)
				c.report(checkedNeedsCheck(line, col, dt, argTy, label, arg))
			}
			return
		}
		if bad := nonLiteralPart(arg); bad != nil {
			line, col := startOf(bad)
			c.report(checkedNeedsCheck(line, col, dt, nil, label, arg))
			c.checkNode(arg)
			return
		}
		c.checkNodeExpecting(arg, refinementBaseOf(dt))
		c.recordRefinedLiteral(arg, dt)
		return
	}
	argTy := c.checkNode(arg)
	if argTy == nil {
		return
	}
	if dt.Rule == "" && TypeAssignable(inner, argTy) {
		// `UserId(p)` with p a PositiveInt: already checked.
		return
	}
	line, col := startOf(arg)
	c.report(checkedNeedsCheck(line, col, dt, argTy, label, arg))
}

// checkedNeedsCheck is the error for a computed value given to the call
// form of a checked distinct type: it names `T.try_from` on the argument.
func checkedNeedsCheck(line, col int, dt *DistinctType, got Type, label string, arg ast.Node) TypeError {
	argText := RenderLiteral(arg)
	inner := formatTypeForError(DistinctInner(dt))
	var msg string
	switch {
	case dt.Rule != "":
		what := "a value"
		if got != nil {
			what = ArticleFor(formatTypeForError(got)) + " " + formatTypeForError(got)
		}
		msg = fmt.Sprintf("%s(...) takes a literal: %s is a validated type, and %s computed when the program runs may not satisfy its rule `%s`",
			label, dt.Name, what, dt.Rule)
	default:
		msg = fmt.Sprintf("%s(...) takes a literal or %s %s: %s wraps the subtype %s, and %s %s computed when the program runs may not be one",
			label, ArticleFor(inner), inner, dt.Name, inner, ArticleFor(formatTypeForError(got)), formatTypeForError(got))
	}
	return TypeError{Line: line, Col: col, Message: msg}.WithHint(fmt.Sprintf(
		"check the value with `%s.try_from(%s)`, a `Maybe<%s>`", dt.Name, argText, formatTypeForError(dt)))
}

// refinementNeedsCheck is the error for a value of a refinement's base, or
// of an ancestor, where the refinement is expected: its rules are checked
// only on a literal, so the value goes through `try_from`.
func refinementNeedsCheck(line, col int, dt *DistinctType, got Type, what string) TypeError {
	msg := fmt.Sprintf("%s takes a literal: %s is a subtype, and %s %s computed when the program runs may not satisfy its rule",
		what, dt.Name, ArticleFor(formatTypeForError(got)), formatTypeForError(got))
	if what == "" {
		msg = fmt.Sprintf("type mismatch: expected %s, got %s", dt.Name, formatTypeForError(got))
	}
	return TypeError{Line: line, Col: col, Message: msg}.WithHint(fmt.Sprintf(
		"check the value with `%s.try_from(...)`, which is `Some` when it satisfies `%s` and `None` when not", dt.Name, dt.Rule))
}

// refinementMismatchHint is the help for `type mismatch: expected Port, got
// Int`: a value of a refinement's base or ancestor goes through try_from.
func refinementMismatchHint(want, have Type) (string, bool) {
	if cd := checkedDistinctOf(want); cd != nil && have != nil && !containsTypeVar(have) {
		if base := refinementBaseOf(cd); TypesIdentical(base, have) || TypesIdentical(DistinctInner(cd), have) {
			return fmt.Sprintf("%s is a new type, which nothing converts to; build one with `%s(...)` from a literal, or check a value with `%s.try_from(...)`",
				cd.Name, cd.Name, cd.Name), true
		}
	}
	dt := refinementOf(want)
	if dt == nil || have == nil || containsTypeVar(have) {
		return "", false
	}
	if dt.Intersection {
		return fmt.Sprintf("a value is %s when it is each of them: check it with `try_from` on each, or pass a subtype below all of them",
			dt.Name), true
	}
	for _, anc := range RefinementAncestors(dt) {
		if TypesIdentical(anc, have) {
			return fmt.Sprintf("%s is a subtype of %s; check the value with `%s.try_from(...)`, which is `Some` when it satisfies `%s` and `None` when not",
				dt.Name, refinementParentsText(dt), dt.Name, dt.Rule), true
		}
	}
	return "", false
}

// checkUncheckedRefinement types a generated `unchecked`'s body: the value,
// of the refinement's base, as the refinement.
func (c *checker) checkUncheckedRefinement(n *ast.UncheckedRefinement) Type {
	t := c.reg.Lookup(n.Refinement)
	if dt, ok := t.(*DistinctType); ok && len(dt.TypeParamDefs) > 0 {
		// A generic refinement at fresh type arguments, which the value
		// solves: `NonEmptyList.unchecked(xs)` with xs a List<Int> is a
		// NonEmptyList<Int>.
		t = c.freshDistinct(dt)
	}
	base := refinementBaseOf(t)
	if base == nil {
		c.checkNode(n.Value)
		return nil
	}
	have := c.checkNodeExpecting(n.Value, base)
	if have != nil && c.unifyInto(base, have, nil) != nil {
		line, col := startOf(n.Value)
		c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(base), formatTypeForError(have)))
	}
	solved := resolveSolvedVars(t)
	if dt, ok := solved.(*DistinctType); ok && !dt.Subtype && len(dt.TypeArgs) > 0 && c.fa != nil {
		// A generic validated type is built by its call form at the
		// arguments solved here, which the IR builder reads at the
		// node's position as it reads a written call's callee.
		if sym := c.fa.ModuleScope.Lookup(n.Refinement); sym != nil {
			site := *sym
			site.CallType = &FuncType{Params: []Type{resolveSolvedVars(base)}, Return: solved}
			c.fa.References[Pos{Line: n.Line, Col: n.Col}] = &site
		}
	}
	return solved
}

// checkRefinementRule types a generated `try_from`'s call of its rule: the
// rule, a `(P) -> Bool`, applied to the value, a P, is a Bool.
func (c *checker) checkRefinementRule(n *ast.RefinementRule) Type {
	rule := c.checkNode(n.Rule)
	ft, ok := rule.(*FuncType)
	if !ok || len(ft.Params) != 1 {
		c.checkNode(n.Value)
		return TypeBool
	}
	have := c.checkNodeExpecting(n.Value, ft.Params[0])
	if dt := refinementOf(have); dt != nil && dt.Inner == nil {
		// A refinement a parent cycle left with no parent, reported at
		// its declaration.
		return TypeBool
	}
	if have != nil && c.unifyInto(ft.Params[0], have, nil) != nil {
		line, col := startOf(n.Value)
		c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(ft.Params[0]), formatTypeForError(have)))
	}
	return TypeBool
}

// checkRefinementParentCheck types a generated `try_from`'s check against
// the refinement's parent: the parent's own `try_from` when the parent is a
// refinement, and `Some(value)` when it is the base. Only the one that
// applies is checked, and which one is recorded for the IR builder.
func (c *checker) checkRefinementParentCheck(n *ast.RefinementParentCheck, expected Type) Type {
	refined := false
	if n.Parent != nil && n.Refined != nil {
		if parent, err := c.resolveBodyType(n.Parent); err == nil && refinementOf(parent) != nil {
			refined = true
			// A parent over another base is reported at the declaration
			// (checkRefinementParents); its check is not typed again here.
			if call, ok := n.Refined.(*ast.Call); ok && len(call.Args) == 1 {
				if have := c.checkNode(call.Args[0]); have != nil && !TypesIdentical(RefinementBase(parent), RefinementBase(have)) {
					refined = false
				}
			}
		}
	}
	if c.fa.RefinementParents == nil {
		c.fa.RefinementParents = map[*ast.RefinementParentCheck]bool{}
	}
	c.fa.RefinementParents[n] = refined
	if refined {
		return c.checkNodeExpecting(n.Refined, expected)
	}
	return c.checkNodeExpecting(n.Plain, expected)
}

// RefinementParentChoice answers the expression a generated `try_from`'s
// parent check stands for (checkRefinementParentCheck).
func RefinementParentChoice(fa *FileAnalysis, n *ast.RefinementParentCheck) ast.Node {
	if fa != nil && fa.RefinementParents[n] {
		return n.Refined
	}
	return n.Plain
}

// refinementPipedArg checks the piped value of `8080 |> Port()`, already
// typed as pipedTy, as refinementCtorArg checks a prefix call's argument.
func (c *checker) refinementPipedArg(piped ast.Node, pipedTy Type, dt *DistinctType, label string) {
	if pipedTy == nil {
		return
	}
	if TypeAssignable(dt, pipedTy) {
		return
	}
	line, col := startOf(piped)
	if refinementLiteral(piped) {
		if base := refinementBaseOf(dt); !c.argMatchesParam(pipedTy, base, c.recPos(line, col), RecordingKindCallSite) {
			c.addError(line, col, fmt.Sprintf("type mismatch: expected %s, got %s", formatTypeForError(base), formatTypeForError(pipedTy)))
			return
		}
		c.recordRefinedLiteral(piped, dt)
		return
	}
	c.report(refinementNeedsCheck(line, col, dt, pipedTy, label+"()"))
}

// validateRefinementDecls reports the refinement declarations among a
// file's nodes that the language rules out: an opaque one, whose point is
// that its base is visible; a generic struct refinement; one written inside
// a body; and an
// enum variant that embeds one (spec §4 *Subtypes*).
func (b *builder) validateRefinementDecls(nodes []ast.Node) {
	add := func(line, col int, msg string) {
		b.file.TypeErrors = append(b.file.TypeErrors, TypeError{Line: line, Col: col, Message: msg})
	}
	refinements := map[string]bool{}
	for _, node := range nodes {
		switch d := node.(type) {
		case *ast.TypeDef:
			if !d.Subtype {
				continue
			}
			refinements[d.Name] = true
			if d.Opaque {
				add(d.Line, d.Col, fmt.Sprintf("a subtype cannot be opaque: `%s`'s values are values of its parent, which every file sees; drop `opaque`, or declare a new type, `opaque type %s ...`, and check values in its constructor", d.Name, d.Name))
			}
		case *ast.StructDef:
			if d.Rule != nil && len(d.TypeParams) > 0 {
				add(d.TypeParams[0].Line, d.TypeParams[0].Col, fmt.Sprintf("a validated struct takes no type parameters: `%s`'s rule is checked on values of one struct; a generic subtype narrows a type, `subtype %s<T> ... when ...`", d.Name, d.Name))
			}
		}
	}
	for _, node := range nodes {
		switch d := node.(type) {
		case *ast.EnumDef:
			for _, v := range d.Variants {
				if v.Kind == "embedded" && refinements[TypeExprBaseName(v.EmbeddedTypeExpr)] {
					add(v.Line, v.Col, fmt.Sprintf("an enum cannot embed the subtype `%s`: its values are values of its parent; embed that type, or declare a variant over it", TypeExprBaseName(v.EmbeddedTypeExpr)))
				}
			}
		case *ast.FuncDef, *ast.TestDecl, *ast.ImplBlock:
			ast.Inspect(node, func(n ast.Node) bool {
				switch inner := n.(type) {
				case *ast.TypeDef:
					if inner.Subtype {
						add(inner.Line, inner.Col, fmt.Sprintf("a subtype is declared at the top of a file: move `%s` out of the body", inner.Name))
					} else if inner.Rule != nil {
						add(inner.RuleLine, inner.RuleCol, fmt.Sprintf("a type with a rule is declared at the top of a file: move `%s` out of the body", inner.Name))
					}
				case *ast.StructDef:
					if inner.Rule != nil {
						add(inner.RuleLine, inner.RuleCol, fmt.Sprintf("a struct with a rule is declared at the top of a file: move `%s` out of the body", inner.Name))
					}
				}
				return true
			})
		}
	}
}

// checkBodyTypesOverSubtypes reports a `type` inside a body whose inner
// type resolves to a subtype, `type Local PositiveInt`. Such a type is a
// validated type, built only through `try_from`, which is a generated impl
// block, and an impl cannot stand in a body; so it is declared at the top
// of a file, as a subtype and a type with a rule are
// (validateRefinementDecls). Whether the inner type is a subtype is known
// only once names resolve, which is why this is the checker's and not the
// builder's.
func (c *checker) checkBodyTypesOverSubtypes(nodes []ast.Node) {
	for _, node := range nodes {
		switch node.(type) {
		case *ast.FuncDef, *ast.TestDecl, *ast.ImplBlock:
		default:
			continue
		}
		ast.Inspect(node, func(n ast.Node) bool {
			td, ok := n.(*ast.TypeDef)
			if !ok || td.Subtype || td.Rule != nil {
				return true
			}
			sym := c.fa.Definitions[Pos{Line: td.Line, Col: td.Col}]
			if sym != nil && checkedDistinctOf(sym.Type) != nil {
				c.addError(td.Line, td.Col, fmt.Sprintf(
					"a type over a subtype is declared at the top of a file: move `%s` out of the body, so that it has `%s.try_from`",
					td.Name, td.Name))
			}
			return true
		})
	}
}

// checkRefinementParents reports a refinement of several parents
// (`subtype PositiveInt NonNegativeInt and NonZeroInt when ...`) whose parents
// do not all reach one base, the type its values are at run time, or that
// names one parent twice.
func (c *checker) checkRefinementParents(n *ast.TypeDef) {
	if !n.Subtype {
		return
	}
	dt := refinementOf(c.reg.Lookup(n.Name))
	if dt == nil || dt.Inner == nil {
		return
	}
	if c.checkSubtypeHasARule(n, dt) || len(n.MoreParents) == 0 {
		return
	}
	first := DistinctInner(dt)
	firstBase := RefinementBase(first)
	if refinementOf(firstBase) != nil {
		// A chain with no base is reported where it is declared.
		return
	}
	seen := []Type{first}
	for _, te := range n.MoreParents {
		parent, err := ResolveTypeExpr(te, c.reg, nil, nil)
		if err != nil {
			continue
		}
		line, col := typeExprPos(te, n.Line, n.Col)
		dup := false
		for _, s := range seen {
			dup = dup || TypesIdentical(s, parent)
		}
		if dup {
			c.addError(line, col, fmt.Sprintf("`%s` names the parent %s twice", n.Name, formatTypeForError(parent)))
			continue
		}
		seen = append(seen, parent)
		base := RefinementBase(parent)
		if refinementOf(base) != nil || TypesIdentical(base, firstBase) {
			continue
		}
		c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
			"the parents of %s narrow different types: %s is %s %s and %s is %s %s",
			n.Name, formatTypeForError(first), ArticleFor(formatTypeForError(firstBase)), formatTypeForError(firstBase),
			formatTypeForError(parent), ArticleFor(formatTypeForError(base)), formatTypeForError(base))}.WithHint(
			"a subtype's value is one value at run time, its parents' base, so every parent narrows the same type"))
	}
}

// checkSubtypeHasARule reports a subtype that narrows nothing, and
// answers whether it did: one with no rule anywhere in its chain,
// `subtype Celsius Float` or `subtype Item Priced and Costed`, and one of a
// single parent with no `when` of its own, `subtype Small Digit`, whose
// values are its parent's. A subtype narrows with its own `when`, or as the
// intersection of two or more parents with a rule among them. A `where`
// bounds type arguments, not values, so it narrows nothing either. Each is
// the type it names under another name, which is a typealias.
func (c *checker) checkSubtypeHasARule(n *ast.TypeDef, dt *DistinctType) bool {
	if n.Rule != nil {
		return false
	}
	ruleless := len(RefinementChecks(dt)) == 0
	if !ruleless && len(n.MoreParents) > 0 {
		return false
	}
	parents := []string{n.InnerTypeExpr.TypeString()}
	for _, p := range n.MoreParents {
		parents = append(parents, p.TypeString())
	}
	target := strings.Join(parents, " and ")
	head := n.Name
	if len(n.TypeParams) > 0 {
		var names []string
		for _, tp := range n.TypeParams {
			names = append(names, tp.Name)
		}
		head += "<" + strings.Join(names, ", ") + ">"
	}
	where := ""
	if len(n.WhereClauses) > 0 {
		var parts []string
		for _, wc := range n.WhereClauses {
			var bounds []string
			for _, b := range wc.Bounds {
				bounds = append(bounds, b.TypeString())
			}
			parts = append(parts, wc.Name+": "+strings.Join(bounds, " and "))
		}
		where = " where " + strings.Join(parts, ", ")
	}
	msg := fmt.Sprintf("a subtype with no rule has the same values as %s: `%s` adds no `when`, and nothing it narrows has one", target, n.Name)
	if !ruleless {
		msg = fmt.Sprintf("a subtype of one parent with no rule of its own has the same values as %s: `%s` adds no `when`", target, n.Name)
	}
	c.report(TypeError{Line: n.Line, Col: n.Col, Message: msg}.WithHint(fmt.Sprintf(
		"name it with `typealias %s %s%s`, declare a new type with `type %s %s`, or give the subtype a rule with `when`",
		head, target, where, head, target)))
	return true
}

// checkRefinementImpl reports an interface impl whose receiver is a
// distinct refinement: the refinement's value is its base's at run time,
// so it has its base's impls and none of its own (refinement.go). An
// inherent block, and the functions generated for it, are allowed.
func (c *checker) checkRefinementImpl(n *ast.ImplBlock) {
	if n.Interface == nil && !IsSynthesizedLine(n.Line) && n.Receiver != nil {
		// The generated functions' names are taken.
		if recv, err := ResolveTypeExpr(n.Receiver, c.reg, nil, nil); err == nil && IsRefinement(recv) {
			for _, item := range n.Items {
				if fd, ok := item.(*ast.FuncDef); ok && (fd.Name == "try_from" || fd.Name == "unchecked") {
					c.addError(fd.Line, fd.Col, fmt.Sprintf("`%s.%s` is generated for `%s`; name this function otherwise", recv, fd.Name, recv))
				}
			}
		}
	}
	if n.Interface == nil || n.Receiver == nil {
		return
	}
	if !IsSynthesizedLine(n.Line) && TypeExprBaseName(n.Interface) == "TryFrom" {
		if c.reportSuppliedTryFrom(n) {
			return
		}
	}
	line, col := n.Line, n.Col
	if IsSynthesizedLine(line) && n.SynthOriginLine > 0 {
		line, col = n.SynthOriginLine, n.SynthOriginCol
	}
	iface := TypeExprBaseName(n.Interface)
	if gt, generic := n.Interface.(*ast.GenericType); generic {
		// `impl Divide<Port, Int> for Int`: the impl would be selected at
		// run time by Port's base, where the base's own impl already is.
		for _, arg := range gt.Params {
			if at, err := ResolveTypeExpr(arg, c.reg, nil, nil); err == nil && refinementOf(at) != nil {
				c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
					"an impl's interface cannot take the subtype `%s` as an argument: `%s` is %s %s at run time, so the impl is %s's",
					at, at, ArticleFor(formatTypeForError(RefinementBase(at))), formatTypeForError(RefinementBase(at)), formatTypeForError(RefinementBase(at)))}.WithHint(fmt.Sprintf(
					"a %s passes wherever %s %s is expected, so the impl at %s serves it already",
					at, ArticleFor(formatTypeForError(RefinementBase(at))), formatTypeForError(RefinementBase(at)), formatTypeForError(RefinementBase(at)))))
				return
			}
		}
	}
	recv, err := ResolveTypeExpr(n.Receiver, c.reg, nil, nil)
	if err != nil {
		return
	}
	dt := refinementOf(recv)
	if dt == nil {
		return
	}
	c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
		"a subtype implements no interface of its own: `%s` is %s %s at run time and has %s's impls",
		dt.Name, ArticleFor(formatTypeForError(RefinementBase(dt))), formatTypeForError(RefinementBase(dt)), formatTypeForError(RefinementBase(dt)))}.WithHint(fmt.Sprintf(
		"to give `%s` behavior of its own, write functions in `impl %s`; for a type with its own `%s`, declare a distinct type, `type %s %s`, and check values in its constructor",
		dt.Name, dt.Name, iface, dt.Name, formatTypeForError(dt.Inner))))
}

// refinementLiteralsTakeExpected reports whether a call's arguments hold a
// literal that the expected type, which has a refinement in it, would check
// as that refinement: `Some(22)` expected to be a `Maybe<Port>`. The call
// seeds its type parameters from the expected type, as it does for a Byte
// literal (byteLiteralsTakeExpected).
func refinementLiteralsTakeExpected(args []ast.Node, expected Type) bool {
	if !refinementInType(expected, 0) {
		return false
	}
	for _, a := range args {
		if holdsRefinementLiteral(a) {
			return true
		}
	}
	return false
}

// holdsRefinementLiteral reports whether n is a literal a refinement
// position checks, or a collection, tuple or map literal or a call's
// argument list with one inside.
func holdsRefinementLiteral(n ast.Node) bool {
	if refinementLiteral(n) {
		return true
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.NamedArg:
		return holdsRefinementLiteral(v.Value)
	case *ast.ListLit:
		for _, item := range v.Items {
			if holdsRefinementLiteral(item) {
				return true
			}
		}
	case *ast.TupleLit:
		for _, item := range v.Items {
			if holdsRefinementLiteral(item) {
				return true
			}
		}
	case *ast.MapLit:
		for _, e := range v.Entries {
			if holdsRefinementLiteral(e.Key) || holdsRefinementLiteral(e.Value) {
				return true
			}
		}
	case *ast.Call:
		for _, a := range v.Args {
			if holdsRefinementLiteral(a) {
				return true
			}
		}
	}
	return false
}

// refinementInType reports whether ty is a distinct refinement or has one
// among its element or type arguments.
func refinementInType(ty Type, depth int) bool {
	if depth > 8 || ty == nil {
		return false
	}
	switch t := resolveTypeVar(ty).(type) {
	case *DistinctType:
		if t.Subtype {
			return true
		}
		return anyRefinementInTypes(t.TypeArgs, depth)
	case *ListType:
		return refinementInType(t.Elem, depth+1)
	case *MapType:
		return refinementInType(t.Key, depth+1) || refinementInType(t.Val, depth+1)
	case *TupleType:
		return anyRefinementInTypes(t.Elems, depth)
	case *EnumType:
		return anyRefinementInTypes(t.TypeArgs, depth)
	case *StructType:
		return anyRefinementInTypes(t.TypeArgs, depth)
	}
	return false
}

func anyRefinementInTypes(ts []Type, depth int) bool {
	for _, t := range ts {
		if refinementInType(t, depth+1) {
			return true
		}
	}
	return false
}

// checkCollectionRefinementLit checks n, a list, vector, set or map
// literal the checker typed as ty, when ty is a distinct refinement: one
// whose every element, key and value is a literal, through nested
// collection and tuple literals, is recorded for the compile-time check of
// its rules, as a struct refinement's literal is; any other is an error
// naming `try_from`, since its rules would have to run when the program
// does. ty is the answer either way.
func (c *checker) checkCollectionRefinementLit(n ast.Node, ty Type) Type {
	dt := refinementOf(ty)
	if dt == nil {
		return ty
	}
	if line, _ := startOf(n); IsSynthesizedLine(line) {
		return ty
	}
	if bad := nonLiteralPart(n); bad != nil {
		line, col := startOf(bad)
		c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
			"%s is a subtype: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `%s` is not",
			dt.Name, RenderLiteral(bad))}.WithHint(fmt.Sprintf(
			"build a %s from values known only when the program runs with `%s.try_from(...)`, a `Maybe<%s>`, which checks `%s`",
			dt.Name, dt.Name, formatTypeForError(dt), dt.Rule)))
		return ty
	}
	c.recordRefinedLiteral(n, dt)
	return ty
}

// nonLiteralPart is the first part of the collection literal n that is no
// literal a refinement position checks (refinementLiteral), looking through
// nested list, vector, set, map and tuple literals, or nil when every part
// is one. A spread is never one.
func nonLiteralPart(n ast.Node) ast.Node {
	if refinementLiteral(n) {
		return nil
	}
	switch v := ungroupExpr(n).(type) {
	case *ast.ListLit:
		if len(v.Spreads) > 0 {
			return v.Spreads[0].Head
		}
		for _, item := range v.Items {
			if bad := nonLiteralPart(item); bad != nil {
				return bad
			}
		}
		return nil
	case *ast.TupleLit:
		for _, item := range v.Items {
			if bad := nonLiteralPart(item); bad != nil {
				return bad
			}
		}
		return nil
	case *ast.MapLit:
		if len(v.Spreads) > 0 {
			return v.Spreads[0].Head
		}
		for _, e := range v.Entries {
			if bad := nonLiteralPart(e.Key); bad != nil {
				return bad
			}
			if bad := nonLiteralPart(e.Value); bad != nil {
				return bad
			}
		}
		return nil
	}
	return n
}

// checkStructRefinementLit checks a literal of the struct refinement st:
// one whose every field is written as a literal is recorded for the
// compile-time check of its rule; any other is an error naming `try_from`,
// as is a patch, whose result could break the rule.
func (c *checker) checkStructRefinementLit(lit *ast.StructLit, st *StructType, patch bool) {
	if IsSynthesizedLine(lit.Line) {
		return
	}
	hint := fmt.Sprintf("build a %s from values known only when the program runs with `%s.try_from({...})`, a `Maybe<%s>`, which checks `%s`",
		st.Name, st.Name, st.Name, st.Rule)
	if patch {
		c.report(TypeError{Line: lit.Line, Col: lit.Col, Message: fmt.Sprintf(
			"a patch of the validated struct %s could break its rule `%s`", st.Name, st.Rule)}.WithHint(hint))
		return
	}
	written := map[string]bool{}
	for _, f := range lit.Fields {
		written[f.Name] = true
		if !refinementLiteral(f.Value) {
			line, col := startOf(f.Value)
			c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
				"%s is a validated struct: a literal of it is checked when the program is compiled, so each field is a literal, and `%s` is not", st.Name, f.Name)}.WithHint(hint))
			return
		}
	}
	for _, f := range st.Fields {
		if !written[f.Name] {
			c.report(TypeError{Line: lit.Line, Col: lit.Col, Message: fmt.Sprintf(
				"%s is a validated struct: a literal of it writes every field, so its rule can be checked when the program is compiled; `%s` is missing", st.Name, f.Name)}.WithHint(hint))
			return
		}
	}
	c.recordRefinedLiteral(lit, st)
}

// checkIntersections reports each `A and B` the file writes whose members
// are subtypes and interfaces, `NonNegativeInt and Display`, where the
// subtypes' base does not implement an interface among them: no value is
// both. The intersection of the subtypes is the type (type_resolver.go),
// so the check is here, where impls are known.
func (c *checker) checkIntersections(nodes []ast.Node) {
	for _, n := range nodes {
		if IsSynthesizedLine(n.LineNum()) {
			continue
		}
		ast.Inspect(n, func(node ast.Node) bool {
			if gt, ok := node.(*ast.GenericType); ok {
				c.checkAliasBounds(gt)
				return true
			}
			conj, ok := node.(*ast.ConjunctionType)
			if !ok {
				return true
			}
			var base Type
			var first Type
			for _, m := range conj.Members {
				t, err := ResolveTypeExpr(m, c.reg, nil, nil)
				if err == nil && refinementOf(t) != nil {
					base, first = RefinementBase(t), t
					break
				}
			}
			if base == nil || ContainsTypeParam(base) {
				return false
			}
			for _, m := range conj.Members {
				t, err := ResolveTypeExpr(m, c.reg, nil, nil)
				if err != nil {
					continue
				}
				for _, iface := range InterfaceMembers(t) {
					line, col := typeExprPos(m, conj.Line, conj.Col)
					if !c.typeImplementsInterface(base, iface.Name, Pos{Line: line, Col: col}, RecordingKindCallSite) {
						c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
							"no value is both %s and %s: %s is %s %s, and %s does not implement %s",
							formatTypeForError(first), iface.Name, formatTypeForError(first),
							ArticleFor(formatTypeForError(base)), formatTypeForError(base), formatTypeForError(base), iface.Name)})
					}
				}
			}
			return false
		})
	}
}

// checkAliasBounds reports a use of a generic typealias, `Sortable<Circle>`
// with `typealias Sortable<T> List<T> where T: Comparable`, whose type
// argument does not meet the alias's bound.
func (c *checker) checkAliasBounds(gt *ast.GenericType) {
	at, ok := c.reg.Lookup(gt.Name).(*AliasTemplate)
	if !ok || len(gt.Params) != len(at.Params) {
		return
	}
	for i, p := range at.Params {
		if len(p.Bounds) == 0 {
			continue
		}
		arg, err := ResolveTypeExpr(gt.Params[i], c.reg, nil, nil)
		if err != nil || ContainsTypeParam(arg) || containsTypeVar(arg) {
			continue
		}
		for _, b := range p.Bounds {
			line, col := typeExprPos(gt.Params[i], gt.Line, gt.Col)
			if !c.typeImplementsInterface(arg, b.Name, Pos{Line: line, Col: col}, RecordingKindGenericBoundCheck) {
				c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf(
					"%s does not implement %s (required by `where %s: %s` on typealias %s)", arg, b.Name, p.Name_, b.Name, at.Name)}.WithHint(
					c.boundHelp(arg, b)))
			}
		}
	}
}

// reportSuppliedTryFrom reports a hand-written `impl TryFrom<Base> for T`
// where T is a `type` with a rule in its chain over Base: T has that impl
// because of its rule, as every distinct type satisfies `Distinct<T>`, so
// it cannot be written. An impl from another source type is the program's.
// It answers whether it reported.
func (c *checker) reportSuppliedTryFrom(n *ast.ImplBlock) bool {
	gt, ok := n.Interface.(*ast.GenericType)
	if !ok || len(gt.Params) != 1 {
		return false
	}
	recv, err := ResolveTypeExpr(n.Receiver, c.reg, nil, nil)
	if err != nil {
		return false
	}
	checked := checkedDistinctOf(recv)
	if checked == nil {
		return false
	}
	src, err := ResolveTypeExpr(gt.Params[0], c.reg, nil, nil)
	if err != nil {
		return false
	}
	base := refinementBaseOf(checked)
	if !TypesIdentical(src, base) {
		return false
	}
	c.report(TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf(
		"`TryFrom<%s>` for %s is supplied by its rule and cannot be implemented; every `type` with a rule over %s satisfies `TryFrom<%s>` automatically — remove this `impl TryFrom<%s> for %s`",
		formatTypeForError(base), checked.Name, formatTypeForError(base), formatTypeForError(base), formatTypeForError(base), checked.Name)})
	return true
}
