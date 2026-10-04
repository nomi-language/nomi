package ir

// RuleLocalDeclared, PLANTED BOTH WAYS.
//
// `RuleLocalDeclared` requires every local a graph reads to be declared in it.
// A function with a destructuring parameter is where a producer can get this
// wrong: its graph must still carry the whole parameter and declare every name
// the pattern binds.
//
// FOUR CASES, and the third is the one a set-membership rule could get wrong.

import "testing"

// localControl is `fn add(a: Int, b: Int): Int { a + b }`: two parameters,
// two `RefLocal` reads, both resolving.
func localControl() *Func {
	f := NewFunc(v3At(1), "add")
	symA, symB := NewSymbol("a"), NewSymbol("b")
	typedParam(f, symA, IntType)
	typedParam(f, symB, IntType)
	b := f.NewBlock(v3At(1), "entry")
	ra := NewRefLocal(v3At(2), f.NewTemp(), symA)
	b.Append(ra)
	rb := NewRefLocal(v3At(2), f.NewTemp(), symB)
	b.Append(rb)
	sum := NewArith(v3At(2), f.NewTemp(), OpAdd, IntArith(OverflowFaults),
		ra.Dst(), rb.Dst())
	b.Append(sum)
	b.SetTerm(NewReturn(v3At(2), sum.Dst()))
	return f
}

func TestLintLocalDeclared_TheLocalControlIsClean(t *testing.T) {
	if err := Lint(localControl()); err != nil {
		t.Fatalf("two parameters read by two RefLocals is the shape every retained "+
			"function has, and Lint rejected it:\n%v", err)
	}
}

// TestLintLocalDeclared_CatchesItsPlant is the producer defect reproduced in
// miniature: the parameter list is empty and the body reads a name.
//
// It is the shape `sum_point` had at the parent, printed by `vm.Describe` in
// the VM coverage probe's own log:
//
//	func sum_point() 4 temps          <- ZERO parameters
//	  b0 (entry):
//	    t2 = local x                  <- declared nowhere in this function
func TestLintLocalDeclared_CatchesItsPlant(t *testing.T) {
	f := NewFunc(v3At(1), "sum_point")
	b := f.NewBlock(v3At(1), "entry")
	x := NewRefLocal(v3At(2), f.NewTemp(), NewSymbol("x"))
	b.Append(x)
	b.SetTerm(NewReturn(v3At(2), x.Dst()))

	vs := violationsFor(t, f, RuleLocalDeclared)
	if len(vs) != 1 {
		t.Fatalf("a function with no parameters reading `x`: want 1 violation, got %d:\n%v",
			len(vs), Lint(f))
	}
	if got := vs[0].Why; got != "reads local x, which this function neither takes as "+
		"a parameter nor binds" {
		t.Errorf("wrong diagnosis: %q", got)
	}
	// The one thing that makes the plant a plant rather than a coincidence:
	// declaring the parameter, and NOTHING else, clears it.
	g := NewFunc(v3At(1), "sum_point")
	symX := NewSymbol("x")
	typedParam(g, symX, IntType)
	gb := g.NewBlock(v3At(1), "entry")
	gx := NewRefLocal(v3At(2), g.NewTemp(), symX)
	gb.Append(gx)
	gb.SetTerm(NewReturn(v3At(2), gx.Dst()))
	if err := Lint(g); err != nil {
		t.Fatalf("the same graph with the parameter declared must pass:\n%v", err)
	}
}

// TestLintLocalDeclared_ABoundNameIsADeclaration is the half a parameters-only rule would
// get wrong.
//
// `ir.Bind` IS a declaration — destructure.go decomposes a pattern into
// navigate/refute/NAME and names `ir.Bind` as the third — so a `RefLocal` of a
// bound name resolves. A rule that consulted only `Func.Params()` would report
// every `case` arm binding and every local binding in the language.
func TestLintLocalDeclared_ABoundNameIsADeclaration(t *testing.T) {
	f := NewFunc(v3At(1), "half")
	b := f.NewBlock(v3At(1), "entry")
	two := NewInt(v3At(2), f.NewTemp(), 2)
	b.Append(two)
	symN := NewSymbol("n")
	bind := NewBind(v3At(2), f.NewTemp(), two.Dst(), symN)
	b.Append(bind)
	read := NewRefLocal(v3At(3), f.NewTemp(), symN)
	b.Append(read)
	b.SetTerm(NewReturn(v3At(3), read.Dst()))
	if err := Lint(f); err != nil {
		t.Fatalf("a read of a name this function BOUND is resolvable and Lint "+
			"reported it:\n%v", err)
	}
}

// TestLintLocalDeclared_TheRuleComparesIdentityAndNotName is what separates this rule
// from a spell-check, and it is `Symbol`'s founding property applied.
//
// The function declares a parameter PRINTED `x` and the body reads a DIFFERENT
// `x`. Every name agrees; the identities do not. A rule keyed on the printed
// name would pass this, and the value the frame handed the caller would be
// whichever declaration the consumer happened to resolve.
func TestLintLocalDeclared_TheRuleComparesIdentityAndNotName(t *testing.T) {
	f := NewFunc(v3At(1), "shadowed")
	typedParam(f, NewSymbol("x"), IntType)
	b := f.NewBlock(v3At(1), "entry")
	other := NewRefLocal(v3At(2), f.NewTemp(), NewSymbol("x"))
	b.Append(other)
	b.SetTerm(NewReturn(v3At(2), other.Dst()))

	vs := violationsFor(t, f, RuleLocalDeclared)
	if len(vs) != 1 {
		t.Fatalf("two same-named symbols are two declarations: want 1 violation, got %d:\n%v",
			len(vs), Lint(f))
	}
}

// TestLintLocalDeclared_OnlyRefLocalIsAsked fences the rule's population.
//
// The other four `RefKind`s name a MODULE-level declaration — a `once` cell, an
// app field, a sibling's binding, a provider — and no function's parameter list
// or binding set could declare one. Asking them would report every module read
// in the language.
func TestLintLocalDeclared_OnlyRefLocalIsAsked(t *testing.T) {
	f := NewFunc(v3At(1), "reads_a_cell")
	b := f.NewBlock(v3At(1), "entry")
	cell := NewRefOnce(v3At(2), f.NewTemp(), NewSymbol("ready"))
	b.Append(cell)
	b.SetTerm(NewReturn(v3At(2), cell.Dst()))
	if vs := violationsFor(t, f, RuleLocalDeclared); len(vs) != 0 {
		t.Fatalf("a `once` read is not a local and must not be asked; got %v", vs)
	}
}
