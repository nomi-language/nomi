package ir

// The `compare` half of the `arith` class: two operands in, a Bool out.
//
// WHY IT IS A SEPARATE NODE FROM `Arith` RATHER THAN SIX MORE `ArithOp`s.
// `ArithOp.Symbol()` is read by `rt.overflowText` to name the operator in a
// trap message, `ArithKind` carries a `Faults` set, and `Arith`'s destination
// holds the operand domain's own type. A comparison shares none of those: it
// cannot fault, it answers a Bool whatever its operands are, and there is no
// trap text to name it in. Adding `OpEq` to `ArithOp` would put six members
// into an enum whose every other member has an overflow question and a fault
// set, which is the "a field that does not vary over its population" test
// applied to an operator table.
//
// WHY THE DOMAIN IS A `ValShape` AND NOT A `Domain`. `Domain` is arithmetic's
// three families — Int, Float, Decimal — and a comparison runs over String and
// Bool as well, so it would need two of its five answers invented. `ValShape`
// already holds exactly this distinction and is what `lintOperandShapes` and
// the VM's own eleven conditions turn on, so using it means the operand rule
// below and the lint rule are the same vocabulary rather than two.
//
// WHAT THE CONSTRUCTOR REFUSES, AND EACH IS A LANGUAGE RULE RATHER THAN A
// CONVENIENCE:
//
//   - ORDERING OUTSIDE Int AND Float. `<` on a named type is `impl Comparable`
//     dispatch and `internal/irbuild` refuses it by the name `comparison outside
//     Int and Float`. A node that admitted `ValString` for `<` would be a
//     representation for a construct no producer may build.
//   - DISPATCHED EQUALITY ON A NAMED, CONTAINER OR EXISTENTIAL SHAPE. That is two rules
//     — `impl Equatable` when one is reachable, structural comparison
//     otherwise — and which one applies is a program-scoped question
//     (`equatable` is one of `internal/irbuild`'s program-scoped side tables).
//     One node with two answers is the shape this package refuses elsewhere.
//
// Variant, struct and container equality are explicitly structural. Container equality
// currently executes lists; its broad shape does not identify the element type.
// The producer must establish the concrete representation and that
// no Equatable dispatch applies; the node never performs method selection.
// Decimal equality uses rt.EqDecimal's scale-insensitive rule; it never
// compares the representation. Equality admits Int, Float, Decimal, String,
// Bool, Variant, Struct and Container; ordering admits Int, Float and Bool,
// whose order is std's derived `Comparable for Bool`: False before True, the
// declaration order of its variants.
//
// The VM executes Compare. The shape checker
// verifies that each operand agrees with the node's declared shape.

// CompareOp is the relation a comparison asks about.
type CompareOp uint8

const (
	// OpEq is `==`.
	OpEq CompareOp = iota + 1
	// OpNe is `!=`.
	OpNe
	// OpLt is `<`.
	OpLt
	// OpLe is `<=`.
	OpLe
	// OpGt is `>`.
	OpGt
	// OpGe is `>=`.
	OpGe
)

// Symbol is the source spelling, which is also how a failure report prints the
// comparison it came from.
func (o CompareOp) Symbol() string {
	switch o {
	case OpEq:
		return "=="
	case OpNe:
		return "!="
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	}
	return "?"
}

func (o CompareOp) String() string { return o.Symbol() }

// Ordering reports whether this operator asks about order rather than equality.
// The two have different admitted operand shapes, which is the only reason the
// distinction is named.
func (o CompareOp) Ordering() bool { return o == OpLt || o == OpLe || o == OpGt || o == OpGe }

// Compare asks one relation of two operands and writes the Bool answer.
type Compare struct {
	pos   Pos
	dst   Temp
	op    CompareOp
	shape ValShape
	lhs   Temp
	rhs   Temp
	// rank marks the ordering operators on a type whose order comes from
	// its Comparable impl: lhs is that impl's Ordering answer, compared by
	// rank against Equal, and there is no rhs.
	rank bool
}

// NewCompareRank builds `dst = rank(ordering) <op> rank(Equal)`, the answer
// of `<`, `>`, `<=` or `>=` once the operands' Comparable impl has produced
// their Ordering. The operand is an Ordering variant.
func NewCompareRank(pos Pos, dst Temp, op CompareOp, ordering Temp) *Compare {
	requirePos(pos, "ir.NewCompareRank")
	if dst == NoTemp || ordering == NoTemp {
		panic("ir.NewCompareRank: a ranked comparison reads one Ordering and writes a Bool")
	}
	if !op.Ordering() {
		panic("ir.NewCompareRank: " + op.Symbol() + " is not an ordering operator")
	}
	return &Compare{pos: pos, dst: dst, op: op, shape: ValVariant, lhs: ordering, rhs: NoTemp, rank: true}
}

// Ranked reports whether this compares an Ordering answer against Equal.
func (c *Compare) Ranked() bool { return c.rank }

// NewCompare builds `dst = lhs <op> rhs` over two operands of one shape.
//
// The shape is the OPERANDS', not the destination's: the destination is always
// a Bool, which is what `shapeWritten` answers for this node without being
// told.
func NewCompare(pos Pos, dst Temp, op CompareOp, shape ValShape, lhs, rhs Temp) *Compare {
	requirePos(pos, "ir.NewCompare")
	if dst == NoTemp {
		panic("ir.NewCompare: a comparison's answer is a value and needs a destination")
	}
	if lhs == NoTemp || rhs == NoTemp {
		panic("ir.NewCompare: a comparison reads two operands")
	}
	if op.Symbol() == "?" {
		panic("ir.NewCompare: unknown comparison operator")
	}
	if !compareShapeAdmitted(op, shape) {
		panic("ir.NewCompare: " + op.Symbol() + " over " + shape.String() +
			" is outside the admitted set; see compare.go's header for each refusal")
	}
	return &Compare{pos: pos, dst: dst, op: op, shape: shape, lhs: lhs, rhs: rhs}
}

// compareShapeAdmitted is the constructor's operator/domain rule.
func compareShapeAdmitted(op CompareOp, shape ValShape) bool {
	switch shape {
	case ValInt, ValFloat, ValBool:
		return true
	case ValString, ValVariant, ValDecimal, ValContainer, ValStruct:
		return !op.Ordering()
	}
	return false
}

// Op is the relation.
func (c *Compare) Op() CompareOp { return c.op }

// Shape is the shape both operands hold.
func (c *Compare) Shape() ValShape { return c.shape }

// Lhs and Rhs are the operands, in source order — which matters because a
// failure report prints the left operand's row before the right one's.
func (c *Compare) Lhs() Temp { return c.lhs }

// Rhs is the right operand. See Lhs.
func (c *Compare) Rhs() Temp { return c.rhs }

func (c *Compare) Pos() Pos { return c.pos }
func (c *Compare) Dst() Temp {
	return c.dst
}
func (c *Compare) AppendUses(dst []Temp) []Temp {
	if c.rank {
		return append(dst, c.lhs)
	}
	return append(dst, c.lhs, c.rhs)
}

func (c *Compare) String() string {
	if c.rank {
		return c.dst.String() + " = rank " + c.lhs.String() + " " + c.op.Symbol() + " Equal"
	}
	return c.dst.String() + " = " + c.lhs.String() + " " + c.op.Symbol() + " " +
		c.rhs.String() + " : " + c.shape.String()
}

func (c *Compare) irNode()  {}
func (c *Compare) irInstr() {}
