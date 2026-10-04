package ir

// RuleTempTyped and RuleTempType: the stored value types of valtype.go, checked.
//
// RuleTempTyped asks that every temporary the function NAMES has a type: a
// parameter, a slot's storage, an instruction's destination, and every
// operand an instruction or terminator reads. A temporary allocated and named
// by nothing is not part of the graph: a lambda whose arms all `return`
// allocates a result it never writes. Every temporary the builder names in
// the functions it retains for the corpus, irbuild's testdata,
// the benchmarks and the stdlib is typed, and a handful of those types
// contain Any. It also asks that every module
// cell's type states its value type.
//
// Where a type comes from: the producer states it (`Func.SetType`); a slot's
// storage takes its declaration's (`Type.Val`) at append; and an instruction
// whose result the instruction fixes, a Copy or a Bind, and a read of a
// parameter or a binding take theirs at append (`Func.noteDef`) when the
// producer has stated none.
//
// RuleTempType asks that each stored type agrees with the graph where the
// graph says something:
//
//   - an instruction whose result type is fixed by the instruction itself (a
//     scalar constant, a comparison, a match, a concatenation, a rendering,
//     scalar arithmetic) writes a destination of exactly that type;
//   - every writer whose result SHAPE is derivable (shape.go's shapeWritten)
//     agrees with the stored type's shape;
//   - a Copy or a Bind stores a value its destination's type accepts;
//   - a parameter's recorded shape agrees with its stored type;
//   - a slot's storage has the type the slot declares;
//   - scalar arithmetic reads operands of its domain's type, a negation and a
//     branch read a Bool, and a concatenation reads Strings.
//
// "Accepts" treats Any as agreeing with anything: an untyped literal before
// its context discharges it, or a type parameter's position, is Any and
// matches whatever it is stored beside.

import "strconv"

// intrinsicType is the type instruction in fixes for its destination by itself, or nil when the
// producer has to state it.
func intrinsicType(in Instr) *ValType {
	switch n := in.(type) {
	case *Const:
		switch n.Kind() {
		case ConstUnit:
			return UnitType
		case ConstBool:
			return BoolType
		case ConstInt:
			return IntType
		case ConstFloat:
			return FloatType
		case ConstDecimal:
			return DecimalType
		case ConstString:
			return StringType
		}
	case *Not, *Compare, *Match:
		return BoolType
	case *Concat, *Render:
		return StringType
	case *Arith:
		return arithDomainType(n.Domain())
	}
	return nil
}

func arithDomainType(d Domain) *ValType {
	switch d {
	case DomainInt:
		return IntType
	case DomainFloat:
		return FloatType
	case DomainDecimal:
		return DecimalType
	}
	return nil
}

func lintTempTypes(f *Func, vs *[]Violation) {
	named := map[Temp]bool{}
	report := func(rule LintRule, pos Pos, what, why string) {
		*vs = append(*vs, Violation{Rule: rule, Pos: pos, What: what, Why: why})
	}
	need := func(t Temp, pos Pos, what string) *ValType {
		if t == NoTemp {
			return nil
		}
		ty := f.TempType(t)
		if ty == nil && !named[t] {
			named[t] = true
			report(RuleTempTyped, pos, what, t.String()+" has no stored type")
		}
		return ty
	}
	want := func(t Temp, pos Pos, what, position string, w *ValType) {
		have := need(t, pos, what)
		if have == nil || w == nil || w.Accepts(have) {
			return
		}
		report(RuleTempType, pos, what, position+" "+t.String()+" is "+have.String()+", not "+w.String())
	}
	for _, p := range f.Params() {
		ty := need(p.Temp, f.Pos(), "param "+p.Sym.Name())
		if ty != nil && p.Shape != ValUnknown && ty.Shape() != p.Shape {
			report(RuleTempType, f.Pos(), "param "+p.Sym.Name(),
				"declared shape "+p.Shape.String()+" but stored type "+ty.String())
		}
	}
	var uses []Temp
	for _, b := range f.Blocks() {
		for i, in := range b.Instrs() {
			what := b.ID().String() + " instr " + strconv.Itoa(i)
			pos := in.Pos()
			if s, isSlot := in.(*Slot); isSlot {
				ty := need(s.Slot(), pos, what)
				if v := s.Type().Val(); ty != nil && v != nil && !ty.Identical(v) {
					report(RuleTempType, pos, what, "slot "+s.Slot().String()+" is declared "+
						v.String()+" but stored as "+ty.String())
				}
			}
			uses = in.AppendUses(uses[:0])
			for _, u := range uses {
				need(u, pos, what)
			}
			if d := in.Dst(); d != NoTemp {
				ty := need(d, pos, what)
				if ty != nil {
					it := intrinsicType(in)
					if it != nil && !ty.Identical(it) {
						report(RuleTempType, pos, what, "writes "+it.String()+" into "+
							d.String()+", stored as "+ty.String())
					}
					switch in.(type) {
					case *Copy, *Bind, *Ref:
						// Derived from another temporary; checked below.
					default:
						if it != nil {
							// The exact check above already answered.
							break
						}
						if s := shapeWritten(f, in, 0); s != ValUnknown && ty.Shape() != s {
							report(RuleTempType, pos, what, "writes "+s.String()+" into "+
								d.String()+", stored as "+ty.String())
						}
					}
				}
			}
			switch n := in.(type) {
			case *Copy:
				want(n.Src(), pos, what, "copied value", f.TempType(n.Dst()))
			case *Bind:
				want(n.Src(), pos, what, "bound value", f.TempType(n.Dst()))
			case *Ref:
				if n.Kind() == RefLocal {
					for _, p := range f.Params() {
						if p.Sym == n.Sym() {
							want(p.Temp, pos, what, "read parameter", f.TempType(n.Dst()))
						}
					}
				}
			case *Not:
				want(n.Val(), pos, what, "negation operand", BoolType)
			case *Concat:
				for p := range n.NumParts() {
					want(n.Part(p), pos, what, "concat part", StringType)
				}
			case *Arith:
				if w := arithDomainType(n.Domain()); w != nil {
					want(n.Lhs(), pos, what, "arithmetic operand", w)
					want(n.Rhs(), pos, what, "arithmetic operand", w)
				}
			}
		}
		t := b.Term()
		if t == nil {
			continue
		}
		what := b.ID().String() + " terminator"
		uses = t.AppendUses(uses[:0])
		for _, u := range uses {
			need(u, t.Pos(), what)
		}
		if br, isBranch := t.(*Branch); isBranch {
			want(br.Cond(), br.Pos(), what, "branched condition", BoolType)
		}
	}
}
