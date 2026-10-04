package ir

// RuleOperandShape's plants and its controls.
//
// A ZERO IS VALIDATED BY A PLANTED POSITIVE. The rule's failing population in
// production is zero — 331 operand positions over the 231 retained functions,
// 146 answered and none contradictory — so without a plant per condition a
// clean corpus reading is indistinguishable from a rule that does not run.
//
// EACH PLANT COMES WITH A CONTROL THAT DIFFERS IN ONE OPERAND, because a rule
// that reported every graph would pass the plant too.

import (
	"strings"
	"testing"
)

func shapePos() Pos { return At("shape.nomi", 4, 2) }

// shapeViolations is every RuleOperandShape violation Lint reports for f.
func shapeViolations(t *testing.T, f *Func) []Violation {
	t.Helper()
	err := Lint(f)
	if err == nil {
		return nil
	}
	le, isLint := err.(*LintError)
	if !isLint {
		t.Fatalf("Lint returned %T, not *LintError: %v", err, err)
	}
	var out []Violation
	for _, v := range le.Violations {
		if v.Rule == RuleOperandShape {
			out = append(out, v)
			continue
		}
		if v.Rule == RuleTempTyped || v.Rule == RuleTempType {
			// These fixtures state no types on purpose: this rule measures
			// what the graph DERIVES, and a stored type would answer for it.
			// The stored-type rules have their own plants in typelint_test.go.
			continue
		}
		t.Fatalf("the fixture violates %s as well, so this test is not measuring "+
			"RuleOperandShape: %v", v.Rule, v)
	}
	return out
}

// TestShape_EveryConditionCatchesItsPlant plants one wrong operand per
// condition the rule checks, with a control per condition that differs only
// in that operand.
func TestShape_EveryConditionCatchesItsPlant(t *testing.T) {
	p := shapePos()

	// Each case builds a function whose ONE operand is supplied by `mk`, so
	// the control and the plant differ in exactly that instruction.
	type cond struct {
		name string
		// want names the shape the position requires, for the message check.
		want string
		// build appends the operand-producing instruction and then the
		// condition's own instruction, returning the function.
		build func(mk func(*Func, *Block) Temp) *Func
	}

	// The operand producers. `good` is per condition; `bad` is shared,
	// because a String constant is the wrong shape at every one of them.
	str := func(f *Func, b *Block) Temp {
		t0 := f.NewTemp()
		b.Append(NewString(p, t0, "x"))
		return t0
	}
	anInt := func(f *Func, b *Block) Temp {
		t0 := f.NewTemp()
		b.Append(NewInt(p, t0, 3))
		return t0
	}
	aFloat := func(f *Func, b *Block) Temp {
		t0 := f.NewTemp()
		b.Append(NewFloat(p, t0, 1.5))
		return t0
	}
	aBool := func(f *Func, b *Block) Temp {
		t0 := f.NewTemp()
		b.Append(NewBool(p, t0, true))
		return t0
	}
	aStruct := func(f *Func, b *Block) Temp {
		t0, in := f.NewTemp(), f.NewTemp()
		b.Append(NewInt(p, in, 1))
		b.Append(NewMakeStruct(p, t0, NewSymbol("Point"), []string{"x"}, []Temp{in}))
		return t0
	}
	aTuple := func(f *Func, b *Block) Temp {
		t0, x, y := f.NewTemp(), f.NewTemp(), f.NewTemp()
		b.Append(NewInt(p, x, 1))
		b.Append(NewInt(p, y, 2))
		b.Append(NewMakeTuple(p, t0, []Temp{x, y}))
		return t0
	}
	aVariant := func(f *Func, b *Block) Temp {
		t0 := f.NewTemp()
		b.Append(NewMakeVariant(p, t0, NewSymbol("Shape"), "Dot", nil))
		return t0
	}
	aDistinct := func(f *Func, b *Block) Temp {
		t0, in := f.NewTemp(), f.NewTemp()
		b.Append(NewInt(p, in, 1))
		b.Append(NewMakeDistinct(p, t0, NewSymbol("Meters"), in))
		return t0
	}

	conds := []struct {
		cond
		good func(*Func, *Block) Temp
	}{{
		cond{"arith Int lhs", "Int", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "arith_int")
			b := f.NewBlock(p, "entry")
			lhs := mk(f, b)
			rhs, d := f.NewTemp(), f.NewTemp()
			b.Append(NewInt(p, rhs, 1))
			b.Append(NewArith(p, d, OpAdd, IntArith(OverflowFaults), lhs, rhs))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, anInt,
	}, {
		cond{"arith Float rhs", "Float", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "arith_float")
			b := f.NewBlock(p, "entry")
			lhs, d := f.NewTemp(), f.NewTemp()
			b.Append(NewFloat(p, lhs, 2.5))
			rhs := mk(f, b)
			b.Append(NewArith(p, d, OpMul, FloatArith(), lhs, rhs))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, aFloat,
	}, {
		cond{"concat part", "String", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "concat")
			b := f.NewBlock(p, "entry")
			a := mk(f, b)
			c, d := f.NewTemp(), f.NewTemp()
			b.Append(NewString(p, c, "!"))
			b.Append(NewConcat(p, d, a, c))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, str,
	}, {
		cond{"branch condition", "Bool", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "branch")
			b := f.NewBlock(p, "entry")
			c := mk(f, b)
			v := f.NewTemp()
			b.Append(NewInt(p, v, 1))
			yes, no := f.NewBlock(p, "yes"), f.NewBlock(p, "no")
			yes.SetTerm(NewReturn(p, v))
			no.SetTerm(NewReturn(p, v))
			b.SetTerm(NewBranch(p, c, yes.ID(), no.ID()))
			return f
		}}, aBool,
	}, {
		cond{"proj field subject", "a struct", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "field")
			b := f.NewBlock(p, "entry")
			s := mk(f, b)
			d := f.NewTemp()
			b.Append(NewProjField(p, d, s, NewSymbol("x"), "x", ValUnknown))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, aStruct,
	}, {
		cond{"proj tuple subject", "a tuple", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "component")
			b := f.NewBlock(p, "entry")
			s := mk(f, b)
			d := f.NewTemp()
			b.Append(NewProjSlot(p, d, s, 0, ValUnknown))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, aTuple,
	}, {
		cond{"proj payload subject", "a variant", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "payload")
			b := f.NewBlock(p, "entry")
			s := mk(f, b)
			d := f.NewTemp()
			b.Append(NewProjPayload(p, d, s, NewSymbol("Shape"), "Dot", 0, ValUnknown))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, aVariant,
	}, {
		cond{"proj inner subject", "a distinct type", func(mk func(*Func, *Block) Temp) *Func {
			f := NewFunc(p, "inner")
			b := f.NewBlock(p, "entry")
			s := mk(f, b)
			d := f.NewTemp()
			b.Append(NewProjInner(p, d, s, NewSymbol("Meters"), ValUnknown))
			b.SetTerm(NewReturn(p, d))
			return f
		}}, aDistinct,
	}}

	if len(conds) != 8 {
		t.Fatalf("%d conditions, want 8: the rule checks ten positions and two pairs "+
			"share a shape requirement (Int lhs/rhs, Float lhs/rhs), so eight distinct "+
			"requirements is the set. A different count means the rule moved and this "+
			"test is measuring a population it was not written for", len(conds))
	}

	for _, c := range conds {
		t.Run(c.name, func(t *testing.T) {
			// CONTROL: the right shape. The rule must be silent.
			if vs := shapeViolations(t, c.build(c.good)); len(vs) != 0 {
				t.Fatalf("THE CONTROL FAILED, so the plant below measures nothing: %v", vs)
			}
			// PLANT: a String where the position requires otherwise. The
			// `concat part` case plants an Int instead, because a String is
			// what that position wants.
			plant := str
			if c.name == "concat part" {
				plant = anInt
			}
			vs := shapeViolations(t, c.build(plant))
			if len(vs) != 1 {
				t.Fatalf("the rule reported %d violations for the plant, want 1: %v", len(vs), vs)
			}
			if !strings.Contains(vs[0].Why, "not "+c.want) {
				t.Fatalf("the violation does not name the required shape %q: %s",
					c.want, vs[0].Why)
			}
			if !vs[0].Pos.IsValid() {
				t.Fatal("the violation carries no position")
			}
			t.Logf("%s", vs[0])
		})
	}
}

// TestShape_TheRuleIsSilentWhereTheGraphDoesNotSay is the other half of the
// reading in shape.go's header: the rule must not fire at a position whose
// operand shape the graph cannot state.
//
// A parameter and a projection both record a shape, so each fixture below is
// silent only because the producer states `ValUnknown`, which `irParamShape`
// does for an existential, a bound-free type parameter, an `rtOpaque` leaf
// and a generic std instance. That is a decline rather than an absence, and
// these fixtures keep `ValUnknown` from being read as a shape.
func TestShape_TheRuleIsSilentWhereTheGraphDoesNotSay(t *testing.T) {
	p := shapePos()

	// A PARAMETER WHOSE PRODUCER DECLINED. Its temporary is written by
	// nothing and its declaration states nothing.
	param := NewFunc(p, "from_param")
	pt := param.AddParam(NewSymbol("n"), ValUnknown)
	pb := param.NewBlock(p, "entry")
	pd := param.NewTemp()
	pb.Append(NewProjInner(p, pd, pt, NewSymbol("Meters"), ValUnknown))
	pb.SetTerm(NewReturn(p, pd))
	if vs := shapeViolations(t, param); len(vs) != 0 {
		t.Fatalf("the rule fired on a parameter whose shape the producer declined "+
			"to state: %v", vs)
	}

	// A `RefLocal` TO ONE. The read resolves to the declaration by identity,
	// so a declined shape has to travel the extra hop rather than becoming an
	// unknown that the rule treats differently from the parameter itself.
	local := NewFunc(p, "from_local")
	local.AddParam(NewSymbol("n"), ValUnknown)
	lb := local.NewBlock(p, "entry")
	r, d := local.NewTemp(), local.NewTemp()
	lb.Append(NewRefLocal(p, r, local.Params()[0].Sym))
	lb.Append(NewProjInner(p, d, r, NewSymbol("Meters"), ValUnknown))
	lb.SetTerm(NewReturn(p, d))
	if vs := shapeViolations(t, local); len(vs) != 0 {
		t.Fatalf("the rule fired on a RefLocal to a parameter whose shape the "+
			"producer declined to state: %v", vs)
	}

	// A `Proj` whose producer declined. `Proj.Shape` records the result, so
	// this is the decline path: a producer that states `ValUnknown` keeps the
	// rule silent.
	//
	// The subject is a recorded struct, so this isolates the RESULT. With
	// `ValUnknown` there too the graph would hold two unknowns and a rule
	// that had stopped reading either recorded shape would still pass.
	proj := NewFunc(p, "from_proj")
	sp := proj.AddParam(NewSymbol("pt"), ValStruct)
	b := proj.NewBlock(p, "entry")
	fv, sum, one := proj.NewTemp(), proj.NewTemp(), proj.NewTemp()
	b.Append(NewProjField(p, fv, sp, NewSymbol("x"), "x", ValUnknown))
	b.Append(NewInt(p, one, 1))
	b.Append(NewArith(p, sum, OpAdd, IntArith(OverflowFaults), fv, one))
	b.SetTerm(NewReturn(p, sum))
	if vs := shapeViolations(t, proj); len(vs) != 0 {
		t.Fatalf("the rule fired on a Proj result, whose type the node does not carry: %v", vs)
	}
}

// TestShape_ARecordedParameterShapeIsReadAtEveryPosition is the plant for
// the `ir.Param.Shape` field.
//
// WITHOUT IT THE 145 NEW POSITIONS ARE UNFALSIFIABLE. Production reports zero
// contradictions over all 291 answered positions, so a field that was recorded
// and never read, or read and never compared, produces exactly the same clean
// reading as one that works. Each case here records a WRONG shape and demands
// the violation, with a control differing in the recorded shape alone.
//
// SEVEN CASES, one per shape demand the 145 positions actually make — Bool 1,
// Int 53, String 12, struct 54, tuple 2, variant 3, distinct 20 — and each is
// run twice, once with the parameter read DIRECTLY and once through a
// `RefLocal`, because those are two arms in shape.go and production uses both
// (27 direct, 118 through a read).
func TestShape_ARecordedParameterShapeIsReadAtEveryPosition(t *testing.T) {
	p := shapePos()

	cases := []struct {
		name string
		// bad is a shape the position must reject, good one it must accept.
		bad, good ValShape
		// build appends the position's instructions, reading the parameter's
		// value out of `src`, and returns the terminator's value.
		build func(f *Func, b *Block, src Temp) Temp
		want  string
	}{
		{"branch condition", ValString, ValBool,
			func(f *Func, b *Block, src Temp) Temp {
				out := f.NewTemp()
				arm := f.NewBlock(p, "arm")
				b.SetTerm(NewBranch(p, src, arm.ID(), arm.ID()))
				arm.Append(NewInt(p, out, 1))
				arm.SetTerm(NewReturn(p, out))
				return NoTemp
			},
			"the branched condition is String, not Bool"},

		{"Int arith lhs", ValString, ValInt,
			func(f *Func, b *Block, src Temp) Temp {
				one, sum := f.NewTemp(), f.NewTemp()
				b.Append(NewInt(p, one, 1))
				b.Append(NewArith(p, sum, OpAdd, IntArith(OverflowFaults), src, one))
				return sum
			},
			"the left operand of this Int operation is String, not Int"},

		// TWO PARTS, because `NewConcat` rejects a join of one — "one part IS
		// the answer and zero parts is the empty String constant". Part 1 is a
		// String constant, so exactly one part is wrong and the count
		// assertion below is about the plant rather than about the arity.
		{"concat part", ValInt, ValString,
			func(f *Func, b *Block, src Temp) Temp {
				out, tail := f.NewTemp(), f.NewTemp()
				b.Append(NewString(p, tail, "!"))
				b.Append(NewConcat(p, out, src, tail))
				return out
			},
			"concat part 0 is Int, not String"},

		{"field subject", ValString, ValStruct,
			func(f *Func, b *Block, src Temp) Temp {
				out := f.NewTemp()
				b.Append(NewProjField(p, out, src, NewSymbol("x"), "x", ValUnknown))
				return out
			},
			"the subject of this field projection is String, not a struct"},

		{"tuple subject", ValString, ValTuple,
			func(f *Func, b *Block, src Temp) Temp {
				out := f.NewTemp()
				b.Append(NewProjSlot(p, out, src, 0, ValUnknown))
				return out
			},
			"the subject of this slot projection is String, not a tuple"},

		{"payload subject", ValString, ValVariant,
			func(f *Func, b *Block, src Temp) Temp {
				out := f.NewTemp()
				b.Append(NewProjPayload(p, out, src, NewSymbol("Maybe"), "Some", 0, ValUnknown))
				return out
			},
			"the subject of this payload projection is String, not a variant"},

		{"inner subject", ValString, ValDistinct,
			func(f *Func, b *Block, src Temp) Temp {
				out := f.NewTemp()
				b.Append(NewProjInner(p, out, src, NewSymbol("Meters"), ValUnknown))
				return out
			},
			"the subject of this inner projection is String, not a distinct type"},
	}

	// reads is the two ways production reaches a parameter's value: the
	// temporary itself, and a `RefLocal` naming the declaration.
	reads := []struct {
		name string
		src  func(f *Func, b *Block, pt Temp) Temp
	}{
		{"direct", func(_ *Func, _ *Block, pt Temp) Temp { return pt }},
		{"through a RefLocal", func(f *Func, b *Block, _ Temp) Temp {
			r := f.NewTemp()
			b.Append(NewRefLocal(p, r, f.Params()[0].Sym))
			return r
		}},
	}

	for _, c := range cases {
		for _, rd := range reads {
			build := func(shape ValShape) *Func {
				f := NewFunc(p, "v12")
				pt := f.AddParam(NewSymbol("n"), shape)
				b := f.NewBlock(p, "entry")
				ret := c.build(f, b, rd.src(f, b, pt))
				if ret != NoTemp {
					b.SetTerm(NewReturn(p, ret))
				}
				return f
			}
			t.Run(c.name+"/"+rd.name, func(t *testing.T) {
				// THE CONTROL FIRST. A rule that reported every recorded shape
				// would pass the plant, so the plant alone says nothing.
				if vs := shapeViolations(t, build(c.good)); len(vs) != 0 {
					t.Fatalf("the control records the shape the position wants "+
						"and the rule fired anyway: %v", vs)
				}
				// AND `ValUnknown` MUST BE SILENT TOO, which is the third
				// answer and the one a producer gives when it declines.
				if vs := shapeViolations(t, build(ValUnknown)); len(vs) != 0 {
					t.Fatalf("the rule fired on a declined shape, so ValUnknown is "+
						"being read as a shape rather than as an absence: %v", vs)
				}
				vs := shapeViolations(t, build(c.bad))
				if len(vs) != 1 {
					t.Fatalf("recording %s where the position wants %s produced %d "+
						"violation(s), want 1: %v", c.bad, c.good, len(vs), vs)
				}
				if !strings.Contains(vs[0].Why, c.want) {
					t.Fatalf("the violation reads %q, want it to contain %q",
						vs[0].Why, c.want)
				}
				if !vs[0].Pos.IsValid() {
					t.Fatal("the violation carries no position")
				}
				t.Logf("%s", vs[0])
			})
		}
	}
}

// TestShape_AWriterDisagreeingWithTheDeclarationIsNotReported is the
// soundness check for the merge shape.go does, and it has the control that
// makes it a reading rather than a blanket.
//
// A parameter's temporary is written by nothing in any graph the producer
// builds today, but a `Copy` into one is LEGAL — `irtail.go`'s self-recursive
// hop rebinds parameters — so the declaration is merged with every writer by
// the same disagreement rule two branch arms get, rather than treated as
// authoritative. Treating it as authoritative would describe the entry and
// report the hop.
func TestShape_AWriterDisagreeingWithTheDeclarationIsNotReported(t *testing.T) {
	p := shapePos()

	// A parameter declared Int, rebound from a String. The two disagree, so
	// the rule declines rather than reporting the Int position.
	f := NewFunc(p, "rebound")
	pt := f.AddParam(NewSymbol("n"), ValInt)
	b := f.NewBlock(p, "entry")
	s, one, sum := f.NewTemp(), f.NewTemp(), f.NewTemp()
	b.Append(NewString(p, s, "9"))
	b.Append(NewCopy(p, pt, s))
	b.Append(NewInt(p, one, 1))
	b.Append(NewArith(p, sum, OpAdd, IntArith(OverflowFaults), pt, one))
	b.SetTerm(NewReturn(p, sum))
	if vs := shapeViolations(t, f); len(vs) != 0 {
		t.Fatalf("a writer disagreeing with the declaration was reported, so the "+
			"merge is picking one rather than declining: %v", vs)
	}

	// THE CONTROL. The same graph with an AGREEING writer must still derive
	// Int, or the disagreement check is indistinguishable from a blanket
	// unknown for every parameter something writes.
	g := NewFunc(p, "rebound_ok")
	gt := g.AddParam(NewSymbol("n"), ValInt)
	gb := g.NewBlock(p, "entry")
	gi, gone, gsum := g.NewTemp(), g.NewTemp(), g.NewTemp()
	gb.Append(NewInt(p, gi, 7))
	gb.Append(NewCopy(p, gt, gi))
	gb.Append(NewInt(p, gone, 1))
	gb.Append(NewArith(p, gsum, OpAdd, IntArith(OverflowFaults), gt, gone))
	gb.SetTerm(NewReturn(p, gsum))
	if got := shapeOfTemp(g, gt, 0); got != ValInt {
		t.Fatalf("an agreeing writer derived %s, not Int, so the control does not "+
			"distinguish the disagreement check from a blanket unknown", got)
	}
}

// TestShape_TwoArmsWritingTwoShapesAreNotReported is the non-SSA soundness
// check, and it is the one place this rule could have refused a legal graph.
//
// This package's header says a temporary may be assigned by more than one
// instruction, "because that is what the two arms of a branch writing one
// destination requires". `Func.Def` answers the FIRST writer, so a derivation
// built on it would describe one arm and could report the other. `shapeOfTemp`
// reads EVERY writer and declines on disagreement, and this is what says so.
func TestShape_TwoArmsWritingTwoShapesAreNotReported(t *testing.T) {
	p := shapePos()
	f := NewFunc(p, "diamond")
	entry := f.NewBlock(p, "entry")
	join := f.NewBlock(p, "join")
	armA := f.NewBlock(p, "a")
	armB := f.NewBlock(p, "b")

	cond, res := f.NewTemp(), f.NewTemp()
	entry.Append(NewBool(p, cond, true))
	entry.SetTerm(NewBranch(p, cond, armA.ID(), armB.ID()))

	// Arm A writes an Int into res; arm B writes a String. A well-typed
	// program cannot do this, and the point is that the RULE must not be the
	// thing that decides so: it has no type to compare against, only two
	// disagreeing derivations.
	ia, sb := f.NewTemp(), f.NewTemp()
	armA.Append(NewInt(p, ia, 1))
	armA.Append(NewCopy(p, res, ia))
	armA.SetTerm(NewJump(p, join.ID()))
	armB.Append(NewString(p, sb, "s"))
	armB.Append(NewCopy(p, res, sb))
	armB.SetTerm(NewJump(p, join.ID()))

	d := f.NewTemp()
	one := f.NewTemp()
	join.Append(NewInt(p, one, 1))
	join.Append(NewArith(p, d, OpAdd, IntArith(OverflowFaults), res, one))
	join.SetTerm(NewReturn(p, d))

	if got := shapeOfTemp(f, res, 0); got != ValUnknown {
		t.Fatalf("two arms writing Int and String derived %s; a disagreement must be "+
			"unknown or the rule can refuse a legal diamond", got)
	}
	if vs := shapeViolations(t, f); len(vs) != 0 {
		t.Fatalf("the rule reported a temporary two arms disagree about: %v", vs)
	}

	// AND THE CONTROL: make both arms write an Int and the derivation
	// answers. Without this the test above would pass for a `shapeOfTemp`
	// that answered unknown for every multiply-written temporary, which is a
	// different and weaker property.
	g := NewFunc(p, "diamond_agreed")
	ge := g.NewBlock(p, "entry")
	gj := g.NewBlock(p, "join")
	ga, gb := g.NewBlock(p, "a"), g.NewBlock(p, "b")
	gc, gres := g.NewTemp(), g.NewTemp()
	ge.Append(NewBool(p, gc, true))
	ge.SetTerm(NewBranch(p, gc, ga.ID(), gb.ID()))
	g1, g2 := g.NewTemp(), g.NewTemp()
	ga.Append(NewInt(p, g1, 1))
	ga.Append(NewCopy(p, gres, g1))
	ga.SetTerm(NewJump(p, gj.ID()))
	gb.Append(NewInt(p, g2, 2))
	gb.Append(NewCopy(p, gres, g2))
	gb.SetTerm(NewJump(p, gj.ID()))
	gj.SetTerm(NewReturn(p, gres))
	if got := shapeOfTemp(g, gres, 0); got != ValInt {
		t.Fatalf("two arms both writing an Int derived %s, want Int: the disagreement "+
			"check above cannot be distinguished from a blanket unknown otherwise", got)
	}
}
