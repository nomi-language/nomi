package ir

// The `arith` class, and the fault/delivery division (point 1 of ir.go's
// package header).
//
// A checked operation records that a fault is POSSIBLE AT
// THIS POINT and where "this point" is. It records nothing about how the fault
// is delivered: rt carries the overflow predicate (`AddOverflows`), the error
// (`OverflowError`) and the trapping call (`AddInt`), with one predicate and
// one message text, and which surface a consumer uses is its own choice.
// Delivery is a consumer property.
//
// There is no `trap bool` and there is nowhere to put one. Faults is a METHOD,
// computed from the operator, the operand domain, and — for Int add, subtract,
// multiply and negate only — the overflow discipline the producer chose. Two of
// those three are forced by the front end's type answer and the third is a
// property of the source construct (`derive Hashable` synthesizes modular
// mixers; a programmer's `+` does not). None of them is a fact about which
// consumer is reading. A `trap bool` would have been the one field a producer
// could not fill without knowing, which is the coupling the IR exists to
// remove.
//
// The equivalence that makes this more than a naming preference is checked
// in arith_test.go: across every constructible
// operator-and-domain combination, the `rt` function the builder names takes a
// `line int` parameter EXACTLY when Faults reports a fault. The position is the
// fault point. That is why the position rides as an operand for these twelve
// operations rather than as an ambient cursor, and it is why `fn add(a, b) { a
// + b }` blames `a + b` rather than the line of the call.
//
// THE SIX OPERATIONS, AND WHY THEY ARE NOT SIX OPCODES. Nomi's primitive
// `a + b` has six shapes: trapping `rt.<Op>Int(a, b, line)`, wrapping
// `rt.Wrap<Op>Int(a, b)`, Go's own `a Op b` for Float, `rt.DivFloat(a, b)`,
// `rt.ModFloat(a, b, line)` and `rt.<Op>Decimal(...)`. All six are
// recoverable here — Shape returns one of exactly six values — and NOT ONE of
// them is stored. They are a function of three fields. Storing the shape would
// be storing one consumer's selection in the representation.
//
// Arithmetic on a named type is not here: `a + b` on a type with an `impl Add`
// lowers to an ordinary call of that impl.
type ArithOp uint8

const (
	// OpAdd is `+`.
	OpAdd ArithOp = iota + 1
	// OpSub is binary `-`.
	OpSub
	// OpMul is `*`.
	OpMul
	// OpDiv is `/`.
	OpDiv
	// OpRem is `%`.
	OpRem
	// OpNeg is unary `-`. It reads one operand.
	OpNeg
)

// Symbol is the source spelling. The trap message names the operator this way
// (`rt.overflowText`: "line %d: integer overflow: %d %s %d"), so the two
// consumers' texts agree only if they agree here.
func (o ArithOp) Symbol() string {
	switch o {
	case OpAdd:
		return "+"
	case OpSub, OpNeg:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	case OpRem:
		return "%"
	}
	return "?"
}

func (o ArithOp) String() string {
	if o == OpNeg {
		return "neg"
	}
	return o.Symbol()
}

// Unary reports whether this operator reads one operand.
func (o ArithOp) Unary() bool { return o == OpNeg }

// Domain is the operand type family the operation runs in. It is not a full
// type: it is the distinction the arithmetic itself turns on.
type Domain uint8

const (
	// DomainInt is Nomi's Int, which is int64 and which TRAPS where Go wraps.
	DomainInt Domain = iota + 1
	// DomainFloat is Nomi's Float, which is float64 and IEEE.
	DomainFloat
	// DomainDecimal is Nomi's exact Decimal.
	DomainDecimal
)

func (d Domain) String() string {
	switch d {
	case DomainInt:
		return "int"
	case DomainFloat:
		return "float"
	case DomainDecimal:
		return "decimal"
	}
	return "domain?"
}

// Overflow is the discipline an Int operation's overflow follows. It exists
// because it is a real choice a producer makes and not a consumer's: `derive
// Hashable`'s synthesized mixers are modular, a programmer's `+` is checked,
// and both lower from the same AST shape.
type Overflow uint8

const (
	// OverflowFaults says overflow is a FAULT at this operation's position.
	// What a consumer does about it is the consumer's.
	OverflowFaults Overflow = iota + 1
	// OverflowWraps says overflow is DEFINED to wrap here. `rt` has wrapping
	// forms for `+`, `-` and `*` and for nothing else, which is why the
	// constructors reject this discipline on the other operators.
	OverflowWraps
)

func (o Overflow) String() string {
	if o == OverflowWraps {
		return "wraps"
	}
	return "faults"
}

// Faults is the set of ways one operation can fault at its own position. It is
// a set rather than one value because Int `/` faults two ways: on a zero
// divisor, and on MinInt64 / -1, which is the division case a backend forgets
// (`rt.QuoOverflows` exists to say so).
type Faults uint8

const (
	// FaultOverflow: the result leaves the representable range.
	FaultOverflow Faults = 1 << iota
	// FaultDivByZero: the divisor is zero.
	FaultDivByZero
	// FaultUndefined: the operation has no answer in this domain at all.
	// Nomi's `%` on Float and on Decimal is this — `rt.ModFloat` and
	// `rt.ModDecimal` always trap.
	FaultUndefined
	// FaultNoMatch: no arm of a `case` matched its scrutinee. See
	// nomatch.go. It is in this set rather than in a set of its own because
	// `Faulting` asks one question — what are the ways this instruction can
	// fault — and a second enumeration would make `Block.CanFault` ask two.
	FaultNoMatch
	// FaultTodo: a `todo` was reached. See todo.go. In this set for
	// FaultNoMatch's reason.
	FaultTodo
)

// Any reports whether the operation can fault at all.
func (f Faults) Any() bool { return f != 0 }

// Has reports whether f includes k.
func (f Faults) Has(k Faults) bool { return f&k != 0 }

func (f Faults) String() string {
	if f == 0 {
		return "none"
	}
	s := ""
	for _, k := range [...]struct {
		bit  Faults
		name string
	}{{FaultOverflow, "overflow"}, {FaultDivByZero, "div0"}, {FaultUndefined, "undefined"},
		{FaultNoMatch, "nomatch"}, {FaultTodo, "todo"}} {
		if f.Has(k.bit) {
			if s != "" {
				s += "|"
			}
			s += k.name
		}
	}
	return s
}

// Shape is one of the six shapes of Nomi's primitive arithmetic. It is DERIVED, never stored: see the file
// comment.
type Shape uint8

const (
	// ShapeIntChecked is `rt.<Op>Int(a, b, line)`.
	ShapeIntChecked Shape = iota + 1
	// ShapeIntWrapping is `rt.Wrap<Op>Int(a, b)`.
	ShapeIntWrapping
	// ShapeFloatNative is Go's own `a Op b`.
	ShapeFloatNative
	// ShapeFloatDiv is `rt.DivFloat(a, b)`.
	ShapeFloatDiv
	// ShapeFloatMod is `rt.ModFloat(a, b, line)`.
	ShapeFloatMod
	// ShapeDecimal is `rt.<Op>Decimal(...)`.
	ShapeDecimal
)

func (s Shape) String() string {
	switch s {
	case ShapeIntChecked:
		return "int-checked"
	case ShapeIntWrapping:
		return "int-wrapping"
	case ShapeFloatNative:
		return "float-native"
	case ShapeFloatDiv:
		return "float-div"
	case ShapeFloatMod:
		return "float-mod"
	case ShapeDecimal:
		return "decimal"
	}
	return "shape?"
}

// ArithKind is the operand domain plus the overflow discipline an Int
// operation needs.
//
// It has unexported fields and three factories, so the Int case cannot be
// built without naming a discipline.
type ArithKind struct {
	dom  Domain
	over Overflow
}

// IntArith is arithmetic on Int under a named overflow discipline.
func IntArith(over Overflow) ArithKind {
	if over != OverflowFaults && over != OverflowWraps {
		panic("ir: IntArith: an Int operation needs an overflow discipline")
	}
	return ArithKind{dom: DomainInt, over: over}
}

// FloatArith is arithmetic on Float.
func FloatArith() ArithKind { return ArithKind{dom: DomainFloat} }

// DecimalArith is exact arithmetic on Decimal.
func DecimalArith() ArithKind { return ArithKind{dom: DomainDecimal} }

// Domain is the operand domain.
func (k ArithKind) Domain() Domain { return k.dom }

// Arith is one arithmetic operation.
type Arith struct {
	pos  Pos
	dst  Temp
	lhs  Temp
	rhs  Temp
	op   ArithOp
	dom  Domain
	over Overflow
}

// NewArith is a binary arithmetic operation.
//
// pos is the OPERATOR'S position, not the enclosing statement's and not
// whatever was lowered last. The trap message names it, so it is a semantic
// requirement and not a debugger nicety.
func NewArith(pos Pos, dst Temp, op ArithOp, k ArithKind, lhs, rhs Temp) *Arith {
	a := newArith(pos, dst, op, k, "NewArith")
	if op.Unary() {
		panic("ir: NewArith: " + op.String() + " reads one operand; use NewUnaryArith")
	}
	if lhs == NoTemp || rhs == NoTemp {
		panic("ir: NewArith: a binary operation needs both operands")
	}
	a.lhs, a.rhs = lhs, rhs
	return a
}

// NewUnaryArith is a unary arithmetic operation. Only OpNeg is one.
func NewUnaryArith(pos Pos, dst Temp, op ArithOp, k ArithKind, operand Temp) *Arith {
	a := newArith(pos, dst, op, k, "NewUnaryArith")
	if !op.Unary() {
		panic("ir: NewUnaryArith: " + op.String() + " reads two operands; use NewArith")
	}
	if operand == NoTemp {
		panic("ir: NewUnaryArith: a unary operation needs an operand")
	}
	a.lhs = operand
	return a
}

func newArith(pos Pos, dst Temp, op ArithOp, k ArithKind, who string) *Arith {
	requirePos(pos, who)
	if dst == NoTemp {
		panic("ir: " + who + ": an arithmetic operation with no destination is dead; do not build it")
	}
	if op < OpAdd || op > OpNeg {
		panic("ir: " + who + ": unknown operator")
	}
	if k.dom == 0 {
		panic("ir: " + who + ": an ArithKind must come from IntArith, FloatArith, " +
			"or DecimalArith")
	}
	// Measured against rt/arith.go, not assumed: the wrapping forms are
	// WrapAddInt, WrapSubInt and WrapMulInt and there are no others, because
	// `derive Hashable`'s mixers use exactly those three. A wrapping `/`, `%`
	// or unary `-` has no emitted shape, so it must not be representable.
	if k.dom == DomainInt && k.over == OverflowWraps && op != OpAdd && op != OpSub && op != OpMul {
		panic("ir: " + who + ": no wrapping form of Int `" + op.Symbol() +
			"` exists; rt has WrapAddInt, WrapSubInt and WrapMulInt and nothing else")
	}
	return &Arith{pos: pos, dst: dst, op: op, dom: k.dom, over: k.over}
}

// Op is the operator.
func (a *Arith) Op() ArithOp { return a.op }

// Domain is the operand domain.
func (a *Arith) Domain() Domain { return a.dom }

// Overflow is the discipline, meaningful only on DomainInt.
func (a *Arith) Overflow() Overflow { return a.over }

// Lhs is the left operand, or the only operand of a unary operation.
func (a *Arith) Lhs() Temp { return a.lhs }

// Rhs is the right operand, or NoTemp for a unary operation.
func (a *Arith) Rhs() Temp { return a.rhs }

// Faults is the set of ways this operation can fault AT ITS OWN POSITION.
//
// Derived from rt/arith.go's actual behaviour rather than from a general rule
// about arithmetic:
//
//   - Int `+ - *` and unary `-` fault on overflow, unless the producer chose the
//     modular discipline. `rt.NegInt` is `SubInt(0, a, line)`, so negation
//     faults on MinInt64 like the subtraction it performs.
//   - Int `/` faults two ways: `rt.QuoInt` traps on a zero divisor and on the
//     one overflowing pair, MinInt64 / -1.
//   - Int `%` faults on a zero divisor only. Go's `%` answers MinInt64 % -1
//     without a panic (the answer is 0), so there is no
//     overflow fault to record.
//   - Float `+ - * /` and unary `-` never fault. IEEE has an answer for every
//     input including a zero divisor, and `rt.DivFloat` is Go's own `/`.
//   - Float `%` is UNDEFINED: `rt.ModFloat` always traps.
//   - Decimal `+ - *` and unary `-` are exact and never fail. `/` faults on a
//     zero divisor. `%` is undefined and `rt.ModDecimal` always traps.
func (a *Arith) Faults() Faults {
	switch a.dom {
	case DomainInt:
		switch a.op {
		case OpDiv:
			return FaultDivByZero | FaultOverflow
		case OpRem:
			return FaultDivByZero
		default:
			if a.over == OverflowWraps {
				return 0
			}
			return FaultOverflow
		}
	case DomainFloat:
		if a.op == OpRem {
			return FaultUndefined
		}
		return 0
	case DomainDecimal:
		switch a.op {
		case OpDiv:
			return FaultDivByZero
		case OpRem:
			return FaultUndefined
		}
		return 0
	}
	return 0
}

// Shape is which of the six shapes this operation is. Derived; see the file
// comment for why it is not a field.
func (a *Arith) Shape() Shape {
	switch a.dom {
	case DomainDecimal:
		return ShapeDecimal
	case DomainFloat:
		switch a.op {
		case OpDiv:
			return ShapeFloatDiv
		case OpRem:
			return ShapeFloatMod
		}
		return ShapeFloatNative
	}
	if a.over == OverflowWraps {
		return ShapeIntWrapping
	}
	return ShapeIntChecked
}

func (a *Arith) Pos() Pos  { return a.pos }
func (a *Arith) Dst() Temp { return a.dst }

func (a *Arith) AppendUses(dst []Temp) []Temp {
	if a.rhs == NoTemp {
		return append(dst, a.lhs)
	}
	return append(dst, a.lhs, a.rhs)
}

func (a *Arith) String() string {
	s := a.dst.String() + " = "
	if a.op.Unary() {
		s += a.op.Symbol() + a.lhs.String()
	} else {
		s += a.lhs.String() + " " + a.op.Symbol() + " " + a.rhs.String()
	}
	s += " [" + a.dom.String()
	if a.dom == DomainInt {
		s += " " + a.over.String()
	}
	if f := a.Faults(); f.Any() {
		s += " faults:" + f.String()
	}
	return s + "]"
}

func (a *Arith) irNode()  {}
func (a *Arith) irInstr() {}
