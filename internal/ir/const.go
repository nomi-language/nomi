package ir

import "strconv"

// The `const` class: a value with no operands.
//
// Const is one instruction with a kind rather than eleven instruction types,
// because a constant's emitted shape is fixed and its payload fills a hole.
// A difference in payload is an operand, not a new operation.
//
// Two notes on what the class contains:
//
//   - Scalar literals (`Int`, `Float`, `String`, `Bool`) and the non-scalar
//     nullary constants are both modelled here. The builder lowers the
//     scalars through `expr` rather than through a dedicated `const` owner,
//     but a consumer needs both.
//
//   - Three owners build an EMPTY CONTAINER: `emptyList`, `setNewCall` and
//     `vectorEmptyCall`, all at the Unit instantiation and none at an element
//     type. The element type is not merely unavailable at those positions:
//     `Set.new()` names no `T` at all, and the type argument arrives later,
//     from the context that discharges the sentinel.
//
//     So the element type is OPTIONAL. A producer that has one supplies it; a
//     producer at a position where Nomi fixes no type argument passes nil and
//     Undischarged reports so. A typed empty container has a real producer —
//     the builder's discharge path — and it is not in this class, which is why
//     the typed form stays.
//
// `rangeLit` is deliberately NOT modelled here. A range over non-literal
// bounds reads two operands, which makes it `make`-shaped rather than
// nullary; it is `MakeRange` in make.go.
type ConstKind uint8

const (
	// ConstUnit is `rt.Unit{}`: the value of a statement.
	ConstUnit ConstKind = iota + 1
	// ConstBool is a Bool literal.
	ConstBool
	// ConstInt is an Int literal.
	ConstInt
	// ConstFloat is a Float literal.
	ConstFloat
	// ConstDecimal is a Decimal literal, carried as EXACT TEXT. A Decimal is
	// not a float and must not round on its way through the IR.
	ConstDecimal
	// ConstString is a String literal. An interpolated string is not a
	// constant; it is the `interp` class.
	ConstString
	// ConstMarker is the sole value of a zero-sized distinct type. Owner: markerValue.
	ConstMarker
	// ConstEmptyList is the empty List at an element type. Owners: emptyList,
	// untypedListAnswer.
	ConstEmptyList
	// ConstEmptySet is the empty Set at an element type. Owner: setNewCall.
	ConstEmptySet
	// ConstEmptyVector is the empty Vector at an element type. Owner:
	// vectorEmptyCall.
	ConstEmptyVector
)

func (k ConstKind) String() string {
	switch k {
	case ConstUnit:
		return "unit"
	case ConstBool:
		return "bool"
	case ConstInt:
		return "int"
	case ConstFloat:
		return "float"
	case ConstDecimal:
		return "decimal"
	case ConstString:
		return "string"
	case ConstMarker:
		return "marker"
	case ConstEmptyList:
		return "list"
	case ConstEmptySet:
		return "set"
	case ConstEmptyVector:
		return "vector"
	}
	return "const?"
}

// Const materializes a value that reads no temporary.
type Const struct {
	pos  Pos
	dst  Temp
	kind ConstKind
	num  int64
	flt  float64
	text string
	typ  *Symbol
}

// NewUnit is `rt.Unit{}`.
func NewUnit(pos Pos, dst Temp) *Const { return newConst(pos, dst, ConstUnit, "NewUnit") }

// NewBool is a Bool literal.
func NewBool(pos Pos, dst Temp, v bool) *Const {
	c := newConst(pos, dst, ConstBool, "NewBool")
	if v {
		c.num = 1
	}
	return c
}

// NewInt is an Int literal.
func NewInt(pos Pos, dst Temp, v int64) *Const {
	c := newConst(pos, dst, ConstInt, "NewInt")
	c.num = v
	return c
}

// NewFloat is a Float literal.
func NewFloat(pos Pos, dst Temp, v float64) *Const {
	c := newConst(pos, dst, ConstFloat, "NewFloat")
	c.flt = v
	return c
}

// NewDecimal is a Decimal literal, given as the exact text the programmer
// wrote. It is not parsed here: a Decimal that round-trips through float64 is
// a wrong number printed without complaint.
func NewDecimal(pos Pos, dst Temp, text string) *Const {
	c := newConst(pos, dst, ConstDecimal, "NewDecimal")
	c.text = text
	return c
}

// NewString is a String literal.
func NewString(pos Pos, dst Temp, text string) *Const {
	c := newConst(pos, dst, ConstString, "NewString")
	c.text = text
	return c
}

// NewMarker is a std marker type's sole value.
func NewMarker(pos Pos, dst Temp, typ *Symbol) *Const {
	return newTypedConst(pos, dst, ConstMarker, typ, "NewMarker")
}

// NewEmptyList is the empty List. elem is its element type, or nil where Nomi
// fixes no type argument at this position — see the header and Undischarged.
func NewEmptyList(pos Pos, dst Temp, elem *Symbol) *Const {
	return newContainerConst(pos, dst, ConstEmptyList, elem, "NewEmptyList")
}

// NewEmptySet is the empty Set, at an element type or undischarged.
func NewEmptySet(pos Pos, dst Temp, elem *Symbol) *Const {
	return newContainerConst(pos, dst, ConstEmptySet, elem, "NewEmptySet")
}

// NewEmptyVector is the empty Vector, at an element type or undischarged.
func NewEmptyVector(pos Pos, dst Temp, elem *Symbol) *Const {
	return newContainerConst(pos, dst, ConstEmptyVector, elem, "NewEmptyVector")
}

// newContainerConst permits a nil element type, which newTypedConst does not.
// The difference is which fact is absent: a container's type ARGUMENT can be
// absent in Nomi, and a marker's declaration cannot.
func newContainerConst(pos Pos, dst Temp, kind ConstKind, typ *Symbol, who string) *Const {
	c := newConst(pos, dst, kind, who)
	c.typ = typ
	return c
}

func newTypedConst(pos Pos, dst Temp, kind ConstKind, typ *Symbol, who string) *Const {
	if typ == nil {
		panic("ir: " + who + ": this constant IS a declaration and cannot name none")
	}
	c := newConst(pos, dst, kind, who)
	c.typ = typ
	return c
}

func newConst(pos Pos, dst Temp, kind ConstKind, who string) *Const {
	requirePos(pos, who)
	if dst == NoTemp {
		panic("ir: " + who + ": a constant with no destination is dead; do not build it")
	}
	return &Const{pos: pos, dst: dst, kind: kind}
}

// Kind says which constant this is.
func (c *Const) Kind() ConstKind { return c.kind }

// Bool is the value of a ConstBool.
func (c *Const) Bool() bool { return c.kind == ConstBool && c.num != 0 }

// Int is the value of a ConstInt.
func (c *Const) Int() int64 { return c.num }

// Float is the value of a ConstFloat.
func (c *Const) Float() float64 { return c.flt }

// Text is the exact text of a ConstDecimal or the contents of a ConstString.
func (c *Const) Text() string { return c.text }

// Type is the element type of an empty container or the marker's type. Nil for a scalar, and nil for an UNDISCHARGED
// container.
func (c *Const) Type() *Symbol { return c.typ }

// Undischarged reports whether this constant is an empty container at NO type
// argument.
//
// It is a property of the language and not of a producer's inference. `[]`,
// `Set.new()` and `Vector.empty()` name no element type at their own position;
// the argument arrives from the context that consumes them, and until it does
// the value is a sentinel a consumer must not store or return. The producer
// and the consumer both need the distinction: the builder refuses an
// undischarged value at a position with no expected type, and a consumer
// cannot pick a hash or an equality for a container whose element type it
// does not have.
//
// False for every non-container kind. A marker names a declaration that is
// always present, which newTypedConst enforces.
func (c *Const) Undischarged() bool {
	switch c.kind {
	case ConstEmptyList, ConstEmptySet, ConstEmptyVector:
		return c.typ == nil
	}
	return false
}

func (c *Const) Pos() Pos  { return c.pos }
func (c *Const) Dst() Temp { return c.dst }

// AppendUses appends nothing: a constant reads no temporary. That is what
// makes it a constant.
func (c *Const) AppendUses(dst []Temp) []Temp { return dst }

func (c *Const) String() string {
	s := c.dst.String() + " = const " + c.kind.String()
	switch c.kind {
	case ConstBool:
		s += " " + strconv.FormatBool(c.Bool())
	case ConstInt:
		s += " " + strconv.FormatInt(c.num, 10)
	case ConstFloat:
		s += " " + strconv.FormatFloat(c.flt, 'g', -1, 64)
	case ConstDecimal, ConstString:
		s += " " + strconv.Quote(c.text)
	case ConstMarker, ConstEmptyList, ConstEmptySet, ConstEmptyVector:
		if c.typ == nil {
			s += " <undischarged>"
			break
		}
		s += " " + c.typ.Name()
	}
	return s
}

func (c *Const) irNode()  {}
func (c *Const) irInstr() {}
