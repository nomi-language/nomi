package ir

// A recorded `Proj` shape reaches the shipped rule.
//
// Production reports zero contradictions over every answered position, so a
// field that is recorded and never read, read and never compared, or read by
// an instrument that keeps its own copy of the lookup, produces exactly the
// reading a working field produces. These plants are what make a projection's
// recorded shape falsifiable.
//
// `internal/irbuild`'s `axis13ShapeOf` is a deliberate second copy of this
// derivation and reads `Proj.Shape()` directly, so deleting `shapeWritten`'s
// `*Proj` arm would leave that probe unchanged while `RuleOperandShape` goes
// silent. Every case below goes through `LintFunc`, which is the shipped
// path, so that deletion is caught here.

import (
	"strings"
	"testing"
)

// TestShape_ARecordedProjShapeIsReadAtEveryPosition records a WRONG shape
// on a projection and demands the violation, with a control differing in the
// recorded shape alone.
//
// SEVEN POSITIONS, the same seven `TestShape_ARecordedParameterShapeIsRead
// AtEveryPosition` uses, because they are the seven distinct shape demands the
// rule's ten conditions make and a projection's result can land at any of
// them. Production projections land mostly at the arithmetic and concat
// positions; the rest are the fence.
//
// Each is run twice, direct and through a `Copy`, because production reaches
// a projection's value both ways, and an arm that only read the position's
// own definition would cover a narrower population.
func TestShape_ARecordedProjShapeIsReadAtEveryPosition(t *testing.T) {
	p := shapePos()

	cases := []struct {
		name string
		// bad is a shape the position must reject, good one it must accept.
		bad, good ValShape
		// build appends the position's instructions, reading the projection's
		// result out of `src`, and answers the terminator's value.
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

		{"Float arith rhs", ValString, ValFloat,
			func(f *Func, b *Block, src Temp) Temp {
				lhs, prod := f.NewTemp(), f.NewTemp()
				b.Append(NewFloat(p, lhs, 2.5))
				b.Append(NewArith(p, prod, OpMul, FloatArith(), lhs, src))
				return prod
			},
			"the right operand of this Float operation is String, not Float"},

		// TWO PARTS, because `NewConcat` rejects a join of one. Part 1 is a
		// String constant, so exactly one part is wrong.
		{"concat part", ValInt, ValString,
			func(f *Func, b *Block, src Temp) Temp {
				out, tail := f.NewTemp(), f.NewTemp()
				b.Append(NewString(p, tail, "!"))
				b.Append(NewConcat(p, out, src, tail))
				return out
			},
			"concat part 0 is Int, not String"},

		// A projection off a projection, which is the shape most production
		// projections sit in: the subject is a parameter or another read,
		// so there is no construction in the function to derive it from.
		{"field subject", ValString, ValStruct,
			func(f *Func, b *Block, src Temp) Temp {
				out := f.NewTemp()
				b.Append(NewProjField(p, out, src, NewSymbol("x"), "x", ValUnknown))
				return out
			},
			"the subject of this field projection is String, not a struct"},

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

	// reads is the two ways a projection's value is reached.
	reads := []struct {
		name string
		src  func(f *Func, b *Block, projDst Temp) Temp
	}{
		{"direct", func(_ *Func, _ *Block, projDst Temp) Temp { return projDst }},
		{"through a Copy", func(f *Func, b *Block, projDst Temp) Temp {
			c := f.NewTemp()
			b.Append(NewCopy(p, c, projDst))
			return c
		}},
	}

	for _, c := range cases {
		for _, rd := range reads {
			// THE SUBJECT IS A PARAMETER RECORDED AS A STRUCT, so the field
			// projection itself is always legal and the only shape under test
			// is the one the projection ANSWERS with. A fixture whose subject
			// was also unknown would hold two absences and a rule that read
			// neither would still pass.
			build := func(shape ValShape) *Func {
				f := NewFunc(p, "v15")
				pt := f.AddParam(NewSymbol("pt"), ValStruct)
				b := f.NewBlock(p, "entry")
				pd := f.NewTemp()
				b.Append(NewProjField(p, pd, pt, NewSymbol("f"), "f", shape))
				ret := c.build(f, b, rd.src(f, b, pd))
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
				// And `ValUnknown` must be silent: it is the answer a
				// producer gives when it declines.
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

// TestShape_TheSuffixFactoryChecksItsShape is the one construction-time
// fence the field gained, and this is its plant.
//
// `ProjSuffix` IS THE ONLY KIND WHOSE RESULT IS A FUNCTION OF THE KIND: a
// List's suffix is a List whatever the element type is. Every other kind's
// result is the component's type, which nothing in this package can see, so
// there is nothing to check them against and the factory takes the producer's
// word. Stating that asymmetry as a panic at the one place it holds is
// cheaper than a lint rule with one member, and it is the same choice
// `NewProjSlot`'s negative-index check makes.
func TestShape_TheSuffixFactoryChecksItsShape(t *testing.T) {
	p := shapePos()

	// ADMITTED: the shape a List has, and the decline.
	NewProjSuffix(p, 1, 2, 0, ValContainer)
	NewProjSuffix(p, 1, 2, 3, ValUnknown)

	for _, bad := range []ValShape{ValInt, ValString, ValStruct, ValTuple,
		ValVariant, ValDistinct, ValBool, ValFloat, ValDecimal, ValUnit, ValFunc} {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("NewProjSuffix admitted %s as a List's suffix", bad)
				}
				if !strings.Contains(r.(string), "a List's suffix is a List") {
					t.Fatalf("the panic does not name the rule: %v", r)
				}
			}()
			NewProjSuffix(p, 1, 2, 0, bad)
		}()
	}
}
