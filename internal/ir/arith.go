package ir

// The `arith` class, and the fault/delivery division (point 1 of ir.go's
// package header).
//
// An operation that can fault records that a fault is POSSIBLE AT THIS POINT
// and where "this point" is. It records nothing about how the fault is
// delivered: rt carries the error (`DivByZeroError`), with one message text,
// and which surface a consumer uses is its own choice. Delivery is a consumer
// property.
//
// There is no `trap bool` and there is nowhere to put one. Faults is a METHOD,
// computed from the operator and the operand domain. Int arithmetic is exact
// at any size, so only a zero divisor faults it. None of that is a fact about
// which consumer is reading.
//
// The position rides as an operand for these operations rather than as an
// ambient cursor, which is why `fn quo(a, b) { a / b }` blames `a / b` rather
// than the line of the call.
//
// THE SIX OPERATIONS, AND WHY THEY ARE NOT SIX OPCODES. Nomi's primitive
// `a + b` has six shapes: exact `rt.Int<Op>(a, b)`, wrapping (the low 64 bits
// of the exact answer), Go's own `a Op b` for Float, `rt.DivFloat(a, b)`,
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

// Symbol is the source spelling.
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
	// DomainInt is Nomi's Int, which is exact at any size.
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

// Overflow is the discipline an Int operation follows. It exists because it
// is a real choice a producer makes and not a consumer's: `derive
// Hashable`'s synthesized mixers keep a hash in 64 bits, a programmer's `+`
// is exact, and both lower from the same AST shape.
type Overflow uint8

const (
	// OverflowExact says the result is the exact answer, at any size: a
	// programmer's operator.
	OverflowExact Overflow = iota + 1
	// OverflowWraps says the result is the exact answer's low 64 bits, as an
	// int64. There are wrapping forms of `+`, `-` and `*` and of nothing
	// else, which is why the constructors reject this discipline on the
	// other operators.
	OverflowWraps
)

func (o Overflow) String() string {
	if o == OverflowWraps {
		return "wraps"
	}
	return "exact"
}

// Faults is the set of ways one operation can fault at its own position. It is
// a set rather than one value because an instruction of another class (a
// `case`, a `todo`) shares the vocabulary.
type Faults uint8

const (
	// FaultDivByZero: the divisor is zero.
	FaultDivByZero Faults = 1 << iota
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
	// FaultUnreachable: an `unreachable` was reached. See unreachable.go.
	FaultUnreachable
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
	}{{FaultDivByZero, "div0"}, {FaultUndefined, "undefined"},
		{FaultNoMatch, "nomatch"}, {FaultTodo, "todo"}, {FaultUnreachable, "unreachable"}} {
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
	// ShapeIntExact is `rt.Int<Op>(a, b)`: exact, and for `/` and `%` a
	// fault on a zero divisor.
	ShapeIntExact Shape = iota + 1
	// ShapeIntWrapping is the exact answer's low 64 bits (`rt.Wrap64`).
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
	case ShapeIntExact:
		return "int-exact"
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

// IntArith is arithmetic on Int under a named discipline.
func IntArith(over Overflow) ArithKind {
	if over != OverflowExact && over != OverflowWraps {
		panic("ir: IntArith: an Int operation needs a discipline")
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
	// The wrapping forms are `+`, `-` and `*` and there are no others, because
	// `derive Hashable`'s mixers use exactly those three. A wrapping `/`, `%`
	// or unary `-` has no emitted shape, so it must not be representable.
	if k.dom == DomainInt && k.over == OverflowWraps && op != OpAdd && op != OpSub && op != OpMul {
		panic("ir: " + who + ": no wrapping form of Int `" + op.Symbol() +
			"` exists; only `+`, `-` and `*` wrap")
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
//   - Int `+ - *` and unary `-` never fault: the answer is exact at any size,
//     or its low 64 bits under the modular discipline.
//   - Int `/` and `%` fault on a zero divisor only.
//   - Float `+ - * /` and unary `-` never fault. IEEE has an answer for every
//     input including a zero divisor, and `rt.DivFloat` is Go's own `/`.
//   - Float `%` is UNDEFINED: `rt.ModFloat` always traps.
//   - Decimal `+ - *` and unary `-` are exact and never fail. `/` and `%` are
//     undefined: the checker rejects both, `rt.ModDecimal` always traps, and
//     the VM refuses a Decimal `/`.
func (a *Arith) Faults() Faults {
	switch a.dom {
	case DomainInt:
		switch a.op {
		case OpDiv, OpRem:
			return FaultDivByZero
		}
		return 0
	case DomainFloat:
		if a.op == OpRem {
			return FaultUndefined
		}
		return 0
	case DomainDecimal:
		if a.op == OpDiv || a.op == OpRem {
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
	return ShapeIntExact
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
